package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/mcp"
	"github.com/fadymondy/hosbah/pkg/hosbah"
)

/*
A fast deploy the agent aborts (a build-affecting file such as go.sum changed) must fall back to a
real full build. Both used to run under the same pipeline step, so the full build's operation got
the aborted fast build's op id: the queue answered it with the fast build's result, no image was
ever built, and app.start tried to pull hosbah/<app>:<deployment> from a registry.
*/
func TestFastTrackAbortRunsFullBuild(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner := newClient(t, ts.URL)
	owner.register("fb-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Fallback " + suffix}, 201, &team)
	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "fb-"+short, "203.0.113.52", nil)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "fb-" + short, "server": srv,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	base := "/api/teams/" + team.ID + "/apps/" + app.App.ID

	var mu sync.Mutex
	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		mu.Lock()
		defer mu.Unlock()
		switch op.Kind {
		case agentproto.KindProvisionStep, agentproto.KindAppDrain:
			return map[string]any{"ok": true}, ""
		case agentproto.KindAppFastBuild:
			return agentproto.FastBuildResult{Commit: strings.Repeat("b", 40), Aborted: true, AbortPath: "go.sum", AbortReason: "go.sum changed"}, ""
		case agentproto.KindAppBuild:
			var p agentproto.BuildParams
			_ = json.Unmarshal(op.Params, &p)
			return agentproto.BuildResult{Image: p.Image, DetectResult: agentproto.DetectResult{Commit: strings.Repeat("a", 40), Runtime: "dockerfile"}}, ""
		case agentproto.KindAppStart:
			var p agentproto.StartParams
			_ = json.Unmarshal(op.Params, &p)
			if err := p.Validate(); err != nil {
				return nil, err.Error()
			}
			return agentproto.StartResult{Container: agentproto.ContainerName(p.App, p.Process, p.DeploymentID), HostPort: 41011}, ""
		case agentproto.KindRouteSet:
			return map[string]any{"hosts": 0}, ""
		}
		return nil, "this fake agent does not run " + op.Kind
	})

	deploy := func(body map[string]any) {
		t.Helper()
		var d struct{ ID string }
		owner.mustJSON("POST", base+"/deployments", body, 201, &d)
		deadline := time.Now().Add(90 * time.Second)
		for {
			var got struct {
				Deployment struct {
					Status string  `json:"status"`
					Error  *string `json:"error"`
				} `json:"deployment"`
			}
			owner.mustJSON("GET", base+"/deployments/"+d.ID, nil, 200, &got)
			if got.Deployment.Status == "succeeded" {
				return
			}
			if got.Deployment.Status == "failed" || time.Now().After(deadline) {
				msg := ""
				if got.Deployment.Error != nil {
					msg = *got.Deployment.Error
				}
				t.Fatalf("deployment %s: %s %s", d.ID, got.Deployment.Status, msg)
			}
			time.Sleep(150 * time.Millisecond)
		}
	}

	deploy(map[string]any{}) // the first release: always a full build
	owner.mustJSON("PUT", base+"/deploy-config", map[string]any{"mode": "fast", "preset": "dockerfile", "steps": []any{map[string]any{"key": "build", "name": "Build", "run": "true", "enabled": true}}}, 200, nil)
	deploy(map[string]any{"track": "fast"})

	if n := len(agentOps(agent, agentproto.KindAppFastBuild)); n != 1 {
		t.Fatalf("%d app.fast_build operations, want 1", n)
	}
	builds := agentOps(agent, agentproto.KindAppBuild)
	if len(builds) != 2 {
		t.Fatalf("%d app.build operations, want 2 (the first release, then the fallback after the aborted fast build)", len(builds))
	}
	var p agentproto.BuildParams
	_ = json.Unmarshal(builds[1].Params, &p)
	if !strings.HasPrefix(p.Image, agentproto.ImageRepo(app.App.Name)+":") {
		t.Fatalf("fallback build image %q", p.Image)
	}
}

// deploy_app with wait answers within one short hold, so no proxy in front of the API drops it (the
// web app's rewrite proxy gives up after 30s with a bare HTTP 500), and the SDK follows the
// continue: line through wait_deployment until the deployment has finished.
func TestDeployAppWaitOverMCP(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner := newClient(t, ts.URL)
	owner.register("wait-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Wait " + suffix}, 201, &team)
	var tok struct{ ID, Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{
		"name": "wait", "scopes": []string{"read", "deploy"}, "expires_in_days": 1}, 201, &tok)
	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "wt-"+short, "203.0.113.53", nil)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "wt-" + short, "server": srv,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &app)

	startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		switch op.Kind {
		case agentproto.KindProvisionStep, agentproto.KindAppDrain:
			return map[string]any{"ok": true}, ""
		case agentproto.KindAppBuild:
			time.Sleep(2500 * time.Millisecond) // outlasts the hold, so the wait needs a continue
			var p agentproto.BuildParams
			_ = json.Unmarshal(op.Params, &p)
			return agentproto.BuildResult{Image: p.Image, DetectResult: agentproto.DetectResult{Commit: strings.Repeat("a", 40), Runtime: "dockerfile"}}, ""
		case agentproto.KindAppStart:
			var p agentproto.StartParams
			_ = json.Unmarshal(op.Params, &p)
			return agentproto.StartResult{Container: agentproto.ContainerName(p.App, p.Process, p.DeploymentID), HostPort: 41012}, ""
		case agentproto.KindRouteSet:
			return map[string]any{"hosts": 0}, ""
		}
		return nil, "this fake agent does not run " + op.Kind
	})

	before := mcp.WaitHold
	mcp.WaitHold = time.Second
	defer func() { mcp.WaitHold = before }()
	out, err := hosbah.New(ts.URL, tok.Token).Deploy(context.Background(), app.App.Name, hosbah.Wait(), hosbah.With("wait_seconds", 60))
	if err != nil || !strings.Contains(out, "queued") || !strings.Contains(out, "succeeded") || strings.Contains(out, mcp.ContinuePrefix) {
		t.Fatalf("deploy_app wait: %q %v", out, err)
	}
}
