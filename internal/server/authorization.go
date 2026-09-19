package server

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/togo-framework/auth"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/hosbah/internal/app"
)

/*
Default-deny authorization, modelled on fadymondy.com-v2's internal/server/authorization.go
(FM-63 / FM-43), with one tier added for multi-tenancy.

fadymondy.com learned this the hard way: generated REST resources and GraphQL were mounted
with no authentication, and anonymous GET /api/mcp_tokens and DELETE /api/notes/{id} worked in
production. Hosbah starts where that fix ended — everything under /api is closed unless named:

	blocked   — framework routes Hosbah does not use and must not expose; 404 for everyone
	public    — anonymous; enumerated in isPublic
	token     — the two MCP transports; they authenticate their own bearer tokens
	member    — any signed-in account; enumerated in memberPrefixes. Row ownership is the
	            handler's job AND the database's (RLS): these routes are team-scoped by path,
	            and a team you are not in answers 404, exactly like one that does not exist
	admin     — everything else, gated on the platform `admin` role

The admin tier is the default, so a newly generated resource or plugin route is reachable only
by a platform administrator until somebody decides otherwise — and even then, generated CRUD is
never mounted for tenant tables (see server.go), and RLS returns nothing without a team context.

Delegating identity to auth.Service.Middleware (not re-reading the cookie here) keeps one code
path for "who is this", the same one /api/auth/me answers from.
*/
func requireAuthorization(k *togo.Kernel, a *app.App) {
	svc, ok := auth.FromKernel(k)
	if !ok {
		k.Log.Error("auth service unavailable — refusing to start with an unguarded API")
		panic("authorization: auth service not registered")
	}

	k.UseMiddleware(func(next http.Handler) http.Handler {
		// activeAccount: a disabled or deleted account's session stops at its next request.
		active := activeAccount(a)
		member := svc.Middleware(sessionOnly(active(csrf(next))))
		admin := svc.RequireRole(RolePlatformAdmin)(sessionOnly(active(csrf(next))))

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case isBlocked(r):
				writeJSONError(w, http.StatusNotFound, "not found")
			case isPublic(r), isTokenAuthed(r):
				next.ServeHTTP(w, r)
			case isMemberAllowed(r):
				member.ServeHTTP(w, r)
			default:
				admin.ServeHTTP(w, r)
			}
		})
	})
}

// RolePlatformAdmin is the platform operator role (users.roles CSV, carried in the session).
// It is not a team role: team roles live in team_members.role.
const RolePlatformAdmin = "admin"

/*
Framework routes closed to everyone, admins included.

  - /api/auth/tokens: togo personal access tokens. A PAT identity carries no email and no roles
    and Hosbah's handlers do not enforce PAT abilities, so a PAT would act as its user across
    every team. Hosbah's machine access is the team-bound MCP token instead.
  - /api/auth/otp, /api/auth/pin: email OTP and lock-screen PIN, unused; smaller surface.
  - /api/auth/dev, /api/auth/mfa: auth-dev (admin session for an empty body) and auth-mfa
    (magic-link token echo, FM-81; user_id taken from the request body) are not installed.
    Blocking the paths too means re-installing either by accident still exposes nothing.
  - /graphql: not mounted in P0 — the generated resolvers are unscoped CRUD over tenant tables.
*/
func isBlocked(r *http.Request) bool {
	p := r.URL.Path
	for _, prefix := range []string{
		"/api/auth/tokens", "/api/auth/otp", "/api/auth/pin",
		"/api/auth/dev", "/api/auth/mfa", "/api/auth/magic",
		"/graphql",
	} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// isPublic: adding to this list is a security decision — it must be true that an anonymous
// stranger may call it.
func isPublic(r *http.Request) bool {
	p := r.URL.Path
	if !strings.HasPrefix(p, "/api/") && p != "/api" {
		return true // not the API: docs, SSE, 404s
	}
	switch p {
	case "/api/health",
		"/api/auth/csrf",
		"/api/auth/methods",
		"/api/auth/register", // public sign-up (Hosbah is headed for public SaaS)
		"/api/auth/login",    // wrapped by requireSecondFactor (login_2fa.go)
		"/api/auth/logout",
		"/api/settings/public": // sign-up mode, announcement, support email — shown before sign-in
		return true
	}
	return false
}

// isTokenAuthed: the MCP endpoints authenticate bearer tokens against their own tables.
// Exact matches — "/api/mcp" as a prefix would also match "/api/mcp-tokens".
func isTokenAuthed(r *http.Request) bool {
	return r.URL.Path == "/api/mcp" || r.URL.Path == "/api/admin/mcp"
}

// memberPrefixes are reachable by any signed-in account. Each entry's handlers take identity
// from the session only and scope every row by team membership (handlers + RLS).
var memberPrefixes = []string{
	"/api/me",          // your own account, teams, 2FA state, reports
	"/api/teams",       // create a team; /api/teams/{team}/... is membership-checked
	"/api/invitations", // view / accept an invitation by its token
	"/api/feedback",    // "Report a problem" — files as you
	"/api/auth/me",     // plugin: who am I
	"/api/auth/change-password",
	"/api/auth/2fa", // plugin: TOTP enroll / verify for the session's own user
}

func isMemberAllowed(r *http.Request) bool {
	p := r.URL.Path
	for _, prefix := range memberPrefixes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// sessionOnly refuses identities that did not come from a login session (a togo PAT resolves
// to an identity with Guard "pat"; see isBlocked).
func sessionOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := auth.IdentityFrom(r.Context()); !ok || id == nil || id.Guard == "pat" {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// csrf enforces the plugin's double-submit token (cookie togo_csrf == header X-CSRF-Token) on
// unsafe, cookie-authenticated requests to Hosbah's own routes — the plugin applies it only to
// its own. Bearer requests are not cookie-driven and are exempt.
func csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie("togo_csrf")
		header := r.Header.Get("X-CSRF-Token")
		if err != nil || header == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(header)) != 1 {
			writeJSONError(w, http.StatusForbidden, "invalid csrf token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}
