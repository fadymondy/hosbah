// Package push sends Hosbah's alerts to the phone and desktop apps (docs/apps-plan.md).
//
// Every place that already tells a team something (a deploy finished, a monitor opened or
// resolved, a certificate is expiring, a backup failed, a server went quiet or ran hot) calls
// Notify, or Record when it has not written the bell's row itself. Notify fans out in the
// background: the team's members, minus those who muted the event, to every install of theirs with
// a push token. A token FCM reports dead is forgotten.
//
// The package is a process-wide service (Set at boot, Current everywhere else) so the many callers
// need no new constructor argument; with none set, Notify and Record do nothing.
package push

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// The events a person can switch off (GET/PUT /api/me/notification-prefs).
const (
	EventDeploySucceeded = "deploy.succeeded"
	EventDeployFailed    = "deploy.failed"
	EventAppDown         = "app.down"
	EventAppUp           = "app.up"
	EventResourceHigh    = "resource.high"
	EventServerOffline   = "server.offline"
	EventCertExpiring    = "cert.expiring"
	EventBackupFailed    = "backup.failed"
)

// Events lists them in the order the apps show them.
var Events = []string{
	EventDeploySucceeded, EventDeployFailed, EventAppDown, EventAppUp,
	EventResourceHigh, EventServerOffline, EventCertExpiring, EventBackupFailed,
}

// KnownEvent reports whether e is one of Events.
func KnownEvent(e string) bool {
	for _, k := range Events {
		if k == e {
			return true
		}
	}
	return false
}

// Scheme is the apps' deep-link scheme: hosbah://t/<team>/<link>.
const Scheme = "hosbah"

// Event is one alert for one team.
type Event struct {
	TeamID string
	Kind   string // one of Events
	// Title and Body in English; TitleAR / BodyAR for Arabic installs (English when empty).
	Title, Body     string
	TitleAR, BodyAR string
	// SubjectType / SubjectID: what the bell row points at ("deployment", "app", "server", ...).
	SubjectType, SubjectID string
	// Link is where a tap lands inside the team: "apps/<id>", "servers/<id>", "domains",
	// "backups", "alerts" (the default).
	Link string
	// Tag collapses repeats on the phone; default: the kind's family plus the subject, so an
	// app's "down" banner is replaced by its "up" one.
	Tag string
}

// URL is the deep link the apps open on tap.
func (ev Event) URL() string {
	link := strings.Trim(ev.Link, "/")
	if link == "" {
		link = "alerts"
	}
	return Scheme + "://t/" + ev.TeamID + "/" + link
}

func (ev Event) tag() string {
	if ev.Tag != "" {
		return ev.Tag
	}
	family, _, _ := strings.Cut(ev.Kind, ".")
	if ev.SubjectID == "" {
		return ev.Kind
	}
	return family + ":" + ev.SubjectID
}

// Message is ev as one install sees it.
func (ev Event) Message(locale string) Message {
	title, body := ev.Title, ev.Body
	if locale == "ar" {
		if ev.TitleAR != "" {
			title = ev.TitleAR
		}
		if ev.BodyAR != "" {
			body = ev.BodyAR
		}
	}
	return Message{
		Title: title, Body: clip(body, 400), Tag: ev.tag(),
		Data: map[string]string{
			"team": ev.TeamID, "kind": ev.Kind, "subject_type": ev.SubjectType, "subject_id": ev.SubjectID,
			"url": ev.URL(),
		},
	}
}

// Service delivers events. Build it with New and install it with Set.
type Service struct {
	db     *sql.DB
	sender Sender // nil: push is off (the bell and the desktop stream still work)
	log    *slog.Logger
	// publish wakes the team's live streams after Record writes a bell row (ops.TeamTopic).
	publish func(teamID string)
	// channels hands an event to the team's notification channels (email, Slack, ...); set at
	// boot to notifications.Service.DeliverEvent. Only Record's callers use it, and only for the
	// events channels know.
	channels func(ctx context.Context, ev Event)

	wg      sync.WaitGroup
	sweeper *sweeper
}

// New builds the service. sender may be nil.
func New(sqlDB *sql.DB, sender Sender, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: sqlDB, sender: sender, log: log}
}

// SetPublisher installs the team stream wake-up.
func (s *Service) SetPublisher(fn func(teamID string)) { s.publish = fn }

// SetChannels installs the notification-channel hand-off.
func (s *Service) SetChannels(fn func(ctx context.Context, ev Event)) { s.channels = fn }

// Enabled reports whether push messages can actually be sent.
func (s *Service) Enabled() bool { return s != nil && s.sender != nil }

var current atomic.Pointer[Service]

// Set installs the process-wide service (nil uninstalls it).
func Set(s *Service) { current.Store(s) }

// Current is the process-wide service, or nil.
func Current() *Service { return current.Load() }

// Notify sends ev with the process-wide service, in the background.
func Notify(ev Event) { Current().Notify(ev) }

// Record writes ev as a bell row too, then notifies (see Service.Record).
func Record(ctx context.Context, ev Event) { Current().Record(ctx, ev) }

// Notify sends ev to the team's devices in the background; it never blocks its caller.
func (s *Service) Notify(ev Event) {
	if !s.Enabled() || ev.TeamID == "" || ev.Kind == "" {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := s.Deliver(ctx, ev); err != nil {
			s.log.Warn("push: delivery failed", "team", ev.TeamID, "kind", ev.Kind, "err", err)
		}
	}()
}

// Record is for the alerts that had no bell row of their own: it writes one (so the web bell and
// the apps' Alerts list show it), wakes the team's streams and pushes it.
func (s *Service) Record(ctx context.Context, ev Event) {
	if s == nil || ev.TeamID == "" {
		return
	}
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true, TeamID: ev.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		return q.InsertNotification(ctx, db.InsertNotificationParams{
			TeamID: ev.TeamID, Kind: ev.Kind, Title: clip(ev.Title, 300),
			Body: optional(clip(ev.Body, 2000)), SubjectType: optional(ev.SubjectType), SubjectID: optional(ev.SubjectID),
		})
	})
	if err != nil {
		s.log.Warn("push: could not write the team notification", "team", ev.TeamID, "kind", ev.Kind, "err", err)
	} else if s.publish != nil {
		s.publish(ev.TeamID)
	}
	s.Notify(ev)
}

// Channels hands ev to the team's notification channels when a hand-off is installed.
func (s *Service) Channels(ctx context.Context, ev Event) {
	if s != nil && s.channels != nil {
		s.channels(ctx, ev)
	}
}

// Deliver sends ev now and returns how many installs took it.
func (s *Service) Deliver(ctx context.Context, ev Event) (int, error) {
	if !s.Enabled() {
		return 0, nil
	}
	var targets []db.PushTargetsOfTeamRow
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true, TeamID: ev.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		targets, err = q.PushTargetsOfTeam(ctx, db.PushTargetsOfTeamParams{TeamID: ev.TeamID, Kind: ev.Kind})
		return err
	})
	if err != nil {
		return 0, err
	}
	sent := 0
	var errs []error
	for _, t := range targets {
		ok, err := s.sendOne(ctx, t.PushToken, ev.Message(t.Locale))
		if ok {
			sent++
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return sent, errors.Join(errs...)
}

// ErrNoToken: the device has not registered for push yet.
var ErrNoToken = errors.New("this device has no push token yet")

// ErrDisabled: the server has no FCM credentials.
var ErrDisabled = errors.New("push notifications are not configured on this server")

// SendTest sends a test notification to one of the user's own devices.
func (s *Service) SendTest(ctx context.Context, userID, deviceID string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	var rows []db.PushTargetsOfDeviceRow
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{UserID: userID}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		rows, err = q.PushTargetsOfDevice(ctx, db.PushTargetsOfDeviceParams{ID: deviceID, UserID: userID})
		return err
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return ErrNoToken
	}
	ev := Event{Kind: "test", Title: "Hosbah notifications work", Body: "You will get alerts on this device.",
		TitleAR: "تنبيهات حوسبة تعمل", BodyAR: "ستصلك التنبيهات على هذا الجهاز.", Tag: "test"}
	m := ev.Message(rows[0].Locale)
	m.Data["url"] = Scheme + "://alerts"
	ok, err := s.sendOne(ctx, rows[0].PushToken, m)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoToken // FCM said the token is dead; it is forgotten now
	}
	return nil
}

// sendOne sends one message; a dead token is dropped from its device.
func (s *Service) sendOne(ctx context.Context, token string, m Message) (bool, error) {
	res, err := s.sender.Send(ctx, token, m)
	switch res {
	case SentOK:
		return true, nil
	case SentGone:
		if derr := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
			return q.DropPushToken(ctx, &token)
		}); derr != nil {
			s.log.Warn("push: could not forget a dead token", "err", derr)
		}
		return false, nil
	default:
		return false, err
	}
}

// Wait blocks until the background deliveries in flight are done (shutdown and tests).
func (s *Service) Wait() {
	if s != nil {
		s.wg.Wait()
	}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
