package server

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// agentTermSocket authenticates as the server's agent, like agentSocket, but instead of draining
// every frame it hands each one to the caller through a channel — a terminal test needs to read
// the term.* frames the control plane relays, not just send heartbeats.
func agentTermSocket(t *testing.T, base, serverID string, priv ed25519.PrivateKey) (read func(*testing.T) agentproto.Frame, send func(agentproto.Frame)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/agent/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	var ch agentproto.Frame
	if err := wsjson.Read(ctx, c, &ch); err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, agentproto.AuthSigningBytes(ch.Nonce, serverID))
	_ = wsjson.Write(ctx, c, agentproto.Frame{Type: agentproto.TypeAuth, ServerID: serverID, Signature: base64.StdEncoding.EncodeToString(sig), AgentVersion: "fake"})
	var w agentproto.Frame
	if err := wsjson.Read(ctx, c, &w); err != nil || w.Type != agentproto.TypeWelcome {
		t.Fatalf("welcome: %v %+v", err, w)
	}
	frames := make(chan agentproto.Frame, 32)
	go func() {
		for {
			var f agentproto.Frame
			if wsjson.Read(ctx, c, &f) != nil {
				close(frames)
				return
			}
			if f.Type == agentproto.TypeResume {
				continue // the resume list on connect: not what a terminal test is looking for
			}
			frames <- f
		}
	}()
	read = func(t *testing.T) agentproto.Frame {
		t.Helper()
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatal("agent socket closed")
			}
			return f
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for a frame")
			return agentproto.Frame{}
		}
	}
	send = func(f agentproto.Frame) {
		if err := wsjson.Write(ctx, c, f); err != nil {
			t.Fatalf("agent write: %v", err)
		}
	}
	return read, send
}

// TestTerminalWSIsolation: MH-574 E11. The terminal websocket is a raw route (not Huma), so the
// OpenAPI-driven isolation sweep in TestIsolationSweep never reaches it — this is its own test.
// A member of an unrelated team must not be able to open or attach to team A's terminal, at all
// (a plain GET, no websocket upgrade attempted, so the handler's own team-membership check is
// what is under test, not the websocket library's).
func TestTerminalWSIsolation(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	ua := newClient(t, ts.URL)
	ua.register("term-a-" + suffix + "@example.test")
	ub := newClient(t, ts.URL)
	ub.register("term-b-" + suffix + "@example.test")

	var teamA, teamB struct{ ID string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "TermA " + suffix}, 201, &teamA)
	ub.mustJSON("POST", "/api/teams", map[string]any{"name": "TermB " + suffix}, 201, &teamB)
	srvA, _ := enrolServer(t, ua, ts.URL, teamA.ID, "term-a-"+suffix[len(suffix)-6:])

	path := "/api/teams/" + teamA.ID + "/servers/" + srvA + "/terminal/ws"

	// B is not a member of A: 404, indistinguishable from a team or server that does not exist.
	if status, _ := ub.do("GET", path, nil); status != 404 {
		t.Fatalf("another team's member: got %d, want 404", status)
	}
	// The same, addressed by a made-up server id, must also 404 (not leak "server not found" vs
	// "team not found").
	if status, _ := ub.do("GET", "/api/teams/"+teamA.ID+"/servers/does-not-exist/terminal/ws", nil); status != 404 {
		t.Fatalf("bad server id: got %d, want 404", status)
	}
	// Anonymous: unauthorized, not a leak either.
	anon := newClient(t, ts.URL)
	if status, _ := anon.do("GET", path, nil); status != 401 {
		t.Fatalf("anonymous: got %d, want 401", status)
	}
}

// TestTerminalWSAbility: a viewer (below AbilityManageServers) is refused a host terminal even
// though they are a genuine member of the team.
func TestTerminalWSAbility(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	ua := newClient(t, ts.URL)
	ua.register("term-owner-" + suffix + "@example.test")
	uv := newClient(t, ts.URL)
	viewerEmail := "term-viewer-" + suffix + "@example.test"
	uv.register(viewerEmail)

	var teamA struct{ ID string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "TermV " + suffix}, 201, &teamA)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	token := inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]
	uv.mustJSON("POST", "/api/invitations/"+token+"/accept", nil, 200, nil)

	srvA, _ := enrolServer(t, ua, ts.URL, teamA.ID, "term-v-"+suffix[len(suffix)-6:])
	path := "/api/teams/" + teamA.ID + "/servers/" + srvA + "/terminal/ws"

	if status, _ := uv.do("GET", path, nil); status != 403 {
		t.Fatalf("viewer opening a host terminal: got %d, want 403", status)
	}
}

// TestTerminalWSRelay: an owner opens a host terminal on their own, connected server; the agent
// receives the signed terminal.open op with the session id the control plane chose, and
// term_data frames relay both ways by that session id. No round of it ever touches the database.
func TestTerminalWSRelay(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	ua := newClient(t, ts.URL)
	ua.register("term-relay-" + suffix + "@example.test")
	var teamA struct{ ID string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "TermRelay " + suffix}, 201, &teamA)
	srvA, priv := enrolServer(t, ua, ts.URL, teamA.ID, "term-relay-"+suffix[len(suffix)-6:])

	agentRead, agentSend := agentTermSocket(t, ts.URL, srvA, priv)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/teams/" + teamA.ID + "/servers/" + srvA + "/terminal/ws?target=host&cols=80&rows=24"
	browser, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: ua.http})
	if err != nil {
		t.Fatalf("browser dial: %v", err)
	}
	defer browser.CloseNow() //nolint:errcheck

	// The agent gets a signed terminal.open op naming this server and a fresh session id.
	op := agentRead(t)
	if op.Type != agentproto.TypeOp || op.Op == nil || op.Op.Kind != agentproto.KindTerminalOpen {
		t.Fatalf("agent's first frame: %+v", op)
	}
	if op.Op.ServerID != srvA {
		t.Fatalf("op server id = %q, want %q", op.Op.ServerID, srvA)
	}
	var params agentproto.TerminalOpenParams
	if err := json.Unmarshal(op.Op.Params, &params); err != nil {
		t.Fatalf("op params: %v", err)
	}
	if params.Target != agentproto.TerminalTargetHost || params.Cols != 80 || params.Rows != 24 {
		t.Fatalf("op params: %+v", params)
	}

	// Agent -> browser.
	agentSend(agentproto.Frame{Type: agentproto.TypeTermData, SessionID: params.SessionID, TermData: "aGVsbG8="})
	var got agentproto.Frame
	rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
	if err := wsjson.Read(rctx, browser, &got); err != nil {
		rcancel()
		t.Fatalf("browser read: %v", err)
	}
	rcancel()
	if got.Type != agentproto.TypeTermData || got.TermData != "aGVsbG8=" {
		t.Fatalf("browser got: %+v", got)
	}

	// Browser -> agent.
	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	if err := wsjson.Write(wctx, browser, agentproto.Frame{Type: agentproto.TypeTermData, TermData: "d29ybGQ="}); err != nil {
		wcancel()
		t.Fatalf("browser write: %v", err)
	}
	wcancel()
	back := agentRead(t)
	if back.Type != agentproto.TypeTermData || back.SessionID != params.SessionID || back.TermData != "d29ybGQ=" {
		t.Fatalf("agent got: %+v", back)
	}
}

// An app terminal: the browser names only the app and process; the API looks up the live
// release's deployment itself (the web app never knew it), and says so when nothing is live.
func TestTerminalWSAppTarget(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	ua := newClient(t, ts.URL)
	ua.register("term-app-" + suffix + "@example.test")
	var teamA struct{ ID string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "TermApp " + suffix}, 201, &teamA)
	srvA, priv := enrolServer(t, ua, ts.URL, teamA.ID, "term-app-"+short)
	var app struct{ App struct{ ID string } }
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/apps", map[string]any{"name": "term-app-" + short, "server": srvA,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &app)

	agentRead, _ := agentTermSocket(t, ts.URL, srvA, priv)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/teams/" + teamA.ID + "/servers/" + srvA +
		"/terminal/ws?cols=80&rows=24&target=" + url.QueryEscape("app:"+app.App.ID+"/web")

	// Nothing live yet: the session closes with a reason a person can act on.
	b1, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: ua.http})
	if err != nil {
		t.Fatalf("browser dial: %v", err)
	}
	var closed agentproto.Frame
	if err := wsjson.Read(ctx, b1, &closed); err != nil {
		t.Fatalf("browser read: %v", err)
	}
	b1.CloseNow() //nolint:errcheck
	if closed.Type != agentproto.TypeTermClose || !strings.Contains(closed.TermReason, "deploy it first") {
		t.Fatalf("no live release: got %+v", closed)
	}

	// With a live release, the agent's terminal.open carries its deployment id.
	dep := seedLiveRelease(t, teamA.ID, app.App.ID, srvA, "term-app-"+short)
	b2, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: ua.http})
	if err != nil {
		t.Fatalf("browser dial: %v", err)
	}
	defer b2.CloseNow() //nolint:errcheck
	op := agentRead(t)
	if op.Type != agentproto.TypeOp || op.Op == nil || op.Op.Kind != agentproto.KindTerminalOpen {
		t.Fatalf("agent's first frame: %+v", op)
	}
	var params agentproto.TerminalOpenParams
	if err := json.Unmarshal(op.Op.Params, &params); err != nil {
		t.Fatalf("op params: %v", err)
	}
	if params.DeploymentID != dep || params.Target != "app:"+app.App.ID+"/web" {
		t.Fatalf("op params: %+v, want deployment %s", params, dep)
	}
}
