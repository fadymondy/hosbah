package server

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type deployStepJSON struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Run     string `json:"run"`
	Enabled bool   `json:"enabled"`
}

type deployConfigJSON struct {
	Mode      string           `json:"mode"`
	Preset    string           `json:"preset"`
	Steps     []deployStepJSON `json:"steps"`
	Version   int              `json:"version"`
	UpdatedBy *string          `json:"updated_by"`
	UpdatedAt *string          `json:"updated_at"`
	CanSave   bool             `json:"can_save"`
}

/*
An app's deploy script configuration: read defaults from the preset before anything is saved,
round-trips through a save with an incrementing version, keeps every version in history, restore
brings a past version back as a new one, and another team gets 404 and sees no row.
*/
func TestAppDeployConfig(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner, stranger, viewer := newClient(t, ts.URL), newClient(t, ts.URL), newClient(t, ts.URL)
	owner.register("dc-owner-" + suffix + "@example.test")
	stranger.register("dc-stranger-" + suffix + "@example.test")
	viewerEmail := "dc-viewer-" + suffix + "@example.test"
	viewer.register(viewerEmail)
	var team, teamB struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Deploycfg " + suffix}, 201, &team)
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "DeploycfgB " + suffix}, 201, &teamB)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	srv, _ := enrolServer(t, owner, ts.URL, team.ID, "dc-"+short)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "dc-app-" + short, "server": srv,
		"repo_url": "https://github.com/example/app"}, 201, &app)
	base := "/api/teams/" + team.ID + "/apps/" + app.App.ID + "/deploy-config"
	byName := "/api/teams/" + team.ID + "/apps/" + app.App.Name + "/deploy-config"

	t.Run("default before any save", func(t *testing.T) {
		var c deployConfigJSON
		owner.mustJSON("GET", base, nil, 200, &c)
		if c.UpdatedAt != nil || c.Version != 0 || len(c.Steps) == 0 || !c.CanSave {
			t.Fatalf("a new app's deploy config: %+v", c)
		}
		viewer.mustJSON("GET", byName, nil, 200, &c)
		if c.CanSave {
			t.Error("a viewer may not save")
		}
	})

	steps1 := []map[string]any{
		{"key": "install", "name": "Install", "run": "npm ci", "enabled": true},
		{"key": "build", "name": "Build", "run": "npm run build", "enabled": true},
		{"key": "migrate", "name": "Migrate", "run": "", "enabled": false},
		{"key": "release", "name": "Start", "run": "npm start", "enabled": true},
		{"key": "after_release", "name": "After release", "run": "", "enabled": false},
		{"key": "on_failure", "name": "On failure", "run": "", "enabled": false},
	}

	t.Run("save and read back", func(t *testing.T) {
		var saved deployConfigJSON
		owner.mustJSON("PUT", byName, map[string]any{"mode": "auto", "preset": "node", "steps": steps1}, 200, &saved)
		if saved.Version != 1 || saved.Mode != "auto" || saved.Preset != "node" || len(saved.Steps) != 6 || saved.UpdatedAt == nil {
			t.Fatalf("saved: %+v", saved)
		}
		var c deployConfigJSON
		owner.mustJSON("GET", base, nil, 200, &c)
		if c.Version != 1 || c.Preset != "node" || c.Steps[0].Run != "npm ci" {
			t.Fatalf("read back: %+v", c)
		}

		// A second save bumps the version.
		steps2 := append([]map[string]any{}, steps1...)
		steps2[0] = map[string]any{"key": "install", "name": "Install", "run": "npm ci --production", "enabled": true}
		var saved2 deployConfigJSON
		owner.mustJSON("PUT", base, map[string]any{"mode": "full", "preset": "node", "steps": steps2}, 200, &saved2)
		if saved2.Version != 2 || saved2.Mode != "full" {
			t.Fatalf("second save: %+v", saved2)
		}
	})

	t.Run("version history and restore", func(t *testing.T) {
		var versions struct {
			Versions []struct {
				Version int    `json:"version"`
				Mode    string `json:"mode"`
			} `json:"versions"`
		}
		owner.mustJSON("GET", base+"/versions", nil, 200, &versions)
		if len(versions.Versions) != 2 || versions.Versions[0].Version != 2 || versions.Versions[1].Version != 1 {
			t.Fatalf("versions: %+v", versions.Versions)
		}

		// A viewer may read the history but not restore.
		viewer.mustJSON("GET", base+"/versions", nil, 200, &versions)
		if code, _ := viewer.do("POST", base+"/versions/1/restore", nil); code != 403 {
			t.Errorf("viewer restore -> %d (want 403)", code)
		}

		var restored deployConfigJSON
		owner.mustJSON("POST", base+"/versions/1/restore", nil, 200, &restored)
		if restored.Version != 3 || restored.Mode != "auto" || restored.Steps[0].Run != "npm ci" {
			t.Fatalf("restored: %+v", restored)
		}

		owner.mustJSON("GET", base+"/versions", nil, 200, &versions)
		if len(versions.Versions) != 3 {
			t.Fatalf("after restore, versions: %+v", versions.Versions)
		}

		if code, _ := owner.do("POST", base+"/versions/999/restore", nil); code != 404 {
			t.Errorf("restoring an unknown version -> %d (want 404)", code)
		}
	})

	t.Run("presets", func(t *testing.T) {
		var out struct {
			Presets []struct {
				Preset string           `json:"preset"`
				Steps  []deployStepJSON `json:"steps"`
			} `json:"presets"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/deploy-presets", nil, 200, &out)
		if len(out.Presets) < 5 {
			t.Fatalf("presets: %+v", out.Presets)
		}
		names := map[string]bool{}
		for _, p := range out.Presets {
			names[p.Preset] = true
			if len(p.Steps) == 0 {
				t.Errorf("preset %s has no steps", p.Preset)
			}
		}
		for _, want := range []string{"go", "node", "next", "laravel", "php", "python", "static"} {
			if !names[want] {
				t.Errorf("missing preset %s: %+v", want, names)
			}
		}
	})

	t.Run("bad input", func(t *testing.T) {
		if code, _ := owner.do("PUT", base, map[string]any{"mode": "bogus", "preset": "node", "steps": steps1}); code != 422 {
			t.Errorf("bad mode -> %d (want 422)", code)
		}
		bad := append([]map[string]any{}, steps1...)
		bad[0] = map[string]any{"key": "not-a-key", "name": "x", "run": "", "enabled": true}
		if code, _ := owner.do("PUT", base, map[string]any{"mode": "auto", "preset": "node", "steps": bad}); code != 422 {
			t.Errorf("bad step key -> %d (want 422)", code)
		}
	})

	t.Run("roles and other teams", func(t *testing.T) {
		if code, _ := viewer.do("PUT", base, map[string]any{"mode": "auto", "preset": "node", "steps": steps1}); code != 403 {
			t.Errorf("viewer PUT -> %d (want 403)", code)
		}
		for _, m := range []string{"GET", "PUT"} {
			if code, text := stranger.do(m, base, map[string]any{"mode": "auto", "preset": "node", "steps": steps1}); code != 404 {
				t.Errorf("another team's %s -> %d (want 404): %s", m, code, text)
			}
		}
		if code, _ := stranger.do("GET", "/api/teams/"+teamB.ID+"/apps/"+app.App.ID+"/deploy-config", nil); code != 404 {
			t.Errorf("team B's URL with team A's app -> %d (want 404)", code)
		}
	})
}
