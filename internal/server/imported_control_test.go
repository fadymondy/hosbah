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
Imported workloads are controllable like any other service: every control endpoint enqueues exactly
one typed, signed operation with validated params (checked here by a fake agent that verifies each
operation against the control plane's key, as cloudy-agent does); roles gate them; destroying needs
a typed confirm over REST and the approval gate over MCP; domains become proxy routes to the
workload; credentials make an imported database a normal one; Hosbah's own workloads and guests
that are Hosbah servers are refused before anything is queued.
*/
func TestImportedWorkloadControl(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	owner := newClient(t, ts.URL)
	owner.register("ctl-owner-" + suffix + "@example.test")
	var team struct{ ID, Slug string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Control " + suffix}, 201, &team)
	var tok struct{ ID, Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{
		"name": "ctl-all", "scopes": []string{"read", "deploy", "write", "secrets:read", "destructive"}, "expires_in_days": 30,
	}, 201, &tok)
	member := func(role string) *client {
		email := "ctl-" + role + "-" + suffix + "@example.test"
		c := newClient(t, ts.URL)
		c.register(email)
		var inv struct {
			AcceptURL string `json:"accept_url"`
		}
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": email, "role": role}, 201, &inv)
		c.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)
		return c
	}
	deployer, viewer := member("deployer"), member("viewer")

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "pve-"+suffix, "10.9.8.7", []agentproto.Service{
		{Kind: "container", Name: "shop-web", Image: "nginx:1.27", Status: "running", Ports: []string{"0.0.0.0:8081->80/tcp, :::8081->80/tcp"}},
		{Kind: "container", Name: "old-blog", Image: "ghost:5", Status: "running"},
		{Kind: "guest", Name: "wordpress", ID: "101", Type: "lxc", Status: "running", Address: "10.0.0.11"},
		{Kind: "container", Name: "pg-main", Image: "postgres:16-alpine", Status: "running", Ports: []string{"127.0.0.1:5433->5432/tcp"}},
		{Kind: "guest", Name: "pg-prod", ID: "102", Type: "qemu", Status: "running", Address: "10.0.0.12"},
	})

	var mu sync.Mutex
	probed := map[string]map[string]string{} // service id → credentials the agent opened
	created := map[string]string{}           // database → the admin user db.create logged in as
	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		switch op.Kind {
		case agentproto.KindWorkloadStats:
			return agentproto.WorkloadStats{Status: "running", CPUPercent: 7.5, MemUsedBytes: 1 << 20, MemTotalBytes: 1 << 30, UptimeSeconds: 60}, ""
		case agentproto.KindWorkloadPower:
			var p agentproto.PowerParams
			_ = json.Unmarshal(op.Params, &p)
			if p.Action == "stop" || p.Action == "shutdown" {
				return agentproto.PowerResult{Status: "exited"}, ""
			}
			return agentproto.PowerResult{Status: "running"}, ""
		case agentproto.KindGuestSnapshot:
			return agentproto.SnapshotResult{Snapshots: []agentproto.Snapshot{{Name: "pre-up", SnapTime: 1700000000}}}, ""
		case agentproto.KindDBProbe:
			var p agentproto.DBProbeParams
			_ = json.Unmarshal(op.Params, &p)
			pt, err := agentproto.OpenBox(priv, agentproto.DBSecretContext(p.ServiceID, "root"), p.Credentials)
			if err != nil {
				return nil, "the credentials box did not open with this agent's key"
			}
			var cred map[string]string
			_ = json.Unmarshal(pt, &cred)
			if cred["password"] == "wrong" {
				return agentproto.DBProbeResult{OK: false, Detail: "password authentication failed"}, "the credentials were refused: password authentication failed"
			}
			mu.Lock()
			probed[p.ServiceID] = cred
			mu.Unlock()
			if p.Target == "guest" {
				return agentproto.DBProbeResult{OK: true, Detail: "10.0.0.12:5432 answers"}, ""
			}
			return agentproto.DBProbeResult{OK: true, Verified: true, Version: "PostgreSQL 16.4"}, ""
		case agentproto.KindDBCreate:
			var p agentproto.DBCreateParams
			_ = json.Unmarshal(op.Params, &p)
			pt, err := agentproto.OpenBox(priv, agentproto.DBSecretContext(p.ServiceID, "root"), p.RootSecret)
			if err != nil {
				return nil, "root box"
			}
			var root map[string]string
			_ = json.Unmarshal(pt, &root)
			mu.Lock()
			created[p.Database] = root["user"] + "@" + p.Container
			mu.Unlock()
			return agentproto.DBCreateResult{Database: p.Database, User: p.User}, ""
		case agentproto.KindDBBackup:
			return agentproto.DBBackupResult{SnapshotID: "abcd1234", SizeBytes: 42}, ""
		}
		return map[string]any{"ok": true}, ""
	})

	apps, dss := listImported(t, owner, team.ID, srv)
	web, blog, wp := apps["shop-web"], apps["old-blog"], apps["wordpress"]
	pg, pgProd := dss["pg-main"], dss["pg-prod"]
	if web.ID == "" || blog.ID == "" || wp.ID == "" || pg.ID == "" || pgProd.ID == "" {
		t.Fatalf("imports: %v %v", keys(apps), keys(dss))
	}
	appPath := func(a importedApp) string { return "/api/teams/" + team.ID + "/apps/" + a.ID }
	svcPath := func(s importedService) string { return "/api/teams/" + team.ID + "/data-services/" + s.ID }

	type opBody struct {
		Operation struct {
			ID     string          `json:"id"`
			Kind   string          `json:"kind"`
			Params json.RawMessage `json:"params"`
		} `json:"operation"`
	}

	t.Run("each control enqueues exactly one signed, validated operation", func(t *testing.T) {
		for _, c := range []struct {
			name, method, path string
			body               any
			kind               string
			want               map[string]any // params that must be exactly so
		}{
			{"container stop", "POST", appPath(web) + "/power", map[string]any{"action": "stop"}, agentproto.KindWorkloadPower,
				map[string]any{"target": "container", "name": "shop-web", "action": "stop", "label": "shop-web"}},
			{"guest restart", "POST", appPath(wp) + "/power", map[string]any{"action": "restart"}, agentproto.KindWorkloadPower,
				map[string]any{"target": "guest", "vmid": "101", "guest_type": "lxc", "action": "restart", "label": "wordpress"}},
			{"guest resize", "POST", appPath(wp) + "/resize", map[string]any{"cores": 4, "memory_mb": 4096, "disk_grow_gb": 10}, agentproto.KindGuestResize,
				map[string]any{"vmid": "101", "guest_type": "lxc", "cores": float64(4), "memory_mb": float64(4096), "disk_grow_gb": float64(10)}},
			{"snapshot create", "POST", appPath(wp) + "/snapshots", map[string]any{"name": "pre-up", "description": "before"}, agentproto.KindGuestSnapshot,
				map[string]any{"action": "create", "name": "pre-up", "description": "before", "vmid": "101"}},
			{"snapshot rollback", "POST", appPath(wp) + "/snapshots/pre-up/rollback", nil, agentproto.KindGuestSnapshot,
				map[string]any{"action": "rollback", "name": "pre-up"}},
			{"snapshot delete", "DELETE", appPath(wp) + "/snapshots/pre-up", map[string]any{"confirm": "wordpress"}, agentproto.KindGuestSnapshot,
				map[string]any{"action": "delete", "name": "pre-up"}},
			{"guest backup", "POST", appPath(wp) + "/backup", map[string]any{"storage": "local"}, agentproto.KindGuestBackup,
				map[string]any{"vmid": "101", "guest_type": "lxc", "storage": "local"}},
			{"container recreate", "POST", appPath(web) + "/recreate", nil, agentproto.KindContainerRecreate,
				map[string]any{"name": "shop-web", "label": "shop-web"}},
			{"database container start", "POST", svcPath(pg) + "/power", map[string]any{"action": "start"}, agentproto.KindWorkloadPower,
				map[string]any{"target": "container", "name": "pg-main", "action": "start", "label": "pg-main"}},
		} {
			before := operationsOf(t, sqlDB, team.ID)
			var out opBody
			owner.mustJSON(c.method, c.path, c.body, 202, &out)
			if out.Operation.Kind != c.kind {
				t.Fatalf("%s: queued %q, want %q", c.name, out.Operation.Kind, c.kind)
			}
			if n := operationsOf(t, sqlDB, team.ID); n != before+1 {
				t.Fatalf("%s: queued %d operations, want exactly 1", c.name, n-before)
			}
			op := agent.waitOp(t, out.Operation.ID)
			var params map[string]any
			_ = json.Unmarshal(op.Params, &params)
			for k, v := range c.want {
				if params[k] != v {
					t.Fatalf("%s: params[%s] = %v, want %v (params %s)", c.name, k, params[k], v, op.Params)
				}
			}
		}
		// Logs mirror the runtime-logs route: 201 with the operation itself.
		var logOp struct{ ID, Kind string }
		owner.mustJSON("POST", appPath(web)+"/workload-logs", map[string]any{"tail": 50, "since": "15m"}, 201, &logOp)
		op := agent.waitOp(t, logOp.ID)
		var lp agentproto.WorkloadLogsParams
		_ = json.Unmarshal(op.Params, &lp)
		if op.Kind != agentproto.KindWorkloadLogs || lp.Name != "shop-web" || lp.Tail != 50 || lp.Since != "15m" {
			t.Fatalf("workload logs: %s %s", op.Kind, op.Params)
		}
		if len(agent.rejectedList()) != 0 {
			t.Fatalf("the agent refused operations: %v", agent.rejectedList())
		}
		// The server's operation log names the app.
		var list struct {
			Operations []struct {
				Kind   string          `json:"kind"`
				Params json.RawMessage `json:"params"`
			} `json:"operations"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/servers/"+srv+"/operations?limit=50", nil, 200, &list)
		found := false
		for _, o := range list.Operations {
			found = found || o.Kind == agentproto.KindGuestResize && strings.Contains(string(o.Params), `"label":"wordpress"`)
		}
		if !found {
			t.Fatal("the resize must show in the server's operation log with the app's name")
		}
	})

	t.Run("stats and snapshots come back from the agent", func(t *testing.T) {
		var st struct {
			Stats *agentproto.WorkloadStats `json:"stats"`
		}
		owner.mustJSON("GET", appPath(web)+"/stats?wait=15", nil, 200, &st)
		if st.Stats == nil || st.Stats.CPUPercent != 7.5 || st.Stats.MemTotalBytes != 1<<30 {
			t.Fatalf("stats: %+v", st.Stats)
		}
		var sn struct {
			Snapshots []agentproto.Snapshot `json:"snapshots"`
		}
		owner.mustJSON("GET", appPath(wp)+"/snapshots?wait=15", nil, 200, &sn)
		if len(sn.Snapshots) != 1 || sn.Snapshots[0].Name != "pre-up" {
			t.Fatalf("snapshots: %+v", sn)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "workload_stats", map[string]any{"app": web.ID}); res.IsError || !strings.Contains(res.Text, "CPU 7.5%") {
			t.Fatalf("workload_stats: %q", res.Text)
		}
	})

	t.Run("a power action updates the record's state", func(t *testing.T) {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var d struct {
				App struct {
					Status        string  `json:"status"`
					ImportedState *string `json:"imported_state"`
				} `json:"app"`
			}
			owner.mustJSON("GET", appPath(web), nil, 200, &d)
			if d.App.Status == "stopped" && deref(d.App.ImportedState) == "stopped" {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("shop-web should be stopped after the stop")
	})

	t.Run("an action on the wrong kind of workload is refused", func(t *testing.T) {
		for _, c := range []struct {
			method, path string
			body         any
		}{
			{"POST", appPath(web) + "/resize", map[string]any{"cores": 2}},
			{"POST", appPath(web) + "/snapshots", map[string]any{"name": "nope"}},
			{"POST", appPath(wp) + "/recreate", nil},
			{"POST", appPath(wp) + "/workload-logs", map[string]any{}},
		} {
			owner.expectCode(c.method, c.path, c.body, 409, "unsupported_origin")
		}
		owner.expect("POST", appPath(web)+"/power", map[string]any{"action": "hibernate"}, 422)
		owner.expect("POST", appPath(wp)+"/snapshots", map[string]any{"name": "-rf"}, 422)
	})

	t.Run("roles", func(t *testing.T) {
		before := operationsOf(t, sqlDB, team.ID)
		viewer.expect("POST", appPath(web)+"/power", map[string]any{"action": "start"}, 403)
		viewer.expect("GET", appPath(web)+"/stats?wait=0", nil, 403)
		viewer.expect("POST", appPath(web)+"/workload-logs", map[string]any{}, 403)
		deployer.expect("DELETE", appPath(wp)+"/snapshots/pre-up", map[string]any{"confirm": "wordpress"}, 403)
		deployer.expect("DELETE", appPath(blog)+"?destroy=true", map[string]any{"confirm": "old-blog"}, 403)
		deployer.expect("DELETE", svcPath(pg)+"?destroy=true", map[string]any{"confirm": "pg-main"}, 403)
		deployer.expect("POST", svcPath(pg)+"/credentials", map[string]any{"username": "a", "password": "b"}, 403)
		if n := operationsOf(t, sqlDB, team.ID); n != before {
			t.Fatalf("a refused role queued %d operation(s)", n-before)
		}
		var out opBody
		deployer.mustJSON("POST", appPath(web)+"/power", map[string]any{"action": "start"}, 202, &out)
		deployer.mustJSON("POST", appPath(wp)+"/snapshots", map[string]any{"name": "by-deployer"}, 202, &out)
		deployer.mustJSON("GET", appPath(web)+"/stats?wait=0", nil, 200, nil)
	})

	t.Run("domains on imported apps become proxy routes to the workload", func(t *testing.T) {
		var dm struct {
			ID       string  `json:"id"`
			Hostname string  `json:"hostname"`
			Upstream *string `json:"upstream"`
		}
		host := "shop-" + suffix + ".example.test"
		owner.mustJSON("POST", appPath(web)+"/domains", map[string]any{"hostname": host, "tls": "off"}, 201, &dm)
		if deref(dm.Upstream) != "127.0.0.1:8081" {
			t.Fatalf("container upstream: %v", dm.Upstream)
		}
		proxy := waitProxy(t, agent, dm.ID)
		if proxy.Hostname != host || proxy.Upstream != "127.0.0.1:8081" || proxy.TLS != "off" || proxy.Remove {
			t.Fatalf("proxy.set: %+v", proxy)
		}
		owner.expect("POST", appPath(web)+"/domains", map[string]any{"hostname": "x-" + host, "port": 9999}, 422)
		owner.expect("POST", appPath(wp)+"/domains", map[string]any{"hostname": "wp-" + host}, 422)
		var gd struct {
			ID       string  `json:"id"`
			Upstream *string `json:"upstream"`
		}
		owner.mustJSON("POST", appPath(wp)+"/domains", map[string]any{"hostname": "wp-" + host, "port": 80, "tls": "off"}, 201, &gd)
		if deref(gd.Upstream) != "http://10.0.0.11:80" {
			t.Fatalf("guest upstream: %v", gd.Upstream)
		}
		if p := waitProxy(t, agent, gd.ID); p.Upstream != "http://10.0.0.11:80" || p.Hostname != "wp-"+host {
			t.Fatalf("guest proxy.set: %+v", p)
		}
		// A git app's port is refused; the hostname namespace is shared with proxy hosts.
		owner.mustJSON("DELETE", appPath(web)+"/domains/"+host, nil, 204, nil)
		removed := false
		deadline := time.Now().Add(10 * time.Second)
		for !removed && time.Now().Before(deadline) {
			for _, o := range agentOps(agent, agentproto.KindProxySet) {
				var p agentproto.ProxyParams
				_ = json.Unmarshal(o.Params, &p)
				removed = removed || p.ProxyID == dm.ID && p.Remove
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !removed {
			t.Fatal("removing the domain must remove its proxy route")
		}
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/proxy-hosts", map[string]any{"server": srv, "hostname": "nas-" + host, "upstream": "10.0.0.5:5000", "tls": "off"}, 201, nil)
		owner.expect("POST", appPath(web)+"/domains", map[string]any{"hostname": "nas-" + host}, 409)
	})

	t.Run("credentials make an imported database a normal one", func(t *testing.T) {
		owner.expectCode("POST", svcPath(pg)+"/credentials", map[string]any{"username": "admin", "password": "wrong"}, 422, "credentials_rejected")
		var sd struct {
			Service struct {
				HasCredentials bool `json:"has_credentials"`
			} `json:"service"`
		}
		owner.mustJSON("GET", svcPath(pg), nil, 200, &sd)
		if sd.Service.HasCredentials {
			t.Fatal("rejected credentials must not be stored")
		}
		var res struct {
			Service struct {
				HasCredentials bool `json:"has_credentials"`
			} `json:"service"`
			Verified bool   `json:"verified"`
			Version  string `json:"version"`
		}
		owner.mustJSON("POST", svcPath(pg)+"/credentials", map[string]any{"username": "admin", "password": "s3cret", "database": "shop"}, 200, &res)
		if !res.Verified || !res.Service.HasCredentials || res.Version != "PostgreSQL 16.4" {
			t.Fatalf("credentials: %+v", res)
		}
		mu.Lock()
		cred := probed[pg.ID]
		mu.Unlock()
		if cred["user"] != "admin" || cred["password"] != "s3cret" || cred["database"] != "shop" {
			t.Fatalf("the probe must carry the credentials sealed to the agent: %v", cred)
		}
		// Sealed at rest, never in plaintext.
		var sealed string
		if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
			return tx.QueryRow(`SELECT root_sealed FROM data_services WHERE id = $1`, pg.ID).Scan(&sealed)
		}); err != nil || !strings.HasPrefix(sealed, "csv1:") || strings.Contains(sealed, "s3cret") {
			t.Fatalf("root_sealed: %q %v", sealed, err)
		}
		// The database features now work, logging in as the given user inside the container.
		owner.mustJSON("POST", svcPath(pg)+"/databases", map[string]any{"name": "orders"}, 201, nil)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			who := created["orders"]
			mu.Unlock()
			if who != "" {
				if who != "admin@pg-main" {
					t.Fatalf("db.create logged in as %q", who)
				}
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		mu.Lock()
		if created["orders"] == "" {
			t.Fatal("db.create never reached the agent")
		}
		mu.Unlock()
		owner.mustJSON("POST", svcPath(pg)+"/backups", nil, 201, nil)
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "backup_now", map[string]any{"database": pg.ID}); res.IsError {
			t.Fatalf("backup_now after credentials: %q", res.Text)
		}
		// A database in a Proxmox guest: the credentials are stored after a TCP check, but database
		// features inside a guest are not supported yet.
		var g struct {
			Verified bool `json:"verified"`
		}
		owner.mustJSON("POST", svcPath(pgProd)+"/credentials", map[string]any{"username": "admin", "password": "pw"}, 200, &g)
		if g.Verified {
			t.Fatal("a guest's credentials cannot be verified from the hypervisor")
		}
		owner.expectCode("POST", svcPath(pgProd)+"/databases", map[string]any{"name": "x"}, 409, "unsupported_origin")
	})

	t.Run("destroy needs the typed confirm, then removes the workload and the record", func(t *testing.T) {
		before := operationsOf(t, sqlDB, team.ID)
		owner.expect("DELETE", appPath(blog)+"?destroy=true", map[string]any{"confirm": "not-it"}, 422)
		owner.expect("DELETE", appPath(blog)+"?destroy=true", map[string]any{"confirm": "old-blog-x"}, 422)
		if n := operationsOf(t, sqlDB, team.ID); n != before {
			t.Fatal("a wrong confirm queued an operation")
		}
		var out opBody
		owner.mustJSON("DELETE", appPath(blog)+"?destroy=true", map[string]any{"confirm": "old-blog", "purge": true}, 202, &out)
		op := agent.waitOp(t, out.Operation.ID)
		var p agentproto.DestroyParams
		_ = json.Unmarshal(op.Params, &p)
		if op.Kind != agentproto.KindWorkloadDestroy || p.Name != "old-blog" || !p.Purge || p.Target != "container" {
			t.Fatalf("destroy op: %s %s", op.Kind, op.Params)
		}
		waitGone(t, owner, appPath(blog))
	})

	t.Run("over MCP, destroy goes through the approval gate", func(t *testing.T) {
		owner.mustJSON("PATCH", "/api/teams/"+team.ID, map[string]any{"require_destructive_approval": true}, 200, nil)
		defer owner.mustJSON("PATCH", "/api/teams/"+team.ID, map[string]any{"require_destructive_approval": false}, 200, nil)
		before := operationsOf(t, sqlDB, team.ID)
		args := map[string]any{"database": pgProd.ID, "confirm": "pg-prod"}
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "destroy_data_service", args); !res.IsError || !strings.Contains(res.Text, "needs approval") {
			t.Fatalf("destroy_data_service must be held: %q", res.Text)
		}
		if n := operationsOf(t, sqlDB, team.ID); n != before {
			t.Fatal("a held destroy queued an operation")
		}
		var reqs struct {
			Requests []struct{ ID, Tool string } `json:"requests"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/approvals?status=pending", nil, 200, &reqs)
		if len(reqs.Requests) != 1 || reqs.Requests[0].Tool != "destroy_data_service" {
			t.Fatalf("pending: %+v", reqs)
		}
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/approvals/"+reqs.Requests[0].ID+"/approve", nil, 200, nil)
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "destroy_data_service", args); res.IsError {
			t.Fatalf("approved destroy: %q", res.Text)
		}
		found := false
		for _, o := range agentOps(agent, agentproto.KindWorkloadDestroy) {
			var p agentproto.DestroyParams
			_ = json.Unmarshal(o.Params, &p)
			found = found || p.Target == "guest" && p.VMID == "102" && p.GuestType == "qemu"
		}
		deadline := time.Now().Add(10 * time.Second)
		for !found && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			for _, o := range agentOps(agent, agentproto.KindWorkloadDestroy) {
				var p agentproto.DestroyParams
				_ = json.Unmarshal(o.Params, &p)
				found = found || p.Target == "guest" && p.VMID == "102"
			}
		}
		if !found {
			t.Fatal("the approved destroy never reached the agent")
		}
		waitGone(t, owner, svcPath(pgProd))
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "delete_snapshot", map[string]any{"app": wp.ID, "name": "pre-up", "confirm": "wordpress"}); !res.IsError || !strings.Contains(res.Text, "needs approval") {
			t.Fatalf("delete_snapshot must be held too: %q", res.Text)
		}
	})

	t.Run("audited", func(t *testing.T) {
		var audit struct {
			Entries []struct{ Action string } `json:"entries"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/audit?limit=200", nil, 200, &audit)
		seen := map[string]bool{}
		for _, e := range audit.Entries {
			seen[e.Action] = true
		}
		for _, a := range []string{"workload.power", "workload.resize", "workload.snapshot.create", "workload.snapshot.rollback",
			"workload.snapshot.delete", "workload.backup", "workload.recreate", "workload.destroy", "workload.destroyed", "dataservice.credentials_set"} {
			if !seen[a] {
				t.Errorf("no %s audit entry (have %v)", a, keys(seen))
			}
		}
	})

	t.Run("Hosbah's own workloads and Hosbah servers are refused", func(t *testing.T) {
		kind, key, state := "container", "cloudy-sneaky", "running"
		sneakyID := fmt.Sprintf("00000000-0000-4000-8000-%012d", time.Now().UnixNano()%1000000000000)
		if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{TeamID: team.ID}, func(q *db.Queries, _ *sql.Tx) error {
			_, err := q.InsertImportedApp(ctx, db.InsertImportedAppParams{ID: sneakyID, TeamID: team.ID, ServerID: srv,
				Name: "sneaky", Port: 80, Status: "running", OriginKind: &kind, OriginKey: &key, Origin: `{"ports":[]}`, ImportedState: &state, CreatedBy: "test"})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		before := operationsOf(t, sqlDB, team.ID)
		sneaky := "/api/teams/" + team.ID + "/apps/" + sneakyID
		owner.expectCode("POST", sneaky+"/power", map[string]any{"action": "stop"}, 409, "hosbah_owned")
		owner.expectCode("DELETE", sneaky+"?destroy=true", map[string]any{"confirm": "sneaky"}, 409, "hosbah_owned")
		// wordpress's address becomes a Hosbah server's: it is that server now, not a site.
		enrolWithServicesKey(t, owner, ts.URL, team.ID, "wp-host-"+suffix, "10.0.0.11", nil)
		owner.expectCode("POST", appPath(wp)+"/power", map[string]any{"action": "stop"}, 409, "hosbah_owned")
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "workload_power", map[string]any{"app": wp.ID, "action": "stop"}); !res.IsError || !strings.Contains(res.Text, "Hosbah server") {
			t.Fatalf("MCP power on a Hosbah server: %q", res.Text)
		}
		if n := operationsOf(t, sqlDB, team.ID); n != before {
			t.Fatalf("a refused target queued %d operation(s)", n-before)
		}
	})
}

func agentOps(fa *fakeAgent, kind string) []agentproto.SignedOp {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	var out []agentproto.SignedOp
	for _, o := range fa.ops {
		if o.Kind == kind {
			out = append(out, o)
		}
	}
	return out
}

func waitProxy(t *testing.T, fa *fakeAgent, proxyID string) agentproto.ProxyParams {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, o := range agentOps(fa, agentproto.KindProxySet) {
			var p agentproto.ProxyParams
			_ = json.Unmarshal(o.Params, &p)
			if p.ProxyID == proxyID && !p.Remove {
				return p
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no proxy.set for %s (agent got %v)", proxyID, fa.kinds())
	return agentproto.ProxyParams{}
}

func waitGone(t *testing.T, c *client, path string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if status, _ := c.do("GET", path, nil); status == 404 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s should be gone after the workload was destroyed", path)
}
