package server

import (
	"bytes"
	"context"
	"database/sql"
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

// An app's appearance — title, description, color, icon and an uploaded logo — works like a
// server's, for git and imported apps alike, and never touches the app's name.
func TestAppAppearance(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ua, ub, uv := newClient(t, ts.URL), newClient(t, ts.URL), newClient(t, ts.URL)
	ua.register("look-a-" + suffix + "@example.test")
	ub.register("look-b-" + suffix + "@example.test")
	viewerEmail := "look-v-" + suffix + "@example.test"
	uv.register(viewerEmail)
	var teamA, teamB struct{ ID string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "LookA " + suffix}, 201, &teamA)
	ub.mustJSON("POST", "/api/teams", map[string]any{"name": "LookB " + suffix}, 201, &teamB)
	srv, _ := enrolServer(t, ua, ts.URL, teamA.ID, "look-"+short)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	uv.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	appName := "look-app-" + short
	var created struct {
		App map[string]any `json:"app"`
	}
	ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/apps", map[string]any{"name": appName, "server": srv,
		"repo_url": "https://github.com/example/app", "deploy": true}, 201, &created)
	appID, _ := created.App["id"].(string)
	for _, k := range []string{"title", "description", "color", "icon", "logo_url"} {
		if v, ok := created.App[k]; !ok || v != nil {
			t.Errorf("a new app's %s: %v (present %v; want null)", k, v, ok)
		}
	}
	if _, err := importer.Sync(context.Background(), sqlDB, teamA.ID, srv, []agentproto.Service{
		{Kind: "container", Name: "look-imp", Image: "nginx", Status: "running"}}); err != nil {
		t.Fatalf("import: %v", err)
	}
	base := "/api/teams/" + teamA.ID + "/apps/" + appID
	byName := "/api/teams/" + teamA.ID + "/apps/" + appName
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 200)...)
	jpeg := append([]byte("\xff\xd8\xff\xe0"), bytes.Repeat([]byte{9}, 100)...)

	t.Run("patch", func(t *testing.T) {
		for _, bad := range []map[string]any{{"color": "#12345"}, {"color": "red"}, {"icon": "unicorn"},
			{"title": strings.Repeat("t", 81)}, {"title": "two\nlines"}, {"description": strings.Repeat("d", 281)},
			{"title": "fine", "color": "nope"}} {
			if code, text := ua.do("PATCH", base, bad); code != 422 {
				t.Errorf("PATCH %v -> %d (want 422): %s", bad, code, text)
			}
		}
		var out map[string]any
		ua.mustJSON("GET", base, nil, 200, &out)
		if app := out["app"].(map[string]any); app["title"] != nil || app["color"] != nil {
			t.Fatalf("refused patches changed the app: %+v", app)
		}
		// 80 characters, not bytes; trimmed; the color is stored uppercase.
		title := strings.Repeat("ع", 80)
		ua.mustJSON("PATCH", byName, map[string]any{"title": "  " + title + " ", "description": " The shop. ", "color": "#3b82f6", "icon": "rocket"}, 200, &out)
		if out["title"] != title || out["description"] != "The shop." || out["color"] != "#3B82F6" || out["icon"] != "rocket" || out["name"] != appName {
			t.Fatalf("patched: %+v", out)
		}
		// The name is not editable here: an unknown field is refused and nothing changes.
		if code, text := ua.do("PATCH", base, map[string]any{"name": "renamed", "title": "x"}); code != 422 {
			t.Errorf("PATCH name -> %d (want 422): %s", code, text)
		}
		// The settings still work, alone and together with the look.
		ua.mustJSON("PATCH", base, map[string]any{"port": 9000}, 200, &out)
		if out["port"] != float64(9000) || out["title"] != title {
			t.Fatalf("settings patch: %+v", out)
		}
		ua.mustJSON("PATCH", base, map[string]any{"port": 9001, "title": "Shop", "color": ""}, 200, &out)
		if out["port"] != float64(9001) || out["title"] != "Shop" || out["color"] != nil || out["icon"] != "rocket" {
			t.Fatalf("combined patch: %+v", out)
		}
		// One transaction: a bad look leaves the settings alone.
		if code, _ := ua.do("PATCH", base, map[string]any{"port": 9002, "icon": "unicorn"}); code != 422 {
			t.Errorf("bad combined patch -> %d (want 422)", code)
		}
		ua.mustJSON("GET", base, nil, 200, &out)
		if app := out["app"].(map[string]any); app["port"] != float64(9001) || app["name"] != appName || app["title"] != "Shop" {
			t.Fatalf("after a refused patch: %+v", app)
		}
		ua.mustJSON("PATCH", base, map[string]any{"title": "", "description": ""}, 200, &out)
		if out["title"] != nil || out["description"] != nil || out["icon"] != "rocket" {
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
			if e.Action == "app.appearance_updated" && strings.Contains(string(e.Changes), `"old"`) && strings.Contains(string(e.Changes), "#3B82F6") {
				found = true
			}
		}
		if !found {
			t.Errorf("no app.appearance_updated audit entry with old/new values: %+v", audit.Entries)
		}
		if code, _ := uv.do("PATCH", base, map[string]any{"color": "#000000"}); code != 403 {
			t.Errorf("viewer PATCH -> %d (want 403)", code)
		}
		if code, _ := ub.do("PATCH", base, map[string]any{"color": "#000000"}); code != 404 {
			t.Errorf("team B PATCH -> %d (want 404)", code)
		}
		if code, _ := ub.do("PATCH", "/api/teams/"+teamB.ID+"/apps/"+appID, map[string]any{"color": "#000000"}); code != 404 {
			t.Errorf("team B PATCH through its own team -> %d (want 404)", code)
		}

		var tok struct{ Token string }
		ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/mcp-tokens", map[string]any{"name": "look", "scopes": []string{"read", "write"}, "expires_in_days": 1}, 201, &tok)
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "update_app", map[string]any{"app": appID, "icon": "nope"}); !res.IsError {
			t.Errorf("MCP update_app with a bad icon: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "update_app", map[string]any{"app": appName, "icon": "leaf", "color": "#00ff00", "title": "Via MCP"}); res.IsError ||
			!strings.Contains(res.Text, "leaf") || !strings.Contains(res.Text, "#00FF00") || !strings.Contains(res.Text, "Via MCP") {
			t.Errorf("MCP update_app: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "update_app", map[string]any{"app": "look-imp", "title": "Imported, styled"}); res.IsError {
			t.Errorf("MCP update_app (appearance) on an imported app: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "update_app", map[string]any{"app": "look-imp", "title": "x", "port": 9000}); !res.IsError {
			t.Errorf("MCP update_app (settings) on an imported app should be refused: %+v", res)
		}
		var ro struct{ Token string }
		ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/mcp-tokens", map[string]any{"name": "look-ro", "scopes": []string{"read"}, "expires_in_days": 1}, 201, &ro)
		if res := mcpCall(t, ts.URL, "/api/mcp", ro.Token, "update_app", map[string]any{"app": appID, "color": "#111111"}); !res.IsError {
			t.Errorf("MCP update_app with a read-only token: %+v", res)
		}
	})

	t.Run("imported app", func(t *testing.T) {
		imp := "/api/teams/" + teamA.ID + "/apps/look-imp"
		var out map[string]any
		ua.mustJSON("PATCH", imp, map[string]any{"title": "Old site", "icon": "globe", "color": "#abcdef"}, 200, &out)
		if out["title"] != "Old site" || out["icon"] != "globe" || out["color"] != "#ABCDEF" || out["source"] != "imported" {
			t.Fatalf("imported patched: %+v", out)
		}
		// Its git settings stay refused, and a mixed patch changes nothing.
		if code, text := ua.do("PATCH", imp, map[string]any{"port": 9000, "title": "nope"}); code != 409 || !strings.Contains(text, "imported_workload") {
			t.Errorf("imported settings PATCH -> %d: %s", code, text)
		}
		if resp, b := ua.raw("PUT", imp+"/logo", "image/png", png); resp.StatusCode != 200 || !strings.Contains(string(b), "/logo?v=") {
			t.Fatalf("imported PUT logo -> %d: %s", resp.StatusCode, b)
		}
		// A re-sync (discovery reporting it again) keeps the look.
		if _, err := importer.Sync(context.Background(), sqlDB, teamA.ID, srv, []agentproto.Service{
			{Kind: "container", Name: "look-imp", Image: "nginx", Status: "running"}}); err != nil {
			t.Fatalf("re-import: %v", err)
		}
		ua.mustJSON("GET", imp, nil, 200, &out)
		if app := out["app"].(map[string]any); app["title"] != "Old site" || app["logo_url"] == nil {
			t.Errorf("after a re-sync: %+v", app)
		}
	})

	t.Run("logo", func(t *testing.T) {
		logo := base + "/logo"
		if resp, _ := ua.raw("GET", logo, "", nil); resp.StatusCode != 404 {
			t.Fatalf("GET without a logo -> %d (want 404)", resp.StatusCode)
		}
		for name, c := range map[string]struct {
			ct   string
			body []byte
			want int
		}{
			"svg":         {"image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 415},
			"text":        {"text/plain", []byte("hello"), 415},
			"json":        {"application/json", []byte(`{}`), 415},
			"svg as png":  {"image/png", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 422},
			"jpeg as png": {"image/png", jpeg, 422},
			"png as webp": {"image/webp", png, 422},
			"empty":       {"image/png", nil, 422},
			"oversize":    {"image/png", append(append([]byte{}, png...), make([]byte, 512<<10)...), 413},
		} {
			if resp, b := ua.raw("PUT", logo, c.ct, c.body); resp.StatusCode != c.want {
				t.Errorf("PUT %s -> %d (want %d): %s", name, resp.StatusCode, c.want, b)
			}
		}
		if resp, _ := uv.raw("PUT", logo, "image/png", png); resp.StatusCode != 403 {
			t.Errorf("viewer PUT -> %d (want 403)", resp.StatusCode)
		}
		if resp, _ := ub.raw("PUT", logo, "image/png", png); resp.StatusCode != 404 {
			t.Errorf("team B PUT -> %d (want 404)", resp.StatusCode)
		}
		if resp, _ := ub.raw("PUT", "/api/teams/"+teamB.ID+"/apps/"+appID+"/logo", "image/png", png); resp.StatusCode != 404 {
			t.Errorf("team B PUT through its own team -> %d (want 404)", resp.StatusCode)
		}
		var out map[string]any
		ua.mustJSON("GET", base, nil, 200, &out)
		if out["app"].(map[string]any)["logo_url"] != nil {
			t.Fatalf("refused uploads set logo_url: %v", out["app"])
		}

		// Upload (by name): the URL is by id, in the detail, the list and the deployments feed.
		resp, body := ua.raw("PUT", byName+"/logo", "image/png", png)
		if resp.StatusCode != 200 {
			t.Fatalf("PUT png -> %d: %s", resp.StatusCode, body)
		}
		ua.mustJSON("GET", base, nil, 200, &out)
		url, _ := out["app"].(map[string]any)["logo_url"].(string)
		if !strings.HasPrefix(url, logo+"?v=") || len(url) != len(logo)+3+16 || !strings.Contains(string(body), url) {
			t.Fatalf("logo_url after upload: %q (PUT answered %s)", url, body)
		}
		var list struct {
			Apps []struct {
				ID      string
				LogoURL *string `json:"logo_url"`
			} `json:"apps"`
		}
		ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/apps", nil, 200, &list)
		withLogo := 0
		for _, a := range list.Apps {
			if a.ID == appID && (a.LogoURL == nil || *a.LogoURL != url) {
				t.Errorf("list logo_url: %v (want %s)", a.LogoURL, url)
			}
			if a.LogoURL != nil {
				withLogo++
			}
		}
		if len(list.Apps) != 2 || withLogo != 2 {
			t.Errorf("list: %d apps, %d with a logo (want 2 and 2)", len(list.Apps), withLogo)
		}
		var feed struct {
			Deployments []struct {
				App map[string]any `json:"app"`
			} `json:"deployments"`
		}
		ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/deployments", nil, 200, &feed)
		if len(feed.Deployments) == 0 || feed.Deployments[0].App["logo_url"] != url || feed.Deployments[0].App["icon"] != "leaf" ||
			feed.Deployments[0].App["title"] != "Via MCP" || feed.Deployments[0].App["name"] != appName {
			t.Errorf("feed app: %+v", feed.Deployments)
		}

		// Served safely, to any member, not to another team.
		resp, got := uv.raw("GET", url, "", nil)
		if resp.StatusCode != 200 || !bytes.Equal(got, png) {
			t.Fatalf("viewer GET -> %d, %d bytes", resp.StatusCode, len(got))
		}
		for h, want := range map[string]string{"Content-Type": "image/png", "X-Content-Type-Options": "nosniff",
			"Content-Security-Policy": "default-src 'none'; sandbox", "Cache-Control": "private, max-age=31536000, immutable"} {
			if resp.Header.Get(h) != want {
				t.Errorf("GET %s: %q (want %q)", h, resp.Header.Get(h), want)
			}
		}
		etag := resp.Header.Get("ETag")
		if etag == "" {
			t.Error("GET has no ETag")
		}
		req, _ := http.NewRequest("GET", ts.URL+url, nil)
		req.Header.Set("If-None-Match", etag)
		if r2, err := ua.http.Do(req); err != nil || r2.StatusCode != 304 {
			t.Errorf("If-None-Match -> %v %v (want 304)", r2, err)
		}
		if resp, _ := ub.raw("GET", url, "", nil); resp.StatusCode != 404 {
			t.Errorf("team B GET -> %d (want 404)", resp.StatusCode)
		}
		if resp, _ := ub.raw("GET", strings.Replace(url, teamA.ID, teamB.ID, 1), "", nil); resp.StatusCode != 404 {
			t.Errorf("team B GET through its own team -> %d (want 404)", resp.StatusCode)
		}

		// Replace: a new version.
		if resp, b := ua.raw("PUT", logo, "image/jpeg", jpeg); resp.StatusCode != 200 {
			t.Fatalf("PUT jpeg -> %d: %s", resp.StatusCode, b)
		}
		ua.mustJSON("GET", base, nil, 200, &out)
		if out["app"].(map[string]any)["logo_url"] == url {
			t.Errorf("a replaced logo kept its version: %v", out["app"])
		}
		if resp, got := ua.raw("GET", logo, "", nil); resp.Header.Get("Content-Type") != "image/jpeg" || !bytes.Equal(got, jpeg) {
			t.Errorf("after replacing: %s, %d bytes", resp.Header.Get("Content-Type"), len(got))
		}

		// Remove: owner or admin.
		if resp, _ := uv.raw("DELETE", logo, "", nil); resp.StatusCode != 403 {
			t.Errorf("viewer DELETE -> %d (want 403)", resp.StatusCode)
		}
		if resp, _ := ub.raw("DELETE", logo, "", nil); resp.StatusCode != 404 {
			t.Errorf("team B DELETE -> %d (want 404)", resp.StatusCode)
		}
		ua.mustJSON("DELETE", logo, nil, 200, &out)
		if v, ok := out["logo_url"]; !ok || v != nil {
			t.Errorf("logo_url after DELETE: %v", v)
		}
		if resp, _ := ua.raw("GET", logo, "", nil); resp.StatusCode != 404 {
			t.Errorf("GET after DELETE -> %d (want 404)", resp.StatusCode)
		}
		ua.mustJSON("DELETE", logo, nil, 200, nil) // nothing to remove: fine, and not audited
		var audit struct {
			Entries []struct{ Action string } `json:"entries"`
		}
		ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/audit", nil, 200, &audit)
		seen := map[string]int{}
		for _, e := range audit.Entries {
			seen[e.Action]++
		}
		if seen["app.logo_updated"] != 3 || seen["app.logo_removed"] != 1 { // 2 here + the imported app's
			t.Errorf("audit: %v", seen)
		}
	})

	t.Run("the logo goes with its app", func(t *testing.T) {
		count := func(id string) (n int) {
			if err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{TeamID: teamA.ID}, func(_ *db.Queries, tx *sql.Tx) error {
				return tx.QueryRow(`SELECT count(*) FROM app_logos WHERE app_id = $1`, id).Scan(&n)
			}); err != nil {
				t.Fatal(err)
			}
			return
		}
		// Row security: another team's scope sees no row at all.
		if err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{TeamID: teamB.ID}, func(_ *db.Queries, tx *sql.Tx) error {
			var n int
			if err := tx.QueryRow(`SELECT count(*) FROM app_logos`).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Errorf("team B's scope sees %d logo rows", n)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var imp struct {
			App struct{ ID string } `json:"app"`
		}
		ua.mustJSON("GET", "/api/teams/"+teamA.ID+"/apps/look-imp", nil, 200, &imp)
		if count(imp.App.ID) != 1 {
			t.Fatal("the imported app's logo row is missing")
		}
		ua.mustJSON("DELETE", "/api/teams/"+teamA.ID+"/apps/look-imp", map[string]any{"confirm": "look-imp"}, 204, nil)
		if count(imp.App.ID) != 0 {
			t.Error("the logo outlived its imported app")
		}
		// A git app (never deployed, so nothing is running that would refuse the delete).
		var gone struct {
			App struct{ ID, Name string } `json:"app"`
		}
		ua.mustJSON("POST", "/api/teams/"+teamA.ID+"/apps", map[string]any{"name": "look-gone-" + short, "server": srv,
			"repo_url": "https://github.com/example/app"}, 201, &gone)
		goneBase := "/api/teams/" + teamA.ID + "/apps/" + gone.App.ID
		if resp, b := ua.raw("PUT", goneBase+"/logo", "image/png", png); resp.StatusCode != 200 {
			t.Fatalf("PUT -> %d: %s", resp.StatusCode, b)
		}
		if count(gone.App.ID) != 1 {
			t.Fatal("the logo row is missing")
		}
		ua.mustJSON("DELETE", goneBase, map[string]any{"confirm": gone.App.Name}, 204, nil)
		if count(gone.App.ID) != 0 {
			t.Error("the logo outlived its app")
		}
	})
}
