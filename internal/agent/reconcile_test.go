package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

const (
	appA = "11111111-1111-1111-1111-111111111111"
	appB = "22222222-2222-2222-2222-222222222222"
)

// psLine is one `docker ps --format '{{json .}}'` line.
func psLine(name, labels, ports, created string) string {
	b, _ := json.Marshal(map[string]string{"Names": name, "Labels": labels, "Ports": ports, "CreatedAt": created, "State": "running"})
	return string(b)
}

func TestParseRunning(t *testing.T) {
	out := strings.Join([]string{
		psLine("demo-web-v3", "hosbah.app="+appA+",hosbah.process=web,hosbah.deployment=d1", "127.0.0.1:32768->3000/tcp", "2026-10-18 16:50:01 +0000 UTC"),
		psLine("old-web", "cloudy.app="+appB+",cloudy.process=web", "127.0.0.1:32769->8080/tcp, 9000/tcp, 0.0.0.0:5432->5432/tcp", "2026-10-01 10:00:00 +0000 UTC"),
		psLine("rt", "hosbah.app="+appA+",hosbah.process=worker:reverb", "127.0.0.1:32770->8080/tcp, :::8081->8081/tcp", "bad date"),
		"not json",
		"",
	}, "\n")
	cs := parseRunning(out)
	if len(cs) != 3 {
		t.Fatalf("got %d containers, want 3: %+v", len(cs), cs)
	}
	if c := cs[0]; c.Name != "demo-web-v3" || c.AppID != appA || c.Process != "web" || c.Ports[3000] != 32768 || c.Created.IsZero() {
		t.Errorf("hosbah-labelled web: %+v", c)
	}
	if c := cs[1]; c.AppID != appB || c.Process != "web" || len(c.Ports) != 1 || c.Ports[8080] != 32769 {
		t.Errorf("legacy labels / only loopback tcp ports count: %+v", c)
	}
	if c := cs[2]; c.Process != "worker:reverb" || len(c.Ports) != 1 || !c.Created.IsZero() {
		t.Errorf("worker: %+v", c)
	}
}

func ctr(name, app, process string, age time.Duration, ports map[int]int) runningContainer {
	return runningContainer{Name: name, AppID: app, Process: process, Created: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC).Add(-age), Ports: ports}
}

// The incident: after a reboot every container is back, on new ports — and the ports are reused
// across apps, so app A's old port now belongs to app B.
func TestReconcileTableAfterReboot(t *testing.T) {
	tb := routeTable{Apps: map[string]routeEntry{
		appA: {Upstream: "127.0.0.1:33106", Realtime: &agentproto.RealtimeRoute{Path: "/app", Upstream: "127.0.0.1:33107"}},
		appB: {Upstream: "127.0.0.1:32774"},
	}}
	ix := indexRunning([]runningContainer{
		ctr("a-web", appA, "web", 0, map[int]int{3000: 32771}),
		ctr("a-rt", appA, "worker:reverb", 0, map[int]int{8080: 32772}),
		ctr("a-jobs", appA, "worker:jobs", 0, nil),
		ctr("b-web", appB, "web", 0, map[int]int{80: 32768}),
		ctr("c-web", "33333333-3333-3333-3333-333333333333", "web", 0, map[int]int{80: 32774}), // took B's old port
	})
	reports, reload, _ := reconcileTable(&tb, ix)
	if !reload || len(reports) != 2 {
		t.Fatalf("reload=%v reports=%+v", reload, reports)
	}
	if got := tb.Apps[appA].Upstream; got != "127.0.0.1:32771" {
		t.Errorf("app A upstream = %s", got)
	}
	if got := tb.Apps[appA].Realtime.Upstream; got != "127.0.0.1:32772" {
		t.Errorf("app A realtime = %s", got)
	}
	if got := tb.Apps[appB].Upstream; got != "127.0.0.1:32768" {
		t.Errorf("app B must not keep a port another app's container answers on: %s", got)
	}
	r := reports[0]
	if r.AppID != appA || r.Container != "a-web" || r.Previous != "127.0.0.1:33106" || r.RealtimeContainer != "a-rt" || r.RealtimeUpstream != "127.0.0.1:32772" || r.Level != agentproto.RouteReportInfo {
		t.Errorf("report: %+v", r)
	}
	// A second pass is a no-op.
	if reports, reload, _ := reconcileTable(&tb, ix); reload || len(reports) != 0 {
		t.Errorf("second pass changed things: %+v", reports)
	}
}

// Mid-deploy: the old and the new release both run; a route on the new one stays put, and a dead
// route goes to the newest container.
func TestReconcileTableTwoReleases(t *testing.T) {
	ix := indexRunning([]runningContainer{
		ctr("a-web-v1", appA, "web", time.Hour, map[int]int{3000: 32768}),
		ctr("a-web-v2", appA, "web", 0, map[int]int{3000: 32790}),
	})
	tb := routeTable{Apps: map[string]routeEntry{appA: {Upstream: "127.0.0.1:32768"}}}
	if reps, reload, _ := reconcileTable(&tb, ix); reload || len(reps) != 0 {
		t.Fatalf("a live upstream of the app moved: %+v", reps)
	}
	tb.Apps[appA] = routeEntry{Upstream: "127.0.0.1:40000"}
	reconcileTable(&tb, ix)
	if got := tb.Apps[appA].Upstream; got != "127.0.0.1:32790" {
		t.Errorf("dead upstream should go to the newest web container, got %s", got)
	}
}

func TestReconcileTableNoCandidate(t *testing.T) {
	tb := routeTable{Apps: map[string]routeEntry{
		appA: {Upstream: "127.0.0.1:40000"},
		appB: {Upstream: "10.0.0.5:80"}, // not loopback: never touched
	}}
	ix := indexRunning([]runningContainer{ctr("a-jobs", appA, "worker:jobs", 0, nil)})
	if reps, reload, _ := reconcileTable(&tb, ix); reload || len(reps) != 0 || tb.Apps[appA].Upstream != "127.0.0.1:40000" {
		t.Errorf("nothing to route to must leave the route alone: %+v %+v", reps, tb.Apps)
	}
}

func TestReconcileTableProxyLearnAndFollow(t *testing.T) {
	tb := routeTable{Apps: map[string]routeEntry{}, Proxies: map[string]proxyEntry{
		"p1": {Host: agentproto.Route{Hostname: "n8n.example.com"}, Dial: "127.0.0.1:32780"},
		"p2": {Host: agentproto.Route{Hostname: "browser.example.com"}, Dial: "127.0.0.1:19222"},
		"p3": {Host: agentproto.Route{Hostname: "lan.example.com"}, Dial: "10.10.10.5:80"},
	}}
	before := indexRunning([]runningContainer{ctr("n8n", "", "", 0, map[int]int{5678: 32780})})
	reps, reload, learned := reconcileTable(&tb, before)
	if reload || !learned || len(reps) != 0 {
		t.Fatalf("learning only: reload=%v learned=%v %+v", reload, learned, reps)
	}
	if p := tb.Proxies["p1"]; p.Container != "n8n" || p.ContainerPort != 5678 {
		t.Fatalf("p1 binding not learned: %+v", p)
	}
	if p := tb.Proxies["p2"]; p.Container != "" {
		t.Errorf("a host service has no container: %+v", p)
	}
	after := indexRunning([]runningContainer{
		ctr("n8n", "", "", 0, map[int]int{5678: 32769}),
		ctr("other", "", "", 0, map[int]int{80: 32780}), // took the old port
	})
	reps, reload, _ = reconcileTable(&tb, after)
	if !reload || len(reps) != 1 || reps[0].ProxyID != "p1" || reps[0].Upstream != "127.0.0.1:32769" {
		t.Fatalf("follow: reload=%v %+v", reload, reps)
	}
	if p := tb.Proxies["p1"]; p.Dial != "127.0.0.1:32769" || p.Requested != "127.0.0.1:32780" {
		t.Errorf("p1 after follow: %+v", p)
	}
}

func TestKeepFollowed(t *testing.T) {
	prev := proxyEntry{Dial: "127.0.0.1:32769", Requested: "127.0.0.1:32780", Container: "n8n", ContainerPort: 5678}
	e := proxyEntry{Dial: "127.0.0.1:32780"} // the control plane re-sends the original dial
	keepFollowed(&e, prev)
	if e.Dial != "127.0.0.1:32769" || e.Container != "n8n" || e.Requested != "127.0.0.1:32780" {
		t.Errorf("re-sent dial: %+v", e)
	}
	e = proxyEntry{Dial: "127.0.0.1:32769"}
	keepFollowed(&e, prev)
	if e.Dial != "127.0.0.1:32769" || e.Container != "n8n" || e.Requested != "127.0.0.1:32780" {
		t.Errorf("current dial: %+v", e)
	}
	e = proxyEntry{Dial: "127.0.0.1:9999"}
	keepFollowed(&e, prev)
	if e.Dial != "127.0.0.1:9999" || e.Container != "" {
		t.Errorf("a new upstream must be learned afresh: %+v", e)
	}
}

func TestRouteSnapshot(t *testing.T) {
	tb := routeTable{Apps: map[string]routeEntry{
		appA: {Upstream: "127.0.0.1:32771", Realtime: &agentproto.RealtimeRoute{Path: "/app", Upstream: "127.0.0.1:32772"}},
		appB: {Upstream: "127.0.0.1:40000"}, // nothing answers: not reported
	}}
	ix := indexRunning([]runningContainer{
		ctr("a-web", appA, "web", 0, map[int]int{3000: 32771}),
		ctr("a-rt", appA, "worker:reverb", 0, map[int]int{8080: 32772}),
	})
	got := routeSnapshot(tb, ix)
	if len(got) != 1 || got[0].Container != "a-web" || got[0].RealtimeContainer != "a-rt" || got[0].Previous != "" {
		t.Errorf("snapshot: %+v", got)
	}
}

// route.set with the control plane's stale host_port is routed to the port the app publishes.
func TestCorrectUpstream(t *testing.T) {
	restore := appDocker
	defer func() { appDocker = restore }()
	appDocker = func(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
		if name != "docker" || len(args) == 0 || args[0] != "ps" {
			return nil, fmt.Errorf("unexpected %s %v", name, args)
		}
		return []byte(psLine("a-web", "hosbah.app="+appA+",hosbah.process=web", "127.0.0.1:32771->3000/tcp", "2026-10-18 16:50:01 +0000 UTC")), nil
	}
	oc := &OpContext{Ctx: t.Context(), log: func(string, string) {}, notify: func(string, json.RawMessage) {}, progress: func(int, int) {}}
	p := agentproto.RouteParams{AppID: appA, Upstream: "127.0.0.1:33106"}
	r := correctUpstream(oc, &p)
	if r == nil || p.Upstream != "127.0.0.1:32771" || r.Container != "a-web" || r.Previous != "127.0.0.1:33106" {
		t.Fatalf("stale port: %+v %+v", p, r)
	}
	p = agentproto.RouteParams{AppID: appA, Upstream: "127.0.0.1:32771"}
	if r := correctUpstream(oc, &p); r != nil {
		t.Errorf("a live port was changed: %+v", r)
	}
	appDocker = func(context.Context, []string, string, ...string) ([]byte, error) {
		return nil, errors.New("no docker")
	}
	p = agentproto.RouteParams{AppID: appA, Upstream: "127.0.0.1:33106"}
	if r := correctUpstream(oc, &p); r != nil || p.Upstream != "127.0.0.1:33106" {
		t.Errorf("no docker must leave the params alone: %+v", p)
	}
}

// An upstream refusing connections is re-reconciled after healthGrace and WARNed about once per
// outage; it recovering resets that.
func TestHealthCheckWarnsOncePerOutage(t *testing.T) {
	dir := t.TempDir()
	restoreRoutes := routesFile
	routesFile = func() string { return dir + "/routes.json" }
	defer func() { routesFile = restoreRoutes }()
	if err := saveRoutes(routeTable{Apps: map[string]routeEntry{appA: {Upstream: "127.0.0.1:40000"}}}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var sent []agentproto.RouteReport
	up := false
	r := newReconciler(slog.New(slog.NewTextHandler(io.Discard, nil)), func(rs []agentproto.RouteReport) {
		mu.Lock()
		sent = append(sent, rs...)
		mu.Unlock()
	})
	r.now = func() time.Time { return now }
	r.dial = func(context.Context, string) error {
		if up {
			return nil
		}
		return errors.New("connection refused")
	}
	passes := 0
	r.run = func(context.Context, []string, string, ...string) ([]byte, error) { // the app is stopped
		passes++
		return nil, nil
	}
	step := func(d time.Duration) { now = now.Add(d); r.healthCheck(t.Context()) }

	step(0)
	step(45 * time.Second)
	if passes != 0 || len(sent) != 0 {
		t.Fatalf("before the grace period: passes=%d sent=%+v", passes, sent)
	}
	step(15 * time.Second) // 60s down
	if passes != 1 || len(sent) != 1 || sent[0].Level != agentproto.RouteReportWarn || sent[0].AppID != appA || !strings.Contains(sent[0].Message, "127.0.0.1:40000") {
		t.Fatalf("at 60s: passes=%d sent=%+v", passes, sent)
	}
	step(15 * time.Second)
	step(15 * time.Second)
	step(15 * time.Second)
	if passes != 1 {
		t.Errorf("re-reconciled before another grace period: %d", passes)
	}
	step(15 * time.Second) // 120s
	if passes != 2 || len(sent) != 1 {
		t.Errorf("at 120s: passes=%d, warned %d times (once per outage)", passes, len(sent))
	}
	up = true
	step(15 * time.Second)
	up = false
	for range 5 {
		step(15 * time.Second)
	}
	if len(sent) != 2 {
		t.Errorf("a new outage should warn again: %+v", sent)
	}
}
