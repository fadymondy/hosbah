package deploy

import (
	"database/sql"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// TestFastBuildMode (MH-573 A7b): the fast track's mode picks dockerfile for a Dockerfile app,
// builder for an app with a configured builder image, and falls back to runtime otherwise.
func TestFastBuildModeFollowsTheDetectedPreset(t *testing.T) {
	cfg := db.AppDeployConfig{DetectedPreset: PresetDockerfile}
	if got := FastBuildMode(effectivePreset(cfg), cfg.BuilderImage); got != agentproto.FastModeDockerfile {
		t.Fatalf("a detected Dockerfile app fast-builds in %q mode", got)
	}
	cfg.Preset = PresetStatic
	if got := effectivePreset(cfg); got != PresetStatic {
		t.Fatalf("the chosen preset must win: %q", got)
	}
}

// An app last built from its Dockerfile, or with a Dockerfile path set, never fast-builds in
// runtime mode just because detection recorded no preset: that mode copies the new source over
// the old image without building it.
func TestFastPresetKeepsDockerfileApps(t *testing.T) {
	dockerfile := PresetDockerfile
	node := PresetNode
	if got := fastPreset(db.AppDeployConfig{}, &dockerfile); got != PresetDockerfile {
		t.Fatalf("an app last built from its Dockerfile: %q", got)
	}
	if got := fastPreset(db.AppDeployConfig{DockerfilePath: "web/Dockerfile"}, nil); got != PresetDockerfile {
		t.Fatalf("an app with a Dockerfile path: %q", got)
	}
	if got := fastPreset(db.AppDeployConfig{Preset: PresetStatic}, &dockerfile); got != PresetStatic {
		t.Fatalf("the chosen preset must win: %q", got)
	}
	if got := fastPreset(db.AppDeployConfig{}, &node); got != "" {
		t.Fatalf("no Dockerfile signal keeps the detected preset: %q", got)
	}
}

func TestFastBuildMode(t *testing.T) {
	cases := []struct {
		name, preset, builderImage, want string
	}{
		{"dockerfile preset wins regardless of builder image", PresetDockerfile, "golang:1.26", agentproto.FastModeDockerfile},
		{"a builder image with no Dockerfile", PresetGo, "golang:1.26", agentproto.FastModeBuilder},
		{"no Dockerfile, no builder image falls back to runtime", PresetStatic, "", agentproto.FastModeRuntime},
		{"unknown preset with no builder image falls back to runtime", "", "", agentproto.FastModeRuntime},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FastBuildMode(c.preset, c.builderImage); got != c.want {
				t.Fatalf("FastBuildMode(%q, %q) = %q, want %q", c.preset, c.builderImage, got, c.want)
			}
		})
	}
}

// TestDetectPresetDockerfile (MH-573 A7b): a Dockerfile at the repository root always wins, ahead
// of any other marker file, so the fast track defaults to a cached `docker build`.
func TestDetectPresetDockerfile(t *testing.T) {
	if got := DetectPreset([]string{"Dockerfile", "package.json", "go.mod"}); got != PresetDockerfile {
		t.Fatalf("want dockerfile, got %q", got)
	}
	if got := DetectPreset([]string{"package.json", "next.config.js"}); got != PresetNext {
		t.Fatalf("without a Dockerfile the more specific marker still wins: got %q", got)
	}
}

// TestPresetBuilderImageAndOutputs (MH-573 A7b): every non-static, non-dockerfile preset gets a
// default builder image and at least one output path; dockerfile and static need neither.
func TestPresetBuilderImageAndOutputs(t *testing.T) {
	for _, p := range []string{PresetGo, PresetNode, PresetNext, PresetLaravel, PresetPHP, PresetPython} {
		if PresetBuilderImage(p) == "" {
			t.Errorf("preset %s: want a default builder image", p)
		}
		if len(PresetOutputs(p)) == 0 {
			t.Errorf("preset %s: want at least one default output", p)
		}
	}
	for _, p := range []string{PresetDockerfile, PresetStatic} {
		if PresetBuilderImage(p) != "" {
			t.Errorf("preset %s: want no builder image", p)
		}
	}
}

// TestFastBuildBlockedByRelease is the dispatcher's first fast-vs-full gate (MH-573 A7): the fast
// track needs a previous successful release to build from.
func TestFastBuildBlockedByRelease(t *testing.T) {
	if reason, blocked := fastBuildBlockedByRelease(nil); !blocked || reason != "no previous successful release" {
		t.Fatalf("no live release must block fast: %q, %v", reason, blocked)
	}
	if reason, blocked := fastBuildBlockedByRelease(&db.Release{Image: ""}); !blocked || reason != "no previous successful release" {
		t.Fatalf("an empty image must block fast: %q, %v", reason, blocked)
	}
	if _, blocked := fastBuildBlockedByRelease(&db.Release{Image: "hosbah/demo:v1", Version: 1}); blocked {
		t.Fatal("a live release with an image must not block fast")
	}
	// An adopted app's v0 is its old workload image: its first deploy from git must build in full.
	if _, blocked := fastBuildBlockedByRelease(&db.Release{Image: "mahaam-api", Version: 0}); !blocked {
		t.Fatal("the adopted v0 release must block fast")
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
