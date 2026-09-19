package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/metrics"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// enrolServer adds a BYO server and enrols it as its agent would, returning the agent's key.
func enrolServer(t *testing.T, c *client, base, teamID, name string) (string, ed25519.PrivateKey) {
	t.Helper()
	var created struct {
		Server  struct{ ID string }
		Install struct{ URL string }
	}
	c.mustJSON("POST", "/api/teams/"+teamID+"/servers", map[string]any{"name": name, "provider": "byo"}, 201, &created)
	token := created.Install.URL[strings.LastIndex(created.Install.URL, "/")+1:]
	pub, priv, _ := ed25519.GenerateKey(nil)
	body, _ := json.Marshal(agentproto.EnrollRequest{Token: token, PublicKey: base64.StdEncoding.EncodeToString(pub), AgentVersion: "test",
		Facts: agentproto.Facts{Hostname: name, OSID: "ubuntu", OSVersion: "24.04", Arch: "amd64", CPUs: 2, MemTotalBytes: 2 << 30, Systemd: true,
			Hardware: &agentproto.Hardware{CPUModel: "Test CPU", CPUCores: 2, CPUThreads: 4, Disks: []agentproto.Disk{}, Interfaces: []agentproto.NetInterface{},
				GPUs: []agentproto.GPU{{Index: 0, Name: "Tesla T4", Vendor: "NVIDIA", MemoryTotalBytes: 15 << 30}}}}})
	resp, err := http.Post(base+"/agent/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("enrol %s: %d", name, resp.StatusCode)
	}
	return created.Server.ID, priv
}

// agentSocket authenticates as the server's agent and returns a frame sender.
func agentSocket(t *testing.T, base, serverID string, priv ed25519.PrivateKey) func(agentproto.Frame) {
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
	go func() { // drain (resume and anything else)
		for {
			var f agentproto.Frame
			if wsjson.Read(ctx, c, &f) != nil {
				return
			}
		}
	}()
	return func(f agentproto.Frame) {
		if err := wsjson.Write(ctx, c, f); err != nil {
			t.Fatalf("agent write: %v", err)
		}
	}
}

// seedMinutes writes n minute rows ending at end (agent scope, as heartbeats would).
func seedMinutes(t *testing.T, sqlDB *sql.DB, serverID, teamID string, end time.Time, n int, cpu float64, gpu bool) {
	t.Helper()
	err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{ServerID: serverID}, func(q *db.Queries, _ *sql.Tx) error {
		for i := 0; i < n; i++ {
			at := end.Add(-time.Duration(i) * time.Minute)
			var mm metrics.Minute
			m := agentproto.Metrics{CPUPercent: cpu, MemUsedBytes: 1 << 30, MemTotalBytes: 2 << 30, DiskUsedBytes: 5 << 30, DiskTotalBytes: 20 << 30, Load1: 0.5}
			if gpu {
				m.GPUs = []agentproto.GPUMetric{{Index: 0, UtilPercent: 40, MemUsedBytes: 1 << 30, MemTotalBytes: 15 << 30, TempC: 60}}
			}
			mm.Add(m, at)
			mm.Add(m, at.Add(time.Second)) // two beats in the minute
			if err := metrics.Record(context.Background(), q, serverID, teamID, mm); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed metrics: %v", err)
	}
}

func TestHomePage(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ua := newClient(t, ts.URL)
	ua.register("home-a-" + suffix + "@example.test")
	ub := newClient(t, ts.URL)
	ub.register("home-b-" + suffix + "@example.test")
	var teamA, teamB struct{ ID string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "HomeA " + suffix}, 201, &teamA)
	ub.mustJSON("POST", "/api/teams", map[string]any{"name": "HomeB " + suffix}, 201, &teamB)

	srvA, privA := enrolServer(t, ua, ts.URL, teamA.ID, "home-a-"+suffix[len(suffix)-6:])
	srvB, _ := enrolServer(t, ub, ts.URL, teamB.ID, "home-b-"+suffix[len(suffix)-6:])
	now := time.Now().UTC()
	seedMinutes(t, sqlDB, srvA, teamA.ID, now, 3*60, 30, true) // 3 hours of minutes, with a GPU
	seedMinutes(t, sqlDB, srvB, teamB.ID, now, 10, 90, false)

	t.Run("series ranges are bucketed", func(t *testing.T) {
		for _, c := range []struct {
			rng        string
			step, maxN int
		}{{"1h", 60, 60}, {"6h", 120, 180}, {"24h", 300, 288}, {"7d", 3600, 168}, {"30d", 10800, 240}} {
			var s metrics.Series
			ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/servers/"+srvA+"/metrics?range="+c.rng, nil, 200, &s)
			if s.Range != c.rng || s.StepSeconds != c.step || len(s.Points) == 0 || len(s.Points) > c.maxN {
				t.Fatalf("%s: range %q step %d, %d points (max %d)", c.rng, s.Range, s.StepSeconds, len(s.Points), c.maxN)
			}
			p := s.Points[len(s.Points)-1]
			if p.CPU != 30 || p.MemTotal != 2<<30 || len(p.GPUs) != 1 || p.GPUs[0].Util != 40 {
				t.Fatalf("%s: last point %+v", c.rng, p)
			}
		}
		if code, _ := ua.do("GET", "/api/teams/"+teamA.ID+"/servers/"+srvA+"/metrics?range=2h", nil); code != 422 {
			t.Fatalf("an unknown range should be 422, got %d", code)
		}
		// Team B cannot read A's server, by id or through its own team's path.
		if code, _ := ub.do("GET", "/api/teams/"+teamB.ID+"/servers/"+srvA+"/metrics", nil); code != 404 {
			t.Fatalf("B reading A's server through B's team: %d", code)
		}
	})

	t.Run("sparklines in one call", func(t *testing.T) {
		var sp metrics.Sparks
		ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/servers/metrics?range=1h", nil, 200, &sp)
		a, ok := sp.Servers[srvA]
		if !ok || len(a.CPU) == 0 || len(a.CPU) > metrics.MaxSparkPoints || len(a.Mem) != len(a.CPU) || len(a.GPU) != len(a.CPU) {
			t.Fatalf("A's sparklines: %+v", sp)
		}
		if a.Mem[len(a.Mem)-1] != 50 {
			t.Fatalf("memory is a percentage: %v", a.Mem)
		}
		if _, leaked := sp.Servers[srvB]; leaked {
			t.Fatal("team A's sparklines include team B's server")
		}
		var spB struct {
			Servers map[string]struct{ GPU []float64 } `json:"servers"`
		}
		ub.mustJSON("GET", "/api/teams/"+teamB.ID+"/servers/metrics?range=24h", nil, 200, &spB)
		if _, leaked := spB.Servers[srvA]; leaked || spB.Servers[srvB].GPU != nil {
			t.Fatalf("B's sparklines: %+v (gpu must be null without a GPU)", spB)
		}
	})

	t.Run("live readings reach the stream as metrics events", func(t *testing.T) {
		send := agentSocket(t, ts.URL, srvA, privA)
		m := agentproto.Metrics{CPUPercent: 77, MemUsedBytes: 3 << 30, MemTotalBytes: 4 << 30, At: time.Now().UTC(),
			GPUs: []agentproto.GPUMetric{{Index: 0, UtilPercent: 12, TempC: 50}}}
		send(agentproto.Frame{Type: agentproto.TypeHeartbeat, Metrics: &m})
		m.CPUPercent = 55
		send(agentproto.Frame{Type: agentproto.TypeMetrics, Metrics: &m})

		req, _ := http.NewRequest("GET", ts.URL+"/api/teams/"+teamA.ID+"/servers/"+srvA+"/stream", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := ua.http.Do(req.WithContext(ctx))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		event := ""
		var live metrics.Point
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimPrefix(line, "event: ")
			}
			if event == "metrics" && strings.HasPrefix(line, "data: ") {
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &live); err != nil {
					t.Fatal(err)
				}
				if live.CPU == 55 { // the heartbeat's reading (77) may come first
					break
				}
			}
		}
		if live.CPU != 55 || live.MemTotal != 4<<30 || len(live.GPUs) != 1 || live.GPUs[0].Util != 12 {
			t.Fatalf("live metrics event: %+v", live)
		}
	})

	t.Run("deployments feed", func(t *testing.T) {
		var app1, app2 struct {
			App struct{ ID, Name string } `json:"app"`
		}
		ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/apps", map[string]any{"name": "feed-one-" + suffix[len(suffix)-5:], "server": srvA,
			"repo_url": "https://github.com/example/one", "deploy": true}, 201, &app1)
		ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/apps", map[string]any{"name": "feed-two-" + suffix[len(suffix)-5:], "server": srvA,
			"repo_url": "https://github.com/example/two", "branch": "develop", "deploy": true}, 201, &app2)
		var feed struct {
			Deployments []struct {
				ID, Status, Branch, Trigger, Kind string
				Number                            int64
				App, Server                       struct{ ID, Name string }
				TriggeredBy                       *struct{ Email string } `json:"triggered_by"`
				CommitMessage                     *string                 `json:"commit_message"`
			} `json:"deployments"`
			NextCursor *string `json:"next_cursor"`
		}
		ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/deployments?limit=1", nil, 200, &feed)
		if len(feed.Deployments) != 1 || feed.NextCursor == nil {
			t.Fatalf("first page: %+v", feed)
		}
		first := feed.Deployments[0]
		if first.App.ID != app2.App.ID || first.Branch != "develop" || first.Server.ID != srvA || first.Server.Name == "" ||
			first.Trigger != "manual" || first.Number != 1 || first.TriggeredBy == nil || !strings.HasPrefix(first.TriggeredBy.Email, "home-a-") {
			t.Fatalf("newest deployment: %+v", first)
		}
		cursor := *feed.NextCursor
		feed.Deployments, feed.NextCursor = nil, nil
		ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/deployments?limit=1&before="+cursor, nil, 200, &feed)
		if len(feed.Deployments) != 1 || feed.Deployments[0].App.ID != app1.App.ID || feed.NextCursor != nil {
			t.Fatalf("second page: %+v", feed)
		}
		if code, _ := ua.do("GET", "/api/teams/"+teamA.ID+"/deployments?before=bogus", nil); code != 422 {
			t.Fatalf("a bad cursor should be 422, got %d", code)
		}
		ub.mustJSON("GET", "/api/teams/"+teamB.ID+"/deployments", nil, 200, &feed)
		if len(feed.Deployments) != 0 {
			t.Fatalf("team B sees deployments: %+v", feed.Deployments)
		}
		// MCP: list_deployments without an app is the team feed, as JSON.
		var tok struct{ Token string }
		ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/mcp-tokens", map[string]any{"name": "feed", "scopes": []string{"read"}, "expires_in_days": 1}, 201, &tok)
		res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "list_deployments", map[string]any{"limit": 5})
		if res.IsError || !strings.Contains(res.Text, `"next_cursor":null`) || !strings.Contains(res.Text, app1.App.Name) {
			t.Fatalf("MCP team feed: %+v", res)
		}
	})

	t.Run("tunnel summary on the server JSON", func(t *testing.T) {
		var cred struct{ ID string }
		ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/credentials", map[string]any{"provider": "proxmox", "name": "pve-home",
			"config": map[string]string{"api_url": "https://pve.invalid:8006", "token_id": "cloudy@pve!ctl", "node": "pve", "storage": "local-lvm"},
			"secret": "s", "skip_validation": true}, 201, &cred)
		err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{TeamID: teamA.ID}, func(_ *db.Queries, tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO tunnels (team_id, server_id, credential_id, kind, remote_port, status, address, created_by)
				VALUES ($1, $2, $3, 'frp', 7001, 'active', 'frps.example.test:7001', 'test')`, teamA.ID, srvA, cred.ID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		var list struct {
			Servers []struct {
				ID     string
				Tunnel *struct{ Kind, Status, Address string }
			} `json:"servers"`
		}
		ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/servers", nil, 200, &list)
		if len(list.Servers) != 1 || list.Servers[0].Tunnel == nil || list.Servers[0].Tunnel.Kind != "frp" ||
			list.Servers[0].Tunnel.Status != "active" || list.Servers[0].Tunnel.Address != "frps.example.test:7001" {
			t.Fatalf("server list tunnel: %+v", list)
		}
		var detail struct {
			Tunnel *struct{ Kind string }
			Facts  struct {
				Hardware struct {
					CPUModel string `json:"cpu_model"`
					GPUs     []struct{ Name string }
				} `json:"hardware"`
			}
		}
		ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/servers/"+srvA, nil, 200, &detail)
		if detail.Tunnel == nil || detail.Facts.Hardware.CPUModel != "Test CPU" || len(detail.Facts.Hardware.GPUs) != 1 {
			t.Fatalf("server detail: %+v", detail)
		}
	})

	t.Run("rename and restyle", func(t *testing.T) {
		base := "/api/teams/" + teamA.ID + "/servers/" + srvA
		for _, bad := range []map[string]any{{"color": "#12345"}, {"color": "red"}, {"icon": "unicorn"},
			{"logo_url": "http://example.test/logo.png"}, {"name": "has space"}} {
			if code, text := ua.do("PATCH", base, bad); code != 422 {
				t.Errorf("PATCH %v -> %d (want 422): %s", bad, code, text)
			}
		}
		other, _ := enrolServer(t, ua, ts.URL, teamA.ID, "home-other-"+suffix[len(suffix)-6:])
		if code, text := ua.do("PATCH", "/api/teams/"+teamA.ID+"/servers/"+other, map[string]any{"name": "home-a-" + suffix[len(suffix)-6:]}); code != 409 ||
			!strings.Contains(text, "already exists") {
			t.Errorf("rename onto a taken name -> %d: %s", code, text)
		}
		var out map[string]any
		ua.mustJSON("PATCH", base, map[string]any{"name": "renamed-" + suffix[len(suffix)-6:], "color": "#3b82f6", "icon": "rocket",
			"logo_url": "https://example.test/logo.png"}, 200, &out)
		if out["name"] != "renamed-"+suffix[len(suffix)-6:] || out["color"] != "#3B82F6" || out["icon"] != "rocket" || out["logo_url"] != "https://example.test/logo.png" {
			t.Fatalf("patched: %+v", out)
		}
		ua.mustJSON("PATCH", base, map[string]any{"color": "", "logo_url": ""}, 200, &out)
		if out["color"] != nil || out["logo_url"] != nil || out["icon"] != "rocket" {
			t.Fatalf("cleared: %+v", out)
		}
		var audit struct {
			Entries []struct {
				Action  string
				Changes json.RawMessage
			} `json:"entries"`
		}
		ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/audit", nil, 200, &audit)
		found := false
		for _, e := range audit.Entries {
			if e.Action == "server.updated" && strings.Contains(string(e.Changes), `"old"`) && strings.Contains(string(e.Changes), "#3B82F6") {
				found = true
			}
		}
		if !found {
			t.Errorf("no server.updated audit entry with old/new values: %+v", audit.Entries)
		}
		// A viewer may not restyle; team B gets 404.
		viewerEmail := "home-viewer-" + suffix + "@example.test"
		uv := newClient(t, ts.URL)
		uv.register(viewerEmail)
		var inv struct {
			AcceptURL string `json:"accept_url"`
		}
		ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
		uv.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)
		if code, _ := uv.do("PATCH", base, map[string]any{"color": "#000000"}); code != 403 {
			t.Errorf("viewer PATCH -> %d (want 403)", code)
		}
		if code, _ := ub.do("PATCH", base, map[string]any{"color": "#000000"}); code != 404 {
			t.Errorf("team B PATCH -> %d (want 404)", code)
		}
		// MCP update_server needs the write scope and applies the same rules.
		var tok struct{ Token string }
		ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/mcp-tokens", map[string]any{"name": "style", "scopes": []string{"read", "write"}, "expires_in_days": 1}, 201, &tok)
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "update_server", map[string]any{"server": srvA, "icon": "nope"}); !res.IsError {
			t.Errorf("MCP update_server with a bad icon: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "update_server", map[string]any{"server": srvA, "icon": "leaf", "color": "#00ff00"}); res.IsError ||
			!strings.Contains(res.Text, "leaf") || !strings.Contains(res.Text, "#00FF00") {
			t.Errorf("MCP update_server: %+v", res)
		}
	})
}

// The retention job folds minute rows older than 24 h into 15-minute rows (keeping the
// sample-weighted averages) and drops anything older than 30 days.
func TestMetricsRetention(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ua := newClient(t, ts.URL)
	ua.register("ret-" + suffix + "@example.test")
	var team struct{ ID string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "Ret " + suffix}, 201, &team)
	srv, _ := enrolServer(t, ua, ts.URL, team.ID, "ret-"+suffix[len(suffix)-6:])

	now := time.Now().UTC()
	aligned := now.Add(-26 * time.Hour).Truncate(15 * time.Minute).Add(14 * time.Minute) // the last minute of a 15-minute bucket
	seedMinutes(t, sqlDB, srv, team.ID, aligned, 30, 20, true)                           // two whole 15-minute buckets
	seedMinutes(t, sqlDB, srv, team.ID, now, 5, 50, false)                               // recent: kept as minutes
	seedMinutes(t, sqlDB, srv, team.ID, now.Add(-(31*24)*time.Hour), 1, 10, false)       // will be 15-min, then pruned

	count := func(step int) (n, samples int64) {
		_ = tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			return tx.QueryRow(`SELECT count(*), coalesce(sum(samples), 0) FROM server_metrics WHERE server_id = $1 AND step = $2`, srv, step).Scan(&n, &samples)
		})
		return
	}
	if n, _ := count(60); n != 36 {
		t.Fatalf("seeded %d minute rows, want 36", n)
	}
	if _, _, err := metrics.Compact(context.Background(), sqlDB, now); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if n, _ := count(60); n != 5 {
		t.Fatalf("minute rows after compaction: %d (want the 5 recent ones)", n)
	}
	n, samples := count(900)
	if n != 2 || samples != 60 {
		t.Fatalf("15-minute rows: %d with %d samples (want 2 with 60: the 31-day-old one pruned)", n, samples)
	}
	// Idempotent, and the averages survive.
	if _, _, err := metrics.Compact(context.Background(), sqlDB, now); err != nil {
		t.Fatal(err)
	}
	var s metrics.Series
	ua.mustJSON("GET", "/api/teams/"+team.ID+"/servers/"+srv+"/metrics?range=30d", nil, 200, &s)
	if len(s.Points) < 2 || s.Points[0].CPU != 20 || len(s.Points[0].GPUs) != 1 || s.Points[0].GPUs[0].Util != 40 {
		t.Fatalf("30d series after compaction: %+v", s.Points)
	}
}
