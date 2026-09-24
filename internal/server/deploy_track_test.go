package server

import (
	"fmt"
	"testing"
	"time"
)

type deploymentTrackJSON struct {
	ID          string  `json:"id"`
	Number      int64   `json:"number"`
	Track       *string `json:"track"`
	TrackReason *string `json:"track_reason"`
}

/*
A deployment's track: the first deploy of a new app has no previous successful release, so it
takes the full track regardless of the app's deploy configuration mode; an explicit track
override on the trigger (auto | fast | full) is stored as asked, bypassing that decision.
*/
func TestDeploymentTrack(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner := newClient(t, ts.URL)
	owner.register("track-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Track " + suffix}, 201, &team)

	srv, _ := enrolServer(t, owner, ts.URL, team.ID, "track-"+short)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "track-app-" + short, "server": srv,
		"repo_url": "https://github.com/example/app"}, 201, &app)
	base := "/api/teams/" + team.ID + "/apps/" + app.App.ID + "/deployments"

	t.Run("first deploy of a new app is full: no previous successful release", func(t *testing.T) {
		var dep deploymentTrackJSON
		owner.mustJSON("POST", base, map[string]any{}, 201, &dep)
		if dep.Track == nil || *dep.Track != "full" {
			t.Fatalf("track: %+v", dep)
		}
		if dep.TrackReason == nil || *dep.TrackReason != "no previous successful release" {
			t.Fatalf("track_reason: %+v", dep)
		}
	})

	t.Run("an explicit fast override is stored as asked", func(t *testing.T) {
		var dep deploymentTrackJSON
		owner.mustJSON("POST", base, map[string]any{"track": "fast"}, 201, &dep)
		if dep.Track == nil || *dep.Track != "fast" {
			t.Fatalf("track: %+v", dep)
		}
		if dep.TrackReason == nil || *dep.TrackReason != "fast rebuild requested" {
			t.Fatalf("track_reason: %+v", dep)
		}
	})

	t.Run("an explicit full override is stored as asked", func(t *testing.T) {
		var dep deploymentTrackJSON
		owner.mustJSON("POST", base, map[string]any{"track": "full"}, 201, &dep)
		if dep.Track == nil || *dep.Track != "full" {
			t.Fatalf("track: %+v", dep)
		}
		if dep.TrackReason == nil || *dep.TrackReason != "full rebuild requested" {
			t.Fatalf("track_reason: %+v", dep)
		}
	})

	t.Run("an unknown track value is rejected", func(t *testing.T) {
		if code, _ := owner.do("POST", base, map[string]any{"track": "bogus"}); code != 422 {
			t.Errorf("bad track -> %d (want 422)", code)
		}
	})

	t.Run("auto is the same as omitting it", func(t *testing.T) {
		var dep deploymentTrackJSON
		owner.mustJSON("POST", base, map[string]any{"track": "auto"}, 201, &dep)
		if dep.Track == nil || *dep.Track != "full" {
			t.Fatalf("track: %+v", dep)
		}
	})
}
