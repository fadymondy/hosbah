package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMatchPath(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"apps/desktop/**", "apps/desktop/Sources/App.swift", true},
		{"apps/desktop/**", "apps/desktop", true},
		{"apps/desktop/**", "apps/desktopx/a.swift", false},
		{"apps/desktop/", "apps/desktop/a/b.swift", true},
		{"docs/**", "web/docs/a.md", false},
		{"*.md", "README.md", true},
		{"*.md", "web/content/post.md", true},
		{"*.md", "web/page.mdx", false},
		{"/*.md", "TODO.md", true},
		{"/*.md", "web/TODO.md", false},
		{"/TODO.md", "TODO.md", true},
		{"TODO.md", "web/TODO.md", true},
		{"Dockerfile", "Dockerfile", true},
		{"go.mod", "apps/tunnel/go.mod", true},
		{"/go.mod", "apps/tunnel/go.mod", false},
		{"cmd/**", "cmd/api/main.go", true},
		{"internal/**/*.sql", "internal/db/queries/app.sql", true},
		{"internal/**/*.sql", "internal/app.sql", true},
		{"internal/*.go", "internal/db/x.go", false},
		{"web/**/*.tsx", "web/app/page.tsx", true},
		{"./docs/**", "docs/a.md", true},
		{"", "a", false},
	}
	for _, c := range cases {
		if got := MatchPath(c.pattern, c.path); got != c.want {
			t.Errorf("MatchPath(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestBuildRulesDecide(t *testing.T) {
	ignore := BuildRules{Ignore: []string{"apps/desktop/**", "assistant/**", "docs/**", "/*.md"}}
	watch := BuildRules{Watch: []string{"cmd/**", "internal/**", "web/**", "go.mod", "go.sum", "Dockerfile"}, Ignore: []string{"web/**/*.md"}}
	cases := []struct {
		name    string
		rules   BuildRules
		changed []string
		known   bool
		want    bool
	}{
		{"no rules build everything", BuildRules{}, []string{"docs/a.md"}, true, true},
		{"ignored only", ignore, []string{"apps/desktop/A.swift", "TODO.md"}, true, false},
		{"one image path among ignored", ignore, []string{"apps/desktop/A.swift", "internal/x.go"}, true, true},
		{"unknown changes build", ignore, nil, false, true},
		{"empty change list builds", ignore, []string{}, true, true},
		{"hosbah.toml always builds", ignore, []string{"hosbah.toml"}, true, true},
		{"cloudy.toml always builds", watch, []string{"cloudy.toml"}, true, true},
		{"watched", watch, []string{"internal/db/x.go"}, true, true},
		{"not watched", watch, []string{"apps/desktop/A.swift", "mobile/app.tsx"}, true, false},
		{"watched but ignored", watch, []string{"web/README.md"}, true, false},
		{"root go.mod watched", watch, []string{"go.mod"}, true, true},
	}
	for _, c := range cases {
		got, reason := c.rules.Decide(c.changed, c.known)
		if got != c.want {
			t.Errorf("%s: Decide = %v (%s), want %v", c.name, got, reason, c.want)
		}
		if reason == "" {
			t.Errorf("%s: no reason", c.name)
		}
	}
	if _, reason := ignore.Decide([]string{"docs/a.md"}, true); reason != "no image changes (docs/a.md)" {
		t.Errorf("reason = %q", reason)
	}
}

func TestBuildRulesConfig(t *testing.T) {
	c, err := ParseConfig("[build]\nwatch = [\"web/**\"]\nignore = [\"docs/**\", \"/TODO.md\"]\ndebounce = 90\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Build.Watch) != 1 || len(c.Build.Ignore) != 2 || c.Build.Debounce != 90 {
		t.Fatalf("parsed %+v", c.Build)
	}
	if got := ParseBuildRules(c.Build.JSON()); got.Debounce != 90 || got.Ignore[1] != "/TODO.md" {
		t.Fatalf("round trip %+v", got)
	}
	if ParseBuildRules("{}").Debounce != 0 || len(ParseBuildRules("not json").Watch) != 0 {
		t.Fatal("unreadable rules must be no rules")
	}
	for _, bad := range []string{
		"[build]\nwatch = [\"\"]\n",
		"[build]\nignore = [\"../x\"]\n",
		"[build]\nignore = [\"//x\"]\n",
		"[build]\ndebounce = -1\n",
		"[build]\ndebounce = 99999\n",
		"[build]\nwatchh = [\"x\"]\n",
	} {
		if _, err := ParseConfig(bad); err == nil {
			t.Errorf("ParseConfig(%q) accepted it", bad)
		}
	}
}

func TestNotBefore(t *testing.T) {
	now := time.Date(2026, 10, 23, 21, 2, 0, 0, time.UTC)
	wait, max := 75*time.Second, 10*time.Minute
	if got := NotBefore(now, time.Time{}, wait, max); !got.Equal(now.Add(wait)) {
		t.Errorf("first push: %v", got)
	}
	if got := NotBefore(now, now.Add(-2*time.Minute), wait, max); !got.Equal(now.Add(wait)) {
		t.Errorf("a later push restarts the wait: %v", got)
	}
	if got := NotBefore(now, now.Add(-9*time.Minute-30*time.Second), wait, max); !got.Equal(now.Add(30 * time.Second)) {
		t.Errorf("max wait caps it: %v", got)
	}
	if got := NotBefore(now, now.Add(-time.Hour), wait, max); !got.Equal(now) {
		t.Errorf("past the max wait it starts at once: %v", got)
	}
	if got := NotBefore(now, time.Time{}, 0, max); !got.IsZero() {
		t.Errorf("no debounce: %v", got)
	}
	if got := (Debounce{Wait: wait}).For(BuildRules{Debounce: 90}); got != 90*time.Second {
		t.Errorf("hosbah.toml debounce: %v", got)
	}
}

func TestDebounceFromEnv(t *testing.T) {
	t.Setenv("HOSBAH_BUILD_DEBOUNCE", "0")
	t.Setenv("HOSBAH_BUILD_MAX_WAIT", "300")
	if d := DebounceFromEnv(); d.Wait != 0 || d.MaxWait != 5*time.Minute {
		t.Fatalf("%+v", d)
	}
	t.Setenv("HOSBAH_BUILD_DEBOUNCE", "nope")
	if d := DebounceFromEnv(); d.Wait != DefaultDebounce {
		t.Fatalf("%+v", d)
	}
	t.Setenv("HOSBAH_BUILD_CPU_SHARE", "0.3")
	if BuildCPUShareFromEnv() != 0.3 {
		t.Fatal("cpu share")
	}
	t.Setenv("HOSBAH_BUILD_CPU_SHARE", "7")
	if BuildCPUShareFromEnv() != DefaultBuildCPUShare {
		t.Fatal("out-of-range cpu share must fall back")
	}
}

func TestPushPayloadChanged(t *testing.T) {
	raw := `{"ref":"refs/heads/master","before":"aaa","after":"bbb","head_commit":{"id":"bbb","message":"Merge pull request #224\n\nbody"},
		"commits":[{"id":"x","added":["apps/desktop/A.swift"],"modified":["TODO.md"]},{"id":"bbb","removed":["apps/desktop/B.swift"],"modified":["TODO.md"]}]}`
	var p PushPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	changed, known := p.Changed()
	if !known || strings.Join(changed, ",") != "apps/desktop/A.swift,TODO.md,apps/desktop/B.swift" {
		t.Fatalf("changed %v %v", changed, known)
	}
	if sha, msg := p.Head(); sha != "bbb" || msg != "Merge pull request #224" {
		t.Fatalf("head %q %q", sha, msg)
	}
	for name, mut := range map[string]func(*PushPayload){
		"new branch":    func(p *PushPayload) { p.Before = strings.Repeat("0", 40) },
		"created":       func(p *PushPayload) { p.Created = true },
		"force push":    func(p *PushPayload) { p.Forced = true },
		"no commits":    func(p *PushPayload) { p.Commits = nil },
		"gitlab capped": func(p *PushPayload) { n := 40; p.TotalCommits = &n },
	} {
		q := p
		mut(&q)
		if _, known := q.Changed(); known {
			t.Errorf("%s: known", name)
		}
	}
	var plain PushPayload
	_ = json.Unmarshal([]byte(`{"ref":"main"}`), &plain)
	if _, known := plain.Changed(); known {
		t.Error("a plain {\"ref\"} hook body has no file list")
	}
}

// ---- queue simulation --------------------------------------------------------------------------------

// simPush is one push to the app's branch.
type simPush struct {
	SHA   string    `json:"sha"`
	At    time.Time `json:"at"`
	Files []string  `json:"files"`
}

type simBuild struct {
	Start, End time.Time
	SHA        string
	Coalesced  int
}

type simResult struct {
	Builds     []simBuild
	Skipped    int
	Superseded int
}

// simulate replays pushes through the deploy queue as the control plane runs it: one deploy running
// per app, one waiting (a newer request supersedes it), the waiting one claimed once the app is
// free and its not_before has passed, and a push skipped when the rules find no image change and
// the app is not behind a failed build. Every build takes buildDur and succeeds; it builds the
// branch head at the moment it starts.
func simulate(pushes []simPush, rules BuildRules, d Debounce, buildDur time.Duration) simResult {
	type waiting struct {
		since, notBefore time.Time
		coalesced        int
	}
	var (
		res     simResult
		running *simBuild
		queued  *waiting
		now     time.Time
		head    string
		builtOK = true // the app's last finished deploy succeeded
	)
	advance := func(until time.Time) {
		for {
			if running != nil && !running.End.After(until) {
				now = running.End
				res.Builds = append(res.Builds, *running)
				running = nil
				continue
			}
			if running == nil && queued != nil {
				start := now
				if queued.notBefore.After(start) {
					start = queued.notBefore
				}
				if !start.After(until) {
					running = &simBuild{Start: start, End: start.Add(buildDur), SHA: head, Coalesced: queued.coalesced}
					queued = nil
					continue
				}
			}
			return
		}
	}
	for _, p := range pushes {
		advance(p.At)
		now, head = p.At, p.SHA
		if build, _ := rules.Decide(p.Files, true); !build && (queued != nil || running != nil || builtOK) {
			res.Skipped++
			continue
		}
		var since time.Time
		coalesced := 0
		if queued != nil {
			since, coalesced = queued.since, queued.coalesced+1
			res.Superseded++
		}
		nb := NotBefore(p.At, since, d.For(rules), d.MaxWait)
		if since.IsZero() {
			since = p.At
		}
		queued = &waiting{since: since, notBefore: nb, coalesced: coalesced}
	}
	advance(time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC))
	return res
}

// The 2026-10-23 incident: five merges to fadymondy.com in seven minutes, four of them only touching
// the desktop app, which is not in the image.
var incidentPushes = []simPush{
	{"68bb1297", at("21:01:58"), []string{"apps/desktop/Sources/Pins.swift"}},
	{"5b5f7ba8", at("21:02:06"), []string{"internal/memory/brain.go", "db/schema/memory.sql", "TODO.md"}},
	{"634896b6", at("21:02:19"), []string{"apps/desktop/Sources/Studio.swift"}},
	{"9f49ff07", at("21:07:39"), []string{"apps/desktop/Sources/UserMenu.swift", "TODO.md"}},
	{"83410062", at("21:09:26"), []string{"apps/desktop/Sources/Home.swift"}},
	{"7eecf515", at("21:09:31"), []string{"apps/desktop/Sources/Avatars.swift"}},
}

func at(hhmmss string) time.Time {
	t, _ := time.Parse("2006-01-02 15:04:05", "2026-10-23 "+hhmmss)
	return t
}

// fadymondyRules is what fadymondy.com's hosbah.toml gets (ignore-based: a new directory builds).
var fadymondyRules = BuildRules{Ignore: []string{
	"assistant/**", "apps/desktop/**", "apps/tunnel/**", "mobile/**", "docs/**", "arts/**", ".claude/**",
	"/*.md", "AGENTS.md", "CLAUDE.md", "codemagic.yaml",
}, Debounce: 90}

const buildTook = 4*time.Minute + 40*time.Second // fadymondy-hb's fast builds on 2026-10-23

func TestSimulateIncident(t *testing.T) {
	before := simulate(incidentPushes, BuildRules{}, Debounce{}, buildTook)
	after := simulate(incidentPushes, fadymondyRules, Debounce{Wait: DefaultDebounce, MaxWait: DefaultMaxWait}, buildTook)
	if len(before.Builds) < 3 {
		t.Fatalf("before: %d builds, expected the back-to-back pile-up", len(before.Builds))
	}
	if len(after.Builds) != 1 || after.Builds[0].SHA != "634896b6" || after.Skipped != 5 {
		t.Fatalf("after: %+v", after)
	}
	debounceOnly := simulate(incidentPushes, BuildRules{}, Debounce{Wait: DefaultDebounce, MaxWait: DefaultMaxWait}, buildTook)
	// Builds here take longer than the gaps between the pushes, so the debounce alone saves nothing in
	// this burst (the path rules do); it must still never build more, and end on the newest commit.
	if len(debounceOnly.Builds) > len(before.Builds) || debounceOnly.Builds[len(debounceOnly.Builds)-1].SHA != "7eecf515" {
		t.Fatalf("debounce only: %+v", debounceOnly)
	}
	t.Logf("incident: %d builds before, %d with debounce only, %d with debounce + path rules", len(before.Builds), len(debounceOnly.Builds), len(after.Builds))
}

func TestSimulateCoalescesABurst(t *testing.T) {
	var burst []simPush
	for i := 0; i < 8; i++ {
		burst = append(burst, simPush{fmt.Sprintf("c%d", i), at("10:00:00").Add(time.Duration(i) * 20 * time.Second), []string{"internal/x.go"}})
	}
	r := simulate(burst, BuildRules{}, Debounce{Wait: 75 * time.Second, MaxWait: 10 * time.Minute}, 3*time.Minute)
	if len(r.Builds) != 1 || r.Builds[0].SHA != "c7" || r.Builds[0].Coalesced != 7 || r.Superseded != 7 {
		t.Fatalf("%+v", r)
	}
	if want := at("10:00:00").Add(140*time.Second + 75*time.Second); !r.Builds[0].Start.Equal(want) {
		t.Fatalf("started %v, want %v", r.Builds[0].Start, want)
	}
	// A steady stream never waits past the max wait.
	var stream []simPush
	for i := 0; i < 40; i++ {
		stream = append(stream, simPush{fmt.Sprintf("s%d", i), at("10:00:00").Add(time.Duration(i) * 60 * time.Second), []string{"internal/x.go"}})
	}
	r = simulate(stream, BuildRules{}, Debounce{Wait: 75 * time.Second, MaxWait: 5 * time.Minute}, 2*time.Minute)
	if len(r.Builds) == 0 || !r.Builds[0].Start.Equal(at("10:05:00")) {
		t.Fatalf("first build of a stream: %+v", r.Builds)
	}
}

// TestDryRunPushHistory replays a real push history (HOSBAH_DRYRUN_PUSHES: a JSON array of
// {"sha","at","files"}, oldest first; see the PR for how fadymondy.com's was exported) before and
// after the change.
func TestDryRunPushHistory(t *testing.T) {
	file := os.Getenv("HOSBAH_DRYRUN_PUSHES")
	if file == "" {
		t.Skip("set HOSBAH_DRYRUN_PUSHES to a push history JSON file")
	}
	raw, err := os.ReadFile(file) //#nosec G304 -- a test input the developer names
	if err != nil {
		t.Fatal(err)
	}
	var pushes []simPush
	if err := json.Unmarshal(raw, &pushes); err != nil {
		t.Fatal(err)
	}
	report := func(name string, r simResult) {
		busy := time.Duration(0)
		for _, b := range r.Builds {
			busy += b.End.Sub(b.Start)
		}
		t.Logf("%-28s %3d builds · %3d skipped · %3d coalesced · %5.0f build-minutes", name, len(r.Builds), r.Skipped, r.Superseded, busy.Minutes())
	}
	deb := Debounce{Wait: DefaultDebounce, MaxWait: DefaultMaxWait}
	t.Logf("%d pushes, %s .. %s", len(pushes), pushes[0].At.Format(time.RFC3339), pushes[len(pushes)-1].At.Format(time.RFC3339))
	report("before (today's behaviour)", simulate(pushes, BuildRules{}, Debounce{}, buildTook))
	report("debounce only", simulate(pushes, BuildRules{}, deb, buildTook))
	report("path rules only", simulate(pushes, fadymondyRules, Debounce{}, buildTook))
	after := simulate(pushes, fadymondyRules, deb, buildTook)
	report("debounce + path rules", after)
	if os.Getenv("HOSBAH_DRYRUN_VERBOSE") != "" {
		for _, b := range after.Builds {
			t.Logf("  build %s  %s → %s  (coalesced %d)", b.SHA, b.Start.Format("15:04:05"), b.End.Format("15:04:05"), b.Coalesced)
		}
	}
}
