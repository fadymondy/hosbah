package deploy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Hosbah as a desktop browser's auth: GET /api/browsers/auth is Caddy's forward_auth target for every
desktop browser hostname (internal/agent's forwardAuthHandler; internal/deploy/browserhost.go wires
it in on every desktop browser's proxy host). It is dialled by Caddy only, as a subrequest, never by
a browser directly — the subrequest is always a GET, carrying the real request's own method and URI
in X-Forwarded-Method/X-Forwarded-Uri (Caddy's reverse_proxy leaves Host as the real hostname).

Two paths reach it, both on the SAME hostname a member's browser is pointed at:

  - GET /.hosbah/auth?token=...  "Open browser" (internal/api/browsers.go) minted a one-time,
    short-lived token bound to (browser, user) and sent the member here. The token is exchanged
    exactly once (ConsumeBrowserAuthToken's UPDATE ... WHERE used_at IS NULL is the guard against a
    replay, raced or not) for a host-only, HttpOnly, Secure, SameSite=Lax session cookie, then a 302
    back to "/" on the same host.
  - anything else                the cookie is opened and its claim checked against the caller's
    LIVE team membership and role — never trusted on its own past that. A 2xx here is Caddy's signal
    (handle_response, status_code match [2]) to let the real request through unmodified; anything
    else is returned to the browser exactly as this handler wrote it, so a 401 here is what the
    member's browser shows.

The cookie's value is not a database session: it is SealPlatform'd (internal/crypto), an AEAD box
bound to a fixed context, so opening it needs the server's own KEK and nothing is stored to revoke —
removing a member from the team (or the browser itself) takes effect on the very next request because
the live check runs every time, not because the cookie was invalidated.
*/

const (
	// browserAuthExchangePath is where "Open browser" sends a member; never a path a browser's own
	// app would otherwise use (KasmVNC and headless Chromium both live under their own root paths).
	browserAuthExchangePath = "/.hosbah/auth"
	// browserSessionCookie is host-only (no Domain attribute is ever set) — it never reaches a
	// different browser's hostname, or Hosbah's own app domain.
	browserSessionCookie  = "hosbah_browser"
	browserSessionContext = "hosbah/browser-session/v1"
	// browserSessionTTL bounds how long a session cookie is honoured at all — membership and role are
	// still re-checked on every single request within that window.
	browserSessionTTL = 12 * time.Hour
	// BrowserAuthTokenTTL is how long "Open browser"'s one-time handoff token is good for.
	BrowserAuthTokenTTL = 2 * time.Minute
)

type browserSession struct {
	BrowserID string    `json:"browser_id"`
	TeamID    string    `json:"team_id"`
	UserID    string    `json:"user_id"`
	Exp       time.Time `json:"exp"`
}

// MountBrowsersAuth adds Hosbah's own forward_auth target. Outside /api/teams/{team}/...  on purpose
// — it is reached through a desktop browser's own hostname, not a team-scoped route, and answers for
// itself: no session, no team in the path, nothing here is default-deny by internal/server's tiers.
func (s *Service) MountBrowsersAuth(r chi.Router) {
	r.Get("/api/browsers/auth", s.handleBrowsersAuth)
}

func (s *Service) handleBrowsersAuth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	host := browserAuthHost(r)
	uri := r.Header.Get("X-Forwarded-Uri")
	if uri == "" {
		uri = r.URL.RequestURI()
	}
	path, query := uri, ""
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		path, query = uri[:i], uri[i+1:]
	}
	if path == browserAuthExchangePath {
		s.exchangeBrowserAuth(w, r, host, query)
		return
	}
	s.checkBrowserAuth(w, r, host)
}

// browserAuthHost is the desktop browser's own hostname: X-Hosbah-Browser-Host, set by the agent's
// Caddy on every forward_auth subrequest, first — X-Forwarded-Host alone does not survive the proxies
// in front of the control plane (each rewrites it to app.<domain>). Neither can widen access: a token
// or cookie is only honoured for the browser it was minted for.
func browserAuthHost(r *http.Request) string {
	h := r.Header.Get("X-Hosbah-Browser-Host")
	if h == "" {
		h = r.Header.Get("X-Forwarded-Host")
	}
	if h == "" {
		h = r.Host
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return NormalizeHostname(h)
}

// browserForHost is BrowserByHostname, system scope: forward_auth knows only the hostname.
func (s *Service) browserForHost(ctx context.Context, host string) (db.AppBrowser, error) {
	var b db.AppBrowser
	err := s.sys(ctx, func(q *db.Queries) error {
		var err error
		b, err = q.BrowserByHostname(ctx, &host)
		return err
	})
	return b, err
}

// exchangeBrowserAuth consumes a one-time token minted by "Open browser" and, only when it is for
// THIS hostname's own browser, sets the session cookie. A token for another browser or team, expired,
// unknown, or already used (replayed) all deny alike — no detail is leaked about which.
func (s *Service) exchangeBrowserAuth(w http.ResponseWriter, r *http.Request, host, query string) {
	deny := func() {
		http.Error(w, "that link is no longer valid — open the browser again from Hosbah", http.StatusUnauthorized)
	}
	vals, err := url.ParseQuery(query)
	token := vals.Get("token")
	if err != nil || token == "" {
		deny()
		return
	}
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	ctx := r.Context()
	var row db.BrowserAuthToken
	if err := s.sys(ctx, func(q *db.Queries) error {
		var err error
		row, err = q.ConsumeBrowserAuthToken(ctx, hash)
		return err
	}); err != nil {
		deny()
		return
	}
	b, err := s.browserForHost(ctx, host)
	if err != nil || b.ID != row.BrowserID || b.TeamID != row.TeamID {
		deny()
		return
	}
	sess := browserSession{BrowserID: b.ID, TeamID: b.TeamID, UserID: row.UserID, Exp: time.Now().Add(browserSessionTTL)}
	sealed, err := s.a.Keys.SealPlatform(browserSessionContext, mustJSON(sess))
	if err != nil {
		http.Error(w, "could not open a session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: browserSessionCookie, Value: sealed, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// checkBrowserAuth is the steady-state check: the cookie's claim, re-checked against the caller's
// LIVE team membership and role (never trusted from the cookie alone) and against this hostname's
// own browser, on every single request.
func (s *Service) checkBrowserAuth(w http.ResponseWriter, r *http.Request, host string) {
	deny := func(msg string) { http.Error(w, msg, http.StatusUnauthorized) }
	c, err := r.Cookie(browserSessionCookie)
	if err != nil || c.Value == "" {
		deny("open this browser from Hosbah")
		return
	}
	raw, err := s.a.Keys.OpenPlatform(browserSessionContext, c.Value)
	if err != nil {
		deny("that session is no longer valid — open the browser again from Hosbah")
		return
	}
	var sess browserSession
	if json.Unmarshal(raw, &sess) != nil || sess.BrowserID == "" || time.Now().After(sess.Exp) {
		deny("that session has expired — open the browser again from Hosbah")
		return
	}
	ctx := r.Context()
	b, err := s.browserForHost(ctx, host)
	if err != nil || b.ID != sess.BrowserID || b.TeamID != sess.TeamID {
		deny("that session does not match this browser")
		return
	}
	// team_members has no *_system_* SELECT policy (its RLS is "the context team's roster, plus your
	// own memberships") — a bare system scope sets neither app.team_id nor app.user_id and sees no
	// rows at all. Scope{System: true, TeamID: …} passes app.team_id, which the policy does allow.
	allowed := false
	_ = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: sess.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		m, err := q.MembershipFor(ctx, db.MembershipForParams{TeamID: sess.TeamID, UserID: sess.UserID})
		if err != nil {
			return nil
		}
		allowed = tenancy.Can(ctx, &tenancy.Membership{TeamID: sess.TeamID, UserID: sess.UserID, Role: m.Role}, tenancy.AbilityDeploy)
		return nil
	})
	if !allowed {
		deny("a desktop browser needs the deployer role or higher in its team")
		return
	}
	w.WriteHeader(http.StatusOK)
}
