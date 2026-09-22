package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/brand"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

type browserJSON struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Image              string   `json:"image"`
	MemoryMB           int64    `json:"memory_mb"`
	CPUs               float64  `json:"cpus"`
	MaxSessions        int64    `json:"max_sessions"`
	IdleTimeoutSeconds int64    `json:"idle_timeout_seconds"`
	EnvPrefix          string   `json:"env_prefix"`
	Env                []string `json:"env"`
	Status             string   `json:"status"`
	Error              *string  `json:"error"`
	Running            *bool    `json:"running"`
	Restarts           *int64   `json:"restarts"`
	MemoryBytes        *int64   `json:"memory_bytes"`
	Applies            string   `json:"applies"`
	Operation          string   `json:"operation"`
}

type browsersJSON struct {
	Browsers        []browserJSON `json:"browsers"`
	StatusOperation *string       `json:"status_operation"`
}

/*
"Browsers" end to end with a fake agent: names and limits, the fixed image, the token sealed at rest,
sealed to the agent in the operation and nowhere in plaintext (params, result, log, REST, audit), the
two sealed variables and where they point, collisions, the cap, best-effort status, removal that
unsets exactly its own variables, roles, other teams, imported apps, MCP and the cascade.
*/
func TestAppBrowsers(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ctx := context.Background()
	// Where a data service is published: HOSBAH_DB_BIND (or CLOUDY_DB_BIND), else the Docker bridge gateway.
	bind := "172.17.0.1"
	if v := strings.TrimSpace(brand.Getenv("DB_BIND")); v != "" {
		bind = v
	}

	owner := newClient(t, ts.URL)
	owner.register("br-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Browsers " + suffix}, 201, &team)
	var readTok, writeTok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "br-read", "scopes": []string{"read"}, "expires_in_days": 30}, 201, &readTok)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "br-write", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &writeTok)

	viewerEmail := "br-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	stranger := newClient(t, ts.URL)
	stranger.register("br-stranger-" + suffix + "@example.test")
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, nil)

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "br-"+short, "203.0.113.41", []agentproto.Service{
		{Kind: "container", Name: "imp-br-" + short, Image: "nginx:1.27", Status: "running", Ports: []string{"0.0.0.0:8084->80/tcp"}}})
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "br-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	appBase := func(id string) string { return "/api/teams/" + team.ID + "/apps/" + id }
	base := appBase(app.App.ID)

	var (
		mu           sync.Mutex
		statusSilent bool
		removeErr    string
	)
	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		mu.Lock()
		defer mu.Unlock()
		switch op.Kind {
		case agentproto.KindBrowserCreate:
			var p agentproto.BrowserCreateParams
			_ = json.Unmarshal(op.Params, &p)
			if err := p.Validate(); err != nil {
				return nil, err.Error()
			}
			return agentproto.BrowserCreateResult{Container: agentproto.BrowserContainerName(p.BrowserID), HostPort: p.Port}, ""
		case agentproto.KindBrowserRemove:
			var p agentproto.BrowserRemoveParams
			_ = json.Unmarshal(op.Params, &p)
			if err := p.Validate(); err != nil {
				return nil, err.Error()
			}
			if removeErr != "" {
				return nil, removeErr
			}
			return agentproto.BrowserRemoveResult{Container: agentproto.BrowserContainerName(p.BrowserID), Removed: true}, ""
		case agentproto.KindBrowserStatus:
			if statusSilent {
				return nil, "docker is not answering"
			}
			var p agentproto.BrowserStatusParams
			_ = json.Unmarshal(op.Params, &p)
			res := agentproto.BrowserStatusResult{}
			for _, id := range p.BrowserIDs {
				mem := int64(222 << 20)
				res.Browsers = append(res.Browsers, agentproto.BrowserState{BrowserID: id, Running: true, Restarts: 2, MemoryBytes: &mem})
			}
			return res, ""
		case agentproto.KindAppRemove:
			return map[string]any{"containers": []string{}}, ""
		}
		return nil, "this fake agent does not run " + op.Kind
	})

	list := func(c *client, id string) browsersJSON {
		t.Helper()
		var s browsersJSON
		c.mustJSON("GET", appBase(id)+"/browsers", nil, 200, &s)
		return s
	}
	wait := func(ref, want string) browserJSON {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			for _, b := range list(owner, app.App.ID).Browsers {
				if (b.ID == ref || b.Name == ref) && b.Status == want {
					return b
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("browser %s never became %s: %+v", ref, want, list(owner, app.App.ID).Browsers)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	envNames := func() map[string]bool {
		t.Helper()
		var env struct {
			Variables []struct{ Name string } `json:"variables"`
		}
		owner.mustJSON("GET", base+"/env", nil, 200, &env)
		out := map[string]bool{}
		for _, v := range env.Variables {
			out[v.Name] = true
		}
		return out
	}
	reveal := func(name string) string {
		t.Helper()
		var out struct{ Value string }
		owner.mustJSON("POST", base+"/env/"+name+"/reveal", nil, 200, &out)
		return out.Value
	}

	t.Run("what a browser must be", func(t *testing.T) {
		if s := list(viewer, app.App.ID); s.Browsers == nil || len(s.Browsers) != 0 || s.StatusOperation != nil {
			t.Fatalf("no browsers is an empty list and no operation: %+v", s)
		}
		for name, body := range map[string]map[string]any{
			"upper-case name":    {"name": "Crawler"},
			"digit first":        {"name": "1crawler"},
			"an underscore":      {"name": "my_crawler"},
			"name too long":      {"name": "a" + strings.Repeat("b", 31)},
			"no name":            {},
			"too little memory":  {"name": "x", "memory_mb": 128},
			"too much memory":    {"name": "x", "memory_mb": 8192},
			"too few cpus":       {"name": "x", "cpus": 0.1},
			"too many cpus":      {"name": "x", "cpus": 8},
			"too many sessions":  {"name": "x", "max_sessions": 21},
			"negative sessions":  {"name": "x", "max_sessions": -1},
			"idle too short":     {"name": "x", "idle_timeout_seconds": 5},
			"idle too long":      {"name": "x", "idle_timeout_seconds": 7200},
			"a bad prefix":       {"name": "x", "env_prefix": "browser"},
			"a prefix with dash": {"name": "x", "env_prefix": "MY-BROWSER"},
			"an image of theirs": {"name": "x", "image": "evil/chromium:latest"},
		} {
			if status, text := owner.do("POST", base+"/browsers", body); status != 422 {
				t.Errorf("%s: %d (want 422): %s", name, status, text)
			}
		}
		viewer.mustJSON("POST", base+"/browsers", map[string]any{"name": "crawler"}, 403, nil)
		stranger.mustJSON("POST", base+"/browsers", map[string]any{"name": "crawler"}, 404, nil)
		stranger.mustJSON("GET", base+"/browsers", nil, 404, nil)
		if s := list(owner, app.App.ID); len(s.Browsers) != 0 || len(envNames()) != 0 || len(agentOps(agent, agentproto.KindBrowserCreate)) != 0 {
			t.Fatalf("refused browsers leave nothing: %+v %v", s.Browsers, envNames())
		}
	})

	t.Run("an imported app has no browsers", func(t *testing.T) {
		apps, _ := listImported(t, owner, team.ID, srv)
		imp, ok := apps["imp-br-"+short]
		if !ok {
			t.Fatalf("no imported app: %+v", apps)
		}
		owner.mustJSON("POST", appBase(imp.ID)+"/browsers", map[string]any{"name": "crawler"}, 422, nil)
		owner.mustJSON("DELETE", appBase(imp.ID)+"/browsers/crawler", map[string]any{"confirm": "crawler"}, 422, nil)
		if s := list(owner, imp.ID); len(s.Browsers) != 0 {
			t.Fatalf("imported app's browsers: %+v", s)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "create_app_browser", map[string]any{"app": imp.ID, "name": "crawler"}); !res.IsError {
			t.Errorf("MCP create_app_browser on an imported app: %+v", res)
		}
	})

	var token string
	var first browserJSON
	t.Run("a browser: the token is sealed at rest, sealed to the agent in the operation, and nowhere else", func(t *testing.T) {
		status, text := owner.do("POST", base+"/browsers", map[string]any{"name": "crawler"})
		if status != 201 {
			t.Fatalf("create: %d %s", status, text)
		}
		var created browserJSON
		_ = json.Unmarshal([]byte(text), &created)
		if created.Name != "crawler" || created.Image != agentproto.BrowserImage || created.MemoryMB != 1024 || created.CPUs != 1 || created.MaxSessions != 5 ||
			created.IdleTimeoutSeconds != 300 || created.EnvPrefix != "BROWSER" || fmt.Sprint(created.Env) != "[BROWSER_WS_ENDPOINT BROWSER_TOKEN]" ||
			created.Status != "creating" || created.Applies != "next_deploy" || created.Operation == "" {
			t.Fatalf("created: %+v", created)
		}
		first = wait(created.ID, "ready")
		if names := envNames(); !names["BROWSER_WS_ENDPOINT"] || !names["BROWSER_TOKEN"] {
			t.Fatalf("the variables were not set: %v", names)
		}
		token = reveal("BROWSER_TOKEN")
		if !regexp.MustCompile(`^[A-Za-z0-9]{40}$`).MatchString(token) {
			t.Fatalf("a token is 40 alphanumerics: %d chars", len(token))
		}
		// Where a data service is: the server's private address — never loopback, never every interface.
		if ep := reveal("BROWSER_WS_ENDPOINT"); ep != fmt.Sprintf("ws://%s:%d?token=%s", bind, agentproto.BrowserPortBase, token) || !agentproto.ValidBrowserBind(bind) || strings.HasPrefix(bind, "127.") {
			t.Fatalf("endpoint: %s", strings.ReplaceAll(ep, token, "<token>"))
		}
		// No response carries it; there is no reveal route for a browser.
		if strings.Contains(text, token) || strings.Contains(text, "token_sealed") || strings.Contains(text, "host_port") {
			t.Error("the create response carries the token")
		}
		if _, body := owner.do("GET", base+"/browsers", nil); strings.Contains(body, token) || strings.Contains(body, "token") {
			t.Errorf("GET browsers carries a token: %s", body)
		}
		for _, p := range []string{"/token", "/reveal", "/token/reveal"} {
			if status, _ := owner.do("POST", base+"/browsers/crawler"+p, nil); status == 200 {
				t.Errorf("POST …/browsers/crawler%s answers 200", p)
			}
			if status, body := owner.do("GET", base+"/browsers/crawler"+p, nil); status == 200 && strings.Contains(body, token) {
				t.Errorf("GET …/browsers/crawler%s reveals the token", p)
			}
		}
		var sealed, params string
		var result sql.NullString
		err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			if err := tx.QueryRow(`SELECT token_sealed FROM app_browsers WHERE id = $1`, created.ID).Scan(&sealed); err != nil {
				return err
			}
			return tx.QueryRow(`SELECT params::text, result::text FROM operations WHERE id = $1`, created.Operation).Scan(&params, &result)
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(sealed, "csv1:") || strings.Contains(sealed, token) {
			t.Errorf("app_browsers.token_sealed is not sealed: %.20s…", sealed)
		}
		if strings.Contains(params, token) || strings.Contains(result.String, token) {
			t.Error("operations.params or .result holds the token in plaintext")
		}
		if _, body := owner.do("GET", "/api/teams/"+team.ID+"/servers/"+srv+"/operations/"+created.Operation, nil); strings.Contains(body, token) {
			t.Error("the operation (params, result or log) shows the token")
		}
		ops := agentOps(agent, agentproto.KindBrowserCreate)
		if len(ops) != 1 {
			t.Fatalf("one browser.create, got %d", len(ops))
		}
		var p agentproto.BrowserCreateParams
		_ = json.Unmarshal(ops[0].Params, &p)
		if p.BrowserID != created.ID || p.AppID != app.App.ID || p.Image != agentproto.BrowserImage || p.Bind != bind || p.Port != agentproto.BrowserPortBase ||
			p.MemoryMB != 1024 || p.CPUs != 1 || p.MaxSessions != 5 || p.IdleTimeoutSeconds != 300 || ops[0].TimeoutSeconds < 600 {
			t.Fatalf("browser.create params: %+v (timeout %d)", p, ops[0].TimeoutSeconds)
		}
		pt, err := agentproto.OpenBox(priv, agentproto.BrowserTokenContext(created.ID), p.Token)
		if err != nil || !strings.Contains(string(pt), `"`+token+`"`) {
			t.Fatalf("the agent's box does not carry the app's token: %v", err)
		}
		if _, err := agentproto.OpenBox(priv, agentproto.BrowserTokenContext("00000000-0000-4000-8000-0000000000b9"), p.Token); err == nil {
			t.Error("the box opens for another browser")
		}
	})

	t.Run("status is best effort", func(t *testing.T) {
		s := list(viewer, app.App.ID)
		if len(s.Browsers) != 1 || s.StatusOperation == nil {
			t.Fatalf("list: %+v", s)
		}
		b := s.Browsers[0]
		if b.Running == nil || !*b.Running || b.Restarts == nil || *b.Restarts != 2 || b.MemoryBytes == nil || *b.MemoryBytes != 222<<20 {
			t.Fatalf("status: %+v", b)
		}
		mu.Lock()
		statusSilent = true
		mu.Unlock()
		s = list(owner, app.App.ID)
		mu.Lock()
		statusSilent = false
		mu.Unlock()
		if b := s.Browsers[0]; b.Running != nil || b.Restarts != nil || b.MemoryBytes != nil || b.Status != "ready" {
			t.Fatalf("a server that does not answer leaves nulls, still 200: %+v", b)
		}
	})

	t.Run("collisions: name, env names, custom limits, cap", func(t *testing.T) {
		owner.mustJSON("POST", base+"/browsers", map[string]any{"name": "crawler", "env_prefix": "OTHER"}, 409, nil)
		if status, text := owner.do("POST", base+"/browsers", map[string]any{"name": "second"}); status != 409 || !strings.Contains(text, "BROWSER_") {
			t.Fatalf("env collision: %d %s", status, text)
		}
		owner.mustJSON("PUT", base+"/env/PDF_TOKEN", map[string]any{"value": "mine"}, 204, nil)
		if status, text := owner.do("POST", base+"/browsers", map[string]any{"name": "pdf", "env_prefix": "PDF"}); status != 409 || !strings.Contains(text, "PDF_TOKEN") {
			t.Fatalf("hand-set variable: %d %s", status, text)
		}
		if reveal("PDF_TOKEN") != "mine" || envNames()["PDF_WS_ENDPOINT"] || len(list(owner, app.App.ID).Browsers) != 1 {
			t.Fatal("a refused browser must not touch the app's variables")
		}
		var shots browserJSON
		owner.mustJSON("POST", base+"/browsers", map[string]any{"name": "shots", "env_prefix": "SHOTS", "memory_mb": 2048, "cpus": 0.5,
			"max_sessions": 20, "idle_timeout_seconds": 30}, 201, &shots)
		if shots.MemoryMB != 2048 || shots.CPUs != 0.5 || shots.MaxSessions != 20 || shots.IdleTimeoutSeconds != 30 {
			t.Fatalf("custom limits: %+v", shots)
		}
		wait("shots", "ready")
		if ep := reveal("SHOTS_WS_ENDPOINT"); !strings.HasPrefix(ep, fmt.Sprintf("ws://%s:%d?token=", bind, agentproto.BrowserPortBase+1)) || strings.Contains(ep, token) {
			t.Errorf("a second browser gets the next port and its own token")
		}
		owner.mustJSON("POST", base+"/browsers", map[string]any{"name": "third", "env_prefix": "THIRD"}, 201, nil)
		wait("third", "ready")
		owner.mustJSON("POST", base+"/browsers", map[string]any{"name": "fourth", "env_prefix": "FOURTH"}, 409, nil)
		if envNames()["FOURTH_TOKEN"] {
			t.Error("the cap must leave nothing")
		}
	})

	t.Run("removing a browser unsets exactly its own variables", func(t *testing.T) {
		ref := base + "/browsers/shots"
		viewer.mustJSON("DELETE", ref, map[string]any{"confirm": "shots"}, 403, nil)
		stranger.mustJSON("DELETE", ref, map[string]any{"confirm": "shots"}, 404, nil)
		owner.mustJSON("DELETE", ref, map[string]any{"confirm": "nope"}, 422, nil)
		owner.mustJSON("DELETE", base+"/browsers/nope", map[string]any{"confirm": "nope"}, 404, nil)

		mu.Lock()
		removeErr = "could not remove the browser's container"
		mu.Unlock()
		if status, text := owner.do("DELETE", ref, map[string]any{"confirm": "shots"}); status != 409 {
			t.Fatalf("a failed removal: %d %s", status, text)
		}
		if b := wait("shots", "ready"); b.Error == nil || !envNames()["SHOTS_TOKEN"] {
			t.Fatalf("a refused removal leaves the browser and its variables: %+v", b)
		}
		mu.Lock()
		removeErr = ""
		mu.Unlock()

		var out struct {
			Removed   bool     `json:"removed"`
			Unset     []string `json:"unset"`
			Applies   string   `json:"applies"`
			Operation *string  `json:"operation"`
		}
		owner.mustJSON("DELETE", ref, map[string]any{"confirm": "shots"}, 200, &out)
		if !out.Removed || fmt.Sprint(out.Unset) != "[SHOTS_WS_ENDPOINT SHOTS_TOKEN]" || out.Operation == nil || out.Applies != "next_deploy" {
			t.Fatalf("removed: %+v", out)
		}
		rm := agentOps(agent, agentproto.KindBrowserRemove)
		var p agentproto.BrowserRemoveParams
		_ = json.Unmarshal(rm[len(rm)-1].Params, &p)
		if p.AppID != app.App.ID || !agentproto.ValidID(p.BrowserID) || p.BrowserID == first.ID {
			t.Errorf("browser.remove params: %+v", p)
		}
		names := envNames()
		if names["SHOTS_WS_ENDPOINT"] || names["SHOTS_TOKEN"] {
			t.Errorf("its variables are still set: %v", names)
		}
		for _, n := range []string{"BROWSER_WS_ENDPOINT", "BROWSER_TOKEN", "THIRD_TOKEN", "PDF_TOKEN"} {
			if !names[n] {
				t.Errorf("%s went too — only the browser's own variables go: %v", n, names)
			}
		}
		if reveal("BROWSER_TOKEN") != token {
			t.Error("another browser's token changed")
		}

		// MCP: list, create and remove, with confirm; never a token.
		res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "list_app_browsers", map[string]any{"app": app.App.ID})
		if res.IsError || !strings.Contains(res.Text, "crawler") || !strings.Contains(res.Text, "running=true") || strings.Contains(res.Text, token) {
			t.Fatalf("MCP list_app_browsers: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "create_app_browser", map[string]any{"app": app.App.ID, "name": "mcp", "env_prefix": "MCPB"}); !res.IsError {
			t.Errorf("a read token must not create: %+v", res)
		}
		res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "create_app_browser", map[string]any{"app": app.App.ID, "name": "mcp", "env_prefix": "MCPB", "memory_mb": 512})
		if res.IsError || !strings.Contains(res.Text, "MCPB_WS_ENDPOINT") || strings.Contains(res.Text, reveal("MCPB_TOKEN")) {
			t.Fatalf("MCP create_app_browser: %+v", res)
		}
		if b := wait("mcp", "ready"); b.MemoryMB != 512 {
			t.Errorf("MCP limits: %+v", b)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "remove_app_browser", map[string]any{"app": app.App.ID, "browser": "mcp"}); !res.IsError || !strings.Contains(res.Text, "confirm") {
			t.Errorf("MCP remove_app_browser without confirm: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "remove_app_browser", map[string]any{"app": app.App.ID, "browser": "mcp", "confirm": "mcp"}); !res.IsError {
			t.Errorf("a read token must not remove: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "remove_app_browser", map[string]any{"app": app.App.ID, "browser": "mcp", "confirm": "mcp"}); res.IsError || !strings.Contains(res.Text, "MCPB_TOKEN") {
			t.Errorf("MCP remove_app_browser: %+v", res)
		}
		if envNames()["MCPB_TOKEN"] {
			t.Error("MCP removal left the variables")
		}
	})

	t.Run("audited, without the token", func(t *testing.T) {
		var audit struct {
			Entries []struct {
				Action  string          `json:"action"`
				Changes json.RawMessage `json:"changes"`
			} `json:"entries"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/audit?limit=200", nil, 200, &audit)
		got := map[string]int{}
		for _, e := range audit.Entries {
			got[e.Action]++
			if strings.Contains(string(e.Changes), token) {
				t.Errorf("the audit log carries the token: %s", e.Action)
			}
		}
		if got["browser.created"] != 4 || got["browser.removed"] != 2 {
			t.Errorf("audit: %v", got)
		}
	})

	t.Run("browsers go with their app, on the server too", func(t *testing.T) {
		count := func() (n int) {
			t.Helper()
			if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
				return tx.QueryRow(`SELECT count(*) FROM app_browsers WHERE app_id = $1`, app.App.ID).Scan(&n)
			}); err != nil {
				t.Fatal(err)
			}
			return n
		}
		if n := count(); n != 2 {
			t.Fatalf("before: %d browsers", n)
		}
		owner.mustJSON("DELETE", base, map[string]any{"confirm": app.App.Name}, 204, nil)
		if n := count(); n != 0 {
			t.Fatalf("after deleting the app: %d browsers", n)
		}
		// The agent's app.remove (which takes the app's browser containers: internal/agent/browsers_test.go).
		deadline := time.Now().Add(15 * time.Second)
		for len(agentOps(agent, agentproto.KindAppRemove)) == 0 && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if len(agentOps(agent, agentproto.KindAppRemove)) != 1 {
			t.Error("app.remove never reached the agent")
		}
	})
}
