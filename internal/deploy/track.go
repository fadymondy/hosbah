package deploy

import (
	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
Track decision: whether a deployment can take the fast path (skip the full image build; MH-573
A7's agent fast executor reuses the running container) or must take the full path (build a new
image from scratch). ChooseTrack is pure — no I/O — so the deploy trigger (internal/api,
internal/mcp) and the pipeline can both call it with whatever changed-paths information they
have (or none, if the control plane cannot compute a diff).
*/

// TrackFast and TrackFull are the values ChooseTrack (and a deploy trigger's track override) return.
const (
	TrackFast = "fast"
	TrackFull = "full"
)

// fullTrackMatch reports the first agentproto.FullTrackPatterns entry a changed path matches. The
// pattern list lives in agentproto so the agent's app.fast_build handler can apply the exact same
// rule to the changed paths it computes from its cached checkout.
func fullTrackMatch(changed []string) (string, bool) { return agentproto.FullTrackMatch(changed) }

// FastBuildMode picks the fast track's sub-mode (MH-573 A7b) from the app's deploy configuration:
// dockerfile builds the checkout's own Dockerfile with a cached `docker build`; builder runs the
// steps in a separate builder image and copies the outputs onto the current runtime image; runtime
// is the original MH-573 A7 fallback (steps run directly inside the current runtime image), for an
// app with no Dockerfile and no configured builder image. Pure, so it is unit-testable without a
// database or an agent.
func FastBuildMode(preset, builderImage string) string {
	if preset == PresetDockerfile {
		return agentproto.FastModeDockerfile
	}
	if builderImage != "" {
		return agentproto.FastModeBuilder
	}
	return agentproto.FastModeRuntime
}

// ChooseTrack decides a deployment's track. mode is the app's deploy configuration mode
// (auto | fast | full); changed is the set of paths that differ from the last successful
// release's commit, or nil when that diff could not be computed; lastOK reports whether the app
// has a previous successful release at all.
//
// The checks run in order and each may force TrackFull on its own; only when none of them fire
// does the app's fast path apply.
func ChooseTrack(mode string, changed []string, lastOK bool) (track, reason string) {
	if mode == TrackFull {
		return TrackFull, "full rebuild requested"
	}
	if !lastOK {
		return TrackFull, "no previous successful release"
	}
	if changed == nil {
		// The agent diffs its cached checkout and falls back to a full build itself when a
		// build-affecting file changed, so an unknown diff is worth trying fast.
		return TrackFast, "agent checks the changes"
	}
	if path, ok := fullTrackMatch(changed); ok {
		return TrackFull, path + " changed"
	}
	return TrackFast, "no build-affecting changes"
}
