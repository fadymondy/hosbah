package deploy

import (
	"path"
	"regexp"
	"strings"
)

/*
Track decision: whether a deployment can take the fast path (skip the full image build; MH-573
A7's agent fast executor reuses the running container) or must take the full path (build a new
image from scratch). ChooseTrack is pure — no I/O — so the deploy trigger (internal/api,
internal/mcp) and the pipeline can both call it with whatever changed-paths information they
have (or none, if the control plane cannot compute a diff).
*/

// fullTrackPatterns match a changed path that forces a full rebuild: build definitions, lockfiles
// and toolchain/version pins that a fast, in-place restart cannot safely pick up.
var fullTrackPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^dockerfile`),
	regexp.MustCompile(`(?i)dockerfile[^/]*$`),
	regexp.MustCompile(`(?i)^docker-compose`),
	regexp.MustCompile(`(?i)^\.dockerignore$`),
	regexp.MustCompile(`(?i)^railpack\.(json|toml|jsonc)$`),
	regexp.MustCompile(`(?i)^nixpacks\.toml$`),
	regexp.MustCompile(`(?i)^package-lock\.json$`),
	regexp.MustCompile(`(?i)^pnpm-lock\.yaml$`),
	regexp.MustCompile(`(?i)^yarn\.lock$`),
	regexp.MustCompile(`(?i)^bun\.lockb$`),
	regexp.MustCompile(`(?i)^go\.sum$`),
	regexp.MustCompile(`(?i)^composer\.lock$`),
	regexp.MustCompile(`(?i)^poetry\.lock$`),
	regexp.MustCompile(`(?i)^requirements.*\.txt$`),
	regexp.MustCompile(`(?i)^gemfile\.lock$`),
	regexp.MustCompile(`(?i)^\.tool-versions$`),
	regexp.MustCompile(`(?i)^\.nvmrc$`),
	// apt/system packages
	regexp.MustCompile(`(?i)^apt\.txt$`),
	regexp.MustCompile(`(?i)^packages\.txt$`),
}

// TrackFast and TrackFull are the values ChooseTrack (and a deploy trigger's track override) return.
const (
	TrackFast = "fast"
	TrackFull = "full"
)

// fullTrackMatch reports the first fullTrackPatterns entry a changed path matches, checked
// against the path's base name (build files live at a repo's top level almost always, and a
// nested lockfile is just as build-affecting).
func fullTrackMatch(changed []string) (string, bool) {
	for _, p := range changed {
		base := path.Base(strings.TrimSpace(p))
		if base == "" {
			continue
		}
		for _, re := range fullTrackPatterns {
			if re.MatchString(base) {
				return p, true
			}
		}
	}
	return "", false
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
		return TrackFull, "changes unknown"
	}
	if path, ok := fullTrackMatch(changed); ok {
		return TrackFull, path + " changed"
	}
	return TrackFast, "no build-affecting changes"
}
