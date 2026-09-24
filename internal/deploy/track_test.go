package deploy

import "testing"

func TestChooseTrack(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		changed   []string
		lastOK    bool
		wantTrack string
		wantWhy   string
	}{
		{"explicit full mode", "full", []string{"main.go"}, true, TrackFull, "full rebuild requested"},
		{"no previous release", "auto", []string{"main.go"}, false, TrackFull, "no previous successful release"},
		{"unknown changes", "auto", nil, true, TrackFull, "changes unknown"},
		{"empty changes is known", "auto", []string{}, true, TrackFast, "no build-affecting changes"},
		{"dockerfile changed", "auto", []string{"Dockerfile"}, true, TrackFull, "Dockerfile changed"},
		{"dockerfile variant", "auto", []string{"Dockerfile.prod"}, true, TrackFull, "Dockerfile.prod changed"},
		{"docker-compose changed", "auto", []string{"docker-compose.yml"}, true, TrackFull, "docker-compose.yml changed"},
		{"dockerignore changed", "auto", []string{".dockerignore"}, true, TrackFull, ".dockerignore changed"},
		{"railpack config changed", "auto", []string{"railpack.json"}, true, TrackFull, "railpack.json changed"},
		{"nixpacks config changed", "auto", []string{"nixpacks.toml"}, true, TrackFull, "nixpacks.toml changed"},
		{"package-lock changed", "auto", []string{"package-lock.json"}, true, TrackFull, "package-lock.json changed"},
		{"pnpm lock changed", "auto", []string{"pnpm-lock.yaml"}, true, TrackFull, "pnpm-lock.yaml changed"},
		{"yarn lock changed", "auto", []string{"yarn.lock"}, true, TrackFull, "yarn.lock changed"},
		{"bun lock changed", "auto", []string{"bun.lockb"}, true, TrackFull, "bun.lockb changed"},
		{"go.sum changed", "auto", []string{"go.sum"}, true, TrackFull, "go.sum changed"},
		{"composer lock changed", "auto", []string{"composer.lock"}, true, TrackFull, "composer.lock changed"},
		{"poetry lock changed", "auto", []string{"poetry.lock"}, true, TrackFull, "poetry.lock changed"},
		{"requirements txt changed", "auto", []string{"requirements.txt"}, true, TrackFull, "requirements.txt changed"},
		{"requirements variant changed", "auto", []string{"requirements-dev.txt"}, true, TrackFull, "requirements-dev.txt changed"},
		{"gemfile lock changed", "auto", []string{"Gemfile.lock"}, true, TrackFull, "Gemfile.lock changed"},
		{"tool-versions changed", "auto", []string{".tool-versions"}, true, TrackFull, ".tool-versions changed"},
		{"nvmrc changed", "auto", []string{".nvmrc"}, true, TrackFull, ".nvmrc changed"},
		{"apt packages changed", "auto", []string{"apt.txt"}, true, TrackFull, "apt.txt changed"},
		{"nested lockfile changed", "auto", []string{"web/package-lock.json"}, true, TrackFull, "web/package-lock.json changed"},
		{"unrelated file, mode auto", "auto", []string{"app/main.go", "README.md"}, true, TrackFast, "no build-affecting changes"},
		{"unrelated file, mode fast", "fast", []string{"app/main.go"}, true, TrackFast, "no build-affecting changes"},
		{"mode fast but lockfile changed still forces full", "fast", []string{"go.sum"}, true, TrackFull, "go.sum changed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			track, reason := ChooseTrack(tc.mode, tc.changed, tc.lastOK)
			if track != tc.wantTrack {
				t.Errorf("track = %q, want %q", track, tc.wantTrack)
			}
			if reason != tc.wantWhy {
				t.Errorf("reason = %q, want %q", reason, tc.wantWhy)
			}
		})
	}
}
