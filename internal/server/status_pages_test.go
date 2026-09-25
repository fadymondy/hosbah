package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

/*
MH-575 H2/H3: a heartbeat monitor that goes overdue opens an incident on the sweep, and a ping
resolves it (internal/monitors/heartbeat.go); a public status page (internal/api/public.go) never
answers for an unpublished page, and never carries a monitor outside the ones it lists, whatever
team or state that monitor is in.
*/

func TestHeartbeatMonitor(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	ev := lastMonitors
	ev.Stop()
	clock := &fakeClock{t: time.Now().UTC().Truncate(time.Second)}
	ev.SetClock(clock.now)

	owner := newClient(t, ts.URL)
	owner.register("hb-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Heartbeat " + suffix}, 201, &team)

	srv, _ := enrolWithServicesKey(t, owner, ts.URL, team.ID, "hb-"+short, "203.0.113.30", nil)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "hb-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	base := "/api/teams/" + team.ID + "/apps/" + app.App.ID

	var m monitorJSON
	owner.mustJSON("POST", base+"/monitors", map[string]any{"name": "heartbeat", "kind": "heartbeat",
		"heartbeat_interval_seconds": 60, "heartbeat_grace_seconds": 30}, 201, &m)
	if m.Kind != "heartbeat" || m.State != "unknown" {
		t.Fatalf("created: %+v", m)
	}
	var raw struct {
		PingURLPath string `json:"ping_url_path"`
	}
	owner.mustJSON("GET", base+"/monitors/"+m.ID, nil, 200, &raw) // read back: no ping_url_path (shown once, at create)
	if raw.PingURLPath != "" {
		t.Fatalf("the ping URL must not be shown again: %+v", raw)
	}
	// Re-fetch the create response's ping_url_path directly.
	var created struct {
		PingURLPath string `json:"ping_url_path"`
	}
	owner.mustJSON("POST", base+"/monitors", map[string]any{"name": "heartbeat2", "kind": "heartbeat"}, 201, &created)
	if created.PingURLPath == "" || !strings.HasPrefix(created.PingURLPath, "/hooks/heartbeat/") {
		t.Fatalf("create must return a ping URL once: %+v", created)
	}
	pingURL := created.PingURLPath
	var m2 monitorJSON
	owner.mustJSON("GET", base+"/monitors/heartbeat2", nil, 200, &m2)

	get := func(id string) monitorJSON {
		t.Helper()
		var out monitorJSON
		owner.mustJSON("GET", base+"/monitors/"+id, nil, 200, &out)
		return out
	}
	tick := func() {
		t.Helper()
		if _, err := ev.Tick(context.Background()); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	// The ping route is outside /api and needs no session, so it is hit directly on the raw test
	// server URL rather than through owner's client.
	pingOnce := func() {
		t.Helper()
		resp, err := http.Get(ts.URL + pingURL) //nolint:gosec,noctx // test-only, url is our own httptest server
		if err != nil {
			t.Fatalf("ping: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("ping status: %d", resp.StatusCode)
		}
	}
	pingOnce()
	m2 = get(m2.ID)
	if m2.LastCheckedAt == nil && m2.State != "unknown" {
		t.Fatalf("first ping: %+v", m2)
	}

	// heartbeat2 used the defaults: interval 300s, grace 120s — advance past both, with no ping.
	clock.advance(500 * time.Second)
	tick()
	m2 = get(m2.ID)
	if m2.State != "failing" || m2.OpenIncident == nil {
		t.Fatalf("an overdue heartbeat must open an incident: %+v", m2)
	}

	// A ping resolves it.
	pingOnce()
	m2 = get(m2.ID)
	if m2.State != "ok" || m2.OpenIncident != nil {
		t.Fatalf("a ping must resolve the open incident: %+v", m2)
	}
}

func TestPublicStatusPage(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner := newClient(t, ts.URL)
	owner.register("sp-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Status " + suffix}, 201, &team)

	other := newClient(t, ts.URL)
	other.register("sp-other-" + suffix + "@example.test")
	var otherTeam struct{ ID string }
	other.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, &otherTeam)

	srv, _ := enrolWithServicesKey(t, owner, ts.URL, team.ID, "sp-"+short, "203.0.113.40", nil)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "sp-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	base := "/api/teams/" + team.ID + "/apps/" + app.App.ID

	var shown, hidden monitorJSON
	owner.mustJSON("POST", base+"/monitors", map[string]any{"name": "shown", "kind": "heartbeat"}, 201, &shown)
	owner.mustJSON("POST", base+"/monitors", map[string]any{"name": "hidden", "kind": "heartbeat"}, 201, &hidden)

	otherSrv, _ := enrolWithServicesKey(t, other, ts.URL, otherTeam.ID, "spo-"+short, "203.0.113.41", nil)
	var otherApp struct {
		App struct{ ID, Name string } `json:"app"`
	}
	other.mustJSON("POST", "/api/teams/"+otherTeam.ID+"/apps", map[string]any{"name": "spo-" + short, "server": otherSrv, "repo_url": "https://github.com/octocat/api.git"}, 201, &otherApp)
	var otherMon monitorJSON
	other.mustJSON("POST", "/api/teams/"+otherTeam.ID+"/apps/"+otherApp.App.ID+"/monitors", map[string]any{"name": "other-team", "kind": "heartbeat"}, 201, &otherMon)

	var page struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	slug := "status-" + suffix
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/status-pages", map[string]any{"slug": slug, "title": "Status", "monitor_ids": []string{shown.ID}}, 201, &page)

	// Unpublished: 404, indistinguishable from "no such slug".
	if status, text := owner.do("GET", "/api/public/status/"+slug, nil); status != 404 {
		t.Fatalf("unpublished page must 404: %d %s", status, text)
	}
	if status, _ := owner.do("GET", "/api/public/status/no-such-"+suffix, nil); status != 404 {
		t.Fatalf("a nonexistent slug must also 404")
	}

	owner.mustJSON("PATCH", "/api/teams/"+team.ID+"/status-pages/"+page.ID, map[string]any{"published": true}, 200, nil)

	var out struct {
		Title    string `json:"title"`
		Slug     string `json:"slug"`
		Monitors []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"monitors"`
	}
	owner.mustJSON("GET", "/api/public/status/"+slug, nil, 200, &out)
	if out.Slug != slug || len(out.Monitors) != 1 || out.Monitors[0].ID != shown.ID {
		t.Fatalf("published page must show exactly its own monitors: %+v", out)
	}
	body := fmt.Sprintf("%+v", out)
	if strings.Contains(body, hidden.ID) || strings.Contains(body, hidden.Name) {
		t.Fatalf("a status page must never show a monitor it does not list: %s", body)
	}
	if strings.Contains(body, otherMon.ID) || strings.Contains(body, otherMon.Name) || strings.Contains(body, otherTeam.ID) {
		t.Fatalf("a status page must never leak another team's data: %s", body)
	}

	// Someone else's status page (even published) can never be listed against — monitor_ids from
	// another team are rejected at write time.
	if status, text := owner.do("POST", "/api/teams/"+team.ID+"/status-pages", map[string]any{"slug": "sp2-" + suffix, "title": "x",
		"monitor_ids": []string{otherMon.ID}}); status != 422 {
		t.Fatalf("a status page must not adopt another team's monitor: %d %s", status, text)
	}
}
