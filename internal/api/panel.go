package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/auth"
	"golang.org/x/crypto/bcrypt"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
MH-1141: a server user's white-label panel and its own accounts.

A panel account is a row in users with panel_server_id set and email stored as
"<email>#panel:<server id>", so one address can hold a Hosbah account and panel accounts on many
servers, and the plugin's own login can never match it by a plain address. The console refuses
any session of such an account (internal/server/accounts.go). A panel account signs in only here,
and its session is an opaque random token whose SHA-256 is in panel_sessions; the cookie is
scoped to /api/panel/{server}, so the browser sends it to that panel's API and nowhere else.

Public routes (internal/server/authorization.go's isPublic; each handles its own auth):

	GET  /api/panel/{server}/brand   name, colour, icon, logo — what the sign-in page shows
	GET  /api/panel/{server}/logo    the uploaded logo, if any
	POST /api/panel/{server}/login   {email, password} → session cookie
	POST /api/panel/{server}/password/forgot  {email} → a reset code mailed under the server's name
	POST /api/panel/{server}/password/reset   {email, code, password} → new password, signed in
	POST /api/panel/{server}/password         {current, password} (signed in) → other sessions end
	POST /api/panel/{server}/logout
	GET  /api/panel/{server}/me      the signed-in panel account, else 401

Team routes (owner/admin): /api/teams/{team}/servers/{server}/panel-accounts[/{panel_account}].
*/

// PanelPathPrefix is where mountPanel adds its routes; authorization.go treats them as public.
const PanelPathPrefix = "/api/panel/"

const (
	panelCookie     = "hb_panel"
	panelSessionTTL = 7 * 24 * time.Hour
)

// panelKey is the stored users.email of a panel account.
func panelKey(email, serverID string) string {
	return strings.ToLower(strings.TrimSpace(email)) + "#panel:" + serverID
}

// panelEmail is the address shown for a stored panel key.
func panelEmail(key string) string {
	if i := strings.Index(key, "#panel:"); i >= 0 {
		return key[:i]
	}
	return key
}

func validPanelEmail(e string) bool {
	at := strings.IndexByte(e, '@')
	return at > 0 && at < len(e)-1 && len(e) <= 200 && !strings.ContainsAny(e, " #\r\n\t")
}

func hashPanelToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// PanelBrand is what a stranger sees on a panel's sign-in page: the server's own brand, nothing of Hosbah.
type PanelBrand struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Color   *string `json:"color"`
	Icon    *string `json:"icon"`
	LogoURL *string `json:"logo_url"`
}

// PanelAccountOut is one panel account.
type PanelAccountOut struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at"`
	Disabled  bool   `json:"disabled"`
}

// panelLimiter caps sign-in attempts per client and panel (10 per 10 minutes).
type panelLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *panelLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	keep := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < 10*time.Minute {
			keep = append(keep, t)
		}
	}
	if len(keep) >= 10 {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	return true
}

var panelLogins = &panelLimiter{hits: map[string][]time.Time{}}

// A real bcrypt hash compared against when the address has no account, so both cost the same.
var panelDummyHash, _ = bcrypt.GenerateFromPassword([]byte("hosbah-panel-dummy"), bcrypt.DefaultCost)

// mountPanel adds the public panel routes.
func (d *deps) mountPanel(r chi.Router) {
	r.Get(PanelPathPrefix+"{server}/brand", d.handlePanelBrand)
	r.Get(PanelPathPrefix+"{server}/logo", d.handlePanelLogo)
	r.Post(PanelPathPrefix+"{server}/login", d.handlePanelLogin)
	r.Post(PanelPathPrefix+"{server}/logout", d.handlePanelLogout)
	r.Get(PanelPathPrefix+"{server}/me", d.handlePanelMe)
	r.Post(PanelPathPrefix+"{server}/password/forgot", d.handlePanelForgot)
	r.Post(PanelPathPrefix+"{server}/password/reset", d.handlePanelReset)
	r.Post(PanelPathPrefix+"{server}/password", d.handlePanelChangePassword)
}

func writePanelJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// panelServer loads the panel's server in system scope, or ok=false (one answer for any miss).
func (d *deps) panelServer(ctx context.Context, id string) (db.PanelServerRow, bool) {
	if d.a == nil || d.a.SQLDB == nil || id == "" || len(id) > 64 {
		return db.PanelServerRow{}, false
	}
	var s db.PanelServerRow
	err := tenancy.Tx(ctx, d.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		s, err = q.PanelServer(ctx, id)
		return err
	})
	return s, err == nil
}

func (d *deps) handlePanelBrand(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := d.panelServer(ctx, chi.URLParam(r, "server"))
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	out := PanelBrand{ID: s.ID, Name: s.Name, Color: s.Color, Icon: s.Icon, LogoURL: s.LogoUrl}
	_ = tenancy.Tx(ctx, d.a.SQLDB, tenancy.Scope{System: true, TeamID: s.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		if etag, err := q.ServerLogoVersion(ctx, db.ServerLogoVersionParams{ServerID: s.ID, TeamID: s.TeamID}); err == nil {
			u := PanelPathPrefix + s.ID + "/logo?v=" + etag
			out.LogoURL = &u
		}
		return nil
	})
	writePanelJSON(w, http.StatusOK, out)
}

func (d *deps) handlePanelLogo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := d.panelServer(ctx, chi.URLParam(r, "server"))
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	var logo db.ServerLogo
	err := tenancy.Tx(ctx, d.a.SQLDB, tenancy.Scope{System: true, TeamID: s.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		logo, err = q.GetServerLogo(ctx, db.GetServerLogoParams{ServerID: s.ID, TeamID: s.TeamID})
		return err
	})
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", logo.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(logo.Data)
}

func panelClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (d *deps) handlePanelLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// JSON only: a cross-site form cannot send it without a CORS preflight.
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSONError(w, http.StatusUnsupportedMediaType, "send JSON")
		return
	}
	s, ok := d.panelServer(ctx, chi.URLParam(r, "server"))
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if !panelLogins.allow(panelClientIP(r) + "|" + s.ID) {
		writeJSONError(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid body")
		return
	}
	q := db.New(d.a.SQLDB)
	acc, err := q.PanelAccountByEmail(ctx, db.PanelAccountByEmailParams{Email: panelKey(body.Email, s.ID), PanelServerID: &s.ID})
	hash := panelDummyHash
	if err == nil {
		hash = []byte(acc.PasswordHash)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(body.Password)) != nil || err != nil || acc.DisabledAt != nil {
		writeJSONError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	if !d.startPanelSession(w, r, acc.ID, s.ID) {
		return
	}
	writePanelJSON(w, http.StatusOK, map[string]any{"account": map[string]string{"id": acc.ID, "email": panelEmail(acc.Email)}})
}

// startPanelSession opens a session for the account and sets its cookie; false after writing the error.
func (d *deps) startPanelSession(w http.ResponseWriter, r *http.Request, userID, serverID string) bool {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not sign in")
		return false
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	exp := time.Now().Add(panelSessionTTL)
	if err := db.New(d.a.SQLDB).CreatePanelSession(r.Context(), db.CreatePanelSessionParams{TokenHash: hashPanelToken(tok), UserID: userID, ServerID: serverID, ExpiresAt: exp}); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not sign in")
		return false
	}
	http.SetCookie(w, &http.Cookie{Name: panelCookie, Value: tok, Path: PanelPathPrefix + serverID, Expires: exp,
		HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode})
	return true
}

func (d *deps) handlePanelLogout(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "server")
	if c, err := r.Cookie(panelCookie); err == nil && c.Value != "" && d.a != nil && d.a.SQLDB != nil {
		_ = db.New(d.a.SQLDB).DeletePanelSession(r.Context(), hashPanelToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: panelCookie, Value: "", Path: PanelPathPrefix + id, MaxAge: -1,
		HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteLaxMode})
	writePanelJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// panelAccount answers the request's panel account on this server, or ok=false.
func (d *deps) panelAccount(r *http.Request, serverID string) (db.PanelSessionRow, bool) {
	c, err := r.Cookie(panelCookie)
	if err != nil || c.Value == "" || d.a == nil || d.a.SQLDB == nil {
		return db.PanelSessionRow{}, false
	}
	row, err := db.New(d.a.SQLDB).PanelSession(r.Context(), db.PanelSessionParams{TokenHash: hashPanelToken(c.Value), ServerID: serverID})
	return row, err == nil
}

func (d *deps) handlePanelMe(w http.ResponseWriter, r *http.Request) {
	acc, ok := d.panelAccount(r, chi.URLParam(r, "server"))
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writePanelJSON(w, http.StatusOK, map[string]any{"account": map[string]string{"id": acc.ID, "email": panelEmail(acc.Email)}})
}

/* ------------------------------------------------------------ team management */

// PanelAccountPath is one panel account of a team's server.
type PanelAccountPath struct {
	ServerPath
	PanelAccount string `path:"panel_account" maxLength:"64"`
}

func toPanelAccountOut(id, email, created string, disabled *time.Time) PanelAccountOut {
	return PanelAccountOut{ID: id, Email: panelEmail(email), CreatedAt: created, Disabled: disabled != nil}
}

func (d *deps) registerPanelAccounts(api huma.API) {
	tags := []string{"Panel accounts"}
	base := "/api/teams/{team}/servers/{server}/panel-accounts"

	type panelAccountsOut struct {
		Body struct {
			Accounts []PanelAccountOut `json:"accounts"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-panel-accounts", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The accounts that can sign in to this server user's panel (owner or admin)",
	}, func(ctx context.Context, in *ServerPath) (*panelAccountsOut, error) {
		out := &panelAccountsOut{}
		out.Body.Accounts = []PanelAccountOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			rows, err := q.ListPanelAccounts(ctx, &s.ID)
			if err != nil {
				return huma.Error500InternalServerError("could not list panel accounts")
			}
			for _, a := range rows {
				out.Body.Accounts = append(out.Body.Accounts, toPanelAccountOut(a.ID, a.Email, a.CreatedAt, a.DisabledAt))
			}
			return nil
		})
		return out, err
	})

	type oneOut struct{ Body PanelAccountOut }
	huma.Register(api, huma.Operation{OperationID: "create-panel-account", Method: http.MethodPost, Path: base,
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add an account that signs in to this server user's panel only (owner or admin)",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			Email    string `json:"email" maxLength:"200"`
			Password string `json:"password" minLength:"10" maxLength:"128"`
		}
	}) (*oneOut, error) {
		out := &oneOut{}
		email := strings.ToLower(strings.TrimSpace(in.Body.Email))
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			// After the team and server checks: a stranger gets the same 404 whatever the body says.
			if !validPanelEmail(email) {
				return huma.Error422UnprocessableEntity("enter a valid email address")
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(in.Body.Password), bcrypt.DefaultCost)
			if err != nil {
				return huma.Error500InternalServerError("could not add the account")
			}
			a, err := q.CreatePanelAccount(ctx, db.CreatePanelAccountParams{Email: panelKey(email, s.ID), PasswordHash: string(hash),
				CreatedAt: time.Now().UTC().Format(time.RFC3339), PanelServerID: &s.ID})
			if err != nil {
				return huma.Error409Conflict("this panel already has an account with that email")
			}
			out.Body = toPanelAccountOut(a.ID, a.Email, a.CreatedAt, a.DisabledAt)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-panel-account", Method: http.MethodPatch, Path: base + "/{panel_account}", Tags: tags,
		Summary: "Set a panel account's password (owner or admin); its sessions end",
	}, func(ctx context.Context, in *struct {
		PanelAccountPath
		Body struct {
			Password string `json:"password" minLength:"10" maxLength:"128"`
		}
	}) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(in.Body.Password), bcrypt.DefaultCost)
			if err != nil {
				return huma.Error500InternalServerError("could not set the password")
			}
			n, err := q.SetPanelAccountPassword(ctx, db.SetPanelAccountPasswordParams{ID: in.PanelAccount, PanelServerID: &s.ID, PasswordHash: string(hash)})
			if err != nil || n == 0 {
				return errNotFound
			}
			return q.DeletePanelSessionsOfUser(ctx, in.PanelAccount)
		})
		return nil, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-panel-account", Method: http.MethodDelete, Path: base + "/{panel_account}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Remove a panel account (owner or admin); its sessions end",
	}, func(ctx context.Context, in *PanelAccountPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			n, err := q.DeletePanelAccount(ctx, db.DeletePanelAccountParams{ID: in.PanelAccount, PanelServerID: &s.ID})
			if err != nil || n == 0 {
				return errNotFound
			}
			return nil
		})
		return nil, err
	})
}
