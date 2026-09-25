package server

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type phpConfigJSON struct {
	Version           string            `json:"php_version"`
	Ini               map[string]string `json:"php_ini"`
	AvailableVersions []string          `json:"available_versions"`
	AvailableIniKeys  []string          `json:"available_ini_keys"`
	CanSave           bool              `json:"can_save"`
}

/*
An app's PHP version pin and php.ini overrides (MH-576 D2): defaults before any save, a round trip
through a save (not versioned, unlike the pipeline steps), validation of the version enum and ini
keys/values, role and tenancy isolation, and that saving them does not disturb the app's separately
saved deploy pipeline (mode/preset/steps).
*/
func TestAppPHPConfig(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner, stranger, viewer := newClient(t, ts.URL), newClient(t, ts.URL), newClient(t, ts.URL)
	owner.register("php-owner-" + suffix + "@example.test")
	stranger.register("php-stranger-" + suffix + "@example.test")
	viewerEmail := "php-viewer-" + suffix + "@example.test"
	viewer.register(viewerEmail)
	var team, teamB struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "PhpCfg " + suffix}, 201, &team)
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "PhpCfgB " + suffix}, 201, &teamB)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	srv, _ := enrolServer(t, owner, ts.URL, team.ID, "php-"+short)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "php-app-" + short, "server": srv,
		"repo_url": "https://github.com/example/app"}, 201, &app)
	base := "/api/teams/" + team.ID + "/apps/" + app.App.ID + "/deploy-config/php"
	byName := "/api/teams/" + team.ID + "/apps/" + app.App.Name + "/deploy-config/php"

	t.Run("default before any save", func(t *testing.T) {
		var c phpConfigJSON
		owner.mustJSON("GET", base, nil, 200, &c)
		if c.Version != "auto" || len(c.Ini) != 0 || !c.CanSave {
			t.Fatalf("a new app's php config: %+v", c)
		}
		if len(c.AvailableVersions) < 4 || len(c.AvailableIniKeys) == 0 {
			t.Fatalf("choices not returned: %+v", c)
		}
		viewer.mustJSON("GET", byName, nil, 200, &c)
		if c.CanSave {
			t.Error("a viewer may not save")
		}
	})

	t.Run("save and read back", func(t *testing.T) {
		var saved phpConfigJSON
		owner.mustJSON("PUT", byName, map[string]any{"php_version": "8.3", "php_ini": map[string]string{
			"memory_limit": "512M", "upload_max_filesize": "64M", "date.timezone": "UTC",
		}}, 200, &saved)
		if saved.Version != "8.3" || saved.Ini["memory_limit"] != "512M" || saved.Ini["date.timezone"] != "UTC" {
			t.Fatalf("saved: %+v", saved)
		}
		var c phpConfigJSON
		owner.mustJSON("GET", base, nil, 200, &c)
		if c.Version != "8.3" || len(c.Ini) != 3 {
			t.Fatalf("read back: %+v", c)
		}

		// Saving php config creates the app_deploy_configs row (version 1) if none existed, but
		// never touches the pipeline itself: mode/preset stay at the preset defaults.
		var dc deployConfigJSON
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/apps/"+app.App.ID+"/deploy-config", nil, 200, &dc)
		if dc.Mode != "auto" || dc.Preset != "static" || dc.Version != 1 {
			t.Fatalf("php save changed the deploy pipeline: %+v", dc)
		}

		// Re-saving with fewer keys replaces the whole map (not merged).
		var saved2 phpConfigJSON
		owner.mustJSON("PUT", base, map[string]any{"php_version": "auto", "php_ini": map[string]string{"memory_limit": "256M"}}, 200, &saved2)
		if saved2.Version != "auto" || len(saved2.Ini) != 1 || saved2.Ini["memory_limit"] != "256M" {
			t.Fatalf("second save: %+v", saved2)
		}
	})

	t.Run("bad input", func(t *testing.T) {
		if code, _ := owner.do("PUT", base, map[string]any{"php_version": "7.4", "php_ini": map[string]string{}}); code != 422 {
			t.Errorf("bad version -> %d (want 422)", code)
		}
		if code, _ := owner.do("PUT", base, map[string]any{"php_version": "8.2", "php_ini": map[string]string{"disable_functions": "exec"}}); code != 422 {
			t.Errorf("unknown ini key -> %d (want 422)", code)
		}
		if code, _ := owner.do("PUT", base, map[string]any{"php_version": "8.2", "php_ini": map[string]string{"memory_limit": "512M\r\nmax_execution_time = 0"}}); code != 422 {
			t.Errorf("ini injection attempt -> %d (want 422)", code)
		}
		if code, _ := owner.do("PUT", base, map[string]any{"php_version": "8.2", "php_ini": map[string]string{"memory_limit": "512M;evil=1"}}); code != 422 {
			t.Errorf("ini value with ';' -> %d (want 422)", code)
		}
	})

	t.Run("roles and other teams", func(t *testing.T) {
		if code, _ := viewer.do("PUT", base, map[string]any{"php_version": "8.2"}); code != 403 {
			t.Errorf("viewer PUT -> %d (want 403)", code)
		}
		for _, m := range []string{"GET", "PUT"} {
			if code, text := stranger.do(m, base, map[string]any{"php_version": "8.2"}); code != 404 {
				t.Errorf("another team's %s -> %d (want 404): %s", m, code, text)
			}
		}
		if code, _ := stranger.do("GET", "/api/teams/"+teamB.ID+"/apps/"+app.App.ID+"/deploy-config/php", nil); code != 404 {
			t.Errorf("team B's URL with team A's app -> %d (want 404)", code)
		}
	})
}
