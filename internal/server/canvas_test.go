package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

type canvasJSON struct {
	Layout struct {
		Nodes    map[string]struct{ X, Y float64 } `json:"nodes"`
		Viewport *struct{ X, Y, Zoom float64 }     `json:"viewport"`
	} `json:"layout"`
	UpdatedAt   *string `json:"updated_at"`
	CanSave     bool    `json:"can_save"`
	Connections []struct {
		ServiceID string `json:"service_id"`
		Variable  string `json:"variable"`
	} `json:"connections"`
}

/*
A site's overview canvas: any member reads it, only owner/admin save it (a viewer's drags stay on
their screen), its shape and size are checked, another team gets 404 and sees no row, it goes with
its app — and its connections are the data services the app's connection variables really point at,
compared by host and port, never by variable name alone and never echoing a value.
*/
func TestAppCanvas(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner, stranger, viewer := newClient(t, ts.URL), newClient(t, ts.URL), newClient(t, ts.URL)
	owner.register("cv-owner-" + suffix + "@example.test")
	stranger.register("cv-stranger-" + suffix + "@example.test")
	viewerEmail := "cv-viewer-" + suffix + "@example.test"
	viewer.register(viewerEmail)
	var team, teamB struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Canvas " + suffix}, 201, &team)
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "CanvasB " + suffix}, 201, &teamB)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	srv, _ := enrolServer(t, owner, ts.URL, team.ID, "cv-"+short)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "cv-app-" + short, "server": srv,
		"repo_url": "https://github.com/example/app"}, 201, &app)
	base := "/api/teams/" + team.ID + "/apps/" + app.App.ID + "/canvas"
	byName := "/api/teams/" + team.ID + "/apps/" + app.App.Name + "/canvas"

	layout := map[string]any{
		"nodes":    map[string]any{"app": map[string]any{"x": 420, "y": 80}, "domain:shop.example.test": map[string]any{"x": 160.5, "y": -40}},
		"viewport": map[string]any{"x": -12, "y": 30, "zoom": 0.9},
	}

	t.Run("read and save", func(t *testing.T) {
		var c canvasJSON
		owner.mustJSON("GET", base, nil, 200, &c)
		if c.UpdatedAt != nil || len(c.Layout.Nodes) != 0 || c.Layout.Viewport != nil || !c.CanSave || c.Connections == nil {
			t.Fatalf("a new app's canvas: %+v", c)
		}
		viewer.mustJSON("GET", byName, nil, 200, &c)
		if c.CanSave {
			t.Error("a viewer may not save")
		}
		owner.mustJSON("PUT", byName, map[string]any{"layout": layout}, 200, nil)
		owner.mustJSON("GET", base, nil, 200, &c)
		if c.UpdatedAt == nil || len(c.Layout.Nodes) != 2 || c.Layout.Nodes["app"].X != 420 || c.Layout.Nodes["domain:shop.example.test"].X != 160.5 ||
			c.Layout.Viewport == nil || c.Layout.Viewport.Zoom != 0.9 {
			t.Fatalf("saved: %+v", c)
		}
		// Replaced as a whole; no viewport is fine (fit to view).
		owner.mustJSON("PUT", base, map[string]any{"layout": map[string]any{"nodes": map[string]any{"app": map[string]any{"x": 1, "y": 2}}}}, 200, nil)
		var r canvasJSON
		owner.mustJSON("GET", base, nil, 200, &r)
		if len(r.Layout.Nodes) != 1 || r.Layout.Viewport != nil {
			t.Fatalf("replaced: %+v", r)
		}
		owner.mustJSON("PUT", base, map[string]any{"layout": layout}, 200, nil)
	})

	t.Run("roles and other teams", func(t *testing.T) {
		if code, text := viewer.do("PUT", base, map[string]any{"layout": layout}); code != 403 {
			t.Errorf("viewer PUT -> %d (want 403): %s", code, text)
		}
		for _, m := range []string{"GET", "PUT"} {
			if code, text := stranger.do(m, base, map[string]any{"layout": layout}); code != 404 {
				t.Errorf("another team's %s -> %d (want 404): %s", m, code, text)
			}
		}
		// Team B's own team with team A's app: still 404.
		if code, _ := stranger.do("GET", "/api/teams/"+teamB.ID+"/apps/"+app.App.ID+"/canvas", nil); code != 404 {
			t.Errorf("team B's URL with team A's app -> %d (want 404)", code)
		}
		if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{TeamID: teamB.ID}, func(_ *db.Queries, tx *sql.Tx) error {
			var n int
			if err := tx.QueryRow(`SELECT count(*) FROM app_canvas`).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Errorf("team B's scope sees %d canvas rows", n)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		// Without any context, row security returns nothing either.
		if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			var n int
			if err := tx.QueryRow(`SELECT count(*) FROM app_canvas`).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Errorf("system scope sees %d canvas rows (no policy grants it)", n)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("shape and size", func(t *testing.T) {
		many := map[string]any{}
		for i := 0; i < 501; i++ {
			many[fmt.Sprintf("worker:w%d", i)] = map[string]any{"x": i, "y": i}
		}
		bad := map[string]map[string]any{
			"zoom too far":   {"nodes": map[string]any{}, "viewport": map[string]any{"x": 0, "y": 0, "zoom": 10}},
			"zoom zero":      {"nodes": map[string]any{}, "viewport": map[string]any{"x": 0, "y": 0, "zoom": 0}},
			"far away":       {"nodes": map[string]any{"app": map[string]any{"x": 2e6, "y": 0}}},
			"an empty key":   {"nodes": map[string]any{"": map[string]any{"x": 0, "y": 0}}},
			"a control char": {"nodes": map[string]any{"app\n": map[string]any{"x": 0, "y": 0}}},
			"a long key":     {"nodes": map[string]any{strings.Repeat("k", 301): map[string]any{"x": 0, "y": 0}}},
			"501 boxes":      {"nodes": many},
			"not a point":    {"nodes": map[string]any{"app": "here"}},
		}
		for name, l := range bad {
			if code, text := owner.do("PUT", base, map[string]any{"layout": l}); code != 422 {
				t.Errorf("%s -> %d (want 422): %s", name, code, text)
			}
		}
		huge := map[string]any{}
		for i := 0; i < 400; i++ {
			huge[fmt.Sprintf("domain:%s-%03d.example.test", strings.Repeat("h", 150), i)] = map[string]any{"x": 1, "y": 1}
		}
		if code, text := owner.do("PUT", base, map[string]any{"layout": map[string]any{"nodes": huge}}); code != 413 && code != 422 {
			t.Errorf("more than 64 KiB -> %d (want 413 or 422): %.200s", code, text)
		}
		var c canvasJSON
		owner.mustJSON("GET", base, nil, 200, &c)
		if len(c.Layout.Nodes) != 2 || c.Layout.Viewport == nil || c.Layout.Viewport.Zoom != 0.9 {
			t.Errorf("a refused save changed the canvas: %+v", c)
		}
	})

	t.Run("connections", func(t *testing.T) {
		// Two Postgres services and a Redis on the app's server (records only: nothing runs).
		ids := map[string]string{}
		if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
			for _, s := range []struct {
				name, kind string
				port       int
			}{{"cv-pg1-" + short, "postgres", 5433}, {"cv-pg2-" + short, "postgres", 5434}, {"cv-rd-" + short, "redis", 6380}} {
				var id string
				if err := tx.QueryRow(`INSERT INTO data_services (team_id, server_id, name, kind, version, status, host, port, root_sealed, created_by)
					VALUES ($1, $2, $3, $4, '16', 'running', '10.9.8.7', $5, 'csv1:test', (SELECT created_by FROM apps WHERE id = $6)) RETURNING id`,
					team.ID, srv, s.name, s.kind, s.port, app.App.ID).Scan(&id); err != nil {
					return err
				}
				ids[s.kind+fmt.Sprint(s.port)] = id
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var c, none canvasJSON
		owner.mustJSON("GET", base, nil, 200, &none)
		if len(none.Connections) != 0 {
			t.Fatalf("no variable, no connection: %+v", none.Connections)
		}
		// DATABASE_URL points at the second Postgres only (the name alone would match both).
		owner.mustJSON("PUT", "/api/teams/"+team.ID+"/apps/"+app.App.ID+"/env/DATABASE_URL", map[string]any{"value": "postgres://u:cv-secret@10.9.8.7:5434/shop"}, 204, nil)
		owner.mustJSON("PUT", "/api/teams/"+team.ID+"/apps/"+app.App.ID+"/env/REDIS_URL", map[string]any{"value": "redis://10.9.8.7:6380"}, 204, nil)
		owner.mustJSON("PUT", "/api/teams/"+team.ID+"/apps/"+app.App.ID+"/env/S3_URL", map[string]any{"value": "s3://k:s@10.1.1.1:9000/b"}, 204, nil) // points nowhere known
		code, text := viewer.do("GET", base, nil)
		if code != 200 || strings.Contains(text, "cv-secret") || strings.Contains(text, "postgres://") {
			t.Fatalf("viewer GET -> %d, and the answer must not carry a value: %s", code, text)
		}
		viewer.mustJSON("GET", base, nil, 200, &c)
		got := map[string]string{}
		for _, l := range c.Connections {
			got[l.ServiceID] = l.Variable
		}
		if len(got) != 2 || got[ids["postgres5434"]] != "DATABASE_URL" || got[ids["redis6380"]] != "REDIS_URL" {
			t.Fatalf("connections: %+v (services %v)", c.Connections, ids)
		}
		// Unsetting the variable (the canvas's "disconnect") drops the edge.
		owner.mustJSON("DELETE", "/api/teams/"+team.ID+"/apps/"+app.App.ID+"/env/DATABASE_URL", nil, 204, nil)
		var after canvasJSON
		owner.mustJSON("GET", base, nil, 200, &after)
		if len(after.Connections) != 1 || after.Connections[0].Variable != "REDIS_URL" {
			t.Errorf("after unset: %+v", after.Connections)
		}
	})

	t.Run("goes with its app", func(t *testing.T) {
		count := func() (n int) {
			if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
				return tx.QueryRow(`SELECT count(*) FROM app_canvas WHERE app_id = $1`, app.App.ID).Scan(&n)
			}); err != nil {
				t.Fatal(err)
			}
			return
		}
		if count() != 1 {
			t.Fatal("the canvas row is missing")
		}
		owner.mustJSON("DELETE", "/api/teams/"+team.ID+"/apps/"+app.App.ID, map[string]any{"confirm": app.App.Name}, 204, nil)
		if count() != 0 {
			t.Error("the canvas outlived its app")
		}
	})
}
