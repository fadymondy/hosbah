package monitors

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/pgtext"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

/*
Monitor actions — one implementation for the REST API and the user MCP. Like internal/deploy's,
every function runs in the CALLER's team-scoped transaction (q) after the caller checked the
member's role, names team_id in every statement and writes the audit entry on the same transaction.
*/

// Kinds and states.
const (
	KindHTTP      = "http"
	KindCPU       = "cpu"
	KindMemory    = "memory"
	KindDisk      = "disk"
	KindHeartbeat = "heartbeat"

	StateUnknown = "unknown"
	StateOK      = "ok"
	StateFailing = "failing"

	// MaxPerApp bounds an app's monitors.
	MaxPerApp = 20
	// MaxEmails bounds notify_emails.
	MaxEmails = 10
	// MaxChannels bounds notify_channel_ids.
	MaxChannels = 10
	// MinHeartbeatInterval / MaxHeartbeatInterval bound heartbeat_interval_seconds.
	MinHeartbeatInterval, MaxHeartbeatInterval = 30, 86400
	// MinHeartbeatGrace / MaxHeartbeatGrace bound heartbeat_grace_seconds.
	MinHeartbeatGrace, MaxHeartbeatGrace = 0, 86400
)

func invalid(msg string) error  { return &deploy.ActionError{Status: 422, Msg: msg} }
func conflict(msg string) error { return &deploy.ActionError{Status: 409, Msg: msg} }
func notFound() error           { return &deploy.ActionError{Status: 404, Msg: "monitor not found"} }

// OpenIncident is the incident a monitor has open.
type OpenIncident struct {
	ID       string    `json:"id"`
	OpenedAt time.Time `json:"opened_at"`
	Reason   string    `json:"reason"`
}

// Monitor is a monitor as the API shows it.
type Monitor struct {
	ID                       string        `json:"id"`
	AppID                    string        `json:"app_id"`
	Name                     string        `json:"name"`
	Kind                     string        `json:"kind" enum:"http,cpu,memory,disk,heartbeat"`
	Target                   string        `json:"target" doc:"http: the URL that is fetched; its host is one of the app's own hostnames. Empty otherwise."`
	ExpectedStatus           int           `json:"expected_status" doc:"http: the status a check must answer; 0 = any 2xx or 3xx."`
	Threshold                float64       `json:"threshold" doc:"http: the slowest acceptable response in ms (0 = no limit). cpu, memory, disk: percent."`
	WindowMinutes            int           `json:"window_minutes" doc:"cpu, memory, disk: the threshold is held against the average over this many minutes."`
	IntervalSeconds          int           `json:"interval_seconds"`
	Enabled                  bool          `json:"enabled"`
	NotifyEmails             []string      `json:"notify_emails"`
	NotifyWebhook            string        `json:"notify_webhook"`
	NotifyChannelIDs         []string      `json:"notify_channel_ids" doc:"Team notification channels (internal/notifications) this monitor's incidents also reach."`
	HeartbeatIntervalSeconds int           `json:"heartbeat_interval_seconds" doc:"kind heartbeat: how often a ping is expected."`
	HeartbeatGraceSeconds    int           `json:"heartbeat_grace_seconds" doc:"kind heartbeat: how late a ping may be before an incident opens."`
	PingURLPath              string        `json:"ping_url_path,omitempty" doc:"kind heartbeat only, and only right after it is created: POST (or GET) this path on the API origin to check in. Never shown again."`
	State                    string        `json:"state" enum:"unknown,ok,failing"`
	LastCheckedAt            *time.Time    `json:"last_checked_at"`
	LastPingAt               *time.Time    `json:"last_ping_at,omitempty" doc:"kind heartbeat: when the last check-in arrived."`
	LastError                string        `json:"last_error"`
	ConsecutiveFailures      int           `json:"consecutive_failures"`
	OpenIncident             *OpenIncident `json:"open_incident"`
	CreatedAt                time.Time     `json:"created_at"`
	UpdatedAt                time.Time     `json:"updated_at"`
}

func view(m db.Monitor, open *db.MonitorIncident) Monitor {
	out := Monitor{ID: m.ID, AppID: m.AppID, Name: m.Name, Kind: m.Kind, Target: m.Target, ExpectedStatus: int(m.ExpectedStatus),
		Threshold: m.Threshold, WindowMinutes: int(m.WindowMinutes), IntervalSeconds: int(m.IntervalSeconds), Enabled: m.Enabled,
		NotifyEmails: []string{}, NotifyWebhook: m.NotifyWebhook, NotifyChannelIDs: []string{},
		HeartbeatIntervalSeconds: int(m.HeartbeatIntervalSeconds), HeartbeatGraceSeconds: int(m.HeartbeatGraceSeconds),
		State: m.State, LastCheckedAt: m.LastCheckedAt, LastPingAt: m.LastPingAt, LastError: m.LastError,
		ConsecutiveFailures: int(m.ConsecutiveFailures), CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
	out.NotifyEmails = append(out.NotifyEmails, m.NotifyEmails...)
	out.NotifyChannelIDs = append(out.NotifyChannelIDs, m.NotifyChannelIds...)
	if open != nil {
		out.OpenIncident = &OpenIncident{ID: open.ID, OpenedAt: open.OpenedAt, Reason: open.Reason}
	}
	return out
}

// Input is a monitor's settings as a caller gives them. nil = not given (create: the default;
// update: unchanged). Kind is only read on create.
type Input struct {
	Name                     *string
	Kind                     string
	Target                   *string
	ExpectedStatus           *int
	Threshold                *float64
	WindowMinutes            *int
	IntervalSeconds          *int
	Enabled                  *bool
	NotifyEmails             *[]string
	NotifyWebhook            *string
	NotifyChannelIDs         *[]string
	HeartbeatIntervalSeconds *int
	HeartbeatGraceSeconds    *int
}

// spec is a complete, validated set of settings.
type spec struct {
	Name, Kind, Target, Webhook       string
	ExpectedStatus, Window, Interval  int
	Threshold                         float64
	Enabled                           bool
	Emails, Channels                  []string
	HeartbeatInterval, HeartbeatGrace int
}

func (in Input) over(s spec) spec {
	if in.Name != nil {
		s.Name = *in.Name
	}
	if in.Target != nil {
		s.Target = *in.Target
	}
	if in.ExpectedStatus != nil {
		s.ExpectedStatus = *in.ExpectedStatus
	}
	if in.Threshold != nil {
		s.Threshold = *in.Threshold
	}
	if in.WindowMinutes != nil {
		s.Window = *in.WindowMinutes
	}
	if in.IntervalSeconds != nil {
		s.Interval = *in.IntervalSeconds
	}
	if in.Enabled != nil {
		s.Enabled = *in.Enabled
	}
	if in.NotifyEmails != nil {
		s.Emails = *in.NotifyEmails
	}
	if in.NotifyWebhook != nil {
		s.Webhook = *in.NotifyWebhook
	}
	if in.NotifyChannelIDs != nil {
		s.Channels = *in.NotifyChannelIDs
	}
	if in.HeartbeatIntervalSeconds != nil {
		s.HeartbeatInterval = *in.HeartbeatIntervalSeconds
	}
	if in.HeartbeatGraceSeconds != nil {
		s.HeartbeatGrace = *in.HeartbeatGraceSeconds
	}
	return s
}

// TargetHost is the hostname of an http monitor's target, or an error saying why the URL cannot be
// one. It does not decide whether the host is the app's: see hostOf.
func TargetHost(target string) (string, error) {
	if len(target) > 500 {
		return "", errors.New("target is longer than 500 characters")
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "", errors.New("target must be an http:// or https:// URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("target must be an http:// or https:// URL")
	}
	if u.User != nil {
		return "", errors.New("target must not carry credentials")
	}
	if u.Fragment != "" {
		return "", errors.New("target must not carry a fragment")
	}
	return deploy.NormalizeHostname(u.Hostname()), nil
}

// validate normalises s against the app's own hostnames.
func validate(s spec, hostnames []string) (spec, error) {
	s.Name = strings.TrimSpace(s.Name)
	if n := utf8.RuneCountInString(s.Name); n < 1 || n > 80 {
		return s, invalid("name must be 1 to 80 characters")
	}
	if strings.IndexFunc(s.Name, unicode.IsControl) >= 0 {
		return s, invalid("name must not contain control characters")
	}
	if s.Interval == 0 {
		s.Interval = 60
	}
	if s.Interval < 30 || s.Interval > 3600 {
		return s, invalid("interval_seconds must be between 30 and 3600")
	}
	switch s.Kind {
	case KindHTTP:
		s.Target = strings.TrimSpace(s.Target)
		host, err := TargetHost(s.Target)
		if err != nil {
			return s, invalid(err.Error())
		}
		// The SSRF rule of writes: Hosbah only ever fetches the app's own hostnames.
		own := false
		for _, h := range hostnames {
			own = own || h == host
		}
		if !own {
			return s, invalid("target must be on one of the app's own hostnames")
		}
		s.Target = lowerScheme(s.Target)
		if s.ExpectedStatus != 0 && (s.ExpectedStatus < 100 || s.ExpectedStatus > 599) {
			return s, invalid("expected_status must be 0 (any 2xx or 3xx) or an HTTP status")
		}
		if s.Threshold < 0 || s.Threshold > 60000 {
			return s, invalid("threshold is the slowest acceptable response in ms: 0 (no limit) to 60000")
		}
		s.Window = 5 // unused by http
	case KindCPU, KindMemory, KindDisk:
		if strings.TrimSpace(s.Target) != "" {
			return s, invalid("a " + s.Kind + " monitor has no target: it reads the app's server's metrics")
		}
		s.Target = ""
		if s.Threshold < 1 || s.Threshold > 100 {
			return s, invalid("threshold is a percentage: 1 to 100")
		}
		if s.Window == 0 {
			s.Window = 5
		}
		if s.Window < 1 || s.Window > 60 {
			return s, invalid("window_minutes must be between 1 and 60")
		}
		s.ExpectedStatus = 200 // unused by resource kinds
	case KindHeartbeat:
		if strings.TrimSpace(s.Target) != "" {
			return s, invalid("a heartbeat monitor has no target: it waits for a ping")
		}
		s.Target = ""
		s.ExpectedStatus, s.Threshold, s.Window = 200, 0, 5 // unused by heartbeat
		if s.HeartbeatInterval == 0 {
			s.HeartbeatInterval = 300
		}
		if s.HeartbeatInterval < MinHeartbeatInterval || s.HeartbeatInterval > MaxHeartbeatInterval {
			return s, invalid(fmt.Sprintf("heartbeat_interval_seconds must be between %d and %d", MinHeartbeatInterval, MaxHeartbeatInterval))
		}
		if s.HeartbeatGrace == 0 {
			s.HeartbeatGrace = 120
		}
		if s.HeartbeatGrace < MinHeartbeatGrace || s.HeartbeatGrace > MaxHeartbeatGrace {
			return s, invalid(fmt.Sprintf("heartbeat_grace_seconds must be between %d and %d", MinHeartbeatGrace, MaxHeartbeatGrace))
		}
		s.Interval = 60 // unused by heartbeat (it is never claimed by ClaimDueMonitors)
	default:
		return s, invalid("kind must be one of http, cpu, memory, disk, heartbeat")
	}

	if len(s.Emails) > MaxEmails {
		return s, invalid(fmt.Sprintf("notify_emails holds at most %d addresses", MaxEmails))
	}
	if len(s.Channels) > MaxChannels {
		return s, invalid(fmt.Sprintf("notify_channel_ids holds at most %d channels", MaxChannels))
	}
	{
		seen := map[string]bool{}
		ids := []string{}
		for _, id := range s.Channels {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		s.Channels = ids
	}
	seen := map[string]bool{}
	emails := []string{}
	for _, e := range s.Emails {
		e = strings.ToLower(strings.TrimSpace(e))
		a, err := mail.ParseAddress(e)
		if err != nil || a.Address != e || len(e) > 254 || strings.ContainsAny(e, " \r\n\t,;<>") || !strings.Contains(e[strings.LastIndex(e, "@")+1:], ".") {
			return s, invalid("notify_emails: that is not an email address")
		}
		if !seen[e] {
			seen[e] = true
			emails = append(emails, e)
		}
	}
	s.Emails = emails

	s.Webhook = strings.TrimSpace(s.Webhook)
	if s.Webhook != "" {
		u, err := url.Parse(s.Webhook)
		switch {
		case len(s.Webhook) > 500:
			return s, invalid("notify_webhook is longer than 500 characters")
		case err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Opaque != "":
			return s, invalid("notify_webhook must be an https:// URL")
		case u.User != nil:
			return s, invalid("notify_webhook must not carry credentials")
		}
		if ip := net.ParseIP(u.Hostname()); ip != nil && addrClass(ip) != "public" {
			return s, invalid("notify_webhook must not be a private address")
		}
		s.Webhook = lowerScheme(s.Webhook)
	}
	return s, nil
}

func hostnamesOf(ctx context.Context, q *db.Queries, teamID string, a db.App) ([]string, error) {
	rows, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, d := range rows {
		out = append(out, d.Hostname)
	}
	return out, nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "monitors_app_name_key")
}

// List is the app's monitors, each with its open incident.
func List(ctx context.Context, q *db.Queries, teamID string, a db.App) ([]Monitor, error) {
	rows, err := q.ListMonitorsOfApp(ctx, db.ListMonitorsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	open, err := q.OpenIncidentsOfApp(ctx, db.OpenIncidentsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	by := map[string]*db.MonitorIncident{}
	for i := range open {
		by[open[i].MonitorID] = &open[i]
	}
	out := make([]Monitor, 0, len(rows))
	for _, m := range rows {
		out = append(out, view(m, by[m.ID]))
	}
	return out, nil
}

// Find resolves ref (id or name) to one of the app's monitors.
func Find(ctx context.Context, q *db.Queries, teamID string, a db.App, ref string) (db.Monitor, error) {
	m, err := q.MonitorOfApp(ctx, db.MonitorOfAppParams{AppID: a.ID, TeamID: teamID, Ref: strings.TrimSpace(ref)})
	if err != nil {
		return db.Monitor{}, notFound()
	}
	return m, nil
}

// View is m with its open incident.
func View(ctx context.Context, q *db.Queries, m db.Monitor) (Monitor, error) {
	open, err := q.OpenIncidentOfMonitor(ctx, db.OpenIncidentOfMonitorParams{MonitorID: m.ID, TeamID: m.TeamID})
	if errors.Is(err, sql.ErrNoRows) {
		return view(m, nil), nil
	}
	if err != nil {
		return Monitor{}, err
	}
	return view(m, &open), nil
}

// Create adds a monitor to the app. kind heartbeat mints a ping token; the plaintext (PingURLPath)
// is returned once, in this call's result only — nothing but its hash is ever stored. Audited.
func Create(ctx context.Context, q *db.Queries, teamID string, actor deploy.Actor, a db.App, in Input) (Monitor, error) {
	hosts, err := hostnamesOf(ctx, q, teamID, a)
	if err != nil {
		return Monitor{}, err
	}
	s, err := validate(in.over(spec{Kind: strings.TrimSpace(in.Kind), ExpectedStatus: 200, Enabled: true}), hosts)
	if err != nil {
		return Monitor{}, err
	}
	if err := channelsExist(ctx, q, teamID, s.Channels); err != nil {
		return Monitor{}, err
	}
	if n, err := q.CountMonitorsOfApp(ctx, db.CountMonitorsOfAppParams{AppID: a.ID, TeamID: teamID}); err != nil {
		return Monitor{}, err
	} else if n >= MaxPerApp {
		return Monitor{}, conflict(fmt.Sprintf("an app holds at most %d monitors", MaxPerApp))
	}
	// A savepoint-free uniqueness check: the unique index is the authority, this is the message.
	if _, err := q.MonitorOfApp(ctx, db.MonitorOfAppParams{AppID: a.ID, TeamID: teamID, Ref: s.Name}); err == nil {
		return Monitor{}, conflict("the app already has a monitor with that name")
	}
	plainToken := ""
	var tokenHash *string
	if s.Kind == KindHeartbeat {
		var hash string
		plainToken, hash = tokens.New(tokens.PrefixPing)
		tokenHash = &hash
	}
	m, err := q.InsertMonitor(ctx, db.InsertMonitorParams{TeamID: teamID, AppID: a.ID, Name: s.Name, Kind: s.Kind, Target: s.Target,
		ExpectedStatus: int64(s.ExpectedStatus), Threshold: s.Threshold, WindowMinutes: int64(s.Window), IntervalSeconds: int64(s.Interval),
		Enabled: s.Enabled, NotifyEmails: pgtext.Array(s.Emails), NotifyWebhook: s.Webhook, NotifyChannelIds: pgtext.Array(s.Channels),
		HeartbeatIntervalSeconds: int64(s.HeartbeatInterval), HeartbeatGraceSeconds: int64(s.HeartbeatGrace), PingTokenHash: tokenHash})
	if isUnique(err) {
		return Monitor{}, conflict("the app already has a monitor with that name")
	}
	if err != nil {
		return Monitor{}, err
	}
	out := view(m, nil)
	if plainToken != "" {
		out.PingURLPath = PingPath(plainToken)
	}
	return out, tenancy.Record(ctx, q, teamID, actor.UserID, "monitor.created", "app", a.ID,
		auditOf(actor, map[string]any{"monitor": m.ID, "name": m.Name, "kind": m.Kind, "target": m.Target}))
}

// PingPath is the path (on the API origin, outside /api — see internal/monitors.MountPublic) a
// heartbeat's plaintext ping token checks in at.
func PingPath(plainToken string) string { return "/hooks/heartbeat/" + plainToken }

// channelsExist checks that every id in ids names one of the team's notification channels — a 422,
// not a silent drop, so a typo is caught at create/update time.
func channelsExist(ctx context.Context, q *db.Queries, teamID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.NotificationChannelsByIDsInTeam(ctx, db.NotificationChannelsByIDsInTeamParams{TeamID: teamID, Ids: strings.Join(ids, ",")})
	if err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return invalid("notify_channel_ids: one or more of those channels do not exist")
	}
	return nil
}

// Update changes a monitor's settings (never its kind). Changing the rule, or switching the
// monitor off, forgets what the evaluator learned and closes its open incident quietly. Audited.
func Update(ctx context.Context, q *db.Queries, teamID string, actor deploy.Actor, a db.App, m db.Monitor, in Input, now time.Time) (Monitor, error) {
	hosts, err := hostnamesOf(ctx, q, teamID, a)
	if err != nil {
		return Monitor{}, err
	}
	old := spec{Name: m.Name, Kind: m.Kind, Target: m.Target, Webhook: m.NotifyWebhook, ExpectedStatus: int(m.ExpectedStatus),
		Window: int(m.WindowMinutes), Interval: int(m.IntervalSeconds), Threshold: m.Threshold, Enabled: m.Enabled, Emails: m.NotifyEmails,
		Channels: m.NotifyChannelIds, HeartbeatInterval: int(m.HeartbeatIntervalSeconds), HeartbeatGrace: int(m.HeartbeatGraceSeconds)}
	s, err := validate(in.over(old), hosts)
	if err != nil {
		return Monitor{}, err
	}
	if err := channelsExist(ctx, q, teamID, s.Channels); err != nil {
		return Monitor{}, err
	}
	if s.Name != m.Name {
		if other, err := q.MonitorOfApp(ctx, db.MonitorOfAppParams{AppID: a.ID, TeamID: teamID, Ref: s.Name}); err == nil && other.ID != m.ID {
			return Monitor{}, conflict("the app already has a monitor with that name")
		}
	}
	reset := s.Target != old.Target || s.ExpectedStatus != old.ExpectedStatus || s.Threshold != old.Threshold || s.Window != old.Window ||
		(old.Enabled && !s.Enabled)
	up, err := q.UpdateMonitorInTeam(ctx, db.UpdateMonitorInTeamParams{ID: m.ID, TeamID: teamID, Name: s.Name, Target: s.Target,
		ExpectedStatus: int64(s.ExpectedStatus), Threshold: s.Threshold, WindowMinutes: int64(s.Window), IntervalSeconds: int64(s.Interval),
		Enabled: s.Enabled, NotifyEmails: pgtext.Array(s.Emails), NotifyWebhook: s.Webhook, NotifyChannelIds: pgtext.Array(s.Channels),
		HeartbeatIntervalSeconds: int64(s.HeartbeatInterval), HeartbeatGraceSeconds: int64(s.HeartbeatGrace), Reset: reset})
	if isUnique(err) {
		return Monitor{}, conflict("the app already has a monitor with that name")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Monitor{}, notFound()
	}
	if err != nil {
		return Monitor{}, err
	}
	if reset {
		if err := q.ResolveIncidentsOfMonitor(ctx, db.ResolveIncidentsOfMonitorParams{MonitorID: m.ID, TeamID: teamID, ResolvedAt: now}); err != nil {
			return Monitor{}, err
		}
	}
	out, err := View(ctx, q, up)
	if err != nil {
		return Monitor{}, err
	}
	return out, tenancy.Record(ctx, q, teamID, actor.UserID, "monitor.updated", "app", a.ID,
		auditOf(actor, map[string]any{"monitor": m.ID, "name": up.Name, "enabled": up.Enabled}))
}

// Delete removes a monitor and its incidents. Audited.
func Delete(ctx context.Context, q *db.Queries, teamID string, actor deploy.Actor, a db.App, m db.Monitor) error {
	n, err := q.DeleteMonitorInTeam(ctx, db.DeleteMonitorInTeamParams{ID: m.ID, TeamID: teamID})
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound()
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "monitor.deleted", "app", a.ID,
		auditOf(actor, map[string]any{"monitor": m.ID, "name": m.Name, "kind": m.Kind}))
}

func auditOf(a deploy.Actor, extra map[string]any) map[string]any {
	if a.Via != "" && a.Via != "web" {
		extra["via"] = a.Via
	}
	if a.TokenID != "" {
		extra["mcp_token_id"] = a.TokenID
	}
	return extra
}

// ---- incidents ------------------------------------------------------------------------------------

// MonitorRef names the monitor of an incident.
type MonitorRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind" enum:"http,cpu,memory,disk"`
}

// Incident is one stretch of a monitor failing.
type Incident struct {
	ID         string     `json:"id"`
	Monitor    MonitorRef `json:"monitor"`
	AppID      string     `json:"app_id"`
	OpenedAt   time.Time  `json:"opened_at"`
	ResolvedAt *time.Time `json:"resolved_at" doc:"null while the incident is open."`
	Reason     string     `json:"reason" doc:"Why it opened."`
	LastValue  string     `json:"last_value" doc:"The latest reading while open; the reading that resolved it afterwards."`
	Notified   bool       `json:"notified" doc:"The opening was announced (team notification, emails, webhook)."`
}

// IncidentFeed is a page of an app's incidents.
type IncidentFeed struct {
	Incidents  []Incident `json:"incidents"`
	NextCursor *string    `json:"next_cursor" doc:"Pass as before= for the next page; null on the last page."`
}

func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", deploy.ErrBadCursor
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	at, err := time.Parse(time.RFC3339Nano, ts)
	if !ok || err != nil || id == "" {
		return time.Time{}, "", deploy.ErrBadCursor
	}
	return at, id, nil
}

// Incidents is the app's incidents, newest first (team scope). A bad cursor is deploy.ErrBadCursor.
func Incidents(ctx context.Context, q *db.Queries, teamID string, a db.App, limit int, before string) (IncidentFeed, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	p := db.AppIncidentFeedParams{AppID: a.ID, TeamID: teamID, LimitRows: int32(limit + 1)} //nolint:gosec // bounded above
	if before != "" {
		at, id, err := decodeCursor(before)
		if err != nil {
			return IncidentFeed{}, err
		}
		p.BeforeAt, p.BeforeID = &at, id
	}
	rows, err := q.AppIncidentFeed(ctx, p)
	if err != nil {
		return IncidentFeed{}, err
	}
	out := IncidentFeed{Incidents: []Incident{}}
	for i, r := range rows {
		if i == limit {
			last := rows[limit-1]
			c := encodeCursor(last.OpenedAt, last.ID)
			out.NextCursor = &c
			break
		}
		out.Incidents = append(out.Incidents, Incident{ID: r.ID, Monitor: MonitorRef{ID: r.MonitorID, Name: r.MonitorName, Kind: r.MonitorKind},
			AppID: r.AppID, OpenedAt: r.OpenedAt, ResolvedAt: r.ResolvedAt, Reason: r.Reason, LastValue: r.LastValue, Notified: r.Notified})
	}
	return out, nil
}

// lowerScheme: url.Parse accepts HTTPS://, the table's checks (and the webhook's https rule) do not.
func lowerScheme(u string) string {
	if i := strings.Index(u, "://"); i > 0 {
		return strings.ToLower(u[:i]) + u[i:]
	}
	return u
}
