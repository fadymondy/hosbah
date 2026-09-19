package server

import (
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

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Imported workloads, end to end: what a connected server already runs becomes imported apps
(sites) and imported data services, idempotently; something that disappears goes missing (never
deleted); something the user deletes stays deleted; importing never queues an agent operation; git-only
actions answer 409 imported_workload and database features 409 credentials_required, also without
queueing one. The controls themselves are in imported_control_test.go.
*/

// enrolWithServices adds a BYO server and enrols it with the given discovered services (the
// server.connect import path: the agent's enrolment facts carry what it found).
func enrolWithServices(t *testing.T, c *client, base, teamID, name string, services []agentproto.Service) string {
	t.Helper()
	id, _ := enrolWithServicesKey(t, c, base, teamID, name, "10.9.8.7", services)
	return id
}

// enrolWithServicesKey is enrolWithServices reporting primaryIP, returning the agent's private key
// (so a fakeAgent can connect as it).
func enrolWithServicesKey(t *testing.T, c *client, base, teamID, name, primaryIP string, services []agentproto.Service) (string, ed25519.PrivateKey) {
	t.Helper()
	var created struct {
		Server  struct{ ID string }
		Install struct{ URL string }
	}
	c.mustJSON("POST", "/api/teams/"+teamID+"/servers", map[string]any{"name": name, "provider": "byo"}, 201, &created)
	token := created.Install.URL[strings.LastIndex(created.Install.URL, "/")+1:]
	pub, priv, _ := ed25519.GenerateKey(nil)
	body, _ := json.Marshal(agentproto.EnrollRequest{Token: token, PublicKey: base64.StdEncoding.EncodeToString(pub), AgentVersion: "test",
		Facts: agentproto.Facts{Hostname: name, OSID: "ubuntu", OSVersion: "24.04", Arch: "amd64", CPUs: 2, MemTotalBytes: 2 << 30,
			Systemd: true, PrimaryIP: primaryIP, Services: services}})
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

type importedApp struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	ServerID      string  `json:"server_id"`
	RepoURL       *string `json:"repo_url"`
	Branch        *string `json:"branch"`
	Status        string  `json:"status"`
	Source        string  `json:"source"`
	OriginKind    *string `json:"origin_kind"`
	ImportedState *string `json:"imported_state"`
	LastSeenAt    *string `json:"last_seen_at"`
	Origin        *struct {
		Image     string   `json:"image"`
		Ports     []string `json:"ports"`
		Address   string   `json:"address"`
		GuestType string   `json:"guest_type"`
		VMID      string   `json:"vmid"`
		CPUs      int      `json:"cpus"`
		MemBytes  uint64   `json:"mem_bytes"`
		Status    string   `json:"status"`
	} `json:"origin"`
}

type importedService struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	ServerID      string  `json:"server_id"`
	Kind          string  `json:"kind"`
	Version       string  `json:"version"`
	Status        string  `json:"status"`
	Host          *string `json:"host"`
	Port          *int64  `json:"port"`
	Container     *string `json:"container"`
	Source        string  `json:"source"`
	OriginKind    *string `json:"origin_kind"`
	ImportedState *string `json:"imported_state"`
}

func listImported(t *testing.T, c *client, teamID, serverID string) (map[string]importedApp, map[string]importedService) {
	t.Helper()
	var apps struct{ Apps []importedApp }
	c.mustJSON("GET", "/api/teams/"+teamID+"/apps", nil, 200, &apps)
	var dss struct{ Services []importedService }
	c.mustJSON("GET", "/api/teams/"+teamID+"/data-services", nil, 200, &dss)
	am, dm := map[string]importedApp{}, map[string]importedService{}
	for _, a := range apps.Apps {
		if a.ServerID == serverID && a.Source == "imported" {
			am[a.Name] = a
		}
	}
	for _, d := range dss.Services {
		if d.ServerID == serverID && d.Source == "imported" {
			dm[d.Name] = d
		}
	}
	return am, dm
}

// operationsOf counts the team's operation rows (team scope, like every tenant read).
func operationsOf(t *testing.T, sqlDB *sql.DB, teamID string) int {
	t.Helper()
	n := 0
	err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{TeamID: teamID}, func(_ *db.Queries, tx *sql.Tx) error {
		return tx.QueryRow(`SELECT count(*) FROM operations WHERE team_id = $1`, teamID).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func sampleServices() []agentproto.Service {
	return []agentproto.Service{
		// Sites.
		{Kind: "container", Name: "shop-web", Image: "nginx:1.27", Status: "running", Ports: []string{"0.0.0.0:8081->80/tcp"}},
		{Kind: "guest", Name: "wordpress", ID: "101", Type: "lxc", Status: "running", Address: "10.0.0.11", CPUs: 2, MemBytes: 2 << 30, DiskBytes: 8 << 30},
		{Kind: "guest", Name: "My Site!!", ID: "103", Type: "qemu", Status: "stopped"},
		// Databases.
		{Kind: "container", Name: "pg-main", Image: "postgres:16-alpine", Status: "running", Ports: []string{"127.0.0.1:5433->5432/tcp"}},
		{Kind: "container", Name: "cache", Image: "redis", Status: "exited"},
		{Kind: "container", Name: "docs-mongo", Image: "mongo:7", Status: "running", Ports: []string{"0.0.0.0:27017->27017/tcp"}},
		{Kind: "guest", Name: "pg-prod", ID: "102", Type: "qemu", Status: "running", Address: "10.0.0.12"},
		// Hosbah's own, and base-system units: never imported.
		{Kind: "container", Name: "cloudy-db-managed", Image: "postgres:16", Status: "running"},
		{Kind: "container", Name: "cloudy-shop-web-0123abcd", Image: "cloudy/shop:v1", Status: "running"},
		{Kind: "container", Name: "hosbah-helper", Image: "busybox", Status: "running"},
		{Kind: "systemd", Name: "cloudy-caddy", Status: "active", Ports: []string{"80"}},
		{Kind: "systemd", Name: "sshd", Status: "active", Ports: []string{"22"}},
		{Kind: "systemd", Name: "nginx", Status: "active"},
	}
}

func TestImportedWorkloads(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner := newClient(t, ts.URL)
	owner.register("import-owner-" + suffix + "@example.test")
	var team struct{ ID, Slug string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Import " + suffix}, 201, &team)
	var tok struct{ ID, Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{
		"name": "import-all", "scopes": []string{"read", "deploy", "write", "secrets:read", "destructive"}, "expires_in_days": 30,
	}, 201, &tok)

	srv := enrolWithServices(t, owner, ts.URL, team.ID, "docker-"+suffix, sampleServices())

	t.Run("a report creates the right apps and data services", func(t *testing.T) {
		apps, dss := listImported(t, owner, team.ID, srv)
		if len(apps) != 3 || len(dss) != 4 {
			t.Fatalf("want 3 imported apps and 4 imported data services, got %d apps %v and %d services %v", len(apps), keys(apps), len(dss), keys(dss))
		}
		web := apps["shop-web"]
		if web.RepoURL != nil || web.Branch != nil || deref(web.OriginKind) != "container" || deref(web.ImportedState) != "running" ||
			web.Status != "running" || web.LastSeenAt == nil || web.Origin == nil || web.Origin.Image != "nginx:1.27" ||
			len(web.Origin.Ports) != 1 {
			t.Fatalf("shop-web: %+v", web)
		}
		wp := apps["wordpress"]
		if deref(wp.OriginKind) != "guest" || wp.Origin == nil || wp.Origin.VMID != "101" || wp.Origin.GuestType != "lxc" ||
			wp.Origin.Address != "10.0.0.11" || wp.Origin.CPUs != 2 {
			t.Fatalf("wordpress: %+v %+v", wp, wp.Origin)
		}
		if s := apps["my-site"]; deref(s.ImportedState) != "stopped" || s.Status != "stopped" {
			t.Fatalf("my-site (a stopped guest, name made valid): %+v", s)
		}
		pg := dss["pg-main"]
		if pg.Kind != "postgres" || pg.Version != "16-alpine" || deref(pg.Host) != "127.0.0.1" || pg.Port == nil || *pg.Port != 5433 ||
			deref(pg.Container) != "pg-main" || deref(pg.OriginKind) != "container" {
			t.Fatalf("pg-main: %+v", pg)
		}
		if c := dss["cache"]; c.Kind != "redis" || c.Version != "latest" || deref(c.ImportedState) != "stopped" || c.Host != nil {
			t.Fatalf("cache: %+v", c)
		}
		if m := dss["docs-mongo"]; m.Kind != "mongodb" || m.Version != "7" || deref(m.Host) != "10.9.8.7" || m.Port == nil || *m.Port != 27017 {
			t.Fatalf("docs-mongo (bound to every interface: the server's address): %+v", m)
		}
		if g := dss["pg-prod"]; g.Kind != "postgres" || deref(g.Host) != "10.0.0.12" || deref(g.OriginKind) != "guest" {
			t.Fatalf("pg-prod: %+v", g)
		}
		for name := range apps {
			if strings.HasPrefix(name, "cloudy") || strings.HasPrefix(name, "hosbah") || name == "sshd" || name == "nginx" {
				t.Fatalf("Hosbah's own or a base workload was imported: %s", name)
			}
		}
		for name := range dss {
			if strings.HasPrefix(name, "cloudy") {
				t.Fatalf("Hosbah's managed database was imported: %s", name)
			}
		}
		if n := operationsOf(t, sqlDB, team.ID); n != 0 {
			t.Fatalf("importing must not queue any operation, found %d", n)
		}
	})

	t.Run("a second report is idempotent", func(t *testing.T) {
		before, beforeDS := listImported(t, owner, team.ID, srv)
		res, err := importer.Sync(ctx, sqlDB, team.ID, srv, sampleServices())
		if err != nil {
			t.Fatal(err)
		}
		if res.Created != 0 || res.Updated != 7 {
			t.Fatalf("second sync: %+v", res)
		}
		after, afterDS := listImported(t, owner, team.ID, srv)
		for name, a := range before {
			if after[name].ID != a.ID {
				t.Fatalf("%s changed id on a second report", name)
			}
		}
		if len(after) != len(before) || len(afterDS) != len(beforeDS) {
			t.Fatalf("a second report changed the counts: %d/%d -> %d/%d", len(before), len(beforeDS), len(after), len(afterDS))
		}
	})

	t.Run("a same-named workload on another server gets a numeric suffix", func(t *testing.T) {
		other := enrolWithServices(t, owner, ts.URL, team.ID, "docker2-"+suffix, []agentproto.Service{
			{Kind: "container", Name: "shop-web", Image: "nginx", Status: "running"}})
		apps, _ := listImported(t, owner, team.ID, other)
		if _, ok := apps["shop-web-2"]; !ok || len(apps) != 1 {
			t.Fatalf("want shop-web-2 on the second server, got %v", keys(apps))
		}
	})

	t.Run("a workload that disappears goes missing, not away", func(t *testing.T) {
		var rest []agentproto.Service
		for _, s := range sampleServices() {
			if s.Name != "cache" && s.Name != "wordpress" {
				rest = append(rest, s)
			}
		}
		raw, _ := json.Marshal(agentproto.Facts{Services: rest})
		res, err := importer.SyncFacts(ctx, sqlDB, team.ID, srv, raw)
		if err != nil {
			t.Fatal(err)
		}
		if res.Missing != 2 {
			t.Fatalf("want 2 newly missing, got %+v", res)
		}
		apps, dss := listImported(t, owner, team.ID, srv)
		if deref(apps["wordpress"].ImportedState) != "missing" || deref(dss["cache"].ImportedState) != "missing" {
			t.Fatalf("wordpress %+v, cache %+v", apps["wordpress"], dss["cache"])
		}
		// A report that says nothing about services (an older agent) changes nothing.
		if res, err := importer.SyncFacts(ctx, sqlDB, team.ID, srv, []byte(`{"hostname":"x"}`)); err != nil || res != (importer.Result{}) {
			t.Fatalf("a report without services: %+v %v", res, err)
		}
		// Back again: running.
		if _, err := importer.Sync(ctx, sqlDB, team.ID, srv, sampleServices()); err != nil {
			t.Fatal(err)
		}
		apps, _ = listImported(t, owner, team.ID, srv)
		if deref(apps["wordpress"].ImportedState) != "running" {
			t.Fatalf("wordpress should be back: %+v", apps["wordpress"])
		}
	})

	apps, dss := listImported(t, owner, team.ID, srv)
	web, pg := apps["shop-web"], dss["pg-main"]
	appPath := "/api/teams/" + team.ID + "/apps/" + web.ID
	svcPath := "/api/teams/" + team.ID + "/data-services/" + pg.ID

	t.Run("git-only actions are 409 imported_workload and database features need credentials, with no operation", func(t *testing.T) {
		opsBefore := operationsOf(t, sqlDB, team.ID)
		for _, c := range []struct {
			method, path string
			body         any
		}{
			{"POST", appPath + "/deployments", map[string]any{}},
			{"POST", appPath + "/rollback", map[string]any{}},
			{"POST", appPath + "/restart", nil},
			{"PATCH", appPath, map[string]any{"port": 9000}},
			{"PATCH", appPath, map[string]any{"autodeploy_paused": true}},
			{"POST", appPath + "/deploy-hook", nil},
			{"PUT", appPath + "/env/FOO", map[string]any{"value": "bar"}},
			{"DELETE", appPath + "/env/FOO", nil},
			{"POST", appPath + "/deployments/1/cancel", nil},
		} {
			status, text := owner.do(c.method, c.path, c.body)
			var e struct{ Code, Message string }
			_ = json.Unmarshal([]byte(text), &e)
			if status != 409 || e.Code != "imported_workload" || !strings.Contains(e.Message, "is imported; deploy from git applies to Hosbah-built apps") {
				t.Errorf("%s %s -> %d %s (want 409 imported_workload)", c.method, c.path, status, text)
			}
		}
		for _, c := range []struct {
			method, path string
			body         any
		}{
			{"POST", svcPath + "/databases", map[string]any{"name": "app"}},
			{"POST", svcPath + "/reveal-url", map[string]any{}},
			{"POST", svcPath + "/backups", nil},
			{"POST", svcPath + "/backups/00000000-0000-4000-8000-0000000000b1/restore-test", nil},
			{"PUT", svcPath + "/schedule", map[string]any{"cron": "0 3 * * *"}},
		} {
			status, text := owner.do(c.method, c.path, c.body)
			var e struct{ Code, Message string }
			_ = json.Unmarshal([]byte(text), &e)
			if status != 409 || e.Code != "credentials_required" {
				t.Errorf("%s %s -> %d %s (want 409 credentials_required)", c.method, c.path, status, text)
			}
		}
		for tool, args := range map[string]map[string]any{
			"deploy_app": {"app": web.ID}, "restart_app": {"app": web.ID}, "rollback_app": {"app": web.ID},
			"pause_autodeploy": {"app": web.ID}, "set_env": {"app": web.ID, "name": "X", "value": "y"},
			"unset_env": {"app": web.ID, "name": "X"}, "update_app": {"app": web.ID, "port": 9000}, "rotate_deploy_hook": {"app": web.ID},
		} {
			res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, tool, args)
			if !res.IsError || !strings.Contains(res.Text, "is imported; deploy from git applies to Hosbah-built apps") {
				t.Errorf("MCP %s on an imported workload: isError=%v %q", tool, res.IsError, res.Text)
			}
		}
		for tool, args := range map[string]map[string]any{
			"backup_now": {"database": pg.ID}, "create_db": {"database": pg.ID, "name": "x"}, "reveal_db_url": {"database": pg.ID},
			"set_backup_schedule": {"database": pg.ID, "cron": "0 3 * * *"}, "drop_database": {"database": pg.ID, "name": "x", "confirm": "x"},
		} {
			res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, tool, args)
			if !res.IsError || !strings.Contains(res.Text, "give Hosbah its credentials first") {
				t.Errorf("MCP %s on an imported database without credentials: isError=%v %q", tool, res.IsError, res.Text)
			}
		}
		if n := operationsOf(t, sqlDB, team.ID); n != opsBefore {
			t.Fatalf("a refused mutation queued %d operation(s)", n-opsBefore)
		}
	})

	t.Run("reads show imported records, marked", func(t *testing.T) {
		var detail struct {
			App struct {
				Source string  `json:"source"`
				Repo   *string `json:"repo_url"`
			} `json:"app"`
		}
		owner.mustJSON("GET", appPath, nil, 200, &detail)
		if detail.App.Source != "imported" || detail.App.Repo != nil {
			t.Fatalf("get-app: %+v", detail)
		}
		var sd struct {
			Service struct{ Source, Kind string } `json:"service"`
		}
		owner.mustJSON("GET", svcPath, nil, 200, &sd)
		if sd.Service.Source != "imported" || sd.Service.Kind != "postgres" {
			t.Fatalf("get-data-service: %+v", sd)
		}
		sites := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "list_sites", map[string]any{"server": srv})
		if sites.IsError || !strings.Contains(sites.Text, "shop-web") || !strings.Contains(sites.Text, "lxc 101") ||
			!strings.Contains(sites.Text, "imported database (postgres 16-alpine)") || strings.Contains(sites.Text, "cloudy-db-managed") {
			t.Fatalf("list_sites: %q", sites.Text)
		}
		for tool, want := range map[string]string{"list_apps": "imported container", "list_databases": "imported (running)"} {
			if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, tool, nil); res.IsError || !strings.Contains(res.Text, want) {
				t.Errorf("%s should mark imported records (%q): %q", tool, want, res.Text)
			}
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "get_app", map[string]any{"app": web.ID}); !strings.Contains(res.Text, "imported:") {
			t.Errorf("get_app: %q", res.Text)
		}
		// A git app still needs its repository.
		if status, _ := owner.do("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "no-repo", "server": srv}); status != 422 {
			t.Errorf("creating a git app without repo_url -> %d (want 422)", status)
		}
	})

	t.Run("deleting an imported record forgets it, queues nothing, and it stays deleted", func(t *testing.T) {
		opsBefore := operationsOf(t, sqlDB, team.ID)
		owner.mustJSON("DELETE", appPath, map[string]any{"confirm": web.Name}, 204, nil)
		owner.mustJSON("DELETE", svcPath, nil, 204, nil)
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "delete_app", map[string]any{"app": apps["wordpress"].ID, "confirm": "wordpress"}); res.IsError {
			t.Fatalf("delete_app (MCP) of an imported app: %q", res.Text)
		}
		if n := operationsOf(t, sqlDB, team.ID); n != opsBefore {
			t.Fatalf("deleting an imported record queued %d operation(s)", n-opsBefore)
		}
		var audit struct {
			Entries []struct{ Action string } `json:"entries"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/audit?limit=100", nil, 200, &audit)
		seen := map[string]bool{}
		for _, e := range audit.Entries {
			seen[e.Action] = true
		}
		if !seen["app.import_forgotten"] || !seen["dataservice.import_forgotten"] {
			t.Fatalf("the forget must be audited, got %v", seen)
		}
		// The next reports must not resurrect them.
		for i := 0; i < 2; i++ {
			if _, err := importer.Sync(ctx, sqlDB, team.ID, srv, sampleServices()); err != nil {
				t.Fatal(err)
			}
		}
		apps2, dss2 := listImported(t, owner, team.ID, srv)
		if _, ok := apps2["shop-web"]; ok {
			t.Fatal("a deleted imported app came back")
		}
		if _, ok := apps2["wordpress"]; ok {
			t.Fatal("a deleted imported app (via MCP) came back")
		}
		if _, ok := dss2["pg-main"]; ok {
			t.Fatal("a deleted imported data service came back")
		}
		if len(apps2) != 1 || len(dss2) != 3 {
			t.Fatalf("the others must stay: %v %v", keys(apps2), keys(dss2))
		}
	})

	t.Run("boot backfill imports servers that already list services", func(t *testing.T) {
		var created struct{ Server struct{ ID string } }
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/servers", map[string]any{"name": "pve-" + suffix, "provider": "byo"}, 201, &created)
		facts, _ := json.Marshal(agentproto.Facts{Hostname: "pve", Services: []agentproto.Service{
			{Kind: "guest", Name: "blog", ID: "200", Type: "lxc", Status: "running"},
			{Kind: "guest", Name: "mysql-main", ID: "201", Type: "lxc", Status: "running", Address: "10.1.1.5"}}})
		if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
			return q.RefreshServerFacts(ctx, db.RefreshServerFactsParams{Facts: string(facts), ID: created.Server.ID})
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := importer.Backfill(ctx, sqlDB, nil); err != nil {
			t.Fatal(err)
		}
		apps, dss := listImported(t, owner, team.ID, created.Server.ID)
		if _, ok := apps["blog"]; !ok || dss["mysql-main"].Kind != "mysql" || deref(dss["mysql-main"].Host) != "10.1.1.5" {
			t.Fatalf("backfill: %v %+v", keys(apps), dss)
		}
		// Idempotent across boots.
		if _, err := importer.Backfill(ctx, sqlDB, nil); err != nil {
			t.Fatal(err)
		}
		if apps2, dss2 := listImported(t, owner, team.ID, created.Server.ID); len(apps2) != 1 || len(dss2) != 1 {
			t.Fatalf("a second backfill duplicated records: %v %v", keys(apps2), keys(dss2))
		}
	})

	t.Run("another team sees none of it", func(t *testing.T) {
		other := newClient(t, ts.URL)
		other.register("import-other-" + suffix + "@example.test")
		var teamB struct{ ID string }
		other.mustJSON("POST", "/api/teams", map[string]any{"name": "Other " + suffix}, 201, &teamB)
		for _, p := range []string{
			"/api/teams/" + team.ID + "/apps/" + apps["my-site"].ID,
			"/api/teams/" + team.ID + "/data-services/" + dss["cache"].ID,
			"/api/teams/" + teamB.ID + "/apps/" + apps["my-site"].ID,
			"/api/teams/" + teamB.ID + "/data-services/" + dss["cache"].ID,
		} {
			if status, text := other.do("GET", p, nil); status != 404 {
				t.Errorf("GET %s as another team -> %d: %s", p, status, text)
			}
		}
		if status, _ := other.do("DELETE", "/api/teams/"+teamB.ID+"/data-services/"+dss["cache"].ID, nil); status != 404 {
			t.Errorf("another team deleting an imported service -> %d", status)
		}
		var list struct{ Apps []importedApp }
		other.mustJSON("GET", "/api/teams/"+teamB.ID+"/apps", nil, 200, &list)
		if len(list.Apps) != 0 {
			t.Errorf("another team lists apps: %+v", list.Apps)
		}
		// Syncing a server into the wrong team does nothing (the server is not that team's).
		if res, err := importer.Sync(ctx, sqlDB, teamB.ID, srv, sampleServices()); err != nil || res != (importer.Result{}) {
			t.Errorf("a sync naming another team's server must do nothing: %+v %v", res, err)
		}
		other.mustJSON("GET", "/api/teams/"+teamB.ID+"/apps", nil, 200, &list)
		if len(list.Apps) != 0 {
			t.Errorf("a cross-team sync created apps: %+v", list.Apps)
		}
	})

	t.Run("removing the server forgets its imported records", func(t *testing.T) {
		owner.mustJSON("DELETE", "/api/teams/"+team.ID+"/servers/"+srv, nil, 204, nil)
		apps, dss := listImported(t, owner, team.ID, srv)
		if len(apps) != 0 || len(dss) != 0 {
			t.Fatalf("imported records outlived their server: %v %v", keys(apps), keys(dss))
		}
	})
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
