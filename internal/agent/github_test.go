package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// fakeGH records every command line and answers from a script. Nothing on the host runs, and
// nothing ever reaches GitHub.
type fakeGH struct {
	mu        sync.Mutex
	calls     []ghCall
	installed bool
	answers   map[string]fakeAnswer // key: argv joined by spaces
	written   map[string]string
	fetched   []string
	stream    map[string][]string // lines fed to OnLine for a command, in order
}

type fakeAnswer struct {
	out, errOut string
	code        int
}

func (f *fakeGH) host() *ghHost {
	return &ghHost{
		run: func(oc *OpContext, c ghCall) (ghResult, error) {
			f.mu.Lock()
			f.calls = append(f.calls, c)
			key := strings.Join(append([]string{c.Name}, c.Args...), " ")
			a, ok := f.answers[key]
			lines := f.stream[key]
			f.mu.Unlock()
			for _, l := range lines {
				if c.OnLine != nil {
					c.OnLine(l)
				}
				if !c.Quiet {
					oc.log("stderr", redact(l, c.Redact))
				}
			}
			if c.Name == "apt-get" && len(c.Args) > 2 && c.Args[2] == "install" || c.Name == "dnf" {
				f.installed = true
			}
			if !ok {
				return ghResult{}, nil
			}
			if a.code != 0 {
				return ghResult{Stdout: a.out, Stderr: a.errOut, Code: a.code}, errors.New("exited")
			}
			return ghResult{Stdout: a.out, Stderr: a.errOut}, nil
		},
		fetch: func(_ context.Context, url string) ([]byte, error) {
			f.fetched = append(f.fetched, url)
			if strings.HasSuffix(url, ".repo") {
				return []byte("[gh-cli]\nbaseurl=https://cli.github.com/packages/rpm\n"), nil
			}
			return []byte("KEY"), nil
		},
		writeFile: func(path string, data []byte, _ os.FileMode) error {
			if f.written == nil {
				f.written = map[string]string{}
			}
			f.written[path] = string(data)
			return nil
		},
		mkdirAll: func(string, os.FileMode) error { return nil },
		lookPath: func(string) (string, error) {
			if f.installed {
				return "/usr/bin/gh", nil
			}
			return "", errors.New("not found")
		},
		rpm: func() bool { return false },
		now: func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) },
	}
}

func (f *fakeGH) argv() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(append([]string{c.Name}, c.Args...), " "))
	}
	return out
}

const apiUserOK = "HTTP/2.0 200 OK\r\nContent-Type: application/json\r\nX-Oauth-Scopes: read:org, repo, gist\r\n\r\n{\"login\":\"octocat\",\"id\":1}"

func loggedInAnswers() map[string]fakeAnswer {
	return map[string]fakeAnswer{
		"gh --version": {out: "gh version 2.63.2 (2024-12-05)\nhttps://github.com/cli/cli/releases/tag/v2.63.2\n"},
		"gh api --hostname github.com --include user": {out: apiUserOK},
	}
}

func notifyCtx(t *testing.T, params any, priv ed25519.PrivateKey) (*OpContext, *[]string, *[]json.RawMessage) {
	t.Helper()
	oc, lines := opCtx(t, params)
	oc.ServerID, oc.priv = "srv-1", priv
	var data []json.RawMessage
	oc.notify = func(line string, d json.RawMessage) {
		*lines = append(*lines, line)
		data = append(data, d)
	}
	return oc, lines, &data
}

func TestGitHubInstallDebian(t *testing.T) {
	f := &fakeGH{answers: map[string]fakeAnswer{"dpkg --print-architecture": {out: "amd64\n"}}}
	for k, v := range loggedInAnswers() {
		f.answers[k] = v
	}
	oc, _ := opCtx(t, map[string]any{})
	res, code, err := f.host().install(oc)
	if err != nil || code != 0 {
		t.Fatalf("install: %v (%d)", err, code)
	}
	want := []string{
		"dpkg --print-architecture",
		"apt-get -o DPkg::Lock::Timeout=300 update",
		"apt-get -o DPkg::Lock::Timeout=300 install -y --no-install-recommends gh",
	}
	got := f.argv()
	for i, w := range want {
		if i >= len(got) || got[i] != w {
			t.Fatalf("command %d: got %q, want %q (all: %q)", i, got, w, got)
		}
	}
	if f.written[ghAptList] != "deb [arch=amd64 signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main\n" {
		t.Errorf("sources list: %q", f.written[ghAptList])
	}
	if f.written[ghKeyringPath] != "KEY" || len(f.fetched) != 1 || f.fetched[0] != ghKeyringURL {
		t.Errorf("keyring: %v %v", f.written, f.fetched)
	}
	if st := res.(agentproto.GitHubStatus); !st.Installed || st.Version != "2.63.2" || st.User != "octocat" {
		t.Errorf("status: %+v", st)
	}

	// Idempotent: a second run changes nothing.
	f.calls = nil
	if _, _, err := f.host().install(oc); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.argv() {
		if strings.HasPrefix(c, "apt-get") {
			t.Errorf("second install ran %q", c)
		}
	}
}

func TestGitHubInstallRHEL(t *testing.T) {
	f := &fakeGH{answers: loggedInAnswers()}
	h := f.host()
	h.rpm = func() bool { return true }
	oc, _ := opCtx(t, map[string]any{})
	if _, _, err := h.install(oc); err != nil {
		t.Fatal(err)
	}
	if got := f.argv(); got[0] != "dnf install -y gh" {
		t.Errorf("rhel: %q", got)
	}
	if !strings.Contains(f.written[ghRPMRepoPath], "cli.github.com") {
		t.Errorf("repo file: %v", f.written)
	}
}

func TestGitHubDeviceLoginReportsTheCodeAsSoonAsItAppears(t *testing.T) {
	f := &fakeGH{installed: true, answers: loggedInAnswers(), stream: map[string][]string{
		"gh auth login --hostname github.com --git-protocol https --web --scopes repo,read:org": {
			"! First copy your one-time code: 3A1F-B2C4",
			"Open this URL to continue in your web browser: https://github.com/login/device",
			"✓ Authentication complete.",
		},
	}}
	oc, lines, data := notifyCtx(t, agentproto.GitHubLoginParams{Mode: "device"}, nil)
	res, code, err := f.host().login(oc)
	if err != nil || code != 0 {
		t.Fatalf("login: %v (%d) %q", err, code, *lines)
	}
	got := f.argv()
	if got[0] != "gh auth login --hostname github.com --git-protocol https --web --scopes repo,read:org" {
		t.Errorf("login argv: %q", got)
	}
	if !contains(got, "gh auth setup-git --hostname github.com") {
		t.Errorf("setup-git missing: %q", got)
	}
	if len(*data) != 1 {
		t.Fatalf("want one device-code event, got %d", len(*data))
	}
	var dc agentproto.GitHubDeviceCode
	_ = json.Unmarshal((*data)[0], &dc)
	if dc.Type != agentproto.DataGitHubDeviceCode || dc.UserCode != "3A1F-B2C4" || dc.VerificationURI != "https://github.com/login/device" ||
		!dc.ExpiresAt.Equal(time.Date(2026, 9, 19, 12, 15, 0, 0, time.UTC)) {
		t.Errorf("device code: %+v", dc)
	}
	if st := res.(agentproto.GitHubStatus); !st.LoggedIn || st.User != "octocat" || strings.Join(st.Scopes, ",") != "gist,read:org,repo" {
		t.Errorf("result: %+v", st)
	}
}

func TestDeviceWatcherParsing(t *testing.T) {
	var got []agentproto.GitHubDeviceCode
	w := &deviceWatcher{now: time.Now, emit: func(c agentproto.GitHubDeviceCode) { got = append(got, c) }}
	for _, l := range []string{"Open this URL to continue in your web browser: https://github.com/login/device?x=1", "noise",
		"! First copy your one-time code: ABCD-1234", "! First copy your one-time code: ZZZZ-9999"} {
		w.line(l)
	}
	if len(got) != 1 || got[0].UserCode != "ABCD-1234" || got[0].VerificationURI != "https://github.com/login/device?x=1" {
		t.Errorf("%+v", got)
	}
}

func TestGitHubTokenLoginNeverLogsTheToken(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	const token = "ghp_S3cr3tT0kenValue1234567890abcdefXYZ"
	box, err := agentproto.SealBox(base64.StdEncoding.EncodeToString(pub), agentproto.GitHubTokenContext("srv-1"), []byte(token))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGH{installed: true, answers: loggedInAnswers()}
	// gh echoing the token in an error must not leak it either.
	f.stream = map[string][]string{"gh auth login --hostname github.com --git-protocol https --with-token": {"validating " + token}}
	oc, lines, _ := notifyCtx(t, agentproto.GitHubLoginParams{Mode: "token", Token: box}, priv)
	if _, _, err := f.host().login(oc); err != nil {
		t.Fatalf("login: %v", err)
	}
	if f.calls[0].Stdin != token+"\n" {
		t.Errorf("the token must reach gh on stdin")
	}
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c.Args, " "), token) {
			t.Errorf("token in argv: %q", c.Args)
		}
	}
	for _, l := range *lines {
		if strings.Contains(l, token) {
			t.Errorf("token in a log line: %q", l)
		}
	}

	// A box sealed for another server does not open.
	other, _ := agentproto.SealBox(base64.StdEncoding.EncodeToString(pub), agentproto.GitHubTokenContext("srv-2"), []byte(token))
	oc, _, _ = notifyCtx(t, agentproto.GitHubLoginParams{Mode: "token", Token: other}, priv)
	if _, _, err := f.host().login(oc); err == nil || strings.Contains(err.Error(), token) {
		t.Errorf("a box for another server: %v", err)
	}
	// A plaintext token is refused before anything runs.
	oc, _, _ = notifyCtx(t, agentproto.GitHubLoginParams{Mode: "token", Token: token}, priv)
	if _, code, err := f.host().login(oc); err == nil || code != 2 {
		t.Errorf("plaintext token: %v %d", err, code)
	}
}

func TestGitHubStatusParsing(t *testing.T) {
	u, s, ok := parseAPIUser(apiUserOK)
	if !ok || u != "octocat" || strings.Join(s, ",") != "gist,read:org,repo" {
		t.Errorf("api user: %q %q %v", u, s, ok)
	}
	for _, out := range []string{
		"github.com\n  ✓ Logged in to github.com account octocat (/etc/cloudy/gh/hosts.yml)\n  - Active account: true\n  - Git operations protocol: https\n  - Token: gho_************************************\n  - Token scopes: 'gist', 'read:org', 'repo'\n",
		"github.com\n  ✓ Logged in to github.com as octocat (/etc/cloudy/gh/hosts.yml)\n  ✓ Token scopes: gist, read:org, repo\n",
	} {
		u, s, ok := parseAuthStatus(out)
		if !ok || u != "octocat" || strings.Join(s, ",") != "gist,read:org,repo" {
			t.Errorf("auth status: %q %q %v", u, s, ok)
		}
	}
	if _, _, ok := parseAuthStatus("You are not logged into any GitHub hosts. To log in, run: gh auth login"); ok {
		t.Error("not logged in parsed as logged in")
	}
	if v := parseGHVersion("gh version 2.40.1 (2023-12-13)\n"); v != "2.40.1" {
		t.Errorf("version %q", v)
	}

	// Not installed; installed but logged out (API fails, auth status says so).
	f := &fakeGH{}
	oc, _ := opCtx(t, map[string]any{})
	if st := f.host().status(oc); st.Installed || st.LoggedIn {
		t.Errorf("%+v", st)
	}
	f = &fakeGH{installed: true, answers: map[string]fakeAnswer{
		"gh --version": {out: "gh version 2.63.2 (2024-12-05)\n"},
		"gh api --hostname github.com --include user": {errOut: "gh: To use GitHub CLI, please run: gh auth login", code: 4},
		"gh auth status --hostname github.com":        {errOut: "You are not logged into any GitHub hosts.", code: 1},
	}}
	if st := f.host().status(oc); !st.Installed || st.LoggedIn || st.Version != "2.63.2" {
		t.Errorf("%+v", st)
	}
}

const repoJSON = `[{"name":"api","nameWithOwner":"octocat/api","isPrivate":true,"defaultBranchRef":{"name":"main"},"url":"https://github.com/octocat/api","updatedAt":"2026-09-01T10:00:00Z","description":"The API"},
{"name":"empty","nameWithOwner":"octocat/empty","isPrivate":false,"defaultBranchRef":null,"url":"https://github.com/octocat/empty","updatedAt":"2025-01-01T00:00:00Z","description":""}]`

func TestGitHubRepos(t *testing.T) {
	f := &fakeGH{installed: true, answers: map[string]fakeAnswer{
		"gh api --hostname github.com user/orgs --paginate --jq .[].login": {out: "acme\n"},
		"gh repo list --limit 50 --json " + ghRepoFields:                   {out: repoJSON},
		"gh repo list acme --limit 48 --json " + ghRepoFields:              {out: `[{"name":"site","nameWithOwner":"acme/site","isPrivate":true,"defaultBranchRef":{"name":"prod"},"url":"https://github.com/acme/site","updatedAt":"2026-09-10T00:00:00Z"}]`},
	}}
	oc, _ := opCtx(t, agentproto.GitHubReposParams{Limit: 50})
	res, _, err := f.host().repos(oc)
	if err != nil {
		t.Fatalf("repos: %v (%q)", err, f.argv())
	}
	r := res.(agentproto.GitHubReposResult)
	if len(r.Repos) != 3 || r.Repos[0].NameWithOwner != "acme/site" || r.Repos[0].DefaultBranch != "prod" ||
		!r.Repos[1].IsPrivate || r.Repos[2].DefaultBranch != "" || strings.Join(r.Owners, ",") != "(you),acme" {
		t.Errorf("%+v", r)
	}

	f.calls = nil
	oc, _ = opCtx(t, agentproto.GitHubReposParams{Owner: "acme", Limit: 10})
	f.answers["gh repo list acme --limit 10 --json "+ghRepoFields] = fakeAnswer{out: "[]"}
	if _, _, err := f.host().repos(oc); err != nil {
		t.Fatal(err)
	}
	if got := f.argv(); len(got) != 1 || got[0] != "gh repo list acme --limit 10 --json "+ghRepoFields {
		t.Errorf("owner listing: %q", got)
	}
	for _, bad := range []agentproto.GitHubReposParams{{Owner: "-x"}, {Owner: "a b"}, {Limit: 1001}, {Owner: "--help"}} {
		oc, _ = opCtx(t, bad)
		if _, code, err := f.host().repos(oc); err == nil || code != 2 {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestGitHubBranches(t *testing.T) {
	f := &fakeGH{installed: true, answers: map[string]fakeAnswer{
		"gh api --hostname github.com repos/octocat/api --jq .default_branch":              {out: "main\n"},
		"gh api --hostname github.com repos/octocat/api/branches --paginate --jq .[].name": {out: "dev\nmain\nrelease/1.x\n"},
	}}
	oc, _ := opCtx(t, agentproto.GitHubBranchesParams{Repo: "octocat/api"})
	res, _, err := f.host().branches(oc)
	if err != nil {
		t.Fatal(err)
	}
	if r := res.(agentproto.GitHubBranchesResult); r.Default != "main" || strings.Join(r.Branches, ",") != "dev,main,release/1.x" {
		t.Errorf("%+v", r)
	}
	for _, bad := range []string{"octocat", "octocat/../x", "a/b/c", "-o/x", "o/.."} {
		oc, _ = opCtx(t, agentproto.GitHubBranchesParams{Repo: bad})
		if _, code, err := f.host().branches(oc); err == nil || code != 2 {
			t.Errorf("%q accepted", bad)
		}
	}
}

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func commitJSON(sha, msg string) string {
	return `{"sha":"` + sha + `","html_url":"https://github.com/octocat/api/commit/` + sha + `","commit":{"message":` + msg +
		`,"author":{"name":"The Octocat","date":"2026-09-01T10:00:00Z"},"committer":{"name":"GitHub","date":"2026-09-02T11:00:00Z"}},` +
		`"author":{"login":"octocat","avatar_url":"https://avatars.githubusercontent.com/u/1?v=4"}}`
}

func TestGitHubCommits(t *testing.T) {
	const call = "gh api --hostname github.com --method GET repos/octocat/api/commits?sha=release/1.x&per_page=2"
	f := &fakeGH{installed: true, answers: map[string]fakeAnswer{
		call: {out: "[" + commitJSON(shaB, `"Fix the login\n\nA long body that is never returned"`) + `,{"sha":"` + shaA + `","html_url":"","commit":{"message":"no author"},"author":null},{"sha":"nope"}]`},
	}}
	oc, _ := opCtx(t, agentproto.GitHubCommitsParams{Repo: "octocat/api", Branch: "release/1.x", Limit: 2})
	res, _, err := f.host().commits(oc)
	if err != nil {
		t.Fatalf("commits: %v (%q)", err, f.argv())
	}
	if got := f.argv(); len(got) != 1 || got[0] != call {
		t.Fatalf("argv: %q", got)
	}
	r := res.(agentproto.GitHubCommitsResult)
	if len(r.Commits) != 2 {
		t.Fatalf("%+v", r)
	}
	c := r.Commits[0]
	if c.SHA != shaB || c.ShortSHA != "bbbbbbb" || c.Message != "Fix the login" || c.AuthorName != "The Octocat" || c.AuthorLogin != "octocat" ||
		c.AuthorAvatarURL != "https://avatars.githubusercontent.com/u/1?v=4" || !c.CommittedAt.Equal(time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC)) ||
		c.URL != "https://github.com/octocat/api/commit/"+shaB {
		t.Errorf("commit: %+v", c)
	}
	if r.Commits[1].SHA != shaA || r.Commits[1].AuthorLogin != "" {
		t.Errorf("a commit without a GitHub author: %+v", r.Commits[1])
	}

	for _, bad := range []agentproto.GitHubCommitsParams{
		{Repo: "octocat", Branch: "main"}, {Repo: "octocat/api/x", Branch: "main"}, {Repo: "octocat/api", Branch: ""},
		{Repo: "octocat/api", Branch: "-X"}, {Repo: "octocat/api", Branch: "main&per_page=1000"}, {Repo: "octocat/api", Branch: "a..b"},
		{Repo: "octocat/api", Branch: "main?x"}, {Repo: "octocat/api", Branch: "ma in"}, {Repo: "octocat/api", Branch: "{branch}"},
		{Repo: "octocat/api", Branch: "main", Limit: 101}, {Repo: "octocat/api", Branch: "main", Limit: -1}, {Repo: "o/..", Branch: "main"},
	} {
		f.calls = nil
		oc, _ = opCtx(t, bad)
		if _, code, err := f.host().commits(oc); err == nil || code != 2 || len(f.argv()) != 0 {
			t.Errorf("%+v accepted (ran %q)", bad, f.argv())
		}
	}

	// gh's own reason comes back; without gh nothing runs.
	f = &fakeGH{installed: true, answers: map[string]fakeAnswer{
		"gh api --hostname github.com --method GET repos/octocat/api/commits?sha=main&per_page=30": {errOut: "gh: Not Found (HTTP 404)", code: 1}}}
	oc, _ = opCtx(t, agentproto.GitHubCommitsParams{Repo: "octocat/api", Branch: "main"})
	if _, code, err := f.host().commits(oc); err == nil || code != 1 || !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("a failing gh: %v %d", err, code)
	}
	f = &fakeGH{}
	if _, code, err := f.host().commits(oc); err == nil || code != 1 || !strings.Contains(err.Error(), "not installed") || len(f.argv()) != 0 {
		t.Errorf("without gh: %v %d %q", err, code, f.argv())
	}
}

func TestGitHubCompare(t *testing.T) {
	var files []string
	for i := 0; i < 305; i++ {
		files = append(files, `{"filename":"src/f`+strings.Repeat("x", i%3)+`.go","status":"modified","additions":3,"deletions":1,"patch":"@@ SECRET-PATCH-BODY @@"}`)
	}
	call := "gh api --hostname github.com --method GET repos/octocat/api/compare/" + shaA + "...main?per_page=100"
	f := &fakeGH{installed: true, answers: map[string]fakeAnswer{
		call: {out: `{"ahead_by":2,"behind_by":1,"total_commits":2,"commits":[` + commitJSON(shaA, `"older"`) + "," + commitJSON(shaB, `"newer"`) +
			`],"files":[` + strings.Join(files, ",") + `]}`},
	}}
	oc, _ := opCtx(t, agentproto.GitHubCompareParams{Repo: "octocat/api", Base: shaA, Head: "main"})
	res, _, err := f.host().compare(oc)
	if err != nil {
		t.Fatalf("compare: %v (%q)", err, f.argv())
	}
	r := res.(agentproto.GitHubCompareResult)
	if r.AheadBy != 2 || r.BehindBy != 1 || r.TotalCommits != 2 || len(r.Commits) != 2 || r.Commits[0].SHA != shaB || r.Commits[0].Message != "newer" {
		t.Errorf("newest first: %+v", r.Commits)
	}
	if len(r.Files) != agentproto.MaxGitHubCompareFiles || !r.Truncated || r.Files[0].Status != "modified" || r.Files[0].Additions != 3 || r.Files[0].Deletions != 1 {
		t.Errorf("files: %d truncated=%v", len(r.Files), r.Truncated)
	}
	if raw, _ := json.Marshal(r); strings.Contains(string(raw), "SECRET-PATCH-BODY") || strings.Contains(string(raw), "patch") {
		t.Error("a patch body is in the result")
	}
	for _, bad := range []agentproto.GitHubCompareParams{
		{Repo: "octocat/api", Base: "", Head: "main"}, {Repo: "octocat/api", Base: "main", Head: ""}, {Repo: "octocat/api", Base: "a...b", Head: "main"},
		{Repo: "octocat/api", Base: "main", Head: "--jq"}, {Repo: "octocat/api", Base: "main", Head: "x?y=1"}, {Repo: "../api", Base: "a", Head: "b"},
		{Repo: "octocat/api", Base: "main", Head: "x/"}, {Repo: "octocat/api", Base: "main#", Head: "x"},
	} {
		f.calls = nil
		oc, _ = opCtx(t, bad)
		if _, code, err := f.host().compare(oc); err == nil || code != 2 || len(f.argv()) != 0 {
			t.Errorf("%+v accepted (ran %q)", bad, f.argv())
		}
	}
}

func TestGitHubPulls(t *testing.T) {
	const call = "gh api --hostname github.com --method GET repos/octocat/api/pulls?state=open&sort=updated&direction=desc&per_page=50"
	f := &fakeGH{installed: true, answers: map[string]fakeAnswer{
		call: {out: `[{"number":12,"title":"Add billing","draft":true,"html_url":"https://github.com/octocat/api/pull/12","updated_at":"2026-09-10T00:00:00Z",` +
			`"user":{"login":"hubot"},"head":{"ref":"feat/billing"},"base":{"ref":"main"}},{"number":0,"title":"bogus"}]`},
	}}
	oc, _ := opCtx(t, agentproto.GitHubPullsParams{Repo: "octocat/api"})
	res, _, err := f.host().pulls(oc)
	if err != nil {
		t.Fatalf("pulls: %v (%q)", err, f.argv())
	}
	r := res.(agentproto.GitHubPullsResult)
	if len(r.Pulls) != 1 {
		t.Fatalf("%+v", r)
	}
	if p := r.Pulls[0]; p.Number != 12 || p.Title != "Add billing" || p.AuthorLogin != "hubot" || p.HeadRef != "feat/billing" || p.BaseRef != "main" ||
		!p.Draft || p.URL != "https://github.com/octocat/api/pull/12" || !p.UpdatedAt.Equal(time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("%+v", p)
	}
	for _, bad := range []agentproto.GitHubPullsParams{{Repo: "octocat"}, {Repo: "octocat/api", Limit: 101}, {Repo: "-o/api"}, {Repo: "o/a b"}} {
		f.calls = nil
		oc, _ = opCtx(t, bad)
		if _, code, err := f.host().pulls(oc); err == nil || code != 2 || len(f.argv()) != 0 {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestGitHubLogout(t *testing.T) {
	f := &fakeGH{installed: true, answers: map[string]fakeAnswer{
		"gh api --hostname github.com --include user": {code: 4},
		"gh auth status --hostname github.com":        {code: 1},
	}}
	oc, _ := opCtx(t, map[string]any{})
	if _, _, err := f.host().logout(oc); err != nil {
		t.Fatal(err)
	}
	got := f.argv()
	for _, w := range []string{"gh auth logout --hostname github.com",
		"git config --global --unset-all credential.https://github.com.helper"} {
		if !contains(got, w) {
			t.Errorf("missing %q in %q", w, got)
		}
	}
}

func TestGitHubCloneHelperArgs(t *testing.T) {
	args := ghCredentialArgs("!'/usr/bin/gh' auth git-credential")
	if strings.Join(args, " ") != "-c credential.https://github.com.helper= -c credential.https://github.com.helper=!'/usr/bin/gh' auth git-credential" {
		t.Errorf("%q", args)
	}
	if e := ghFetchError(false, "main").Error(); !strings.Contains(e, "connect GitHub on this server") {
		t.Errorf("%s", e)
	}
	for in, want := range map[string]string{
		"https://github.com/Octo-Cat/api.git": "Octo-Cat/api", "https://github.com/o/r/": "o/r",
		"https://gitlab.com/o/r": "", "https://github.com/o": "", "git://github.com/o/r": "",
		"git@github.com:octocat/api.git": "octocat/api", "ssh://git@github.com/octocat/api": "octocat/api", "git@gitlab.com:o/r.git": "",
		"git@github.com:o/r/x.git": "", "https://github.com.evil.test/o/r": "",
	} {
		if got := agentproto.GitHubRepoOf(in); got != want {
			t.Errorf("GitHubRepoOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
