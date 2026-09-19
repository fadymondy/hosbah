package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/platform"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Platform administration (/api/admin/*, the admin MCP, and what they enforce elsewhere).

platform_settings is one row shared by every test in this database, so each test that changes it
puts the defaults back when it ends. Tests in this package run sequentially, and no other
package's tests register users, create teams or add servers over HTTP.
*/

// platformAdmin registers an account, makes it a platform admin in the database and signs in
// again so the session carries the role.
func platformAdmin(t *testing.T, base, email string) *client {
	t.Helper()
	c := newClient(t, base)
	c.register(email)
	if _, err := testDB(t).Exec(`UPDATE users SET roles = 'admin' WHERE email = $1`, email); err != nil {
		t.Fatal(err)
	}
	c.login(email)
	return c
}

func userID(t *testing.T, c *client) string {
	t.Helper()
	var me struct {
		User struct{ ID string } `json:"user"`
	}
	c.mustJSON("GET", "/api/me", nil, 200, &me)
	return me.User.ID
}

// resetSettings puts platform_settings back to its defaults (as a platform admin, so RLS allows it).
func resetSettings(t *testing.T) {
	t.Helper()
	err := tenancy.Tx(context.Background(), testDB(t), tenancy.Scope{PlatformAdmin: true}, func(q *db.Queries, _ *sql.Tx) error {
		_, err := q.UpsertPlatformSettings(context.Background(), db.UpsertPlatformSettingsParams{SignupMode: "open", AnnouncementLevel: "info"})
		return err
	})
	if err != nil {
		t.Fatalf("reset platform settings: %v", err)
	}
}

func TestPlatformAdminRoutesRefuseNonAdmins(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u := newClient(t, ts.URL)
	u.register("plain-" + suffix + "@example.test")

	n := 0
	for _, op := range operations(openAPIDoc()) {
		if !strings.HasPrefix(op.Path, "/api/admin/") {
			continue
		}
		path := strings.ReplaceAll(op.Path, "{id}", "some-id")
		if status, text := u.do(op.Method, path, sampleBody(openAPIDoc(), op.Op)); status != http.StatusForbidden {
			t.Errorf("non-admin %s %s -> %d (want 403): %s", op.Method, path, status, text)
		}
		n++
	}
	if n < 13 {
		t.Fatalf("only %d admin routes found — did the platform admin routes go missing?", n)
	}
	// Anonymous: 401, not a leak.
	if code := httpStatus(t, "GET", ts.URL+"/api/admin/overview", ""); code != http.StatusUnauthorized {
		t.Errorf("anonymous GET /api/admin/overview -> %d (want 401)", code)
	}
}

func TestPlatformAdminUsersAndSessions(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	adminEmail := "padmin-" + suffix + "@example.test"
	admin := platformAdmin(t, ts.URL, adminEmail)
	adminID := userID(t, admin)

	// Self-demotion and self-disabling are refused.
	for _, body := range []map[string]any{{"platform_admin": false}, {"disabled": true}} {
		if status, text := admin.do("PATCH", "/api/admin/users/"+adminID, body); status != http.StatusConflict {
			t.Errorf("self PATCH %v -> %d (want 409): %s", body, status, text)
		}
	}

	// The overview, lists and detail answer.
	var ov platform.Overview
	admin.mustJSON("GET", "/api/admin/overview", nil, 200, &ov)
	if ov.Users < 1 || ov.PlatformAdmins < 1 || len(ov.RecentSignups) == 0 {
		t.Errorf("overview looks empty: %+v", ov)
	}
	var users platform.UserPage
	admin.mustJSON("GET", "/api/admin/users?q=PADMIN-"+suffix, nil, 200, &users)
	if users.Total != 1 || len(users.Items) != 1 || !users.Items[0].PlatformAdmin {
		t.Errorf("case-insensitive user search should find exactly the admin: %+v", users)
	}

	// A member with a session, a team and an MCP token; the admin disables them.
	memberEmail := "victim-" + suffix + "@example.test"
	m := newClient(t, ts.URL)
	m.register(memberEmail)
	memberID := userID(t, m)
	var team struct{ ID string }
	m.mustJSON("POST", "/api/teams", map[string]any{"name": "Victim " + suffix}, 201, &team)
	var tok struct{ Token string }
	m.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "t", "scopes": []string{"read"}, "expires_in_days": 1}, 201, &tok)
	if code := mcpStatus(ts.URL, "/api/mcp", tok.Token); code != 200 {
		t.Fatalf("the member's MCP token should work before disabling: %d", code)
	}

	var detail platform.UserDetail
	admin.mustJSON("PATCH", "/api/admin/users/"+memberID, map[string]any{"disabled": true}, 200, &detail)
	if !detail.Disabled || len(detail.Teams) != 1 || detail.McpTokenCount != 0 {
		t.Errorf("after disabling: want disabled, 1 team, 0 live tokens: %+v", detail)
	}
	if status, text := m.do("GET", "/api/me", nil); status != http.StatusUnauthorized || !strings.Contains(text, "account_disabled") {
		t.Errorf("a disabled account's session: GET /api/me -> %d %s (want 401 account_disabled)", status, text)
	}
	if code := mcpStatus(ts.URL, "/api/mcp", tok.Token); code != http.StatusUnauthorized {
		t.Errorf("a disabled account's MCP token -> %d (want 401)", code)
	}
	fresh := newClient(t, ts.URL)
	if status, text := fresh.do("POST", "/api/auth/login", map[string]any{"email": memberEmail, "password": fresh.pw}); status != http.StatusForbidden {
		t.Errorf("a disabled account signing in -> %d (want 403): %s", status, text)
	}

	// Re-enabling does not revive the old session; a new sign-in works.
	admin.mustJSON("PATCH", "/api/admin/users/"+memberID, map[string]any{"disabled": false}, 200, nil)
	if status, _ := m.do("GET", "/api/me", nil); status != http.StatusUnauthorized {
		t.Errorf("a session from before the account was disabled should stay dead, got %d", status)
	}
	time.Sleep(1100 * time.Millisecond) // JWT iat has one-second resolution
	fresh.login(memberEmail)
	fresh.mustJSON("GET", "/api/me", nil, 200, nil)

	// Deleting: refused while they are the sole owner of a team; fine for an account with none.
	if status, text := admin.do("DELETE", "/api/admin/users/"+memberID, nil); status != http.StatusConflict || !strings.Contains(text, "only owner") {
		t.Errorf("deleting a sole owner -> %d (want 409 naming the team): %s", status, text)
	}
	loner := newClient(t, ts.URL)
	lonerEmail := "loner-" + suffix + "@example.test"
	loner.register(lonerEmail)
	admin.mustJSON("DELETE", "/api/admin/users/"+userID(t, loner), nil, 204, nil)
	if status, _ := loner.do("GET", "/api/me", nil); status != http.StatusUnauthorized {
		t.Errorf("a deleted account's session should be refused, got %d", status)
	}

	// Every change above is in the platform audit log, attributed to the admin.
	var audit platform.AuditPage
	admin.mustJSON("GET", "/api/admin/audit?limit=200", nil, 200, &audit)
	seen := map[string]bool{}
	for _, e := range audit.Items {
		if e.ActorID == adminID {
			seen[e.Action] = true
			if e.ActorEmail != adminEmail {
				t.Errorf("audit entry actor email = %q, want %q", e.ActorEmail, adminEmail)
			}
		}
	}
	for _, a := range []string{"user.updated", "user.deleted"} {
		if !seen[a] {
			t.Errorf("platform audit has no %s by the admin: %+v", a, audit.Items)
		}
	}

	// The same service over the admin MCP.
	var at struct{ Token string }
	admin.mustJSON("POST", "/api/admin/mcp-tokens", map[string]any{"name": "ops", "expires_in_days": 1}, 201, &at)
	for tool, args := range map[string]map[string]any{
		"admin_overview": nil, "list_all_servers": nil, "list_operations": {"limit": 5}, "get_settings": nil, "list_platform_audit": {"limit": 5},
	} {
		if res := mcpCall(t, ts.URL, "/api/admin/mcp", at.Token, tool, args); res.IsError || !strings.HasPrefix(strings.TrimSpace(res.Text), "{") {
			t.Errorf("admin MCP %s: %+v", tool, res)
		}
	}
	if res := mcpCall(t, ts.URL, "/api/admin/mcp", at.Token, "update_user", map[string]any{"user": adminEmail, "platform_admin": false}); !res.IsError {
		t.Errorf("update_user demoting yourself over MCP should be refused: %+v", res)
	}
}

// The last enabled platform admin cannot be demoted or disabled. Through the API the actor is
// always an admin other than the target, so this is reached only by a race; the service is
// tested directly, inside a transaction that is rolled back, with every other admin demoted.
func TestLastPlatformAdminCannotBeDemoted(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	target := userID(t, platformAdmin(t, ts.URL, "last-"+suffix+"@example.test"))

	rollback := errors.New("rollback")
	ctx := context.Background()
	err := tenancy.Tx(ctx, testDB(t), tenancy.Scope{PlatformAdmin: true}, func(q *db.Queries, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET roles = '' WHERE roles ~ '(^|,)\s*admin\s*(,|$)' AND id <> $1`, target); err != nil {
			return err
		}
		f := false
		tr := true
		for _, p := range []platform.UserPatch{{PlatformAdmin: &f}, {Disabled: &tr}} {
			_, err := platform.UpdateUser(ctx, q, "someone-else", target, p)
			var pe *platform.Error
			if !errors.As(err, &pe) || pe.Status != http.StatusConflict || !strings.Contains(pe.Msg, "last platform admin") {
				t.Errorf("demoting/disabling the last admin (%+v): got %v, want 409 last platform admin", p, err)
			}
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("tx: %v", err)
	}
}

func TestSuspendedTeamIsLocked(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	admin := platformAdmin(t, ts.URL, "sadmin-"+suffix+"@example.test")
	owner := newClient(t, ts.URL)
	owner.register("sowner-" + suffix + "@example.test")
	var team struct{ ID, Slug string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Locked " + suffix}, 201, &team)
	var tok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "t", "scopes": []string{"read"}, "expires_in_days": 1}, 201, &tok)

	var d platform.TeamDetail
	admin.mustJSON("PATCH", "/api/admin/teams/"+team.Slug, map[string]any{"suspended": true}, 200, &d)
	if !d.Team.Suspended || d.Counts.Members != 1 {
		t.Fatalf("PATCH suspended: %+v", d)
	}
	for _, c := range []struct {
		method, path string
		body         map[string]any
	}{
		{"GET", "/api/teams/" + team.ID, nil}, {"GET", "/api/teams/" + team.Slug + "/servers", nil},
		{"POST", "/api/teams/" + team.ID + "/servers", map[string]any{"name": "x", "provider": "byo"}},
		{"PATCH", "/api/teams/" + team.ID, map[string]any{"name": "x"}},
	} {
		if status, text := owner.do(c.method, c.path, c.body); status != http.StatusLocked || !strings.Contains(text, "suspended") {
			t.Errorf("suspended team: %s %s -> %d (want 423): %s", c.method, c.path, status, text)
		}
	}
	if code := mcpStatus(ts.URL, "/api/mcp", tok.Token); code != http.StatusLocked {
		t.Errorf("a suspended team's MCP token -> %d (want 423)", code)
	}
	var me struct {
		Teams []struct {
			ID        string
			Suspended bool
		} `json:"teams"`
	}
	owner.mustJSON("GET", "/api/me", nil, 200, &me)
	if len(me.Teams) != 1 || !me.Teams[0].Suspended {
		t.Errorf("/api/me should flag the team as suspended: %+v", me)
	}
	var teams platform.TeamPage
	admin.mustJSON("GET", "/api/admin/teams?q="+team.Slug, nil, 200, &teams)
	if teams.Total != 1 || !teams.Items[0].Suspended {
		t.Errorf("admin team list should show it suspended: %+v", teams)
	}

	admin.mustJSON("PATCH", "/api/admin/teams/"+team.ID, map[string]any{"suspended": false}, 200, nil)
	owner.mustJSON("GET", "/api/teams/"+team.ID, nil, 200, nil)
	if code := mcpStatus(ts.URL, "/api/mcp", tok.Token); code != 200 {
		t.Errorf("a reinstated team's MCP token -> %d (want 200)", code)
	}
}

func TestPlatformSettingsAndLimits(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	t.Cleanup(func() { resetSettings(t) })
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	admin := platformAdmin(t, ts.URL, "setadmin-"+suffix+"@example.test")

	// Public settings: no session needed.
	admin.mustJSON("PUT", "/api/admin/settings", map[string]any{"announcement": "Maintenance " + suffix, "announcement_level": "warning",
		"support_email": "help@example.test"}, 200, nil)
	resp, err := http.Get(ts.URL + "/api/settings/public")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var pub map[string]any
	_ = json.Unmarshal(raw, &pub)
	if resp.StatusCode != 200 || pub["signup_mode"] != "open" || pub["announcement"] != "Maintenance "+suffix ||
		pub["announcement_level"] != "warning" || pub["support_email"] != "help@example.test" {
		t.Fatalf("GET /api/settings/public anonymously -> %d %s", resp.StatusCode, raw)
	}
	if _, leaked := pub["max_teams_per_user"]; leaked {
		t.Errorf("public settings must not include the limits: %s", raw)
	}
	if status, _ := admin.do("PUT", "/api/admin/settings", map[string]any{"announcement": strings.Repeat("x", 501)}); status != 422 {
		t.Errorf("a 501-character announcement -> %d (want 422)", status)
	}

	// signup_mode=closed blocks registration; invite_only needs a valid invitation for that email.
	admin.mustJSON("PUT", "/api/admin/settings", map[string]any{"signup_mode": "closed"}, 200, nil)
	c := newClient(t, ts.URL)
	if status, text := c.do("POST", "/api/auth/register", map[string]any{"email": "closed-" + suffix + "@example.test", "password": c.pw}); status != http.StatusForbidden || !strings.Contains(text, "closed") {
		t.Errorf("register with signup closed -> %d (want 403): %s", status, text)
	}
	admin.mustJSON("PUT", "/api/admin/settings", map[string]any{"signup_mode": "open"}, 200, nil)
	inviter := newClient(t, ts.URL)
	inviter.register("inviter-" + suffix + "@example.test")
	var team struct{ ID string }
	inviter.mustJSON("POST", "/api/teams", map[string]any{"name": "Inviter " + suffix}, 201, &team)
	invitee := "invitee-" + suffix + "@example.test"
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	inviter.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": invitee, "role": "viewer"}, 201, &inv)
	token := inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]

	admin.mustJSON("PUT", "/api/admin/settings", map[string]any{"signup_mode": "invite_only"}, 200, nil)
	for _, body := range []map[string]any{
		{"email": invitee, "password": c.pw}, // no invitation
		{"email": "other-" + suffix + "@example.test", "password": c.pw, "invitation_token": token}, // someone else's invitation
		{"email": invitee, "password": c.pw, "invitation_token": "not-a-token"},
	} {
		if status, text := c.do("POST", "/api/auth/register", body); status != http.StatusForbidden {
			t.Errorf("invite-only register %v -> %d (want 403): %s", body["email"], status, text)
		}
	}
	c.mustJSON("POST", "/api/auth/register", map[string]any{"email": invitee, "password": c.pw, "invitation_token": token}, 201, nil)
	c.mustJSON("POST", "/api/invitations/"+token+"/accept", nil, 200, nil)
	admin.mustJSON("PUT", "/api/admin/settings", map[string]any{"signup_mode": "open"}, 200, nil)

	// max_teams_per_user.
	admin.mustJSON("PUT", "/api/admin/settings", map[string]any{"max_teams_per_user": 1, "max_servers_per_team": 1}, 200, nil)
	u := newClient(t, ts.URL)
	u.register("limited-" + suffix + "@example.test")
	var t1 struct{ ID string }
	u.mustJSON("POST", "/api/teams", map[string]any{"name": "One " + suffix}, 201, &t1)
	if status, text := u.do("POST", "/api/teams", map[string]any{"name": "Two " + suffix}); status != http.StatusForbidden {
		t.Errorf("a second team over max_teams_per_user=1 -> %d (want 403): %s", status, text)
	}
	// max_servers_per_team, over REST and over the team MCP (both go through AddServer).
	u.mustJSON("POST", "/api/teams/"+t1.ID+"/servers", map[string]any{"name": "s1", "provider": "byo"}, 201, nil)
	if status, text := u.do("POST", "/api/teams/"+t1.ID+"/servers", map[string]any{"name": "s2", "provider": "byo"}); status != http.StatusForbidden || !strings.Contains(text, "most this platform allows") {
		t.Errorf("a second server over max_servers_per_team=1 -> %d (want 403): %s", status, text)
	}
	var tok struct{ Token string }
	u.mustJSON("POST", "/api/teams/"+t1.ID+"/mcp-tokens", map[string]any{"name": "w", "scopes": []string{"read", "write"}, "expires_in_days": 1}, 201, &tok)
	if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "add_server", map[string]any{"name": "s3", "method": "install"}); !res.IsError || !strings.Contains(res.Text, "most this platform allows") {
		t.Errorf("add_server over the limit via MCP should be refused: %+v", res)
	}

	var got platform.Settings
	admin.mustJSON("GET", "/api/admin/settings", nil, 200, &got)
	if got.MaxTeamsPerUser != 1 || got.MaxServersPerTeam != 1 || got.SignupMode != "open" {
		t.Errorf("GET /api/admin/settings: %+v", got)
	}
}
