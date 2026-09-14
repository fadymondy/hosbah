package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/cloudy/internal/agent"
	"github.com/fadymondy/cloudy/internal/agentproto"
)

// The real agent (internal/agent, the code cmd/agent runs) against the real control plane:
// enrol with the one-time token, connect over the WebSocket, heartbeat, run system.info with its
// log streamed over SSE, lose the connection, reconnect by itself and run the next operation.
func TestAgentEndToEnd(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	c := newClient(t, ts.URL)
	c.register("agent-e2e-" + suffix + "@example.test")
	var team struct{ ID, Slug string }
	c.mustJSON("POST", "/api/teams", map[string]any{"name": "E2E " + suffix}, 201, &team)
	var created struct {
		Server  struct{ ID string }
		Install struct{ URL, CommandRoot string }
	}
	c.mustJSON("POST", "/api/teams/"+team.ID+"/servers", map[string]any{"name": "e2e-" + suffix, "provider": "byo"}, 201, &created)
	token := created.Install.URL[strings.LastIndex(created.Install.URL, "/")+1:]

	// The install script is served for a valid token, and names the checksum placeholder.
	resp, err := http.Get(created.Install.URL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("install script: %v %v", err, resp)
	}
	script, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(script), "sha256sum -c") || !strings.Contains(string(script), token) {
		t.Fatalf("the install script must verify the checksum and carry the token")
	}

	facts := agentproto.Facts{Hostname: "e2e", OSID: "ubuntu", OSVersion: "24.04", Arch: "amd64", CPUs: 2, MemTotalBytes: 2 << 30, Systemd: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st, _, err := agent.EnrollWithFacts(ctx, ts.URL, token, "e2e", nil, facts)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}
	// The token was single-use.
	if _, _, err := agent.EnrollWithFacts(ctx, ts.URL, token, "e2e", nil, facts); err == nil {
		t.Fatal("an enrolment token worked twice")
	}

	a, err := agent.New(st, "e2e", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	a.HeartbeatEvery = 300 * time.Millisecond
	go func() { _ = a.Run(ctx) }()

	serverPath := "/api/teams/" + team.ID + "/servers/" + created.Server.ID
	waitServer := func(want func(online bool, status string) bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			var s struct {
				Online bool
				Status string
			}
			c.mustJSON("GET", serverPath, nil, 200, &s)
			if want(s.Online, s.Status) {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("server never reached the wanted state")
	}
	waitServer(func(online bool, status string) bool { return online && status == "enrolled" })

	runOp := func(kind string) string {
		t.Helper()
		var op struct{ ID string }
		c.mustJSON("POST", serverPath+"/operations", map[string]any{"kind": kind}, 201, &op)
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			var d struct {
				Operation struct{ Status string }
				Logs      []struct{ Line string }
			}
			c.mustJSON("GET", serverPath+"/operations/"+op.ID, nil, 200, &d)
			switch d.Operation.Status {
			case "ok":
				return op.ID
			case "failed", "timeout":
				t.Fatalf("%s ended %s: %+v", kind, d.Operation.Status, d.Logs)
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("%s never finished", kind)
		return ""
	}

	opID := runOp(agentproto.KindSystemInfo)
	// The SSE stream replays the whole log and ends with done (the operation is finished).
	req, _ := http.NewRequest("GET", ts.URL+serverPath+"/operations/"+opID+"/stream", nil)
	sresp, err := c.http.Do(req)
	if err != nil || sresp.StatusCode != 200 || !strings.HasPrefix(sresp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %v %v", err, sresp)
	}
	var sawHostname, sawDone bool
	sc := bufio.NewScanner(sresp.Body)
	for sc.Scan() {
		line := sc.Text()
		sawHostname = sawHostname || strings.Contains(line, "hostname")
		if line == "event: done" {
			sawDone = true
			break
		}
	}
	sresp.Body.Close()
	if !sawHostname || !sawDone {
		t.Fatalf("the stream should carry system.info's lines and end with done (hostname %v, done %v)", sawHostname, sawDone)
	}

	// Drop the connection from the control plane's side; the agent reconnects on its own.
	svcOf(t).Disconnect(created.Server.ID)
	time.Sleep(200 * time.Millisecond)
	waitServer(func(online bool, _ string) bool { return online })
	runOp(agentproto.KindDockerCheck)
}
