package deploy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The 2026-10-25 regression: fadymondy.com #467 was desktop-only, but its branch had merged master in
// ("Merge origin/master into platform/p1-desktop-accounts"), and GitHub lists that commit's diff
// against its first parent, the feature branch: every server file master had gained. The push
// payload's union built; the before...after comparison (what the merge did to master) is desktop only.
// The payload below is that push, cut down to a few files per commit.
const pr467Push = `{"ref":"refs/heads/master","before":"2c8573f3a1","after":"1dedfa0ff7",
	"head_commit":{"id":"1dedfa0ff7","message":"Merge pull request #467 from fadymondy/platform/p1-desktop-accounts\n\ndesktop: agent accounts"},
	"commits":[
		{"id":"44c2215f","message":"desktop: AppleAIKit agent-platform accounts client","added":["apps/desktop/Packages/AppleAIKit/Sources/AppleAIKit/Team/CredentialLink.swift"]},
		{"id":"1768cd0f","message":"Merge origin/master into platform/p1-desktop-accounts","modified":["internal/mcp/agent_platform.go","internal/adk/runtime.go","cmd/fady/main.go","db/team_runtime_credentials.postgres.sql","apps/tunnel/node.go"]},
		{"id":"1dedfa0ff7","message":"Merge pull request #467","modified":["apps/desktop/Apps/Shared/Team/TeamPage.swift","apps/desktop/AppleAI.xcodeproj/project.pbxproj"]}
	]}`

// pr467Compare is GitHub's 2c8573f3...1dedfa0f: the merge against its first parent, master's old head.
var pr467Compare = []string{
	"apps/desktop/Packages/AppleAIKit/Sources/AppleAIKit/Team/CredentialLink.swift",
	"apps/desktop/Apps/Shared/Team/TeamPage.swift",
	"apps/desktop/AppleAI.xcodeproj/project.pbxproj",
}

// fakeComparer answers base...head from a table, and records what it was asked.
type fakeComparer struct {
	answers map[string][]string // "base...head" → paths; missing = cannot say
	asked   []string
}

func (f *fakeComparer) cmp(_ context.Context, base, head string) ([]string, bool) {
	key := base + "..." + head
	f.asked = append(f.asked, key)
	paths, ok := f.answers[key]
	return paths, ok
}

func pushOf(before, after string, files ...string) PushPayload {
	return PushPayload{Ref: "refs/heads/master", Before: before, After: after, Commits: []PushCommit{{ID: after, Modified: files}}}
}

// TestPushDecision is the skip-or-build decision for a push (NetChanges + Decide) under fadymondy.com's
// rules, with and without a git host that can compare commits.
func TestPushDecision(t *testing.T) {
	var merge PushPayload
	if err := json.Unmarshal([]byte(pr467Push), &merge); err != nil {
		t.Fatal(err)
	}
	github := &fakeComparer{answers: map[string][]string{
		"2c8573f3a1...1dedfa0ff7": pr467Compare,
		"a1...b2":                 {"apps/desktop/A.swift", "docs/x.md"},
		"b2...c3":                 {"apps/desktop/A.swift", "internal/x.go"},
	}}
	cases := []struct {
		name   string
		push   PushPayload
		cmp    Comparer
		build  bool
		reason string // prefix
	}{
		{"every file ignored", pushOf("a1", "b2", "apps/desktop/A.swift", "docs/x.md", "README.md"), nil, false, "no image changes (3 files"},
		{"one ignored file", pushOf("a1", "b2", "assistant/.assistant/log.md"), nil, false, "no image changes (assistant/.assistant/log.md)"},
		{"mixed", pushOf("a1", "b2", "apps/desktop/A.swift", "internal/x.go"), nil, true, "internal/x.go changed"},
		{"hosbah.toml always builds", pushOf("a1", "b2", "docs/x.md", "hosbah.toml"), nil, true, "hosbah.toml changed"},
		{"normalised paths: /x and ./x are x", pushOf("a1", "b2", "/apps/desktop/A.swift", "./docs/x.md", "  mobile/app.tsx"), nil, false, "no image changes"},
		{"normalised paths: ./hosbah.toml", pushOf("a1", "b2", "./hosbah.toml"), nil, true, "hosbah.toml changed"},
		{"normalised paths: /internal/x.go", pushOf("a1", "b2", "/internal/x.go"), nil, true, "internal/x.go changed"},
		// Fallback: no comparison, so the union, which includes what the branch merged in from master.
		{"merge commit, payload only (superset, builds)", merge, nil, true, "internal/mcp/agent_platform.go changed"},
		// The comparison is before...after: the merge against its first parent.
		{"merge commit, compared to its first parent", merge, github.cmp, false, "no image changes (3 files"},
		{"comparison wins over a payload missing a file", pushOf("b2", "c3", "apps/desktop/A.swift"), github.cmp, true, "internal/x.go changed"},
		{"comparison unavailable falls back to the payload", pushOf("x9", "y9", "docs/x.md"), github.cmp, false, "no image changes (docs/x.md)"},
		// The empty-file-list fallback: nothing proves the image is untouched, so it builds.
		{"no commits in the payload", PushPayload{Ref: "refs/heads/master", Before: "a1", After: "b2"}, nil, true, "the changed files are not known"},
		{"commits without files", PushPayload{Before: "a1", After: "b2", Commits: []PushCommit{{ID: "b2"}}}, nil, true, "the changed files are not known"},
		{"force push", PushPayload{Before: "a1", After: "b2", Forced: true, Commits: []PushCommit{{ID: "b2", Modified: []string{"docs/x.md"}}}}, nil, true, "the changed files are not known"},
		{"new branch is never compared", PushPayload{Before: strings.Repeat("0", 40), After: "b2", Created: true, Commits: []PushCommit{{ID: "b2", Modified: []string{"docs/x.md"}}}}, github.cmp, true, "the changed files are not known"},
	}
	for _, c := range cases {
		changed, known := c.push.NetChanges(context.Background(), c.cmp)
		build, reason := fadymondyRules.Decide(changed, known)
		if build != c.build || !strings.HasPrefix(reason, c.reason) {
			t.Errorf("%s: build %v (%s), want %v (%s…)", c.name, build, reason, c.build, c.reason)
		}
	}
	for _, k := range github.asked {
		if strings.HasPrefix(k, "0000") {
			t.Errorf("compared a new branch: %s", k)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	for in, want := range map[string]string{
		"x": "x", "/x": "x", "./x": "x", ".//x": "x", "/./x": "x", " docs/a.md ": "docs/a.md", "": "", "./": "",
		"apps/desktop/A.swift": "apps/desktop/A.swift", ".github/x.yml": ".github/x.yml",
	} {
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
	var p PushPayload
	_ = json.Unmarshal([]byte(`{"before":"a","after":"b","commits":[{"added":["/docs/a.md"],"modified":["./docs/a.md","docs/a.md"]}]}`), &p)
	if changed, known := p.Changed(); !known || len(changed) != 1 || changed[0] != "docs/a.md" {
		t.Fatalf("one file three ways: %v", changed)
	}
}

// A debounce batch: the pushes inside one debounce window. Each push is decided as it arrives, so a
// batch of ignored pushes never queues a build, and a mixed batch builds once, at its newest commit.
func TestDebounceBatches(t *testing.T) {
	deb := Debounce{Wait: DefaultDebounce, MaxWait: DefaultMaxWait}
	t0 := at("04:40:00")
	ignored := []simPush{
		{"2c8573f3", t0, []string{"apps/tunnel/node.go", "apps/tunnel/go.sum"}},
		{"1dedfa0f", t0.Add(70 * time.Second), pr467Compare},
		{"626e4e37", t0.Add(4 * time.Minute), []string{"apps/desktop/Apps/iOS/Sources/Live/PhoneLiveActivities.swift"}},
		{"67c8d862", t0.Add(5 * time.Minute), []string{"apps/desktop/AppleAI.xcodeproj/project.pbxproj", "docs/x.md", "README.md"}},
	}
	if r := simulate(ignored, fadymondyRules, deb, buildTook); len(r.Builds) != 0 || r.Skipped != 4 {
		t.Fatalf("an all-ignored batch: %+v", r)
	}
	mixed := []simPush{
		ignored[0],
		{"b1595ae6", t0.Add(70 * time.Second), []string{"internal/adk/runtime.go", "apps/desktop/A.swift"}},
		{"90902961", t0.Add(80 * time.Second), []string{"web/app/page.tsx"}},
		ignored[2], ignored[3],
	}
	r := simulate(mixed, fadymondyRules, deb, buildTook)
	if len(r.Builds) != 1 || r.Builds[0].SHA != "90902961" || r.Builds[0].Coalesced != 1 {
		t.Fatalf("a mixed batch: %+v", r)
	}
}

// Compare against a fake GitHub: the endpoint and query, renames counted under both names, and the
// 300-file cap reported as truncated.
func TestGitHubCompare(t *testing.T) {
	files := `[{"filename":"apps/desktop/A.swift"},{"filename":"docs/new.md","previous_filename":"internal/old.md"}]`
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/42/access_tokens":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"ghs_abc","expires_at":"2027-01-15T09:00:00Z"}`))
		case r.URL.Path == "/repos/fadymondy/fadymondy.com/compare/2c8573f3...1dedfa0f" && r.URL.Query().Get("per_page") == "1" && r.Header.Get("Authorization") == "Bearer ghs_abc":
			_, _ = w.Write([]byte(`{"status":"ahead","files":` + files + `}`))
		case r.URL.Path == "/repos/fadymondy/fadymondy.com/compare/aaaaaaa...bbbbbbb":
			many := make([]string, maxCompareFiles)
			for i := range many {
				many[i] = `{"filename":"docs/x.md"}`
			}
			_, _ = w.Write([]byte(`{"status":"ahead","files":[` + strings.Join(many, ",") + `]}`))
		default:
			http.Error(w, "wrong endpoint "+r.URL.String(), 404)
		}
	}))
	defer fake.Close()
	key := testRSAKey(t)
	g := &githubApp{appID: 1234, key: key, secret: []byte("x"), api: fake.URL, http: fake.Client(), now: time.Now}
	c, err := g.Compare(context.Background(), 42, "fadymondy", "fadymondy.com", "2c8573f3", "1dedfa0f")
	if err != nil || c.Status != "ahead" || c.Truncated || strings.Join(c.Files, ",") != "apps/desktop/A.swift,docs/new.md,internal/old.md" {
		t.Fatalf("compare %+v %v", c, err)
	}
	if c, err := g.Compare(context.Background(), 42, "fadymondy", "fadymondy.com", "aaaaaaa", "bbbbbbb"); err != nil || !c.Truncated {
		t.Fatalf("300 files must be truncated: %v %v", c.Truncated, err)
	}
	for _, bad := range [][2]string{{"--upload-pack", "b"}, {"a", "b?x=1"}, {"a..b", "c"}} {
		if _, err := g.Compare(context.Background(), 42, "fadymondy", "fadymondy.com", bad[0], bad[1]); err == nil {
			t.Errorf("compared %q...%q", bad[0], bad[1])
		}
	}

	// Through the service's comparer: only an exact (ahead, complete) answer counts, asked once per push.
	s := &Service{log: testLogger()}
	s.SetGitHub(g)
	cmp := s.pushComparer(42, "https://github.com/fadymondy/fadymondy.com")
	if paths, ok := cmp(context.Background(), "2c8573f3", "1dedfa0f"); !ok || len(paths) != 3 {
		t.Fatalf("comparer %v %v", paths, ok)
	}
	if _, ok := cmp(context.Background(), "aaaaaaa", "bbbbbbb"); ok {
		t.Error("a truncated comparison counted")
	}
	if _, ok := cmp(context.Background(), "ccccccc", "ddddddd"); ok {
		t.Error("a failed comparison counted")
	}
	if s.pushComparer(42, "https://gitlab.com/x/y") != nil || s.pushComparer(0, "https://github.com/x/y") != nil {
		t.Error("a comparer without a GitHub repository or installation")
	}
}

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
