package deploy

import (
	"database/sql"
	"testing"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// TestFastBuildBlockedByRelease is the dispatcher's first fast-vs-full gate (MH-573 A7): the fast
// track needs a previous successful release to build from.
func TestFastBuildBlockedByRelease(t *testing.T) {
	if reason, blocked := fastBuildBlockedByRelease(nil); !blocked || reason != "no previous successful release" {
		t.Fatalf("no live release must block fast: %q, %v", reason, blocked)
	}
	if reason, blocked := fastBuildBlockedByRelease(&db.Release{Image: ""}); !blocked || reason != "no previous successful release" {
		t.Fatalf("an empty image must block fast: %q, %v", reason, blocked)
	}
	if _, blocked := fastBuildBlockedByRelease(&db.Release{Image: "hosbah/demo:v1"}); blocked {
		t.Fatal("a live release with an image must not block fast")
	}
}

// TestFastBuildSteps is the dispatcher choosing which of an app's deploy-config steps app.fast_build
// runs — falling back to full when there is no configuration row, or it cannot be parsed.
func TestFastBuildSteps(t *testing.T) {
	t.Run("no deploy config row falls back", func(t *testing.T) {
		_, reason, ok := fastBuildSteps("", sql.ErrNoRows)
		if ok || reason != "no deploy script configured" {
			t.Fatalf("want a fallback, got ok=%v reason=%q", ok, reason)
		}
	})
	t.Run("unreadable steps JSON falls back", func(t *testing.T) {
		_, reason, ok := fastBuildSteps("not json", nil)
		if ok || reason != "the deploy script configuration is unreadable" {
			t.Fatalf("want a fallback, got ok=%v reason=%q", ok, reason)
		}
	})
	t.Run("only enabled install/build/migrate/on_failure steps with a command are kept, in order", func(t *testing.T) {
		raw := `[
			{"key":"install","name":"Install","run":"npm ci","enabled":true},
			{"key":"build","name":"Build","run":"npm run build","enabled":false},
			{"key":"migrate","name":"Migrate","run":"","enabled":true},
			{"key":"release","name":"Release","run":"npm start","enabled":true},
			{"key":"after_release","name":"After","run":"pm2 reload","enabled":true},
			{"key":"on_failure","name":"On failure","run":"echo boom","enabled":true}
		]`
		steps, _, ok := fastBuildSteps(raw, nil)
		if !ok {
			t.Fatal("a well-formed configuration must not fall back")
		}
		if len(steps) != 2 || steps[0].Key != "install" || steps[1].Key != "on_failure" {
			t.Fatalf("want install then on_failure (disabled build, empty migrate, and release/after_release excluded), got %+v", steps)
		}
	})
}

// TestChooseTrackAndFastBuildAgree: ChooseTrack's own full-trigger patterns are the same ones the
// agent's app.fast_build checks against its diff (agentproto.FullTrackMatch) — the dispatcher-side
// and agent-side halves of the fallback share one rule.
func TestChooseTrackAndFastBuildAgree(t *testing.T) {
	track, reason := ChooseTrack("fast", []string{"go.sum"}, true)
	if track != TrackFull || reason != "go.sum changed" {
		t.Fatalf("ChooseTrack: %q / %q", track, reason)
	}
}
