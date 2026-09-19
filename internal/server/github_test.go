package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
A server's own GitHub connection (the GitHub CLI on it). Every route and MCP tool enqueues the right
typed, signed operation with validated params — verified by a fake agent exactly as cloudy-agent
verifies them; roles gate them; the device flow's one-time code reaches the API while github.login
is still running; and a token (REST or MCP) is sealed to the agent: it never appears in
operations.params, an operation log, the audit log or an MCP answer.
*/
func TestServerGitHubConnection(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	owner := newClient(t, ts.URL)
	owner.register("gh-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "GitHub " + suffix}, 201, &team)
	var tok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{
		"name": "gh-all", "scopes": []string{"read", "deploy", "write"}, "expires_in_days": 30}, 201, &tok)
	member := func(role string) *client {
		email := "gh-" + role + "-" + suffix + "@example.test"
		c := newClient(t, ts.URL)
		c.register(email)
		var inv struct {
			AcceptURL string `json:"accept_url"`
		}
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": email, "role": role}, 201, &inv)
		c.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)
		return c
	}
	admin, deployer, viewer := member("admin"), member("deployer"), member("viewer")

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "gh-"+suffix, "10.9.8.7", nil)
	base := "/api/teams/" + team.ID + "/servers/" + srv + "/github"

	var (
		mu       sync.Mutex
		loggedIn bool
		tokens   []string // tokens the agent opened from sealed boxes
	)
	release := make(chan struct{})
	status := func() agentproto.GitHubStatus {
		mu.Lock()
		defer mu.Unlock()
		if !loggedIn {
			return agentproto.GitHubStatus{Installed: true, Version: "2.63.2"}
		}
		return agentproto.GitHubStatus{Installed: true, Version: "2.63.2", LoggedIn: true, User: "octocat", Scopes: []string{"read:org", "repo"}}
	}
	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		switch op.Kind {
		case agentproto.KindGitHubInstall:
			return status(), ""
		case agentproto.KindGitHubLogin:
			var p agentproto.GitHubLoginParams
			if err := json.Unmarshal(op.Params, &p); err != nil || p.Validate() != nil {
				return nil, "bad params"
			}
			if p.Mode == agentproto.GitHubModeToken {
				pt, err := agentproto.OpenBox(priv, agentproto.GitHubTokenContext(srv), p.Token)
				if err != nil {
					return nil, "the token box did not open with this agent's key"
				}
				mu.Lock()
				tokens = append(tokens, string(pt))
				mu.Unlock()
			} else {
				select {
				case <-release: // the human entered the code
				case <-time.After(20 * time.Second):
					return nil, "the one-time code was not entered in time"
				}
			}
			mu.Lock()
			loggedIn = true
			mu.Unlock()
			return status(), ""
		case agentproto.KindGitHubStatus:
			return status(), ""
		case agentproto.KindGitHubLogout:
			mu.Lock()
			loggedIn = false
			mu.Unlock()
			return status(), ""
		case agentproto.KindGitHubRepos:
			var p agentproto.GitHubReposParams
			if json.Unmarshal(op.Params, &p) != nil || p.Validate() != nil {
				return nil, "bad params"
			}
			return agentproto.GitHubReposResult{Owners: []string{"(you)"}, Repos: []agentproto.GitHubRepo{
				{Name: "api", NameWithOwner: "octocat/api", IsPrivate: true, DefaultBranch: "main", URL: "https://github.com/octocat/api", UpdatedAt: time.Now()},
				{Name: "site", NameWithOwner: "acme/site", DefaultBranch: "prod", URL: "https://github.com/acme/site"}}}, ""
		case agentproto.KindGitHubBranches:
			var p agentproto.GitHubBranchesParams
			if json.Unmarshal(op.Params, &p) != nil || p.Validate() != nil {
				return nil, "bad params"
			}
			return agentproto.GitHubBranchesResult{Default: "main", Branches: []string{"dev", "main"}}, ""
		}
		return nil, "unexpected " + op.Kind
	})
	agent.mu.Lock()
	agent.events = func(op agentproto.SignedOp) []agentproto.Event {
		if op.Kind != agentproto.KindGitHubLogin || !strings.Contains(string(op.Params), `"device"`) {
			return nil
		}
		data, _ := json.Marshal(agentproto.GitHubDeviceCode{Type: agentproto.DataGitHubDeviceCode, UserCode: "WDJB-MJHT",
			VerificationURI: "https://github.com/login/device", ExpiresAt: time.Now().Add(15 * time.Minute)})
		return []agentproto.Event{{Kind: agentproto.EventLog, Stream: "info", Line: "Open https://github.com/login/device and enter WDJB-MJHT", Data: data}}
	}
	agent.mu.Unlock()

	type ghState struct {
		Connected bool    `json:"connected"`
		User      *string `json:"user"`
		CheckedAt *string `json:"checked_at"`
		Pending   *struct {
			Operation       string `json:"operation"`
			UserCode        string `json:"user_code"`
			VerificationURI string `json:"verification_uri"`
		} `json:"pending"`
	}
	getState := func() ghState {
		var out struct {
			GitHub ghState `json:"github"`
		}
		deployer.mustJSON("GET", base, nil, 200, &out)
		return out.GitHub
	}
	waitState := func(what string, ok func(ghState) bool) ghState {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			st := getState()
			if ok(st) {
				return st
			}
			if time.Now().After(deadline) {
				t.Fatalf("waiting for %s: %+v", what, st)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	type opOut struct {
		ID     string          `json:"id"`
		Kind   string          `json:"kind"`
		Status string          `json:"status"`
		Params json.RawMessage `json:"params"`
	}
	waitOp := func(id string) (opOut, string) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			status, text := owner.do("GET", "/api/teams/"+team.ID+"/servers/"+srv+"/operations/"+id, nil)
			var d struct {
				Operation opOut `json:"operation"`
			}
			_ = json.Unmarshal([]byte(text), &d)
			if status == 200 && (d.Operation.Status == "ok" || d.Operation.Status == "failed" || d.Operation.Status == "timeout") {
				return d.Operation, text
			}
			if time.Now().After(deadline) {
				t.Fatalf("operation %s did not finish: %s", id, text)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	// secretIn reports whether s appears in the team's stored operations or its audit log.
	secretIn := func(s string) string {
		t.Helper()
		var nOps, nAudit int
		err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
			if err := tx.QueryRow(`SELECT count(*) FROM operations WHERE team_id = $1 AND (params::text LIKE '%' || $2::text || '%' OR coalesce(result::text, '') LIKE '%' || $2::text || '%')`, team.ID, s).Scan(&nOps); err != nil {
				return err
			}
			return tx.QueryRow(`SELECT count(*) FROM activity_log WHERE team_id = $1 AND changes::text LIKE '%' || $2::text || '%'`, team.ID, s).Scan(&nAudit)
		})
		if err != nil {
			t.Fatal(err)
		}
		_, auditText := owner.do("GET", "/api/teams/"+team.ID+"/audit", nil)
		switch {
		case nOps > 0:
			return "operations"
		case nAudit > 0 || strings.Contains(auditText, s):
			return "audit log"
		}
		return ""
	}

	t.Run("roles", func(t *testing.T) {
		for _, c := range []*client{deployer, viewer} {
			c.expect("POST", base+"/connect", map[string]any{"mode": "device"}, 403)
			c.expect("DELETE", base, nil, 403)
		}
		viewer.expect("GET", base+"/repos", nil, 403)
		viewer.expect("GET", base+"/repos/octocat/api/branches", nil, 403)
		viewer.expect("GET", base+"?refresh=true", nil, 403)
		viewer.mustJSON("GET", base, nil, 200, nil) // the last report is readable by every member
		owner.expect("POST", base+"/connect", map[string]any{"mode": "token", "token": "has space"}, 422)
		owner.expect("POST", base+"/connect", map[string]any{"mode": "token"}, 422)
		owner.expect("POST", base+"/connect", map[string]any{"mode": "device", "token": "ghp_x"}, 422)
		owner.expect("POST", base+"/connect", map[string]any{"mode": "ssh"}, 422)
		if st := getState(); st.Connected || st.CheckedAt != nil {
			t.Fatalf("nothing is connected yet: %+v", st)
		}
	})

	t.Run("device login shows the code while the operation runs", func(t *testing.T) {
		var res struct {
			Operation opOut `json:"operation"`
		}
		admin.mustJSON("POST", base+"/connect", map[string]any{"mode": "device"}, 202, &res)
		if res.Operation.Kind != agentproto.KindGitHubLogin || string(res.Operation.Params) != `{"mode":"device"}` {
			t.Fatalf("connect queued %s %s", res.Operation.Kind, res.Operation.Params)
		}
		st := waitState("the device code", func(s ghState) bool { return s.Pending != nil })
		if st.Pending.UserCode != "WDJB-MJHT" || st.Pending.VerificationURI != "https://github.com/login/device" || st.Pending.Operation != res.Operation.ID || st.Connected {
			t.Fatalf("pending: %+v", st)
		}
		// The same code is on the operation's log, as a line whose data the web can read.
		var detail struct {
			Operation opOut `json:"operation"`
			Logs      []struct {
				Line string          `json:"line"`
				Data json.RawMessage `json:"data"`
			} `json:"logs"`
		}
		admin.mustJSON("GET", "/api/teams/"+team.ID+"/servers/"+srv+"/operations/"+res.Operation.ID, nil, 200, &detail)
		found := false
		for _, l := range detail.Logs {
			var dc agentproto.GitHubDeviceCode
			if json.Unmarshal(l.Data, &dc) == nil && dc.Type == agentproto.DataGitHubDeviceCode && dc.UserCode == "WDJB-MJHT" {
				found = true
			}
		}
		if !found || detail.Operation.Status == "ok" {
			t.Fatalf("the device code should be on the running operation's log: %+v", detail)
		}
		close(release)
		st = waitState("connected", func(s ghState) bool { return s.Connected })
		if st.User == nil || *st.User != "octocat" || st.Pending != nil {
			t.Fatalf("after login: %+v", st)
		}
		kinds := strings.Join(agent.kinds(), ",")
		if !strings.Contains(kinds, "github.install,github.login") {
			t.Errorf("connect should install then log in: %s", kinds)
		}
		var server struct {
			GitHub ghState `json:"github"`
		}
		viewer.mustJSON("GET", "/api/teams/"+team.ID+"/servers/"+srv, nil, 200, &server)
		if !server.GitHub.Connected || server.GitHub.User == nil || *server.GitHub.User != "octocat" || server.GitHub.CheckedAt == nil {
			t.Errorf("server JSON github: %+v", server.GitHub)
		}
	})

	t.Run("a token is sealed to the agent and stored nowhere", func(t *testing.T) {
		const restToken = "ghp_RESTs3cretTokenValue000000000000000001"
		var res struct {
			Operation opOut `json:"operation"`
		}
		owner.mustJSON("POST", base+"/connect", map[string]any{"mode": "token", "token": restToken}, 202, &res)
		done, text := waitOp(res.Operation.ID)
		if done.Status != "ok" {
			t.Fatalf("token login: %s", text)
		}
		if strings.Contains(text, restToken) || strings.Contains(string(res.Operation.Params), restToken) {
			t.Fatal("the token is in the operation or its log")
		}
		var p agentproto.GitHubLoginParams
		_ = json.Unmarshal(done.Params, &p)
		if p.Mode != "token" || !strings.HasPrefix(p.Token, "cab1:") {
			t.Errorf("params should carry a sealed box: %s", done.Params)
		}
		if where := secretIn(restToken); where != "" {
			t.Errorf("the REST token is in the %s", where)
		}

		const mcpToken = "ghp_MCPs3cretTokenValue0000000000000000002"
		r := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "github_connect", map[string]any{"server": srv, "token": mcpToken})
		if r.IsError || strings.Contains(r.Text, mcpToken) || !strings.Contains(r.Text, "Connecting") {
			t.Fatalf("github_connect: %+v", r)
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			mu.Lock()
			got := strings.Join(tokens, ",")
			mu.Unlock()
			if strings.Contains(got, mcpToken) {
				if got != restToken+","+mcpToken {
					t.Errorf("the agent opened %q", got)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the agent never received the MCP token (got %q)", got)
			}
			time.Sleep(100 * time.Millisecond)
		}
		for _, o := range agentOps(agent, agentproto.KindGitHubLogin) {
			if strings.Contains(string(o.Params), restToken) || strings.Contains(string(o.Params), mcpToken) {
				t.Errorf("a signed op carries a token in the clear: %s", o.Params)
			}
		}
		if where := secretIn(mcpToken); where != "" {
			t.Errorf("the MCP token is in the %s", where)
		}
		// A malformed token is refused before anything is queued.
		if r := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "github_connect", map[string]any{"server": srv, "token": "two words"}); !r.IsError {
			t.Errorf("a malformed token should be refused: %+v", r)
		}
	})

	t.Run("repositories and branches", func(t *testing.T) {
		var repos struct {
			Repos []struct {
				NameWithOwner string `json:"name_with_owner"`
				IsPrivate     bool   `json:"is_private"`
				DefaultBranch string `json:"default_branch"`
			} `json:"repos"`
			Operation string `json:"operation"`
		}
		deployer.mustJSON("GET", base+"/repos?owner=acme&limit=5", nil, 200, &repos)
		if len(repos.Repos) != 2 || repos.Repos[0].NameWithOwner != "octocat/api" || !repos.Repos[0].IsPrivate {
			t.Fatalf("repos: %+v", repos)
		}
		var sawOwner bool
		for _, o := range agentOps(agent, agentproto.KindGitHubRepos) {
			if o.OpID == repos.Operation {
				sawOwner = string(o.Params) == `{"owner":"acme","limit":5}`
			}
		}
		if !sawOwner {
			t.Errorf("github.repos params were not exactly {owner, limit}")
		}
		deployer.mustJSON("GET", base+"/repos", nil, 200, &repos)
		for _, o := range agentOps(agent, agentproto.KindGitHubRepos) {
			if o.OpID == repos.Operation && string(o.Params) != `{"limit":200}` {
				t.Errorf("default listing params: %s", o.Params)
			}
		}
		deployer.expect("GET", base+"/repos?owner=-bad", nil, 422)

		var br struct {
			Default  string   `json:"default"`
			Branches []string `json:"branches"`
		}
		deployer.mustJSON("GET", base+"/repos/octocat/api/branches", nil, 200, &br)
		if br.Default != "main" || strings.Join(br.Branches, ",") != "dev,main" {
			t.Errorf("branches: %+v", br)
		}
		for _, o := range agentOps(agent, agentproto.KindGitHubBranches) {
			if string(o.Params) != `{"repo":"octocat/api"}` {
				t.Errorf("github.branches params: %s", o.Params)
			}
		}
		deployer.expect("GET", base+"/repos/octocat/a%20b/branches", nil, 422)

		if r := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "github_repos", map[string]any{"server": srv}); r.IsError || !strings.Contains(r.Text, "octocat/api") || !strings.Contains(r.Text, "private") {
			t.Errorf("github_repos: %+v", r)
		}
		if r := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "github_branches", map[string]any{"server": srv, "repo": "octocat/api"}); r.IsError || !strings.Contains(r.Text, "default: main") {
			t.Errorf("github_branches: %+v", r)
		}
		if r := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "github_branches", map[string]any{"server": srv, "repo": "../etc"}); !r.IsError {
			t.Errorf("a bad repo should be refused: %+v", r)
		}
		if r := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "github_status", map[string]any{"server": srv, "refresh": true}); r.IsError || !strings.Contains(r.Text, "connected to GitHub as octocat") {
			t.Errorf("github_status: %+v", r)
		}
		var refreshed struct {
			GitHub    ghState `json:"github"`
			Operation *opOut  `json:"operation"`
		}
		deployer.mustJSON("GET", base+"?refresh=true", nil, 200, &refreshed)
		if refreshed.Operation == nil || refreshed.Operation.Kind != agentproto.KindGitHubStatus || !refreshed.GitHub.Connected {
			t.Errorf("refresh: %+v", refreshed)
		}
	})

	t.Run("disconnect", func(t *testing.T) {
		var res struct {
			Operation opOut `json:"operation"`
		}
		admin.mustJSON("DELETE", base, nil, 202, &res)
		if res.Operation.Kind != agentproto.KindGitHubLogout || string(res.Operation.Params) != `{}` {
			t.Fatalf("disconnect queued %+v", res.Operation)
		}
		waitState("disconnected", func(s ghState) bool { return !s.Connected })
		deployer.expect("GET", base+"/repos", nil, 409)
		if r := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "github_repos", map[string]any{"server": srv}); !r.IsError || !strings.Contains(r.Text, "not connected") {
			t.Errorf("github_repos when disconnected: %+v", r)
		}
		var audit struct {
			Entries []struct{ Action string } `json:"entries"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/audit", nil, 200, &audit)
		n := map[string]int{}
		for _, e := range audit.Entries {
			n[e.Action]++
		}
		if n["server.github_connect"] != 3 || n["server.github_disconnect"] != 1 {
			t.Errorf("audit: %v", n)
		}
	})
	if rej := agent.rejectedList(); len(rej) > 0 {
		t.Errorf("the agent refused operations: %v", rej)
	}
}
