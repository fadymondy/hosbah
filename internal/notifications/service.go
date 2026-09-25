// Package notifications is a team's alert channels (MH-575 H2): email, Slack, Discord and generic
// webhook destinations, and the delivery that reaches them. A channel's URL and its optional HMAC
// secret are sealed with the team's DEK (internal/crypto) and never read back in full — only a
// masked hint. Delivery goes through the same SSRF-safe transport the monitor checks use
// (internal/monitors.SafeTransport), passed in by the caller rather than imported, so this package
// never needs to import internal/monitors (which itself calls into this one to route a monitor's
// incidents to its channels).
package notifications

import (
	"bytes"
	"context"
	"crypto/hmac"
	crand "crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/app"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	m "github.com/fadymondy/hosbah/internal/mail"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/pgtext"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Kinds.
const (
	KindEmail   = "email"
	KindSlack   = "slack"
	KindDiscord = "discord"
	KindWebhook = "webhook"

	// MaxEmails bounds an email channel's address list.
	MaxEmails = 10
	// MaxPerTeam bounds a team's channels.
	MaxPerTeam = 30
	// DeliverTimeout bounds one channel's delivery.
	DeliverTimeout = 5 * time.Second
	// UserAgent of a delivered request.
	UserAgent = "Hosbah-Notifications"
	// SignatureHeader carries the webhook kind's HMAC-SHA256 of the body, hex-encoded, when the
	// channel has a signing secret.
	SignatureHeader = "X-Hosbah-Signature"
)

// Event names a team may subscribe a channel to.
const (
	EventDeployFailed  = "deploy.failed"
	EventServerOffline = "server.offline"
	EventBackupFailed  = "backup.failed"
	// EventCertExpiring (MH-580 J6): a team TLS certificate is 14, 7 or 1 day(s) from expiry, or a
	// check failed outright (internal/certcheck).
	EventCertExpiring = "cert.expiring"
)

// Events is every event name a channel may subscribe to.
var Events = []string{EventDeployFailed, EventServerOffline, EventBackupFailed, EventCertExpiring}

func invalid(msg string) error  { return &deploy.ActionError{Status: 422, Msg: msg} }
func conflict(msg string) error { return &deploy.ActionError{Status: 409, Msg: msg} }
func notFound() error           { return &deploy.ActionError{Status: 404, Msg: "notification channel not found"} }

// Service delivers to a team's channels. The CRUD helpers below (List, Find, Create, Update,
// Delete) take a *db.Queries directly, like internal/deploy and internal/monitors, so the REST
// and MCP layers run them in the caller's own team-scoped transaction.
type Service struct {
	a         *app.App
	ops       *ops.Service
	log       *slog.Logger
	transport http.RoundTripper
}

// New builds the service. transport nil = http.DefaultTransport (the caller — internal/server —
// always passes monitors.SafeTransport(nil), the same SSRF-safe dialer checks use).
func New(a *app.App, o *ops.Service, transport http.RoundTripper) *Service {
	log := a.Log
	if log == nil {
		log = slog.Default()
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Service{a: a, ops: o, log: log.With("component", "notifications"), transport: transport}
}

// Channel is a notification channel as the API shows it. The url and secret are never returned in
// full — only URLHint / SecretSet.
type Channel struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind" enum:"email,slack,discord,webhook"`
	Name      string    `json:"name"`
	Emails    []string  `json:"emails" doc:"kind email: who is notified."`
	URLHint   string    `json:"url_hint" doc:"kind slack, discord, webhook: the configured URL, masked to its host. Empty for email."`
	SecretSet bool      `json:"secret_set" doc:"kind webhook: an HMAC-SHA256 signing secret is configured."`
	Events    []string  `json:"events" doc:"Which of deploy.failed, server.offline, backup.failed, cert.expiring this channel receives."`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func maskURL(u string) string {
	if u == "" {
		return ""
	}
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return "***"
	}
	return p.Scheme + "://" + p.Host + "/***"
}

func view(c db.NotificationChannel, dek []byte, teamID string) Channel {
	out := Channel{ID: c.ID, Kind: c.Kind, Name: c.Name, Emails: []string{}, Events: []string{}, Enabled: c.Enabled,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	out.Emails = append(out.Emails, c.Emails...)
	out.Events = append(out.Events, c.Events...)
	if c.UrlSealed != nil && dek != nil {
		if plain, err := crypto.Open(dek, teamID, urlContext(c.ID), *c.UrlSealed); err == nil {
			out.URLHint = maskURL(string(plain))
		} else {
			out.URLHint = "***"
		}
	}
	out.SecretSet = c.SecretSealed != nil
	return out
}

func urlContext(id string) string    { return "notification-channel-url:" + id }
func secretContext(id string) string { return "notification-channel-secret:" + id }

// Input is a channel's settings as a caller gives them. nil = not given (create: the default;
// update: unchanged).
type Input struct {
	Name        *string
	Kind        string // create only
	Emails      *[]string
	URL         *string // "" clears it (webhook/slack/discord only ever set, never truly empty)
	Secret      *string
	ClearSecret bool
	Events      *[]string
	Enabled     *bool
}

type spec struct {
	Name, URL, Secret string
	ClearSecret       bool
	Emails, Events    []string
	Enabled           bool
}

func (in Input) over(s spec) spec {
	if in.Name != nil {
		s.Name = *in.Name
	}
	if in.Emails != nil {
		s.Emails = *in.Emails
	}
	if in.URL != nil {
		s.URL = *in.URL
	}
	if in.Secret != nil {
		s.Secret = *in.Secret
	}
	s.ClearSecret = in.ClearSecret
	if in.Events != nil {
		s.Events = *in.Events
	}
	if in.Enabled != nil {
		s.Enabled = *in.Enabled
	}
	return s
}

func validate(kind string, s spec) (spec, error) {
	s.Name = strings.TrimSpace(s.Name)
	if n := len([]rune(s.Name)); n < 1 || n > 80 {
		return s, invalid("name must be 1 to 80 characters")
	}
	seen := map[string]bool{}
	events := []string{}
	for _, e := range s.Events {
		ok := false
		for _, k := range Events {
			ok = ok || k == e
		}
		if !ok {
			return s, invalid("events: " + e + " is not one of " + strings.Join(Events, ", "))
		}
		if !seen[e] {
			seen[e] = true
			events = append(events, e)
		}
	}
	s.Events = events

	switch kind {
	case KindEmail:
		if len(s.Emails) == 0 {
			return s, invalid("emails must hold at least one address")
		}
		if len(s.Emails) > MaxEmails {
			return s, invalid(fmt.Sprintf("emails holds at most %d addresses", MaxEmails))
		}
		eseen := map[string]bool{}
		emails := []string{}
		for _, e := range s.Emails {
			e = strings.ToLower(strings.TrimSpace(e))
			a, err := mail.ParseAddress(e)
			if err != nil || a.Address != e || len(e) > 254 {
				return s, invalid("emails: that is not an email address")
			}
			if !eseen[e] {
				eseen[e] = true
				emails = append(emails, e)
			}
		}
		s.Emails = emails
		s.URL, s.Secret = "", ""
	case KindSlack, KindDiscord, KindWebhook:
		s.Emails = nil
		s.URL = strings.TrimSpace(s.URL)
		if s.URL == "" {
			return s, invalid("url is required")
		}
		if len(s.URL) > 500 {
			return s, invalid("url is longer than 500 characters")
		}
		u, err := url.Parse(s.URL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Opaque != "" || u.User != nil {
			return s, invalid("url must be an https:// URL")
		}
		if kind != KindWebhook && len(s.Secret) > 0 {
			return s, invalid("a signing secret only applies to kind webhook")
		}
		if len(s.Secret) > 200 {
			return s, invalid("secret is longer than 200 characters")
		}
	default:
		return s, invalid("kind must be one of email, slack, discord, webhook")
	}
	return s, nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "notification_channels_team_name_key")
}

// List is the team's channels.
func List(ctx context.Context, q *db.Queries, teamID string, dek []byte) ([]Channel, error) {
	rows, err := q.ListNotificationChannelsOfTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make([]Channel, 0, len(rows))
	for _, c := range rows {
		out = append(out, view(c, dek, teamID))
	}
	return out, nil
}

// Find resolves a channel by id within the team.
func Find(ctx context.Context, q *db.Queries, teamID, id string) (db.NotificationChannel, error) {
	c, err := q.NotificationChannelInTeam(ctx, db.NotificationChannelInTeamParams{ID: id, TeamID: teamID})
	if err != nil {
		return db.NotificationChannel{}, notFound()
	}
	return c, nil
}

// Create adds a channel. dek is the team's own (internal/ops.Service.TeamDEK). Audited by the
// caller like internal/deploy's actions (the REST/MCP layer records the audit entry).
func Create(ctx context.Context, q *db.Queries, teamID, actorID string, dek []byte, in Input) (Channel, error) {
	kind := strings.TrimSpace(in.Kind)
	s, err := validate(kind, in.over(spec{Enabled: true}))
	if err != nil {
		return Channel{}, err
	}
	if n, err := q.ListNotificationChannelsOfTeam(ctx, teamID); err != nil {
		return Channel{}, err
	} else if len(n) >= MaxPerTeam {
		return Channel{}, conflict(fmt.Sprintf("a team holds at most %d notification channels", MaxPerTeam))
	}
	id := newUUID()
	p := db.InsertNotificationChannelParams{ID: id, TeamID: teamID, Kind: kind, Name: s.Name, Emails: pgtext.Array(nilToEmpty(s.Emails)),
		Events: pgtext.Array(nilToEmpty(s.Events)), Enabled: s.Enabled, CreatedBy: &actorID}
	if kind != KindEmail {
		sealed, err := crypto.Seal(dek, teamID, urlContext(id), []byte(s.URL))
		if err != nil {
			return Channel{}, err
		}
		p.UrlSealed = &sealed
		if s.Secret != "" {
			sec, err := crypto.Seal(dek, teamID, secretContext(id), []byte(s.Secret))
			if err != nil {
				return Channel{}, err
			}
			p.SecretSealed = &sec
		}
	}
	c, err := q.InsertNotificationChannel(ctx, p)
	if isUnique(err) {
		return Channel{}, conflict("the team already has a notification channel with that name")
	}
	if err != nil {
		return Channel{}, err
	}
	return view(c, dek, teamID), nil
}

// newUUID mints a random v4-shaped id (like internal/deploy's own, unexported, newUUID).
func newUUID() string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func nilToEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// Update changes a channel's settings (never its kind). dek only needed when url/secret change.
func Update(ctx context.Context, q *db.Queries, teamID string, dek []byte, c db.NotificationChannel, in Input) (Channel, error) {
	old := spec{Name: c.Name, Emails: c.Emails, Events: c.Events, Enabled: c.Enabled}
	if c.UrlSealed != nil && dek != nil {
		if plain, err := crypto.Open(dek, teamID, urlContext(c.ID), *c.UrlSealed); err == nil {
			old.URL = string(plain)
		}
	}
	s, err := validate(c.Kind, in.over(old))
	if err != nil {
		return Channel{}, err
	}
	if s.Name != c.Name {
		if rows, err := q.ListNotificationChannelsOfTeam(ctx, teamID); err == nil {
			for _, r := range rows {
				if r.Name == s.Name && r.ID != c.ID {
					return Channel{}, conflict("the team already has a notification channel with that name")
				}
			}
		}
	}
	p := db.UpdateNotificationChannelInTeamParams{ID: c.ID, TeamID: teamID, Name: s.Name, Emails: pgtext.Array(nilToEmpty(s.Emails)),
		Events: pgtext.Array(nilToEmpty(s.Events)), Enabled: s.Enabled, ClearSecret: s.ClearSecret}
	if c.Kind != KindEmail && in.URL != nil {
		sealed, err := crypto.Seal(dek, teamID, urlContext(c.ID), []byte(s.URL))
		if err != nil {
			return Channel{}, err
		}
		p.UrlSealed = &sealed
	}
	if c.Kind == KindWebhook && !s.ClearSecret && in.Secret != nil && s.Secret != "" {
		sec, err := crypto.Seal(dek, teamID, secretContext(c.ID), []byte(s.Secret))
		if err != nil {
			return Channel{}, err
		}
		p.SecretSealed = &sec
	}
	up, err := q.UpdateNotificationChannelInTeam(ctx, p)
	if isUnique(err) {
		return Channel{}, conflict("the team already has a notification channel with that name")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, notFound()
	}
	if err != nil {
		return Channel{}, err
	}
	return view(up, dek, teamID), nil
}

// Delete removes a channel.
func Delete(ctx context.Context, q *db.Queries, teamID, id string) error {
	n, err := q.DeleteNotificationChannelInTeam(ctx, db.DeleteNotificationChannelInTeamParams{ID: id, TeamID: teamID})
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound()
	}
	return nil
}

// ---- delivery ---------------------------------------------------------------------------------

// Alert is one thing to tell a channel about.
type Alert struct {
	Event   string // e.g. deploy.failed, server.offline, backup.failed, cert.expiring, or "monitor.<opened|resolved>"
	Title   string
	Reason  string
	Link    string // the web app's link to whatever this is about; "" = none
	Subject string // "app" | "server" | ...
}

// AppLink is the web app's URL for one of a team's apps, built from APP_URL (internal/account/oauth.go
// uses the same env var for the same reason: never a hard-coded frontend origin). "en" is used as
// the link's locale: delivery has no request-scoped locale to carry, and the web app redirects a
// signed-in visitor to their own locale regardless of the one in the URL.
func AppLink(teamID, appID string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_URL")), "/")
	if base == "" || teamID == "" || appID == "" {
		return ""
	}
	return fmt.Sprintf("%s/en/t/%s/apps/%s", base, teamID, appID)
}

// DeliverToChannels sends alert to exactly the given, enabled channels of the team — the routing a
// monitor's notify_channel_ids chooses, regardless of their event subscriptions. Failures are
// logged, never retried, never returned (like internal/monitors' own email/webhook announce).
func (s *Service) DeliverToChannels(ctx context.Context, teamID string, channelIDs []string, a Alert) {
	if len(channelIDs) == 0 {
		return
	}
	var (
		chans []db.NotificationChannel
		dek   []byte
	)
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		chans, err = q.NotificationChannelsByIDsInTeam(ctx, db.NotificationChannelsByIDsInTeamParams{TeamID: teamID, Ids: strings.Join(channelIDs, ",")})
		if err != nil {
			return err
		}
		dek, err = s.ops.TeamDEK(ctx, q, teamID)
		return err
	})
	if err != nil {
		s.log.Warn("could not load notification channels", "team", teamID, "err", err)
		return
	}
	s.deliverAll(ctx, teamID, dek, chans, a)
}

// DeliverEvent sends alert to every enabled channel of the team that subscribes to a.Event.
func (s *Service) DeliverEvent(ctx context.Context, teamID string, a Alert) {
	var (
		chans []db.NotificationChannel
		dek   []byte
	)
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		chans, err = q.ListNotificationChannelsOfTeam(ctx, teamID)
		if err != nil {
			return err
		}
		dek, err = s.ops.TeamDEK(ctx, q, teamID)
		return err
	})
	if err != nil {
		s.log.Warn("could not load notification channels", "team", teamID, "err", err)
		return
	}
	var subscribed []db.NotificationChannel
	for _, c := range chans {
		for _, e := range c.Events {
			if e == a.Event {
				subscribed = append(subscribed, c)
				break
			}
		}
	}
	s.deliverAll(ctx, teamID, dek, subscribed, a)
}

func (s *Service) deliverAll(ctx context.Context, teamID string, dek []byte, chans []db.NotificationChannel, a Alert) {
	for _, c := range chans {
		if !c.Enabled {
			continue
		}
		if err := s.deliverOne(ctx, teamID, dek, c, a); err != nil {
			s.log.Warn("notification delivery failed", "channel", c.ID, "kind", c.Kind, "err", err)
		}
	}
}

func (s *Service) deliverOne(ctx context.Context, teamID string, dek []byte, c db.NotificationChannel, a Alert) error {
	switch c.Kind {
	case KindEmail:
		return s.deliverEmail(ctx, c, a)
	case KindSlack:
		return s.deliverWebhookJSON(ctx, teamID, dek, c, slackPayload(a))
	case KindDiscord:
		return s.deliverWebhookJSON(ctx, teamID, dek, c, discordPayload(a))
	case KindWebhook:
		return s.deliverGenericWebhook(ctx, teamID, dek, c, a)
	default:
		return fmt.Errorf("unknown channel kind %q", c.Kind)
	}
}

func (s *Service) deliverEmail(ctx context.Context, c db.NotificationChannel, a Alert) error {
	send := m.FromEnv(s.log)
	text := a.Reason
	if a.Link != "" {
		text += "\n\n" + a.Link
	}
	var lastErr error
	for _, to := range c.Emails {
		mctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := send(mctx, m.Message{To: to, Subject: "[Hosbah] " + a.Title, Text: text})
		cancel()
		if err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func slackPayload(a Alert) map[string]any {
	text := fmt.Sprintf("*%s*\n%s", a.Title, a.Reason)
	if a.Link != "" {
		text += "\n<" + a.Link + "|View in Hosbah>"
	}
	return map[string]any{"text": text}
}

func discordPayload(a Alert) map[string]any {
	content := fmt.Sprintf("**%s**\n%s", a.Title, a.Reason)
	if a.Link != "" {
		content += "\n" + a.Link
	}
	return map[string]any{"content": content}
}

func (s *Service) deliverWebhookJSON(ctx context.Context, teamID string, dek []byte, c db.NotificationChannel, payload any) error {
	u, err := s.unsealURL(teamID, dek, c)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.post(ctx, u, body, nil)
}

func (s *Service) deliverGenericWebhook(ctx context.Context, teamID string, dek []byte, c db.NotificationChannel, a Alert) error {
	u, err := s.unsealURL(teamID, dek, c)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"event": a.Event, "title": a.Title, "reason": a.Reason, "link": a.Link, "at": time.Now().UTC()})
	if err != nil {
		return err
	}
	var secret []byte
	if c.SecretSealed != nil && dek != nil {
		if plain, err := crypto.Open(dek, teamID, secretContext(c.ID), *c.SecretSealed); err == nil {
			secret = plain
		}
	}
	var headers map[string]string
	if len(secret) > 0 {
		mac := hmac.New(sha256.New, secret)
		mac.Write(body)
		headers = map[string]string{SignatureHeader: hex.EncodeToString(mac.Sum(nil))}
	}
	return s.post(ctx, u, body, headers)
}

func (s *Service) unsealURL(teamID string, dek []byte, c db.NotificationChannel) (string, error) {
	if c.UrlSealed == nil || dek == nil {
		return "", errors.New("channel has no url configured")
	}
	plain, err := crypto.Open(dek, teamID, urlContext(c.ID), *c.UrlSealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Service) post(ctx context.Context, u string, body []byte, headers map[string]string) error {
	if !strings.HasPrefix(u, "https://") {
		return errors.New("channel url is not https")
	}
	ctx, cancel := context.WithTimeout(ctx, DeliverTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Transport: s.transport, Timeout: DeliverTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("channel answered HTTP %d", resp.StatusCode)
	}
	return nil
}

// Test sends a small test message through one channel, in the caller's own team-scoped
// transaction's DEK — used by the "send test" REST/MCP action.
func (s *Service) Test(ctx context.Context, teamID string, dek []byte, c db.NotificationChannel) error {
	a := Alert{Event: "test", Title: "Test notification", Reason: "This is a test notification from Hosbah's " + c.Name + " channel."}
	return s.deliverOne(ctx, teamID, dek, c, a)
}
