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
  - /api/auth/2fa/enroll, /api/auth/2fa/verify: the plugin's TOTP stored the secret in plain
    text and its login never asked for it. Two-factor is internal/account's /api/me/2fa/*.
  - /graphql: not mounted in P0 — the generated resolvers are unscoped CRUD over tenant tables.
*/
func isBlocked(r *http.Request) bool {
	p := r.URL.Path
	for _, prefix := range []string{
		"/api/auth/tokens", "/api/auth/otp", "/api/auth/pin",
		"/api/auth/dev", "/api/auth/mfa", "/api/auth/magic",
		"/api/auth/2fa/enroll", "/api/auth/2fa/verify",
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
		"/api/auth/login",    // wrapped by internal/account: disabled, unverified, 2FA challenge
		// The account cycle (internal/account): each answers the same whether or not an address
		// has an account, and is rate-limited per client and per address.
		"/api/auth/verify-email",
		"/api/auth/verify-email/resend",
		"/api/auth/password/forgot",
		"/api/auth/password/reset",
		"/api/auth/code/request",
		"/api/auth/code/verify",
		"/api/auth/2fa/challenge", // needs the challenge a correct first factor returned
		"/api/auth/logout",
		"/api/settings/public", // sign-up mode, announcement, support email — shown before sign-in
		"/api/auth/providers":  // which "Continue with Google / GitHub" buttons to show
		return true
	}
	// The public status page (internal/api/public.go): a published page by its slug, filtered so
	// deeply (PublicStatusPageBySlug, PublicMonitorsByIDs) that anonymous access is the point.
	if strings.HasPrefix(p, "/api/public/status/") {
		return true
	}
	// The RUM beacon's ingest endpoint (internal/rum, MH-575 H5): POST /api/rum/{key}, authenticated
	// only by the app's own non-secret ingest key (it travels in every visitor's page source) —
	// exactly like the heartbeat ping and deploy-hook routes, except those live outside /api/ and
	// this one does not, so it needs its own allow here.
	if strings.HasPrefix(p, "/api/rum/") {
		return true
	}
	// The OTLP/HTTP JSON ingest endpoints (internal/apm, MH-575 H7): POST /api/otlp/v1/traces and
	// /api/otlp/v1/logs, authenticated by the app's own secret APM ingest key (a Bearer/x-hosbah-key
	// header, never a query param or path segment — unlike the RUM key this one is a real secret).
	if strings.HasPrefix(p, "/api/otlp/") {
		return true
	}
	// The SaaS product-key instance-provisioning API (internal/api/product_keys_public.go,
	// MH-576 D7): POST/DELETE /api/products/instances[...], authenticated by the product's own
	// secret hbk_ key (Authorization: Bearer), rate-limited per key (saasify.KeyRateLimiter) and
	// scoped, inside the handler, to exactly that key's team and product — never a browser
	// session, so it is public here the same way RUM and OTLP ingest are.
	if strings.HasPrefix(p, "/api/products/") {
		return true
	}
	// Google / GitHub sign-in (internal/account/oauth.go): the start (redirects to the provider with
	// a single-use state bound to this browser) and the callback (accepts only that state). The
	// start with ?link=1 attaches the provider to the signed-in account: member tier.
	if provider, rest, ok := oauthPath(p); ok && provider != "" {
		return rest == "/callback" || (rest == "" && !r.URL.Query().Has("link"))
	}
	return false
}

// oauthPath splits /api/auth/oauth/{provider}[/callback]. Only the known providers match, so no
// other path under it is ever public.
func oauthPath(p string) (provider, rest string, ok bool) {
	tail, ok := strings.CutPrefix(p, "/api/auth/oauth/")
	if !ok {
		return "", "", false
	}
	provider, rest, _ = strings.Cut(tail, "/")
	if rest != "" {
		rest = "/" + rest
	}
	switch provider {
	case "google", "github":
	default:
		return "", "", false
	}
	if rest != "" && rest != "/callback" {
		return "", "", false
	}
	return provider, rest, true
}

// isTokenAuthed: the MCP endpoints authenticate bearer tokens against their own tables.
// Exact matches — "/api/mcp" as a prefix would also match "/api/mcp-tokens".
func isTokenAuthed(r *http.Request) bool {
	return r.URL.Path == "/api/mcp" || r.URL.Path == "/api/admin/mcp"
}

// memberPrefixes are reachable by any signed-in account. Each entry's handlers take identity
// from the session only and scope every row by team membership (handlers + RLS).
var memberPrefixes = []string{
	"/api/me",                   // your own account, teams, two-factor (/api/me/2fa/*), reports
	"/api/teams",                // create a team; /api/teams/{team}/... is membership-checked
	"/api/invitations",          // view / accept an invitation by its token
	"/api/feedback",             // "Report a problem" — files as you
	"/api/auth/me",              // plugin: who am I
	"/api/auth/change-password", // plugin: needs the current password
	"/api/github",               // the GitHub App's setup callback: the state names the team and the user
}

func isMemberAllowed(r *http.Request) bool {
	p := r.URL.Path
	// Linking Google / GitHub to the signed-in account (…/oauth/{provider}?link=1).
	if provider, rest, ok := oauthPath(p); ok && provider != "" && rest == "" && r.URL.Query().Has("link") {
		return true
	}
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
