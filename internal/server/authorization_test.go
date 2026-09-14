package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

type tier string

func classify(method, path string) tier {
	r := httptest.NewRequest(method, path, nil)
	switch {
	case isBlocked(r):
		return "blocked"
	case isPublic(r):
		return "public"
	case isTokenAuthed(r):
		return "token"
	case isMemberAllowed(r):
		return "member"
	default:
		return "admin"
	}
}

// Every route in the OpenAPI document lands in the tier it was designed for, and nothing is
// public or token-authenticated by accident. A new route defaults to admin until it is listed.
func TestEveryAPIRouteHasTheIntendedTier(t *testing.T) {
	for _, op := range operations(openAPIDoc()) {
		path := strings.NewReplacer("{team}", "t1", "{member}", "m1", "{id}", "i1", "{token}", "k1").Replace(op.Path)
		got := classify(op.Method, path)
		want := tier("member")
		if strings.HasPrefix(op.Path, "/api/admin/") {
			want = "admin"
		}
		if got != want {
			t.Errorf("%s %s is in tier %s, want %s", op.Method, op.Path, got, want)
		}
	}
}

func TestFrameworkAndGeneratedRoutes(t *testing.T) {
	for path, want := range map[string]tier{
		// togo PATs, OTP, PIN, dev login, auth-mfa: never.
		"/api/auth/tokens":            "blocked",
		"/api/auth/tokens/abc":        "blocked",
		"/api/auth/otp":               "blocked",
		"/api/auth/pin/verify":        "blocked",
		"/api/auth/dev/login":         "blocked",
		"/api/auth/mfa/magic-link/send": "blocked",
		"/graphql":                    "blocked",
		// The public sign-in surface.
		"/api/health":        "public",
		"/api/auth/login":    "public",
		"/api/auth/register": "public",
		"/api/auth/csrf":     "public",
		// MCP: exact paths only; the generated-style /api/mcp_tokens is NOT token-authed.
		"/api/mcp":        "token",
		"/api/admin/mcp":  "token",
		"/api/mcp_tokens": "admin",
		"/api/mcp/extra":  "admin",
		// Generated CRUD paths (not mounted, but if they ever were): platform admin only.
		"/api/teams_generated": "admin",
		"/api/team_members":    "admin",
		"/api/reports":         "admin",
		"/api/audit/activity":  "admin",
		"/api/authz/can":       "admin",
		"/api/_meta/resources": "admin",
		// Signed-in plugin routes.
		"/api/auth/me":         "member",
		"/api/auth/2fa/enroll": "member",
	} {
		if got := classify("POST", path); got != want {
			t.Errorf("%s: tier %s, want %s", path, got, want)
		}
	}
}
