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

type realtimeJSON struct {
	Config *struct {
		Enabled   bool   `json:"enabled"`
		Kind      string `json:"kind"`
		Process   string `json:"process"`
		Port      int    `json:"port"`
		Path      string `json:"path"`
		WholeSite bool   `json:"whole_site"`
	} `json:"config"`
	Suggestion struct {
		Kind       string   `json:"kind"`
		Process    string   `json:"process"`
		Port       int      `json:"port"`
		Path       string   `json:"path"`
		Confidence string   `json:"confidence"`
		Evidence   []string `json:"evidence"`
	} `json:"suggestion"`
	Routed            bool            `json:"routed"`
	ProcessRunning    bool            `json:"process_running"`
	DeclaredProcesses []string        `json:"declared_processes"`
	Hostnames         []string        `json:"hostnames"`
	Connections       json.RawMessage `json:"connections"`
	ConnectionsReason string          `json:"connections_reason"`
	Note              string          `json:"note"`
	Applies           string          `json:"applies"`
	Operation         *string         `json:"operation"`
}

/*
"Realtime" end to end with a fake agent, through the real pipeline: what a process, a port and a path
must be; a configuration saved while a release runs without it applies from the next restart; the
restart's signed start params publish the port for that worker ONLY; the release's record makes it
routed; a deploy's route.set carries the realtime route; a changed path, disabling and removing
change the route at once with a typed realtime.route; a domain added meanwhile does not unroute it;
an undeclared process; imported apps; roles; other teams; MCP; the audit log; the cascade.
*/
func TestAppRealtime(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ctx := context.Background()

	owner := newClient(t, ts.URL)
	owner.register("rt-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Realtime " + suffix}, 201, &team)
	var readTok, writeTok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "rt-read", "scopes": []string{"read"}, "expires_in_days": 30}, 201, &readTok)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "rt-write", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &writeTok)

	viewerEmail := "rt-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	stranger := newClient(t, ts.URL)
	stranger.register("rt-stranger-" + suffix + "@example.test")
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, nil)

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "rt-"+short, "203.0.113.41", []agentproto.Service{
		{Kind: "container", Name: "imp-rt-" + short, Image: "nginx:1.27", Status: "running", Ports: []string{"0.0.0.0:8084->80/tcp"}}})
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "rt-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	appBase := func(id string) string { return "/api/teams/" + team.ID + "/apps/" + id }
	base := appBase(app.App.ID)
	host := "rt-" + short + ".example.test"

	const toml = "[processes]\n\"worker:reverb\" = \"php artisan reverb:start --host=0.0.0.0 --port=8080\"\n\"worker:jobs\" = \"php artisan queue:work\"\n"
	const webPort, rtPort = 41001, 41002

	// The fake agent runs the whole pipeline; every start is validated as the real agent would.
	var mu sync.Mutex
	start := func(p agentproto.StartParams) (agentproto.StartResult, string) {
		if err := p.Validate(); err != nil {
			return agentproto.StartResult{}, err.Error()
		}
		res := agentproto.StartResult{Container: agentproto.ContainerName(p.App, p.Process, p.DeploymentID)}
		switch {
		case p.Process == "web":
			res.HostPort = webPort
		case p.Realtime != nil:
			res.HostPort = rtPort
		}
		return res, ""
	}
	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		mu.Lock()
		defer mu.Unlock()
		switch op.Kind {
		case agentproto.KindProvisionStep, agentproto.KindAppDrain:
			return map[string]any{"ok": true}, ""
		case agentproto.KindAppBuild:
			var p agentproto.BuildParams
			_ = json.Unmarshal(op.Params, &p)
			return agentproto.BuildResult{Image: p.Image, DetectResult: agentproto.DetectResult{Commit: strings.Repeat("a", 40), Runtime: "railpack",
				Provider: "php", HosbahToml: toml}}, ""
		case agentproto.KindAppStart:
			var p agentproto.StartParams
			_ = json.Unmarshal(op.Params, &p)
			res, msg := start(p)
			if msg != "" {
				return nil, msg
			}
			return res, ""
		case agentproto.KindAppRollback:
			var p agentproto.RollbackParams
			_ = json.Unmarshal(op.Params, &p)
			web, msg := start(p.Web)
			if msg != "" {
				return nil, msg
			}
			res := agentproto.RollbackResult{Web: web}
			for _, w := range p.Workers {
				r, msg := start(w)
				if msg != "" {
					return nil, msg
				}
				res.Workers = append(res.Workers, r)
			}
			return res, ""
		case agentproto.KindRouteSet:
			var p agentproto.RouteParams
			_ = json.Unmarshal(op.Params, &p)
			if p.Realtime != nil {
				if err := p.Realtime.Validate(); err != nil {
					return nil, err.Error()
				}
			}
			return map[string]any{"hosts": len(p.Hosts)}, ""
		case agentproto.KindRealtimeRoute:
			var p agentproto.RealtimeRouteParams
			_ = json.Unmarshal(op.Params, &p)
			if err := p.Validate(); err != nil {
				return nil, err.Error()
			}
			return agentproto.RealtimeRouteResult{Routed: p.Enabled, Hosts: 1}, ""
		}
		return nil, "this fake agent does not run " + op.Kind
	})

	status := func(c *client, id string) realtimeJSON {
		t.Helper()
		var s realtimeJSON
		c.mustJSON("GET", appBase(id)+"/realtime", nil, 200, &s)
		return s
	}
	waitDeployment := func(id string) {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		for {
			var d struct {
				Deployment struct {
					Status string  `json:"status"`
					Error  *string `json:"error"`
				} `json:"deployment"`
			}
			owner.mustJSON("GET", base+"/deployments/"+id, nil, 200, &d)
			if d.Deployment.Status == "succeeded" {
				return
			}
			if d.Deployment.Status == "failed" || time.Now().After(deadline) {
				msg := ""
				if d.Deployment.Error != nil {
					msg = *d.Deployment.Error
				}
				t.Fatalf("deployment %s: %s %s", id, d.Deployment.Status, msg)
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	// lastOp waits for the n-th operation of a kind and decodes its params.
	lastOp := func(kind string, n int, into any) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			if got := agentOps(agent, kind); len(got) >= n {
				if len(got) > n {
					t.Fatalf("%d %s operations, want %d", len(got), kind, n)
				}
				if err := json.Unmarshal(got[n-1].Params, into); err != nil {
					t.Fatal(err)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("no %s #%d reached the agent", kind, n)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	good := map[string]any{"kind": "reverb", "process": "reverb", "port": 8080}

	t.Run("nothing saved: null config, an honest suggestion, no connection count", func(t *testing.T) {
		s := status(viewer, app.App.ID)
		if s.Config != nil || s.Routed || s.ProcessRunning || s.Hostnames == nil || len(s.Hostnames) != 0 {
			t.Fatalf("empty status: %+v", s)
		}
		if s.Suggestion.Confidence != "unknown" || s.Suggestion.Kind != "" || len(s.Suggestion.Evidence) == 0 {
			t.Fatalf("with nothing live nothing is suggested, and the evidence says what was looked at: %+v", s.Suggestion)
		}
		if string(s.Connections) != "null" || s.ConnectionsReason != "not exposed by this kind" || !strings.Contains(s.Note, "drain") {
			t.Fatalf("connections %s (%q), note %q", s.Connections, s.ConnectionsReason, s.Note)
		}
	})

	t.Run("what a process, a port and a path must be", func(t *testing.T) {
		with := func(k string, v any) map[string]any {
			m := map[string]any{}
			for a, b := range good {
				m[a] = b
			}
			m[k] = v
			return m
		}
		bad := map[string]map[string]any{
			"unknown kind":          with("kind", "pusher"),
			"web":                   with("process", "web"),
			"the worker: prefix":    with("process", "worker:reverb"),
			"an option as process":  with("process", "-p"),
			"a long option":         with("process", "--privileged"),
			"upper-case process":    with("process", "Reverb"),
			"digit first":           with("process", "1reverb"),
			"process with a space":  with("process", "reverb x"),
			"process too long":      with("process", "a"+strings.Repeat("b", 31)),
			"port 0":                with("port", 0),
			"port 65536":            with("port", 65536),
			"negative port":         with("port", -1),
			"relative path":         with("path", "app"),
			"traversal":             with("path", "/app/../admin"),
			"traversal to the root": with("path", "/app/.."),
			"a dot segment":         with("path", "/./app"),
			"an option in the path": with("path", "/app --upstream=evil"),
			"a wildcard":            with("path", "/app*"),
			"a brace":               with("path", "/{http.request.host}"),
			"a query":               with("path", "/app?x=1"),
			"a semicolon":           with("path", "/app;rm"),
			"a newline":             with("path", "/app\n/admin"),
			"a backslash":           with("path", "/app\\admin"),
			"percent-encoded":       with("path", "/app%2f..%2fadmin"),
			"too long":              with("path", "/"+strings.Repeat("a", 100)),
			"the root":              with("path", "/"),
			"the root, spelled":     with("path", "//"),
			"the root, custom":      map[string]any{"kind": "custom", "process": "reverb", "port": 8080, "path": "/"},
			"the root, not custom":  map[string]any{"kind": "ws", "process": "reverb", "port": 8080, "path": "/", "whole_site": true},
			"whole_site elsewhere":  map[string]any{"kind": "custom", "process": "reverb", "port": 8080, "path": "/x", "whole_site": true},
		}
		for name, body := range bad {
			if status, text := owner.do("PUT", base+"/realtime", body); status != 422 {
				t.Errorf("%s: %d (want 422): %s", name, status, text)
			}
		}
		if s := status(owner, app.App.ID); s.Config != nil {
			t.Fatalf("a refused configuration leaves nothing: %+v", s.Config)
		}
	})

	t.Run("roles and other teams", func(t *testing.T) {
		viewer.mustJSON("PUT", base+"/realtime", good, 403, nil)
		viewer.mustJSON("DELETE", base+"/realtime", nil, 403, nil)
		stranger.mustJSON("GET", base+"/realtime", nil, 404, nil)
		stranger.mustJSON("PUT", base+"/realtime", good, 404, nil)
		stranger.mustJSON("DELETE", base+"/realtime", nil, 404, nil)
		owner.mustJSON("DELETE", base+"/realtime", nil, 404, nil) // nothing to remove
	})

	t.Run("an imported app has no realtime", func(t *testing.T) {
		apps, _ := listImported(t, owner, team.ID, srv)
		imp, ok := apps["imp-rt-"+short]
		if !ok {
			t.Fatalf("no imported app: %+v", apps)
		}
		owner.mustJSON("GET", appBase(imp.ID)+"/realtime", nil, 422, nil)
		owner.mustJSON("PUT", appBase(imp.ID)+"/realtime", good, 422, nil)
		owner.mustJSON("DELETE", appBase(imp.ID)+"/realtime", nil, 422, nil)
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "set_app_realtime", map[string]any{"app": imp.ID, "kind": "ws", "process": "ws", "port": 6001}); !res.IsError {
			t.Errorf("MCP set_app_realtime on an imported app: %+v", res)
		}
	})

	t.Run("a deploy without realtime publishes nothing for the workers", func(t *testing.T) {
		owner.mustJSON("POST", base+"/domains", map[string]any{"hostname": host, "tls": "off"}, 201, nil)
		var dep struct {
			ID string `json:"id"`
		}
		owner.mustJSON("POST", base+"/deployments", map[string]any{}, 201, &dep)
		waitDeployment(dep.ID)
		for _, o := range agentOps(agent, agentproto.KindAppStart) {
			var p agentproto.StartParams
			_ = json.Unmarshal(o.Params, &p)
			if p.Realtime != nil {
				t.Errorf("%s was started with realtime params before any were configured: %+v", p.Process, p.Realtime)
			}
		}
		var rp agentproto.RouteParams
		lastOp(agentproto.KindRouteSet, 1, &rp)
		if rp.Realtime != nil || rp.Upstream != fmt.Sprintf("127.0.0.1:%d", webPort) {
			t.Fatalf("the switch: %+v", rp)
		}
		s := status(owner, app.App.ID)
		if s.Suggestion.Kind != "reverb" || s.Suggestion.Process != "reverb" || s.Suggestion.Port != 8080 || s.Suggestion.Path != "/app" || s.Suggestion.Confidence != "detected" {
			t.Fatalf("the live release runs reverb:start: %+v", s.Suggestion)
		}
		if fmt.Sprint(s.DeclaredProcesses) != "[jobs reverb]" {
			t.Fatalf("declared: %v", s.DeclaredProcesses)
		}
	})

	t.Run("a process the live release does not declare", func(t *testing.T) {
		status, text := owner.do("PUT", base+"/realtime", map[string]any{"kind": "ws", "process": "sockets", "port": 6001})
		if status != 422 || !strings.Contains(text, "jobs, reverb") {
			t.Fatalf("%d: %s", status, text)
		}
	})

	t.Run("enabling while a release is live needs a restart", func(t *testing.T) {
		var saved realtimeJSON
		owner.mustJSON("PUT", base+"/realtime", good, 200, &saved)
		if saved.Config == nil || !saved.Config.Enabled || saved.Config.Path != "/app" || saved.Config.Port != 8080 || saved.Applies != "next_deploy" ||
			saved.Routed || saved.ProcessRunning || saved.Operation != nil {
			t.Fatalf("saved: %+v", saved)
		}
		if len(saved.Hostnames) != 1 || saved.Hostnames[0] != "ws://"+host+"/app" {
			t.Fatalf("hostnames: %v", saved.Hostnames)
		}
		if n := len(agentOps(agent, agentproto.KindRealtimeRoute)); n != 0 {
			t.Fatalf("nothing is published yet, so nothing can be routed: %d realtime.route", n)
		}

		var dep struct {
			ID string `json:"id"`
		}
		owner.mustJSON("POST", base+"/restart", nil, 201, &dep)
		waitDeployment(dep.ID)
		var p agentproto.RollbackParams
		lastOp(agentproto.KindAppRollback, 1, &p)
		if p.Web.Realtime != nil || len(p.Workers) != 2 {
			t.Fatalf("rollback params: %+v", p)
		}
		for _, w := range p.Workers {
			switch w.Process {
			case "worker:reverb":
				if w.Realtime == nil || w.Realtime.Port != 8080 || w.Realtime.Path != "/app" || w.Realtime.WholeSite {
					t.Errorf("worker:reverb: %+v", w.Realtime)
				}
			default:
				if w.Realtime != nil {
					t.Errorf("%s must publish nothing: %+v", w.Process, w.Realtime)
				}
			}
		}
		s := status(owner, app.App.ID)
		if !s.Routed || !s.ProcessRunning {
			t.Fatalf("after the restart: %+v", s)
		}
	})

	t.Run("a deploy switches web and the realtime path in one route.set", func(t *testing.T) {
		var dep struct {
			ID string `json:"id"`
		}
		owner.mustJSON("POST", base+"/deployments", map[string]any{}, 201, &dep)
		waitDeployment(dep.ID)
		published := 0
		for _, o := range agentOps(agent, agentproto.KindAppStart) {
			var p agentproto.StartParams
			_ = json.Unmarshal(o.Params, &p)
			if p.DeploymentID != dep.ID {
				continue
			}
			if p.Realtime != nil {
				published++
				if p.Process != "worker:reverb" || p.Realtime.Port != 8080 {
					t.Errorf("published for %s: %+v", p.Process, p.Realtime)
				}
			}
		}
		if published != 1 {
			t.Fatalf("exactly one process publishes the realtime port, got %d", published)
		}
		var rp agentproto.RouteParams
		lastOp(agentproto.KindRouteSet, 2, &rp)
		if rp.Realtime == nil || rp.Realtime.Path != "/app" || rp.Realtime.Upstream != fmt.Sprintf("127.0.0.1:%d", rtPort) || len(rp.Hosts) != 1 {
			t.Fatalf("the switch: %+v", rp)
		}
		if s := status(owner, app.App.ID); !s.Routed || !s.ProcessRunning {
			t.Fatalf("after the deploy: %+v", s)
		}
	})

	t.Run("a domain added meanwhile keeps the realtime route", func(t *testing.T) {
		owner.mustJSON("POST", base+"/domains", map[string]any{"hostname": "rt2-" + short + ".example.test", "tls": "internal"}, 201, nil)
		var rp agentproto.RouteParams
		lastOp(agentproto.KindRouteSet, 3, &rp)
		if len(rp.Hosts) != 2 || rp.Realtime == nil || rp.Realtime.Path != "/app" {
			t.Fatalf("route.set after a domain: %+v", rp)
		}
		s := status(owner, app.App.ID)
		if fmt.Sprint(s.Hostnames) != fmt.Sprintf("[ws://%s/app wss://rt2-%s.example.test/app]", host, short) &&
			fmt.Sprint(s.Hostnames) != fmt.Sprintf("[wss://rt2-%s.example.test/app ws://%s/app]", short, host) {
			t.Fatalf("hostnames: %v", s.Hostnames)
		}
	})

	t.Run("a changed path, disabling and enabling again apply at once", func(t *testing.T) {
		var saved realtimeJSON
		owner.mustJSON("PUT", base+"/realtime", map[string]any{"kind": "reverb", "process": "reverb", "port": 8080, "path": "/sockets//live/"}, 200, &saved)
		if saved.Applies != "now" || saved.Operation == nil || !saved.Routed || saved.Config.Path != "/sockets/live" {
			t.Fatalf("a changed path: %+v", saved)
		}
		var p agentproto.RealtimeRouteParams
		lastOp(agentproto.KindRealtimeRoute, 1, &p)
		if !p.Enabled || p.Route == nil || p.Route.Path != "/sockets/live" || p.Route.Upstream != fmt.Sprintf("127.0.0.1:%d", rtPort) || p.AppID != app.App.ID {
			t.Fatalf("realtime.route: %+v", p)
		}

		// The same again changes nothing: no operation.
		owner.mustJSON("PUT", base+"/realtime", map[string]any{"kind": "reverb", "process": "reverb", "port": 8080, "path": "/sockets/live"}, 200, &saved)
		if saved.Operation != nil || saved.Applies != "now" {
			t.Fatalf("an unchanged configuration: %+v", saved)
		}

		off := false
		owner.mustJSON("PUT", base+"/realtime", map[string]any{"enabled": off, "kind": "reverb", "process": "reverb", "port": 8080, "path": "/sockets/live"}, 200, &saved)
		if saved.Routed || saved.Applies != "now" || saved.Operation == nil || saved.Config.Enabled {
			t.Fatalf("disabled: %+v", saved)
		}
		p = agentproto.RealtimeRouteParams{}
		lastOp(agentproto.KindRealtimeRoute, 2, &p)
		if p.Enabled || p.Route != nil {
			t.Fatalf("realtime.route must remove: %+v", p)
		}

		// A changed port cannot be served by what is published: the next restart.
		owner.mustJSON("PUT", base+"/realtime", map[string]any{"kind": "reverb", "process": "reverb", "port": 9090, "path": "/sockets/live"}, 200, &saved)
		if saved.Routed || saved.Applies != "next_deploy" || saved.Operation != nil {
			t.Fatalf("a changed port: %+v", saved)
		}

		owner.mustJSON("PUT", base+"/realtime", map[string]any{"kind": "reverb", "process": "reverb", "port": 8080, "path": "/sockets/live"}, 200, &saved)
		if !saved.Routed || saved.Applies != "now" || saved.Operation == nil {
			t.Fatalf("enabled again: %+v", saved)
		}
		p = agentproto.RealtimeRouteParams{}
		lastOp(agentproto.KindRealtimeRoute, 3, &p)
		if !p.Enabled || p.Route == nil || p.Route.Path != "/sockets/live" {
			t.Fatalf("realtime.route: %+v", p)
		}
	})

	t.Run("MCP", func(t *testing.T) {
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "get_app_realtime", map[string]any{"app": app.App.Name}); res.IsError ||
			!strings.Contains(res.Text, "/sockets/live") || !strings.Contains(res.Text, "routed") || !strings.Contains(res.Text, "not exposed by this kind") {
			t.Errorf("get_app_realtime: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "set_app_realtime", map[string]any{"app": app.App.ID, "kind": "reverb", "process": "reverb", "port": 8080}); !res.IsError {
			t.Errorf("a read token must not write: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "set_app_realtime", map[string]any{"app": app.App.ID, "kind": "reverb", "process": "reverb", "port": 70000}); !res.IsError {
			t.Errorf("a port out of range is refused, not clamped: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "set_app_realtime", map[string]any{"app": app.App.ID, "kind": "reverb", "process": "reverb", "port": 8080, "path": "/app"}); res.IsError ||
			!strings.Contains(res.Text, "operation") {
			t.Errorf("set_app_realtime: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "remove_app_realtime", map[string]any{"app": app.App.ID}); !res.IsError {
			t.Errorf("a read token must not remove: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "remove_app_realtime", map[string]any{"app": app.App.ID}); res.IsError || !strings.Contains(res.Text, "operation") {
			t.Errorf("remove_app_realtime: %+v", res)
		}
		var p agentproto.RealtimeRouteParams
		lastOp(agentproto.KindRealtimeRoute, 5, &p)
		if p.Enabled {
			t.Fatalf("removing removes the route: %+v", p)
		}
		if s := status(owner, app.App.ID); s.Config != nil || s.Routed {
			t.Fatalf("after removing: %+v", s)
		}
	})

	t.Run("removing over REST, and the whole site", func(t *testing.T) {
		var saved realtimeJSON
		owner.mustJSON("PUT", base+"/realtime", map[string]any{"kind": "custom", "process": "reverb", "port": 8080, "path": "/", "whole_site": true}, 200, &saved)
		if !saved.Config.WholeSite || saved.Config.Path != "/" || !saved.Routed {
			t.Fatalf("whole site: %+v", saved)
		}
		var removed struct {
			Removed   bool    `json:"removed"`
			Routed    bool    `json:"routed"`
			Applies   string  `json:"applies"`
			Operation *string `json:"operation"`
		}
		owner.mustJSON("DELETE", base+"/realtime", nil, 200, &removed)
		if !removed.Removed || removed.Routed || removed.Applies != "now" || removed.Operation == nil {
			t.Fatalf("removed: %+v", removed)
		}
	})

	t.Run("audited", func(t *testing.T) {
		var audit struct {
			Entries []struct {
				Action string `json:"action"`
			} `json:"entries"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/audit?limit=200", nil, 200, &audit)
		got := map[string]int{}
		for _, e := range audit.Entries {
			got[e.Action]++
		}
		for _, a := range []string{"realtime.configured", "realtime.removed"} {
			if got[a] == 0 {
				t.Errorf("no %s in the audit log: %v", a, got)
			}
		}
	})

	t.Run("the configuration goes with its app", func(t *testing.T) {
		owner.mustJSON("PUT", base+"/realtime", good, 200, nil)
		count := func() (n int) {
			t.Helper()
			err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
				return tx.QueryRow(`SELECT count(*) FROM app_realtime WHERE app_id = $1`, app.App.ID).Scan(&n)
			})
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
		if n := count(); n != 1 {
			t.Fatalf("before: %d", n)
		}
		owner.mustJSON("DELETE", base, map[string]any{"confirm": app.App.Name}, 204, nil)
		if n := count(); n != 0 {
			t.Fatalf("after deleting the app: %d", n)
		}
	})
}
