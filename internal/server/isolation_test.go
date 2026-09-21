package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	_ "github.com/jackc/pgx/v5/stdlib"

	"crypto/ed25519"
	"encoding/base64"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/api"
	"github.com/fadymondy/hosbah/internal/config"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/migrate"
	"github.com/fadymondy/hosbah/internal/monitors"
	"github.com/fadymondy/hosbah/internal/ops"
)

/*
The isolation sweep — P0's exit criterion, run as a test so CI keeps it true.

Team A is built up through the real API (a team, a second member who joined by invitation, a
pending invitation, an MCP token). Then user B, who owns an unrelated team, calls EVERY
operation in the OpenAPI document that takes a path parameter, with team A's ids substituted
in — by id and by slug — and a body that passes validation, so the 404 is the handler's answer
and not a 422 masking it. Every team-scoped call must be 404; platform-admin routes must be
403. New routes are covered automatically: an operation whose path parameter the sweep does
not know how to fill fails the test instead of being skipped.

Afterwards team A must be exactly as it was. The same sweep runs over the user MCP with B's
all-scopes token.

Needs TEST_DATABASE_URL (a Postgres the app role owns; it is migrated here).
*/
func TestIsolationSweep(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	ctx := context.Background()
	_ = ctx

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ua := newClient(t, ts.URL)
	ua.register("owner-a-" + suffix + "@example.test")
	ub := newClient(t, ts.URL)
	ub.register("owner-b-" + suffix + "@example.test")
	ua2Email := "member-a-" + suffix + "@example.test"
	ua2 := newClient(t, ts.URL)
	ua2.register(ua2Email)

	// Team A: created by A, joined by A2 through an invitation.
	var teamA struct{ ID, Slug, Name string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "Alpha " + suffix}, 201, &teamA)
	var inv1 struct {
		Invitation struct{ ID string } `json:"invitation"`
		AcceptURL  string              `json:"accept_url"`
	}
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/invitations", map[string]any{"email": ua2Email, "role": "deployer"}, 201, &inv1)
	token1 := inv1.AcceptURL[strings.LastIndex(inv1.AcceptURL, "/")+1:]
	ua2.mustJSON("POST", "/api/invitations/"+token1+"/accept", nil, 200, nil)

	var members struct {
		Members []struct{ ID, Email, Role string } `json:"members"`
	}
	ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/members", nil, 200, &members)
	memberA := ""
	for _, m := range members.Members {
		if m.Email == ua2Email {
			memberA = m.ID
		}
	}
	if memberA == "" || len(members.Members) != 2 {
		t.Fatalf("team A should have 2 members including %s: %+v", ua2Email, members)
	}

	var inv2 struct {
		Invitation struct{ ID string } `json:"invitation"`
	}
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/invitations", map[string]any{"email": "pending-" + suffix + "@example.test", "role": "viewer"}, 201, &inv2)
	var tokA struct{ ID, Token string }
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/mcp-tokens", map[string]any{"name": "a-read", "scopes": []string{"read"}, "expires_in_days": 30}, 201, &tokA)

	// P1: a bring-your-own server that enrolled through the real endpoint, an operation on it, and a
	// provider credential (saved unvalidated: no provider is ever contacted by the tests).
	srvA, opA := enrolledServerWithOp(t, ua, ts.URL, teamA.ID, "alpha-"+suffix)
	var credA struct{ ID string }
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/credentials", map[string]any{"provider": "proxmox", "name": "pve-a",
		"config": map[string]string{"api_url": "https://pve.invalid:8006", "token_id": "cloudy@pve!ctl", "node": "pve", "storage": "local-lvm"},
		"secret": "a-secret-" + suffix, "skip_validation": true}, 201, &credA)

	// P2: an app on that server with a deployment, a variable and a domain.
	var appA struct {
		App struct{ ID, Name string } `json:"app"`
		Deployment struct{ ID string } `json:"deployment"`
	}
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/apps", map[string]any{"name": "alpha-app-" + suffix[len(suffix)-6:], "server": srvA,
		"repo_url": "https://github.com/example/app", "deploy": true}, 201, &appA)
	ua.mustJSON("PUT", "/api/teams/"+teamA.ID+"/apps/"+appA.App.ID+"/env/SECRET_A", map[string]any{"value": "a-env-" + suffix}, 204, nil)
	// A look and an uploaded logo, so the swept PATCH and …/apps/{app}/logo routes have something to leak.
	ua.mustJSON("PATCH", "/api/teams/"+teamA.ID+"/apps/"+appA.App.ID, map[string]any{"title": "Alpha", "icon": "rocket"}, 200, nil)
	if resp, b := ua.raw("PUT", "/api/teams/"+teamA.ID+"/apps/"+appA.App.ID+"/logo", "image/png", []byte("\x89PNG\r\n\x1a\nalpha")); resp.StatusCode != 200 {
		t.Fatalf("app logo: %d %s", resp.StatusCode, b)
	}
	var domA struct{ ID, Hostname string }
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/apps/"+appA.App.ID+"/domains", map[string]any{"hostname": "a-" + suffix + ".example.test", "tls": "off"}, 201, &domA)
	// A monitor of app A (alerts & monitors): an http check of its own hostname.
	var monA struct{ ID, Name string }
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/apps/"+appA.App.ID+"/monitors", map[string]any{"name": "up-a", "kind": "http",
		"target": "https://a-" + suffix + ".example.test/health", "enabled": false}, 201, &monA)

	// Imported workloads of A's: a site and a database found running on that server (records only).
	if _, err := importer.Sync(context.Background(), testDB(t), teamA.ID, srvA, []agentproto.Service{
		{Kind: "container", Name: "imp-site", Image: "nginx", Status: "running"},
		{Kind: "container", Name: "imp-pg", Image: "postgres:16", Status: "running"}}); err != nil {
		t.Fatalf("import: %v", err)
	}
	var impApps struct {
		Apps []struct{ ID, Name, Source string } `json:"apps"`
	}
	ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/apps", nil, 200, &impApps)
	var impDS struct {
		Services []struct{ ID, Name, Source string } `json:"services"`
	}
	ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/data-services", nil, 200, &impDS)
	impAppID, impSvcID := "", ""
	for _, a := range impApps.Apps {
		if a.Source == "imported" && a.Name == "imp-site" {
			impAppID = a.ID
		}
	}
	for _, s := range impDS.Services {
		if s.Source == "imported" && s.Name == "imp-pg" {
			impSvcID = s.ID
		}
	}
	if impAppID == "" || impSvcID == "" {
		t.Fatalf("team A's imported records are missing: %+v %+v", impApps, impDS)
	}

	// P5: a proxy host of A's (Caddy proxy manager) on that server.
	var proxyA struct{ ID, Hostname string }
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/proxy-hosts", map[string]any{"server": srvA, "hostname": "nas-" + suffix + ".example.test",
		"upstream": "10.0.0.5:5000", "tls": "off"}, 201, &proxyA)

	// P4: a pending approval request of A's, made the only way one can be made — by turning the
	// team setting on and letting a destructive MCP call be held. Sweeping it matters because an
	// approval is a capability to destroy something: B must not be able to read it or decide it.
	ua.mustJSON("PATCH", "/api/teams/"+teamA.ID, map[string]any{"require_destructive_approval": true}, 200, nil)
	var tokADestructive struct{ ID, Token string }
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/mcp-tokens", map[string]any{"name": "a-destructive",
		"scopes": []string{"read", "destructive"}, "expires_in_days": 30}, 201, &tokADestructive)
	if res := mcpCall(t, ts.URL, "/api/mcp", tokADestructive.Token, "delete_server",
		map[string]any{"server": srvA, "confirm": "alpha-" + suffix}); !res.IsError {
		t.Fatalf("with approval required, delete_server should have been held: %q", res.Text)
	}
	var approvalsA struct {
		Requests []struct {
			ID string `json:"id"`
		} `json:"requests"`
	}
	ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/approvals?status=pending", nil, 200, &approvalsA)
	if len(approvalsA.Requests) != 1 {
		t.Fatalf("team A should have one pending approval request, got %+v", approvalsA.Requests)
	}
	approvalA := approvalsA.Requests[0].ID

	// Team B: B's own, unrelated team.
	var teamB struct{ ID, Slug string }
	ub.mustJSON("POST", "/api/teams", map[string]any{"name": "Bravo " + suffix}, 201, &teamB)

	// ---- REST sweep -------------------------------------------------------------------------
	oapi := openAPIDoc()
	fillers := map[string]func(path string) []string{
		"team":       func(string) []string { return []string{teamA.ID, teamA.Slug} },
		"member":     func(string) []string { return []string{memberA} },
		"server":     func(string) []string { return []string{srvA, "alpha-" + suffix} }, // an id, and a name (must not resolve either)
		"op":         func(string) []string { return []string{opA} },
		"credential": func(string) []string { return []string{credA.ID} },
		"token":      func(string) []string { return []string{inv2.Invitation.ID} }, // an id is not a token
		"app":        func(string) []string { return []string{appA.App.ID, appA.App.Name, impAppID} },
		"snapshot":   func(string) []string { return []string{"pre-up"} },
		"deployment": func(string) []string { return []string{appA.Deployment.ID, "1"} },
		"name":       func(string) []string { return []string{"SECRET_A"} },
		"domain":     func(string) []string { return []string{domA.ID, domA.Hostname} },
		"hostname":   func(string) []string { return []string{domA.Hostname} }, // …/dns/{hostname}: one of app A's own
		"monitor":    func(string) []string { return []string{monA.ID, monA.Name} },
		"proxy":      func(string) []string { return []string{proxyA.ID, proxyA.Hostname} },
		"kind":       func(string) []string { return []string{"frp", "tailscale"} },
		"owner":      func(string) []string { return []string{"octocat"} }, // GitHub owner of a repository (the server is A's)
		"service":    func(string) []string { return []string{"00000000-0000-4000-8000-0000000000d1", "svc-x", impSvcID, "imp-pg"} }, // services B may not see (one of A's imported)
		"backup":     func(string) []string { return []string{"00000000-0000-4000-8000-0000000000b1"} },
		// An approval request of team A's, plus a made-up id. Team B must get the same 404 for
		// both: an approval authorises destroying something, so being able to tell "exists but
		// not yours" from "does not exist" would be a way to enumerate another team's pending
		// deletions — and, worse, a route that leaked through would let B approve them.
		"approval": func(string) []string { return []string{approvalA, "00000000-0000-4000-8000-0000000000a1"} },
		"id": func(path string) []string {
			switch {
			case strings.Contains(path, "/invitations/"):
				return []string{inv2.Invitation.ID}
			case strings.Contains(path, "/mcp-tokens/"):
				return []string{tokA.ID}
			}
			return []string{inv2.Invitation.ID, tokA.ID}
		},
	}

	checked := 0
	for _, op := range operations(oapi) {
		params := pathParams(op.Path)
		if len(params) == 0 {
			continue // covered by the no-path-parameter checks below
		}
		for _, p := range params {
			if fillers[p] == nil {
				t.Fatalf("%s %s: the sweep does not know how to fill {%s} — teach it before shipping the route", op.Method, op.Path, p)
			}
		}
		for _, path := range expand(op.Path, params, fillers) {
			body := sampleBody(oapi, op.Op)
			status, text := ub.do(op.Method, path, body)
			want := http.StatusNotFound
			if strings.HasPrefix(op.Path, "/api/admin/") {
				want = http.StatusForbidden // platform administration, not a team route
			}
			if status != want {
				t.Errorf("as team B: %s %s -> %d (want %d): %s", op.Method, path, status, want, text)
			}
			checked++
		}
	}
	if checked < 15 {
		t.Fatalf("the sweep only made %d calls — did the OpenAPI document shrink?", checked)
	}
	t.Logf("REST sweep: %d cross-team calls, all refused", checked)

	// ---- routes without path parameters ----------------------------------------------------
	// /api/me lists only B's own team.
	var meB struct {
		Teams []struct{ ID string } `json:"teams"`
	}
	ub.mustJSON("GET", "/api/me", nil, 200, &meB)
	for _, tm := range meB.Teams {
		if tm.ID == teamA.ID {
			t.Errorf("GET /api/me as B lists team A")
		}
	}
	// A report cannot claim a team the reporter is not in.
	if status, text := ub.do("POST", "/api/feedback", map[string]any{"title": "x", "team_id": teamA.ID}); status != 404 {
		t.Errorf("POST /api/feedback with team A's id as B -> %d (want 404): %s", status, text)
	}
	// B cannot mint in A by guessing: POST /api/teams always creates B's own team (covered by
	// the RLS insert policy test in internal/tenancy).

	// ---- user MCP sweep ---------------------------------------------------------------------
	var tokB struct{ Token string }
	ub.mustJSON("POST", "/api/teams/"+teamB.ID+"/mcp-tokens", map[string]any{"name": "b-all", "scopes": []string{"read", "deploy", "write", "secrets:read", "destructive"}, "expires_in_days": 30}, 201, &tokB)
	who := mcpCall(t, ts.URL, "/api/mcp", tokB.Token, "whoami", nil)
	if who.IsError || !strings.Contains(who.Text, teamB.ID) || strings.Contains(who.Text, teamA.ID) {
		t.Errorf("whoami with B's token should name team B only: %+v", who)
	}
	for name, args := range map[string]map[string]any{
		"revoke_invitation": {"id": inv2.Invitation.ID, "confirm": "pending-" + suffix + "@example.test"},
		"remove_member":     {"member_id": memberA, "confirm": ua2Email},
	} {
		res := mcpCall(t, ts.URL, "/api/mcp", tokB.Token, name, args)
		if !res.IsError || !strings.Contains(res.Text, "not found") {
			t.Errorf("MCP %s as team B on team A's id should be not found: %+v", name, res)
		}
	}
	// P2 MCP tools with team A's app: every one must say not found and change nothing.
	for name, args := range map[string]map[string]any{
		"get_app": {"app": appA.App.ID}, "list_deployments": {"app": appA.App.Name}, "get_deployment": {"app": appA.App.ID, "deployment": "1"},
		"deploy_logs": {"app": appA.App.ID, "deployment": appA.Deployment.ID}, "list_env": {"app": appA.App.ID},
		"list_domains": {"app": appA.App.ID}, "deploy_app": {"app": appA.App.ID}, "cancel_deployment": {"app": appA.App.ID, "deployment": "1"},
		"rollback_app": {"app": appA.App.ID}, "restart_app": {"app": appA.App.ID}, "pause_autodeploy": {"app": appA.App.ID},
		"set_env": {"app": appA.App.ID, "name": "X", "value": "y"}, "unset_env": {"app": appA.App.ID, "name": "SECRET_A"},
		"add_domain": {"app": appA.App.ID, "hostname": "b.example.test"}, "verify_domain": {"app": appA.App.ID, "domain": domA.ID},
		"reveal_env": {"app": appA.App.ID, "name": "SECRET_A"}, "delete_app": {"app": appA.App.ID, "confirm": appA.App.Name},
		"verify_proxy_host": {"proxy": proxyA.ID}, "update_proxy_host": {"proxy": proxyA.Hostname, "upstream": "1.2.3.4:80"},
		"remove_proxy_host": {"proxy": proxyA.ID},
		"list_sites": {"server": srvA},
		"enable_tunnel": {"server": srvA, "credential": credA.ID}, "disable_tunnel": {"server": srvA, "kind": "frp"},
		"update_app": {"app": appA.App.ID, "branch": "x", "title": "stolen", "color": "#000000"}, "list_releases": {"app": appA.App.ID}, "rotate_deploy_hook": {"app": appA.App.ID},
		"attach_database": {"app": appA.App.ID, "database": "svc-x"}, "run_server_step": {"server": srvA, "kind": "system.info"},
		"update_member_role": {"member": memberA, "role": "viewer"}, "delete_credential": {"credential": credA.ID, "confirm": "pve-a"},
		"github_status": {"server": srvA, "refresh": true}, "github_connect": {"server": srvA, "token": "ghp_sweep"},
		"github_disconnect": {"server": srvA}, "github_repos": {"server": srvA}, "github_branches": {"server": srvA, "repo": "octocat/api"},
		"update_server": {"server": srvA, "color": "#000000", "name": "stolen"},
		"get_app_repository": {"app": appA.App.ID}, "list_app_commits": {"app": appA.App.Name, "branch": "main"},
		"compare_app_commits": {"app": appA.App.ID, "base": "main", "head": "dev"},
		"get_app_dns": {"app": appA.App.ID}, "get_app_certificates": {"app": appA.App.Name},
		"set_app_dns_record": {"app": appA.App.ID, "hostname": domA.Hostname, "takeover": true},
		"list_app_monitors": {"app": appA.App.ID}, "list_app_incidents": {"app": appA.App.Name},
		"create_app_monitor": {"app": appA.App.ID, "name": "stolen", "kind": "cpu", "threshold": 90},
		"update_app_monitor": {"app": appA.App.ID, "monitor": monA.ID, "enabled": true}, "delete_app_monitor": {"app": appA.App.ID, "monitor": monA.Name},
	} {
		res := mcpCall(t, ts.URL, "/api/mcp", tokB.Token, name, args)
		if !res.IsError || !strings.Contains(res.Text, "not found") || strings.Contains(res.Text, "a-env-") {
			t.Errorf("MCP %s as team B on team A's app should be not found: %+v", name, res)
		}
	}
	// The team-wide deployment feed (list_deployments without an app) is B's own only.
	if res := mcpCall(t, ts.URL, "/api/mcp", tokB.Token, "list_deployments", nil); res.IsError || strings.Contains(res.Text, appA.App.ID) || strings.Contains(res.Text, appA.Deployment.ID) {
		t.Errorf("list_deployments (team feed) as B leaked team A's deployments: %+v", res)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", tokB.Token, "list_proxy_hosts", nil); strings.Contains(res.Text, proxyA.Hostname) {
		t.Errorf("list_proxy_hosts as B leaked team A's proxy host: %s", res.Text)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", tokB.Token, "list_apps", nil); strings.Contains(res.Text, appA.App.Name) || strings.Contains(res.Text, "imp-site") {
		t.Errorf("list_apps as B leaked team A's app: %s", res.Text)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", tokB.Token, "list_databases", nil); strings.Contains(res.Text, "imp-pg") {
		t.Errorf("list_databases as B leaked team A's imported database: %s", res.Text)
	}
	// Imported records of A's, by id and name: not found, and nothing acts on them.
	for name, args := range map[string]map[string]any{
		"get_app": {"app": impAppID}, "delete_app": {"app": impAppID, "confirm": "imp-site"}, "deploy_app": {"app": "imp-site"},
		"get_database": {"database": impSvcID}, "delete_data_service": {"database": "imp-pg", "confirm": "imp-pg"},
		"backup_now": {"database": impSvcID},
		"workload_power": {"app": impAppID, "action": "stop"}, "workload_stats": {"app": impAppID}, "workload_logs": {"app": "imp-site"},
		"recreate_container": {"app": impAppID}, "resize_guest": {"app": impAppID, "cores": 2}, "list_snapshots": {"app": impAppID},
		"create_snapshot": {"app": impAppID, "name": "snap1"}, "rollback_snapshot": {"app": impAppID, "name": "snap1"},
		"delete_snapshot": {"app": impAppID, "name": "snap1", "confirm": "imp-site"}, "backup_guest": {"app": impAppID},
		"permanently_destroy_app": {"app": impAppID, "confirm": "imp-site"}, "permanently_destroy_data_service": {"database": impSvcID, "confirm": "imp-pg"},
		"set_database_credentials": {"database": "imp-pg", "username": "u", "password": "p"},
	} {
		res := mcpCall(t, ts.URL, "/api/mcp", tokB.Token, name, args)
		if !res.IsError || !strings.Contains(res.Text, "not found") {
			t.Errorf("MCP %s as team B on team A's imported record should be not found: %+v", name, res)
		}
	}
	// P3 data-service MCP tools: a service ref team B may not see is not found; nothing acts.
	for name, args := range map[string]map[string]any{
		"get_database": {"database": "svc-x"}, "list_backups": {"database": "svc-x"}, "backup_now": {"database": "svc-x"},
		"reveal_db_url": {"database": "svc-x"}, "restore_backup": {"database": "svc-x", "confirm": "svc-x"},
		"create_db": {"database": "svc-x", "name": "x"}, "drop_database": {"database": "svc-x", "name": "x", "confirm": "x"},
		"delete_data_service": {"database": "svc-x", "confirm": "svc-x"}, "set_backup_schedule": {"database": "svc-x", "cron": "0 3 * * *"},
	} {
		res := mcpCall(t, ts.URL, "/api/mcp", tokB.Token, name, args)
		if !res.IsError {
			t.Errorf("MCP %s as team B on an unknown data service should error: %+v", name, res)
		}
	}
	// A deploy-hook token is not a session: hooks of unknown tokens are 404, the TLS ask says no.
	if code := httpStatus(t, "POST", ts.URL+"/hooks/deploy/cdh_nope", ""); code != 404 {
		t.Errorf("unknown deploy hook -> %d", code)
	}
	if code := httpStatus(t, "GET", ts.URL+"/internal/tls/allow?domain="+domA.Hostname, ""); code != 404 {
		t.Errorf("TLS ask for an unverified tls=off domain -> %d (want 404)", code)
	}
	if code := httpStatus(t, "POST", ts.URL+"/hooks/github", "{}"); code != 503 {
		t.Errorf("GitHub webhook without the App configured -> %d (want 503)", code)
	}

	for _, name := range []string{"list_members", "list_invitations", "list_audit", "list_mcp_tokens"} {
		res := mcpCall(t, ts.URL, "/api/mcp", tokB.Token, name, nil)
		if strings.Contains(res.Text, ua2Email) || strings.Contains(res.Text, "a-read") || strings.Contains(res.Text, "pending-"+suffix) {
			t.Errorf("MCP %s as team B leaked team A data: %s", name, res.Text)
		}
	}

	// A's read-only token: whoami works, an out-of-scope call fails in-band.
	if res := mcpCall(t, ts.URL, "/api/mcp", tokA.Token, "whoami", nil); res.IsError || !strings.Contains(res.Text, teamA.ID) {
		t.Errorf("whoami with A's token: %+v", res)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", tokA.Token, "invite_member", map[string]any{"email": "x@example.test", "role": "viewer"}); !res.IsError || !strings.Contains(res.Text, `"write" scope`) {
		t.Errorf("invite_member with a read-only token should fail in-band naming the write scope: %+v", res)
	}
	// A deployer's token cannot manage members even with the scope (role still applies).
	// A bad token is a transport-level 401.
	if code := mcpStatus(ts.URL, "/api/mcp", "cld_notavalidtoken"); code != 401 {
		t.Errorf("an invalid MCP token should be 401, got %d", code)
	}
	if code := mcpStatus(ts.URL, "/api/admin/mcp", tokB.Token); code != 401 {
		t.Errorf("a team token on the admin MCP should be 401, got %d", code)
	}

	// ---- team A is untouched ------------------------------------------------------------------
	var envAfter struct {
		Variables []struct{ Name string } `json:"variables"`
	}
	ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/apps/"+appA.App.ID+"/env", nil, 200, &envAfter)
	if len(envAfter.Variables) != 1 {
		t.Errorf("team A's env changed during the sweep: %+v", envAfter)
	}
	var revealed struct{ Value string }
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/apps/"+appA.App.ID+"/env/SECRET_A/reveal", nil, 200, &revealed)
	if revealed.Value != "a-env-"+suffix {
		t.Errorf("reveal as owner: %q", revealed.Value)
	}
	ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/apps/"+appA.App.ID, nil, 200, nil)
	ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/apps/"+impAppID, nil, 200, nil)
	ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/data-services/"+impSvcID, nil, 200, nil)
	var after struct {
		Name        string
		MemberCount int64 `json:"member_count"`
	}
	ua.mustJSON("GET", "/api/teams/"+teamA.ID, nil, 200, &after)
	if after.Name != "Alpha "+suffix || after.MemberCount != 2 {
		t.Errorf("team A changed during the sweep: %+v", after)
	}
	var invs struct {
		Invitations []struct{ ID, Status string } `json:"invitations"`
	}
	ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/invitations", nil, 200, &invs)
	for _, i := range invs.Invitations {
		if i.ID == inv2.Invitation.ID && i.Status != "pending" {
			t.Errorf("team A's pending invitation became %s during the sweep", i.Status)
		}
	}
	var toks struct {
		Tokens []struct {
			ID        string
			RevokedAt *string `json:"revoked_at"`
		} `json:"tokens"`
	}
	ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/mcp-tokens", nil, 200, &toks)
	for _, tk := range toks.Tokens {
		if tk.ID == tokA.ID && tk.RevokedAt != nil {
			t.Errorf("team A's token was revoked during the sweep")
		}
	}
}

// A report filed through the UI's endpoint lands in Hosbah's own tracker and the admin MCP
// lists it; a team token is refused there, and a non-admin cannot mint an admin token.
func TestFeedbackReachesAdminMCP(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	reporter := newClient(t, ts.URL)
	reporter.register("reporter-" + suffix + "@example.test")
	var filed struct{ Key string }
	reporter.mustJSON("POST", "/api/feedback", map[string]any{"title": "Button does nothing " + suffix, "issue_type": "bug", "route": "/t/x/members", "viewport": "1440x900"}, 201, &filed)
	if !strings.HasPrefix(filed.Key, "CLD-") {
		t.Fatalf("expected a CLD- key, got %q", filed.Key)
	}

	if status, _ := reporter.do("POST", "/api/admin/mcp-tokens", map[string]any{"name": "x", "expires_in_days": 1}); status != 403 {
		t.Errorf("a non-admin minting an admin token -> %d (want 403)", status)
	}

	adminEmail := "admin-" + suffix + "@example.test"
	admin := newClient(t, ts.URL)
	admin.register(adminEmail)
	db := testDB(t)
	if _, err := db.Exec(`UPDATE users SET roles = 'admin' WHERE email = $1`, adminEmail); err != nil {
		t.Fatal(err)
	}
	admin.login(adminEmail) // the session carries roles from sign-in
	var at struct{ Token string }
	admin.mustJSON("POST", "/api/admin/mcp-tokens", map[string]any{"name": "triage", "expires_in_days": 1}, 201, &at)

	res := mcpCall(t, ts.URL, "/api/admin/mcp", at.Token, "list_issues", map[string]any{"q": suffix})
	if res.IsError || !strings.Contains(res.Text, filed.Key) {
		t.Fatalf("admin list_issues should include %s: %+v", filed.Key, res)
	}
	res = mcpCall(t, ts.URL, "/api/admin/mcp", at.Token, "get_issue", map[string]any{"key": filed.Key})
	if res.IsError || !strings.Contains(res.Text, "reporter-"+suffix) {
		t.Fatalf("admin get_issue should show the reporter: %+v", res)
	}
	if res := mcpCall(t, ts.URL, "/api/admin/mcp", at.Token, "start_issue", map[string]any{"key": filed.Key}); res.IsError {
		t.Fatalf("start_issue: %+v", res)
	}
	if res := mcpCall(t, ts.URL, "/api/admin/mcp", at.Token, "comment_issue", map[string]any{"key": filed.Key, "body": "Looking."}); res.IsError {
		t.Fatalf("comment_issue: %+v", res)
	}
	if res := mcpCall(t, ts.URL, "/api/admin/mcp", at.Token, "finish_issue", map[string]any{"key": filed.Key, "resolution": "fixed"}); res.IsError {
		t.Fatalf("finish_issue: %+v", res)
	}
	// The reporter sees their own report's new status; nobody else's.
	var mine struct {
		Reports []struct{ Key, Status string } `json:"reports"`
	}
	reporter.mustJSON("GET", "/api/me/reports", nil, 200, &mine)
	if len(mine.Reports) != 1 || mine.Reports[0].Status != "done" {
		t.Fatalf("reporter should see exactly their one report, now done: %+v", mine)
	}
	// Demoting the admin kills their admin token at once.
	if _, err := db.Exec(`UPDATE users SET roles = '' WHERE email = $1`, adminEmail); err != nil {
		t.Fatal(err)
	}
	if code := mcpStatus(ts.URL, "/api/admin/mcp", at.Token); code != 401 {
		t.Errorf("an admin token of a demoted account should be 401, got %d", code)
	}
}

// ---- harness ------------------------------------------------------------------------------

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	config.LoadDotEnv()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the isolation sweep")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func bootTestServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	db := testDB(t)
	if err := migrate.Run(context.Background(), db, migrate.RepoDir(migrate.Dir), false, io.Discard); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Setenv("DATABASE_URL", os.Getenv("TEST_DATABASE_URL"))
	t.Setenv("APP_ENV", "development")
	mailSender = mailbox.send
	a := Boot()
	if err := a.Kernel.Boot(context.Background()); err != nil {
		t.Fatalf("boot: %v", err)
	}
	ts := httptest.NewServer(a.Kernel.Handler())
	a.Ops.(*ops.Service).SetPublicURL(ts.URL)
	lastService = a.Ops.(*ops.Service)
	lastDeploy, _ = a.Deploy.(*deploy.Service)
	lastMonitors, _ = a.Monitors.(*monitors.Service)
	return ts, func() { a.Stop(); ts.Close(); a.Kernel.Close() }
}

// lastService is the ops service of the most recent bootTestServer (tests in this package run
// sequentially).
var lastService *ops.Service

// lastMonitors is the alerts evaluator of the most recent bootTestServer (a test drives its clock,
// its HTTP transport and its ticks).
var lastMonitors *monitors.Service

// lastDeploy is the deploy service of the most recent bootTestServer (a test points its Cloudflare
// clients at a fake).
var lastDeploy *deploy.Service

func svcOf(t *testing.T) *ops.Service {
	t.Helper()
	if lastService == nil {
		t.Fatal("no test server booted")
	}
	return lastService
}

// enrolledServerWithOp adds a BYO server to a team, enrols it through POST /agent/enroll with the
// token from its install command (as the agent would), and queues a system.info on it.
func enrolledServerWithOp(t *testing.T, c *client, base, teamID, name string) (serverID, opID string) {
	t.Helper()
	var created struct {
		Server  struct{ ID string }
		Install struct{ URL string }
	}
	c.mustJSON("POST", "/api/teams/"+teamID+"/servers", map[string]any{"name": name, "provider": "byo"}, 201, &created)
	token := created.Install.URL[strings.LastIndex(created.Install.URL, "/")+1:]
	pub, _, _ := ed25519.GenerateKey(nil)
	body, _ := json.Marshal(agentproto.EnrollRequest{Token: token, PublicKey: base64.StdEncoding.EncodeToString(pub), AgentVersion: "test",
		Facts: agentproto.Facts{Hostname: name, OSID: "ubuntu", OSVersion: "24.04", Arch: "amd64", CPUs: 2, MemTotalBytes: 2 << 30, Systemd: true}})
	resp, err := http.Post(base+"/agent/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("enrol %s: %d", name, resp.StatusCode)
	}
	var op struct{ ID string }
	c.mustJSON("POST", "/api/teams/"+teamID+"/servers/"+created.Server.ID+"/operations", map[string]any{"kind": "system.info"}, 201, &op)
	return created.Server.ID, op.ID
}

type client struct {
	t    *testing.T
	base string
	http *http.Client
	pw   string
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar, Timeout: 20 * time.Second}, pw: "correct horse battery"}
}

func (c *client) csrf() string {
	u, _ := url.Parse(c.base)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == "togo_csrf" {
			return ck.Value
		}
	}
	resp, err := c.http.Get(c.base + "/api/auth/csrf")
	if err != nil {
		c.t.Fatal(err)
	}
	resp.Body.Close()
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == "togo_csrf" {
			return ck.Value
		}
	}
	c.t.Fatal("no csrf cookie")
	return ""
}

func (c *client) do(method, path string, body any) (int, string) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if method != "GET" {
		req.Header.Set("X-CSRF-Token", c.csrf())
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (c *client) mustJSON(method, path string, body any, want int, out any) {
	c.t.Helper()
	status, text := c.do(method, path, body)
	if status != want {
		c.t.Fatalf("%s %s -> %d (want %d): %s", method, path, status, want, text)
	}
	if out != nil && text != "" {
		if err := json.Unmarshal([]byte(text), out); err != nil {
			c.t.Fatalf("%s %s: decode %v: %s", method, path, err, text)
		}
	}
}

// register signs up, which answers 403 email_unverified, then enters the emailed code, which
// signs in (internal/account).
func (c *client) register(email string) {
	c.t.Helper()
	before := mailbox.count(email)
	c.mustJSON("POST", "/api/auth/register", map[string]any{"email": email, "password": c.pw}, 403, nil)
	code := mailbox.wait(c.t, email, before)
	c.mustJSON("POST", "/api/auth/verify-email", map[string]any{"email": email, "code": code}, 200, nil)
}

func (c *client) login(email string) {
	c.t.Helper()
	c.mustJSON("POST", "/api/auth/login", map[string]any{"email": email, "password": c.pw}, 200, nil)
}

type mcpResult struct {
	IsError bool
	Text    string
}

func mcpCall(t *testing.T, base, path, token, tool string, args map[string]any) mcpResult {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	req, _ := http.NewRequest("POST", base+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
		Error *struct{ Message string } `json:"error"`
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	if out.Error != nil {
		return mcpResult{IsError: true, Text: "rpc error: " + out.Error.Message + " (http " + resp.Status + ")"}
	}
	text := ""
	for _, c := range out.Result.Content {
		text += c.Text
	}
	return mcpResult{IsError: out.Result.IsError, Text: text}
}

func httpStatus(t *testing.T, method, url, body string) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func mcpStatus(base, path, token string) int {
	req, _ := http.NewRequest("POST", base+path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

// ---- OpenAPI walking ----------------------------------------------------------------------

type operation struct {
	Method, Path string
	Op           *huma.Operation
}

func openAPIDoc() *huma.OpenAPI {
	h := humachi.New(chi.NewMux(), humaConfig())
	api.Register(h, nil, nil)
	return h.OpenAPI()
}

func operations(o *huma.OpenAPI) []operation {
	var out []operation
	for path, item := range o.Paths {
		for method, op := range map[string]*huma.Operation{"GET": item.Get, "POST": item.Post, "PUT": item.Put, "PATCH": item.Patch, "DELETE": item.Delete} {
			if op != nil {
				out = append(out, operation{Method: method, Path: path, Op: op})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path+out[i].Method < out[j].Path+out[j].Method })
	return out
}

func pathParams(path string) []string {
	var out []string
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			out = append(out, strings.Trim(seg, "{}"))
		}
	}
	return out
}

// expand returns every concrete path for the combinations of filler values.
func expand(path string, params []string, fillers map[string]func(string) []string) []string {
	paths := []string{path}
	for _, p := range params {
		var next []string
		for _, cur := range paths {
			for _, v := range fillers[p](path) {
				next = append(next, strings.Replace(cur, "{"+p+"}", url.PathEscape(v), 1))
			}
		}
		paths = next
	}
	return paths
}

// sampleBody builds a body that passes the operation's schema, so a 422 cannot mask the 404.
func sampleBody(o *huma.OpenAPI, op *huma.Operation) any {
	if op.RequestBody == nil || op.RequestBody.Content["application/json"] == nil {
		return nil
	}
	return sample(o, op.RequestBody.Content["application/json"].Schema)
}

func sample(o *huma.OpenAPI, s *huma.Schema) any {
	if s == nil {
		return nil
	}
	if s.Ref != "" {
		s = o.Components.Schemas.SchemaFromRef(s.Ref)
	}
	if len(s.Enum) > 0 {
		return s.Enum[0]
	}
	switch s.Type {
	case "object":
		out := map[string]any{}
		for _, name := range s.Required {
			if prop := s.Properties[name]; prop != nil {
				out[name] = sample(o, prop)
			}
		}
		return out
	case "array":
		return []any{sample(o, s.Items)}
	case "integer", "number":
		if s.Minimum != nil {
			return *s.Minimum
		}
		return 1
	case "boolean":
		return false
	default:
		if s.Format == "email" {
			return "sweep@example.test"
		}
		return "sweep12"
	}
}
