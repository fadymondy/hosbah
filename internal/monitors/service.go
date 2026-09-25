// Package monitors is the "Alerts & monitors" of a site (app): HTTP checks of the app's own
// hostnames and CPU / memory / disk rules over its server's metrics history, the incidents they
// open and resolve, and the announcements (team notification, email, webhook).
//
// The evaluator is a control-plane worker like the metrics retention loop: every TickEvery it
// claims the monitors that are due (system scope, FOR UPDATE SKIP LOCKED — see ClaimDueMonitors),
// checks them with no database lock held, and applies each result in the monitor's team
// (Scope{System: true, TeamID: …}, so the team's own policies cover what system scope alone may not
// read back). Everything it fetches goes through the SSRF rule in safehttp.go.
package monitors

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/mail"
	"github.com/fadymondy/hosbah/internal/notifications"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

const (
	// TickEvery is how often the evaluator looks for due monitors.
	TickEvery = 15 * time.Second
	// CheckTimeout bounds one HTTP check, redirects included.
	CheckTimeout = 10 * time.Second
	// WebhookTimeout bounds one webhook delivery.
	WebhookTimeout = 5 * time.Second
	// MaxRedirects is how many redirects a check follows; one more fails it.
	MaxRedirects = 3
	// HTTPFailuresToOpen: an http monitor opens an incident on its second consecutive failure; a
	// cpu | memory | disk monitor (already an average over a window) on the first.
	HTTPFailuresToOpen = 2
	// UserAgent of checks and webhooks.
	UserAgent = "Hosbah-Monitor"

	claimBatch  = 50
	workers     = 8
	maxBodyRead = 64 << 10
)

// Service is the evaluator.
type Service struct {
	db  *sql.DB
	log *slog.Logger

	mu        sync.RWMutex
	now       func() time.Time
	transport http.RoundTripper
	send      mail.Sender
	notify    *notifications.Service

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// SetNotifier wires the team notification-channel service (internal/notifications) in: a monitor's
// notify_channel_ids then also receive its incidents, alongside notify_emails / notify_webhook.
// nil (the default) just skips channel delivery.
func (s *Service) SetNotifier(n *notifications.Service) { s.mu.Lock(); s.notify = n; s.mu.Unlock() }

func (s *Service) notifier() *notifications.Service {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.notify
}

// New builds the service. send nil = mail.FromEnv.
func New(a *app.App, send mail.Sender) *Service {
	log := a.Log
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "monitors")
	if send == nil {
		send = mail.FromEnv(log)
	}
	return &Service{db: a.SQLDB, log: log, now: time.Now, transport: SafeTransport(nil), send: send}
}

// SetClock replaces the evaluator's clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.mu.Lock(); s.now = now; s.mu.Unlock() }

// SetTransport replaces the HTTP transport of checks and webhooks (tests: httptest targets). The
// default is SafeTransport — whoever replaces it replaces the SSRF dialer with it.
func (s *Service) SetTransport(rt http.RoundTripper) { s.mu.Lock(); s.transport = rt; s.mu.Unlock() }

// SetSender replaces the mailer (tests).
func (s *Service) SetSender(send mail.Sender) { s.mu.Lock(); s.send = send; s.mu.Unlock() }

// Now is the evaluator's clock.
func (s *Service) Now() time.Time { s.mu.RLock(); defer s.mu.RUnlock(); return s.now() }

func (s *Service) deps() (http.RoundTripper, mail.Sender) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.transport, s.send
}

// Start launches the evaluator loop; Stop ends it. The first tick comes TickEvery after Start.
func (s *Service) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(TickEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
					s.log.Warn("monitor evaluation failed", "err", err)
				}
			}
		}
	}()
}

// Stop is the counterpart of Start. Safe to call twice.
func (s *Service) Stop() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.wg.Wait()
}

// Tick claims and checks every monitor that is due, and returns how many it checked.
func (s *Service) Tick(ctx context.Context) (int, error) {
	total := 0
	for round := 0; round < 20; round++ {
		var due []db.Monitor
		err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
			var e error
			due, e = q.ClaimDueMonitors(ctx, db.ClaimDueMonitorsParams{Now: s.Now(), Lim: claimBatch})
			return e
		})
		if err != nil {
			return total, fmt.Errorf("claim: %w", err)
		}
		jobs := make(chan db.Monitor)
		var wg sync.WaitGroup
		for i := 0; i < min(workers, len(due)); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for m := range jobs {
					if _, err := s.run(ctx, m); err != nil && ctx.Err() == nil {
						s.log.Warn("monitor check failed", "monitor", m.ID, "err", err)
					}
				}
			}()
		}
		for _, m := range due {
			jobs <- m
		}
		close(jobs)
		wg.Wait()
		total += len(due)
		if len(due) < claimBatch {
			break
		}
	}
	s.sweepHeartbeats(ctx)
	return total, nil
}

// CheckNow checks one monitor at once (POST …/check) and returns it as it is afterwards. The
// caller resolved m within its own team. A disabled monitor is a 409.
func (s *Service) CheckNow(ctx context.Context, m db.Monitor) (db.Monitor, error) {
	if !m.Enabled {
		return m, conflict("the monitor is disabled")
	}
	return s.run(ctx, m)
}

// result is what one evaluation found.
type result struct {
	State  string // StateOK | StateFailing | StateUnknown
	Reason string // why it is failing or unknown
	Value  string // the reading, in words
}

// target is what a check needs to know about the monitor's app.
type target struct {
	App       db.App
	Server    db.Server
	Hostnames []string
}

func (s *Service) run(ctx context.Context, m db.Monitor) (db.Monitor, error) {
	scope := tenancy.Scope{System: true, TeamID: m.TeamID}
	var (
		tg  target
		avg db.ServerMetricsAverageRow
		now = s.Now()
	)
	err := tenancy.Tx(ctx, s.db, scope, func(q *db.Queries, _ *sql.Tx) error {
		a, err := deploy.AppByRef(ctx, q, m.TeamID, m.AppID)
		if err != nil {
			return err
		}
		tg.App = a
		if tg.Server, err = q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: m.TeamID}); err != nil {
			return err
		}
		if tg.Hostnames, err = hostnamesOf(ctx, q, m.TeamID, a); err != nil {
			return err
		}
		if m.Kind != KindHTTP {
			avg, err = q.ServerMetricsAverage(ctx, db.ServerMetricsAverageParams{ServerID: a.ServerID, TeamID: m.TeamID,
				Since: now.Add(-time.Duration(m.WindowMinutes) * time.Minute), Until: now})
		}
		return err
	})
	if err != nil {
		return m, err
	}

	var res result
	if m.Kind == KindHTTP {
		res = s.checkHTTP(ctx, m, tg)
	} else {
		res = judgeResource(m, avg)
	}
	up, ev, err := s.apply(ctx, m, tg, res)
	if err != nil {
		return m, err
	}
	if ev != nil {
		s.announce(ctx, *ev)
	}
	return up, nil
}

// judgeResource holds the window's average against the threshold. No samples = unknown, not failing.
func judgeResource(m db.Monitor, avg db.ServerMetricsAverageRow) result {
	if avg.Rows == 0 {
		return result{State: StateUnknown, Reason: fmt.Sprintf("the server reported no metrics in the last %d minutes", m.WindowMinutes)}
	}
	v := map[string]float64{KindCPU: avg.Cpu, KindMemory: avg.Memory, KindDisk: avg.Disk}[m.Kind]
	v = math.Round(v*10) / 10
	value := fmt.Sprintf("%.1f%%", v)
	if v > m.Threshold {
		return result{State: StateFailing, Value: value,
			Reason: fmt.Sprintf("%s averaged %.1f%% over %d minutes, above %g%%", m.Kind, v, m.WindowMinutes, m.Threshold)}
	}
	return result{State: StateOK, Value: value}
}

var errTooManyRedirects = fmt.Errorf("more than %d redirects", MaxRedirects)

// checkHTTP fetches the monitor's target.
func (s *Service) checkHTTP(ctx context.Context, m db.Monitor, tg target) result {
	// The write-time rule again: the app may have lost the hostname since. Never fetched then.
	host, err := TargetHost(m.Target)
	own := false
	for _, h := range tg.Hostnames {
		own = own || h == host
	}
	if err != nil || !own {
		return result{State: StateUnknown, Reason: "the target is no longer on one of the app's hostnames"}
	}
	// Rule 3 of safehttp.go: a site on a LAN. Only when the app's server has a private address,
	// and only for the app's own hostnames.
	if tg.Server.IpAddress != nil && IsPrivateAddress(*tg.Server.IpAddress) {
		ctx = allowPrivate(ctx, tg.Hostnames)
	}
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Target, nil)
	if err != nil {
		return result{State: StateFailing, Reason: "bad target"}
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "*/*")
	rt, _ := s.deps()
	client := &http.Client{Transport: rt, Timeout: CheckTimeout, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > MaxRedirects {
			return errTooManyRedirects
		}
		if r.URL.Scheme != "http" && r.URL.Scheme != "https" {
			return errors.New("redirect to a URL that is not http(s)")
		}
		return nil
	}}
	started := time.Now() // how long the request took is real time, whatever the evaluator's clock says
	resp, err := client.Do(req)
	if err != nil {
		return result{State: StateFailing, Reason: clip(errText(err), 300)}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyRead))
	resp.Body.Close()
	ms := time.Since(started).Milliseconds()
	value := fmt.Sprintf("HTTP %d in %d ms", resp.StatusCode, ms)
	switch {
	case m.ExpectedStatus == 0 && (resp.StatusCode < 200 || resp.StatusCode > 399):
		return result{State: StateFailing, Value: value, Reason: fmt.Sprintf("expected a 2xx or 3xx answer, got HTTP %d", resp.StatusCode)}
	case m.ExpectedStatus != 0 && int64(resp.StatusCode) != m.ExpectedStatus:
		return result{State: StateFailing, Value: value, Reason: fmt.Sprintf("expected HTTP %d, got HTTP %d", m.ExpectedStatus, resp.StatusCode)}
	case m.Threshold > 0 && float64(ms) > m.Threshold:
		return result{State: StateFailing, Value: value, Reason: fmt.Sprintf("answered in %d ms, slower than %g ms", ms, m.Threshold)}
	}
	return result{State: StateOK, Value: value}
}

func errText(err error) string {
	switch {
	case errors.Is(err, ErrRefusedAddress):
		return ErrRefusedAddress.Error()
	case errors.Is(err, errTooManyRedirects):
		return errTooManyRedirects.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("no answer within %d s", int(CheckTimeout/time.Second))
	}
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Sprintf("no answer within %d s", int(CheckTimeout/time.Second))
	}
	return err.Error()
}

func clip(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// Event is an incident opening or resolving — the webhook's JSON body.
type Event struct {
	Event   string     `json:"event"` // opened | resolved
	Monitor MonitorRef `json:"monitor"`
	App     EventApp   `json:"app"`
	Reason  string     `json:"reason"`
	Value   string     `json:"value"`
	At      time.Time  `json:"at"`

	teamID, incidentID, webhook string
	emails, channelIDs          []string
}

// EventApp names the app of an event.
type EventApp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// apply records a result on the monitor as it is NOW (locked), opens or resolves its incident and
// writes the team notification — one transaction. The event, if any, is announced after it.
func (s *Service) apply(ctx context.Context, m db.Monitor, tg target, res result) (db.Monitor, *Event, error) {
	var (
		up  = m
		ev  *Event
		now = s.Now()
	)
	// System AND team: notifications allow a system INSERT but no SELECT, and the team's policies
	// cover everything else this reads back.
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true, TeamID: m.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		cur, err := q.LockMonitor(ctx, db.LockMonitorParams{ID: m.ID, TeamID: m.TeamID})
		if errors.Is(err, sql.ErrNoRows) {
			return nil // deleted while it was being checked
		}
		if err != nil {
			return err
		}
		if !cur.Enabled {
			up = cur // switched off while it was being checked
			return nil
		}
		failures := cur.ConsecutiveFailures
		switch res.State {
		case StateOK:
			failures = 0
		case StateFailing:
			failures++
		}
		if up, err = q.RecordMonitorResult(ctx, db.RecordMonitorResultParams{ID: m.ID, TeamID: m.TeamID, State: res.State,
			LastError: clip(res.Reason, 500), ConsecutiveFailures: failures, CheckedAt: now}); err != nil {
			return err
		}
		open, err := q.OpenIncidentOfMonitor(ctx, db.OpenIncidentOfMonitorParams{MonitorID: m.ID, TeamID: m.TeamID})
		hasOpen := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		needed := int64(1)
		if m.Kind == KindHTTP {
			needed = HTTPFailuresToOpen
		}
		base := Event{Monitor: MonitorRef{ID: up.ID, Name: up.Name, Kind: up.Kind}, App: EventApp{ID: tg.App.ID, Name: tg.App.Name},
			Value: res.Value, At: now.UTC(), teamID: m.TeamID, webhook: up.NotifyWebhook, emails: up.NotifyEmails, channelIDs: up.NotifyChannelIds}
		switch {
		case res.State == StateFailing && hasOpen:
			return q.TouchMonitorIncident(ctx, db.TouchMonitorIncidentParams{ID: open.ID, TeamID: m.TeamID, LastValue: res.Value})
		case res.State == StateFailing && failures >= needed:
			inc, err := q.OpenMonitorIncident(ctx, db.OpenMonitorIncidentParams{TeamID: m.TeamID, MonitorID: m.ID, AppID: m.AppID,
				OpenedAt: now, Reason: clip(res.Reason, 500), LastValue: res.Value})
			if errors.Is(err, sql.ErrNoRows) {
				return nil // another instance opened it
			}
			if err != nil {
				return err
			}
			base.Event, base.Reason, base.incidentID = "opened", inc.Reason, inc.ID
		case res.State == StateOK && hasOpen:
			inc, err := q.ResolveMonitorIncident(ctx, db.ResolveMonitorIncidentParams{ID: open.ID, TeamID: m.TeamID, ResolvedAt: now, LastValue: res.Value})
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			base.Event, base.Reason, base.incidentID = "resolved", inc.Reason, inc.ID
		default:
			return nil
		}
		ev = &base
		body := ev.text()
		return q.InsertNotification(ctx, db.InsertNotificationParams{TeamID: m.TeamID, Kind: "monitor." + ev.Event, Title: ev.title(),
			Body: &body, SubjectType: ptr("app"), SubjectID: &tg.App.ID})
	})
	return up, ev, err
}

func ptr[T any](v T) *T { return &v }

func (e Event) title() string {
	if e.Event == "resolved" {
		return fmt.Sprintf("%s: %s has recovered", e.App.Name, e.Monitor.Name)
	}
	return fmt.Sprintf("%s: %s is failing", e.App.Name, e.Monitor.Name)
}

func (e Event) text() string {
	var b strings.Builder
	if e.Event == "resolved" {
		fmt.Fprintf(&b, "The %s monitor %q of %s has recovered.\n", e.Monitor.Kind, e.Monitor.Name, e.App.Name)
		fmt.Fprintf(&b, "It had opened because: %s\n", e.Reason)
	} else {
		fmt.Fprintf(&b, "The %s monitor %q of %s is failing.\n", e.Monitor.Kind, e.Monitor.Name, e.App.Name)
		fmt.Fprintf(&b, "Reason: %s\n", e.Reason)
	}
	if e.Value != "" {
		fmt.Fprintf(&b, "Latest reading: %s\n", e.Value)
	}
	fmt.Fprintf(&b, "At: %s", e.At.Format(time.RFC1123))
	return b.String()
}

// announce emails and posts an event. Failures are logged, never retried. An opening is then
// marked notified.
func (s *Service) announce(ctx context.Context, ev Event) {
	rt, send := s.deps()
	for _, to := range ev.emails {
		mctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := send(mctx, mail.Message{To: to, Subject: "[Hosbah] " + ev.title(), Text: ev.text()}); err != nil {
			s.log.Warn("monitor email failed", "monitor", ev.Monitor.ID, "err", err)
		}
		cancel()
	}
	if ev.webhook != "" {
		if err := postWebhook(ctx, rt, ev); err != nil {
			s.log.Warn("monitor webhook failed", "monitor", ev.Monitor.ID, "err", clip(err.Error(), 300))
		}
	}
	if notify := s.notifier(); notify != nil && len(ev.channelIDs) > 0 {
		notify.DeliverToChannels(ctx, ev.teamID, ev.channelIDs, notifications.Alert{Event: "monitor." + ev.Event, Title: ev.title(),
			Reason: ev.Reason, Link: notifications.AppLink(ev.teamID, ev.App.ID), Subject: "app"})
	}
	if ev.Event != "opened" {
		return
	}
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true, TeamID: ev.teamID}, func(q *db.Queries, _ *sql.Tx) error {
		return q.MarkMonitorIncidentNotified(ctx, db.MarkMonitorIncidentNotifiedParams{ID: ev.incidentID, TeamID: ev.teamID})
	})
	if err != nil && ctx.Err() == nil {
		s.log.Warn("could not mark an incident notified", "incident", ev.incidentID, "err", err)
	}
}

// postWebhook delivers ev: https only, through the same transport as the checks (never with the
// private-address exception), no redirects, 5 s.
func postWebhook(ctx context.Context, rt http.RoundTripper, ev Event) error {
	if !strings.HasPrefix(ev.webhook, "https://") {
		return errors.New("webhook is not https")
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, WebhookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ev.webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	client := &http.Client{Transport: rt, Timeout: WebhookTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyRead))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook answered HTTP %d", resp.StatusCode)
	}
	return nil
}
