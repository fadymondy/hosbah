package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
GitHub through the GitHub CLI (gh) on this server.

gh is installed from GitHub's own package repository (cli.github.com/packages, apt or dnf) and keeps
its login in agentproto.GHConfigDir (root-only) — never in the agent's HOME, which systemd may not
even set. Every gh this agent runs, and every git clone of a github.com repository, gets
GH_CONFIG_DIR pointing there, so the login a human made through Hosbah is the one deploy clones use.

Nothing here runs a shell: fixed argv lists only. A token (token mode) reaches gh on stdin, never in
argv, the environment or a log line.
*/

// ghCall is one command line. The seam tests replace (ghHost.run).
type ghCall struct {
	Name   string
	Args   []string
	Stdin  string            // fed on stdin (a token); never logged
	Quiet  bool              // do not relay output lines to the operation's log
	OnLine func(line string) // every output line (stdout and stderr) as it arrives
	Redact string            // replaced by "***" in anything logged or returned as an error
}

type ghResult struct {
	Stdout string
	Stderr string
	Code   int
}

// ghHost is what the gh handlers need from the machine; tests swap every field.
type ghHost struct {
	run       func(oc *OpContext, c ghCall) (ghResult, error)
	fetch     func(ctx context.Context, url string) ([]byte, error)
	writeFile func(path string, data []byte, perm os.FileMode) error
	mkdirAll  func(path string, perm os.FileMode) error
	lookPath  func(name string) (string, error)
	rpm       func() bool
	now       func() time.Time
}

func newGHHost() *ghHost {
	return &ghHost{run: runGH, fetch: fetchURL, writeFile: os.WriteFile, mkdirAll: os.MkdirAll, lookPath: exec.LookPath,
		rpm: func() bool { return hostPkgManager().rpm }, now: time.Now}
}

// ghEnv is the environment of every gh (and of git when gh is its credential helper): gh's own
// config directory, no prompts, no colour, no update checks, no browser — and no token variable
// inherited from the agent's environment overriding the stored login.
func ghEnv() []string {
	env := []string{"GH_CONFIG_DIR=" + agentproto.GHConfigDir, "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1",
		"GH_NO_EXTENSION_UPDATE_NOTIFIER=1", "GH_SPINNER_DISABLED=1", "NO_COLOR=1", "CLICOLOR=0", "BROWSER=true", "GH_BROWSER=true",
		"GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive"}
	if os.Getenv("HOME") == "" { // systemd units may run without one; gh auth setup-git writes ~/.gitconfig
		home := "/root"
		if h, err := os.UserHomeDir(); err == nil && h != "" {
			home = h
		}
		env = append(env, "HOME="+home)
	}
	return env
}

// baseEnv is os.Environ without variables that would override gh's stored login.
func baseEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST", "GH_CONFIG_DIR":
			continue
		}
		out = append(out, kv)
	}
	return out
}

func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

// runGH runs one command line, streaming its output line by line.
func runGH(oc *OpContext, c ghCall) (ghResult, error) {
	oc.Info("$ %s", shellQuote(append([]string{c.Name}, c.Args...)))
	cmd := exec.CommandContext(oc.Ctx, c.Name, c.Args...) //#nosec G204 -- fixed argv from the typed handlers
	cmd.Env = append(baseEnv(), ghEnv()...)
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ghResult{Code: 127}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ghResult{Code: 127}, err
	}
	if err := cmd.Start(); err != nil {
		return ghResult{Code: 127}, fmt.Errorf("%s: %w", c.Name, err)
	}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		out, eb  bytes.Buffer
		limitOut = 32 << 20
	)
	pump := func(r io.Reader, buf *bytes.Buffer, stream string) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		for sc.Scan() {
			line := sc.Text()
			mu.Lock()
			if buf.Len() < limitOut {
				buf.WriteString(line)
				buf.WriteByte('\n')
			}
			mu.Unlock()
			if c.OnLine != nil {
				c.OnLine(line)
			}
			if !c.Quiet {
				l := redact(line, c.Redact)
				if len(l) > maxLine {
					l = l[:maxLine] + "…"
				}
				oc.log(stream, l)
			}
		}
	}
	wg.Add(2)
	go pump(stdout, &out, "stdout")
	go pump(stderr, &eb, "stderr")
	wg.Wait()
	err = cmd.Wait()
	res := ghResult{Stdout: out.String(), Stderr: redact(eb.String(), c.Redact)}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case errors.As(err, &exitErr):
		res.Code = exitErr.ExitCode()
		return res, fmt.Errorf("%s exited with status %d", c.Name, res.Code)
	default:
		res.Code = 1
		return res, err
	}
}

func fetchURL(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// gh runs gh with args.
func (g *ghHost) gh(oc *OpContext, c ghCall, args ...string) (ghResult, error) {
	c.Name, c.Args = "gh", args
	return g.run(oc, c)
}

func (g *ghHost) installed() bool {
	_, err := g.lookPath("gh")
	return err == nil
}

// lastErr is the most useful line of a failed command's output (gh prints its reason last).
func lastErr(r ghResult, err error) error {
	for _, s := range []string{r.Stderr, r.Stdout} {
		lines := strings.Split(strings.TrimSpace(s), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if l := strings.TrimSpace(lines[i]); l != "" {
				return errors.New(clip(l, 300))
			}
		}
	}
	return err
}

// ---- github.install ------------------------------------------------------------------------------

const (
	ghKeyringURL  = "https://cli.github.com/packages/githubcli-archive-keyring.gpg"
	ghKeyringPath = "/etc/apt/keyrings/githubcli-archive-keyring.gpg"
	ghAptList     = "/etc/apt/sources.list.d/github-cli.list"
	ghRPMRepoURL  = "https://cli.github.com/packages/rpm/gh-cli.repo"
	ghRPMRepoPath = "/etc/yum.repos.d/gh-cli.repo"
)

// aptLockWait: the host lane may be running apt (provisioning) while the github lane installs gh.
var aptLockWait = []string{"-o", "DPkg::Lock::Timeout=300"}

func (g *ghHost) install(oc *OpContext) (any, int, error) {
	if g.installed() {
		oc.Info("the GitHub CLI is already installed — nothing to do")
		return g.status(oc), 0, nil
	}
	if err := g.ensureInstalled(oc); err != nil {
		return nil, 1, err
	}
	return g.status(oc), 0, nil
}

// ensureInstalled installs gh from GitHub's package repository (Debian/Ubuntu or the RHEL family).
func (g *ghHost) ensureInstalled(oc *OpContext) error {
	if g.installed() {
		return nil
	}
	oc.Info("installing the GitHub CLI from cli.github.com/packages")
	if g.rpm() {
		repo, err := g.fetch(oc.Ctx, ghRPMRepoURL)
		if err != nil {
			return fmt.Errorf("could not download the GitHub CLI repository file: %w", err)
		}
		if !bytes.Contains(repo, []byte("cli.github.com")) {
			return errors.New("the GitHub CLI repository file is not what was expected — nothing was installed")
		}
		if err := g.writeFile(ghRPMRepoPath, repo, 0o644); err != nil { //#nosec G306 -- a repo file is world-readable
			return err
		}
		if r, err := g.run(oc, ghCall{Name: "dnf", Args: []string{"install", "-y", "gh"}}); err != nil {
			return lastErr(r, err)
		}
	} else {
		key, err := g.fetch(oc.Ctx, ghKeyringURL)
		if err != nil {
			return fmt.Errorf("could not download the GitHub CLI signing key: %w", err)
		}
		if len(key) == 0 {
			return errors.New("the GitHub CLI signing key is empty — nothing was installed")
		}
		if err := g.mkdirAll(filepath.Dir(ghKeyringPath), 0o755); err != nil {
			return err
		}
		if err := g.writeFile(ghKeyringPath, key, 0o644); err != nil { //#nosec G306 -- apt reads keyrings as _apt
			return err
		}
		r, err := g.run(oc, ghCall{Name: "dpkg", Args: []string{"--print-architecture"}, Quiet: true})
		arch := strings.TrimSpace(r.Stdout)
		if err != nil || !regexp.MustCompile(`^[a-z0-9]+$`).MatchString(arch) {
			return errors.New("could not read the package architecture (dpkg --print-architecture)")
		}
		line := "deb [arch=" + arch + " signed-by=" + ghKeyringPath + "] https://cli.github.com/packages stable main\n"
		if err := g.writeFile(ghAptList, []byte(line), 0o644); err != nil { //#nosec G306 -- a sources list is world-readable
			return err
		}
		if r, err := g.run(oc, ghCall{Name: "apt-get", Args: append(append([]string{}, aptLockWait...), "update")}); err != nil {
			return lastErr(r, err)
		}
		if r, err := g.run(oc, ghCall{Name: "apt-get", Args: append(append([]string{}, aptLockWait...), "install", "-y", "--no-install-recommends", "gh")}); err != nil {
			return lastErr(r, err)
		}
	}
	if !g.installed() {
		return errors.New("the GitHub CLI was installed but gh is not on the PATH")
	}
	return nil
}

// ---- github.status -------------------------------------------------------------------------------

var ghVersionRe = regexp.MustCompile(`gh version ([0-9][0-9A-Za-z.\-+]*)`)

// parseGHVersion reads `gh --version`.
func parseGHVersion(s string) string {
	if m := ghVersionRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// parseAPIUser reads `gh api --include user`: HTTP headers (X-Oauth-Scopes) then the JSON body.
func parseAPIUser(s string) (user string, scopes []string, ok bool) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	head, body, found := strings.Cut(s, "\n\n")
	if !found {
		head, body = "", s
	}
	for _, l := range strings.Split(head, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "X-Oauth-Scopes") {
			scopes = splitScopes(v)
		}
	}
	var u struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &u); err != nil || u.Login == "" {
		return "", nil, false
	}
	return u.Login, scopes, true
}

func splitScopes(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		s = strings.Trim(strings.TrimSpace(s), "'\"")
		if s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

var (
	ghStatusUserRe   = regexp.MustCompile(`Logged in to github\.com (?:account|as) ([A-Za-z0-9-]+)`)
	ghStatusScopesRe = regexp.MustCompile(`Token scopes: (.*)`)
)

// parseAuthStatus reads `gh auth status --hostname github.com` (both the old "as X" and the newer
// "account X" wording). The token line gh prints is masked, and this output is never logged.
func parseAuthStatus(s string) (user string, scopes []string, ok bool) {
	m := ghStatusUserRe.FindStringSubmatch(s)
	if m == nil {
		return "", nil, false
	}
	if sm := ghStatusScopesRe.FindStringSubmatch(s); sm != nil && !strings.Contains(sm[1], "none") {
		scopes = splitScopes(sm[1])
	}
	return m[1], scopes, true
}

func (g *ghHost) status(oc *OpContext) agentproto.GitHubStatus {
	st := agentproto.GitHubStatus{}
	if !g.installed() {
		return st
	}
	st.Installed = true
	if r, err := g.gh(oc, ghCall{Quiet: true}, "--version"); err == nil {
		st.Version = parseGHVersion(r.Stdout)
	}
	if r, err := g.gh(oc, ghCall{Quiet: true}, "api", "--hostname", agentproto.GitHubHost, "--include", "user"); err == nil {
		if user, scopes, ok := parseAPIUser(r.Stdout); ok {
			st.LoggedIn, st.User, st.Scopes = true, user, scopes
			return st
		}
	}
	// The API did not answer (or the login is gone): what gh itself says.
	r, _ := g.gh(oc, ghCall{Quiet: true}, "auth", "status", "--hostname", agentproto.GitHubHost)
	if user, scopes, ok := parseAuthStatus(r.Stdout + "\n" + r.Stderr); ok {
		st.LoggedIn, st.User, st.Scopes = true, user, scopes
	}
	return st
}

func (g *ghHost) statusOp(oc *OpContext) (any, int, error) {
	st := g.status(oc)
	switch {
	case !st.Installed:
		oc.Info("the GitHub CLI is not installed on this server")
	case !st.LoggedIn:
		oc.Info("gh %s is installed; not logged in to GitHub", st.Version)
	default:
		oc.Info("gh %s, logged in to GitHub as %s (scopes: %s)", st.Version, st.User, strings.Join(st.Scopes, ", "))
	}
	return st, 0, nil
}

// ---- github.login --------------------------------------------------------------------------------

var (
	ghCodeRe = regexp.MustCompile(`one-time code: ([A-Z0-9]{4}-[A-Z0-9]{4})`)
	ghURLRe  = regexp.MustCompile(`https://github\.com/login/device[^\s]*`)
)

// deviceFlowTTL: GitHub's device codes expire after 15 minutes.
const deviceFlowTTL = 15 * time.Minute

// deviceWatcher reads gh's device-flow output and reports the code as soon as it appears.
type deviceWatcher struct {
	mu   sync.Mutex
	url  string
	sent bool
	now  func() time.Time
	emit func(agentproto.GitHubDeviceCode)
}

func (w *deviceWatcher) line(l string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if u := ghURLRe.FindString(l); u != "" {
		w.url = u
	}
	m := ghCodeRe.FindStringSubmatch(l)
	if m == nil || w.sent {
		return
	}
	w.sent = true
	url := w.url
	if url == "" {
		url = "https://github.com/login/device"
	}
	w.emit(agentproto.GitHubDeviceCode{Type: agentproto.DataGitHubDeviceCode, UserCode: m[1], VerificationURI: url,
		ExpiresAt: w.now().Add(deviceFlowTTL).UTC()})
}

func (g *ghHost) login(oc *OpContext) (any, int, error) {
	var p agentproto.GitHubLoginParams
	if err := json.Unmarshal(oc.Params, &p); err != nil {
		return nil, 2, fmt.Errorf("bad params: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	token := ""
	if p.Mode == agentproto.GitHubModeToken {
		pt, err := agentproto.OpenBox(oc.priv, agentproto.GitHubTokenContext(oc.ServerID), p.Token)
		if err != nil {
			return nil, 2, errors.New("the token could not be opened with this server's key")
		}
		token = strings.TrimSpace(string(pt))
		if token == "" || len(token) > 255 || strings.ContainsAny(token, " \t\r\n") {
			return nil, 2, errors.New("the token is not a GitHub token")
		}
	}
	if err := g.ensureInstalled(oc); err != nil {
		return nil, 1, err
	}
	if err := g.mkdirAll(agentproto.GHConfigDir, 0o700); err != nil {
		return nil, 1, err
	}
	base := []string{"auth", "login", "--hostname", agentproto.GitHubHost, "--git-protocol", "https"}
	if p.Mode == agentproto.GitHubModeToken {
		oc.Info("logging in to GitHub with a token")
		if r, err := g.gh(oc, ghCall{Stdin: token + "\n", Redact: token}, append(base, "--with-token")...); err != nil {
			return nil, 1, fmt.Errorf("GitHub refused the token: %v", redact(lastErr(r, err).Error(), token))
		}
	} else {
		w := &deviceWatcher{now: g.now, emit: func(c agentproto.GitHubDeviceCode) {
			oc.Notify(fmt.Sprintf("Open %s and enter the code %s (it expires in 15 minutes)", c.VerificationURI, c.UserCode), c)
		}}
		// Without a TTY (and with GH_PROMPT_DISABLED) gh prints the code and the URL instead of
		// waiting for Enter and opening a browser, then polls GitHub until the code is entered or expires.
		r, err := g.gh(oc, ghCall{OnLine: w.line}, append(base, "--web", "--scopes", "repo,read:org")...)
		if err != nil {
			if oc.Ctx.Err() != nil {
				return nil, 124, errors.New("the one-time code was not entered in time")
			}
			return nil, 1, fmt.Errorf("GitHub login did not complete: %v", lastErr(r, err))
		}
	}
	// git over https then uses gh's credentials (deploy clones also name gh as their helper).
	if r, err := g.gh(oc, ghCall{}, "auth", "setup-git", "--hostname", agentproto.GitHubHost); err != nil {
		oc.Info("gh auth setup-git failed (%v); deploy clones still use gh directly", lastErr(r, err))
	}
	st := g.status(oc)
	if !st.LoggedIn {
		return st, 1, errors.New("gh reported success but is not logged in to GitHub")
	}
	oc.Info("logged in to GitHub as %s", st.User)
	return st, 0, nil
}

// ---- github.logout -------------------------------------------------------------------------------

func (g *ghHost) logout(oc *OpContext) (any, int, error) {
	if !g.installed() {
		oc.Info("the GitHub CLI is not installed — nothing to log out of")
		return agentproto.GitHubStatus{}, 0, nil
	}
	if r, err := g.gh(oc, ghCall{}, "auth", "logout", "--hostname", agentproto.GitHubHost); err != nil {
		oc.Info("gh auth logout: %v (treated as already logged out)", lastErr(r, err))
	}
	for _, key := range []string{"credential.https://github.com.helper", "credential.https://gist.github.com.helper"} {
		// Exit 5: the key was not set — nothing to remove.
		_, _ = g.run(oc, ghCall{Name: "git", Args: []string{"config", "--global", "--unset-all", key}})
	}
	st := g.status(oc)
	if st.LoggedIn {
		return st, 1, errors.New("gh is still logged in to GitHub")
	}
	return st, 0, nil
}

// ---- github.repos --------------------------------------------------------------------------------

const ghRepoFields = "name,nameWithOwner,isPrivate,defaultBranchRef,url,updatedAt,description"

// ghRepoJSON is one entry of `gh repo list --json …`.
type ghRepoJSON struct {
	Name             string `json:"name"`
	NameWithOwner    string `json:"nameWithOwner"`
	IsPrivate        bool   `json:"isPrivate"`
	DefaultBranchRef *struct {
		Name string `json:"name"`
	} `json:"defaultBranchRef"`
	URL         string    `json:"url"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Description string    `json:"description"`
}

func parseRepoList(s string) ([]agentproto.GitHubRepo, error) {
	var in []ghRepoJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &in); err != nil {
		return nil, fmt.Errorf("gh repo list answered something unreadable: %w", err)
	}
	out := make([]agentproto.GitHubRepo, 0, len(in))
	for _, r := range in {
		repo := agentproto.GitHubRepo{Name: r.Name, NameWithOwner: r.NameWithOwner, IsPrivate: r.IsPrivate, URL: r.URL,
			UpdatedAt: r.UpdatedAt, Description: clip(r.Description, 500)}
		if r.DefaultBranchRef != nil {
			repo.DefaultBranch = r.DefaultBranchRef.Name
		}
		out = append(out, repo)
	}
	return out, nil
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (g *ghHost) requireGH(oc *OpContext) error {
	if !g.installed() {
		return errors.New("the GitHub CLI is not installed on this server — connect GitHub on this server first")
	}
	return nil
}

func (g *ghHost) repos(oc *OpContext) (any, int, error) {
	var p agentproto.GitHubReposParams
	if err := json.Unmarshal(oc.Params, &p); err != nil {
		return nil, 2, fmt.Errorf("bad params: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if p.Limit == 0 {
		p.Limit = 200
	}
	if err := g.requireGH(oc); err != nil {
		return nil, 1, err
	}
	owners := []string{p.Owner}
	if p.Owner == "" {
		r, err := g.gh(oc, ghCall{Quiet: true}, "api", "--hostname", agentproto.GitHubHost, "user/orgs", "--paginate", "--jq", ".[].login")
		if err != nil {
			return nil, 1, fmt.Errorf("could not list your GitHub organisations: %v — is GitHub connected on this server?", lastErr(r, err))
		}
		for _, o := range nonEmptyLines(r.Stdout) {
			if agentproto.ValidGitHubOwner(o) {
				owners = append(owners, o)
			}
		}
	}
	res := agentproto.GitHubReposResult{Repos: []agentproto.GitHubRepo{}}
	for _, owner := range owners {
		left := p.Limit - len(res.Repos)
		if left <= 0 {
			res.Truncated = true
			break
		}
		args := []string{"repo", "list"}
		if owner != "" {
			args = append(args, owner)
		}
		args = append(args, "--limit", fmt.Sprint(left), "--json", ghRepoFields)
		r, err := g.gh(oc, ghCall{Quiet: true}, args...)
		if err != nil {
			return nil, 1, fmt.Errorf("gh repo list failed: %v", lastErr(r, err))
		}
		repos, err := parseRepoList(r.Stdout)
		if err != nil {
			return nil, 1, err
		}
		if len(repos) >= left && owner != owners[len(owners)-1] {
			res.Truncated = true
		}
		res.Repos = append(res.Repos, repos...)
		if owner == "" {
			owner = "(you)"
		}
		res.Owners = append(res.Owners, owner)
	}
	sort.SliceStable(res.Repos, func(i, j int) bool { return res.Repos[i].UpdatedAt.After(res.Repos[j].UpdatedAt) })
	oc.Info("%d repositories (%s)", len(res.Repos), strings.Join(res.Owners, ", "))
	return res, 0, nil
}

// ---- github.branches -----------------------------------------------------------------------------

func (g *ghHost) branches(oc *OpContext) (any, int, error) {
	var p agentproto.GitHubBranchesParams
	if err := json.Unmarshal(oc.Params, &p); err != nil {
		return nil, 2, fmt.Errorf("bad params: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if err := g.requireGH(oc); err != nil {
		return nil, 1, err
	}
	r, err := g.gh(oc, ghCall{Quiet: true}, "api", "--hostname", agentproto.GitHubHost, "repos/"+p.Repo, "--jq", ".default_branch")
	if err != nil {
		return nil, 1, fmt.Errorf("could not read %s: %v", p.Repo, lastErr(r, err))
	}
	res := agentproto.GitHubBranchesResult{Default: strings.TrimSpace(r.Stdout), Branches: []string{}}
	r, err = g.gh(oc, ghCall{Quiet: true}, "api", "--hostname", agentproto.GitHubHost, "repos/"+p.Repo+"/branches", "--paginate", "--jq", ".[].name")
	if err != nil {
		return nil, 1, fmt.Errorf("could not list the branches of %s: %v", p.Repo, lastErr(r, err))
	}
	res.Branches = append(res.Branches, nonEmptyLines(r.Stdout)...)
	oc.Info("%s: %d branches, default %s", p.Repo, len(res.Branches), res.Default)
	return res, 0, nil
}

// ---- github.commits, github.compare, github.pulls ------------------------------------------------

// ghCommitJSON is one commit of GitHub's REST API (repos/{o}/{n}/commits and …/compare).
type ghCommitJSON struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
		Author  *struct {
			Name string    `json:"name"`
			Date time.Time `json:"date"`
		} `json:"author"`
		Committer *struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
	Author *struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	} `json:"author"`
}

func (c ghCommitJSON) out() (agentproto.GitHubCommit, bool) {
	if len(c.SHA) != 40 || !agentproto.ValidGitHubSHA(c.SHA) {
		return agentproto.GitHubCommit{}, false
	}
	first, _, _ := strings.Cut(strings.TrimSpace(c.Commit.Message), "\n")
	o := agentproto.GitHubCommit{SHA: c.SHA, ShortSHA: c.SHA[:7], Message: clip(strings.TrimSpace(first), 300), URL: clip(c.HTMLURL, 500)}
	if a := c.Commit.Author; a != nil {
		o.AuthorName, o.CommittedAt = clip(a.Name, 100), a.Date
	}
	if cm := c.Commit.Committer; cm != nil && !cm.Date.IsZero() {
		o.CommittedAt = cm.Date
	}
	if a := c.Author; a != nil {
		o.AuthorLogin, o.AuthorAvatarURL = clip(a.Login, 100), clip(a.AvatarURL, 500)
	}
	return o, true
}

func commitsOut(in []ghCommitJSON) []agentproto.GitHubCommit {
	out := make([]agentproto.GitHubCommit, 0, len(in))
	for _, c := range in {
		if o, ok := c.out(); ok {
			out = append(out, o)
		}
	}
	return out
}

// ghAPI is one read of GitHub's REST API through gh: a GET of a path built from validated parts.
func (g *ghHost) ghAPI(oc *OpContext, path string, into any) error {
	r, err := g.gh(oc, ghCall{Quiet: true}, "api", "--hostname", agentproto.GitHubHost, "--method", "GET", path)
	if err != nil {
		return lastErr(r, err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), into); err != nil {
		return fmt.Errorf("GitHub answered something unreadable: %w", err)
	}
	return nil
}

func (g *ghHost) commits(oc *OpContext) (any, int, error) {
	var p agentproto.GitHubCommitsParams
	if err := json.Unmarshal(oc.Params, &p); err != nil {
		return nil, 2, fmt.Errorf("bad params: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if p.Limit == 0 {
		p.Limit = 30
	}
	if err := g.requireGH(oc); err != nil {
		return nil, 1, err
	}
	var in []ghCommitJSON
	if err := g.ghAPI(oc, fmt.Sprintf("repos/%s/commits?sha=%s&per_page=%d", p.Repo, p.Branch, p.Limit), &in); err != nil {
		return nil, 1, fmt.Errorf("could not list the commits of %s on %s: %v", p.Repo, p.Branch, err)
	}
	res := agentproto.GitHubCommitsResult{Commits: commitsOut(in)}
	if len(res.Commits) > p.Limit {
		res.Commits = res.Commits[:p.Limit]
	}
	oc.Info("%s@%s: %d commits", p.Repo, p.Branch, len(res.Commits))
	return res, 0, nil
}

func (g *ghHost) compare(oc *OpContext) (any, int, error) {
	var p agentproto.GitHubCompareParams
	if err := json.Unmarshal(oc.Params, &p); err != nil {
		return nil, 2, fmt.Errorf("bad params: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if err := g.requireGH(oc); err != nil {
		return nil, 1, err
	}
	var in struct {
		AheadBy      int            `json:"ahead_by"`
		BehindBy     int            `json:"behind_by"`
		TotalCommits int            `json:"total_commits"`
		Commits      []ghCommitJSON `json:"commits"` // oldest first
		Files        []struct {
			Filename  string `json:"filename"`
			Status    string `json:"status"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
		} `json:"files"` // patch bodies are never read
	}
	path := fmt.Sprintf("repos/%s/compare/%s...%s?per_page=%d", p.Repo, p.Base, p.Head, agentproto.MaxGitHubCommits)
	if err := g.ghAPI(oc, path, &in); err != nil {
		return nil, 1, fmt.Errorf("could not compare %s...%s in %s: %v", p.Base, p.Head, p.Repo, err)
	}
	res := agentproto.GitHubCompareResult{AheadBy: in.AheadBy, BehindBy: in.BehindBy, TotalCommits: in.TotalCommits,
		Commits: commitsOut(in.Commits), Files: []agentproto.GitHubFile{}}
	for i, j := 0, len(res.Commits)-1; i < j; i, j = i+1, j-1 { // newest first, like github.commits
		res.Commits[i], res.Commits[j] = res.Commits[j], res.Commits[i]
	}
	for _, f := range in.Files {
		if len(res.Files) >= agentproto.MaxGitHubCompareFiles {
			res.Truncated = true
			break
		}
		res.Files = append(res.Files, agentproto.GitHubFile{Filename: clip(f.Filename, 500), Status: clip(f.Status, 20), Additions: f.Additions, Deletions: f.Deletions})
	}
	// GitHub lists at most 300 files, and one page of commits was asked for.
	if in.TotalCommits > len(res.Commits) || len(in.Files) >= agentproto.MaxGitHubCompareFiles {
		res.Truncated = true
	}
	oc.Info("%s %s...%s: %d ahead, %d behind, %d files", p.Repo, p.Base, p.Head, res.AheadBy, res.BehindBy, len(res.Files))
	return res, 0, nil
}

func (g *ghHost) pulls(oc *OpContext) (any, int, error) {
	var p agentproto.GitHubPullsParams
	if err := json.Unmarshal(oc.Params, &p); err != nil {
		return nil, 2, fmt.Errorf("bad params: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if p.Limit == 0 {
		p.Limit = 50
	}
	if err := g.requireGH(oc); err != nil {
		return nil, 1, err
	}
	var in []struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		Draft     bool      `json:"draft"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
		User      *struct {
			Login string `json:"login"`
		} `json:"user"`
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := g.ghAPI(oc, fmt.Sprintf("repos/%s/pulls?state=open&sort=updated&direction=desc&per_page=%d", p.Repo, p.Limit), &in); err != nil {
		return nil, 1, fmt.Errorf("could not list the pull requests of %s: %v", p.Repo, err)
	}
	res := agentproto.GitHubPullsResult{Pulls: []agentproto.GitHubPull{}}
	for _, pr := range in {
		if pr.Number <= 0 || len(res.Pulls) >= p.Limit {
			continue
		}
		o := agentproto.GitHubPull{Number: pr.Number, Title: clip(pr.Title, 300), HeadRef: clip(pr.Head.Ref, 250), BaseRef: clip(pr.Base.Ref, 250),
			Draft: pr.Draft, URL: clip(pr.HTMLURL, 500), UpdatedAt: pr.UpdatedAt}
		if pr.User != nil {
			o.AuthorLogin = clip(pr.User.Login, 100)
		}
		res.Pulls = append(res.Pulls, o)
	}
	oc.Info("%s: %d open pull requests", p.Repo, len(res.Pulls))
	return res, 0, nil
}

// ---- deploy clones -------------------------------------------------------------------------------

// ghCredentialHelper is the git credential helper for github.com when gh is installed and holds a
// login here; "" otherwise. Deploy clones ignore the host's global git config (GIT_CONFIG_GLOBAL is
// /dev/null), so they name the helper themselves instead of relying on `gh auth setup-git`.
func ghCredentialHelper() string {
	path, err := exec.LookPath("gh")
	if err != nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(agentproto.GHConfigDir, "hosts.yml")); err != nil {
		return ""
	}
	return "!'" + strings.ReplaceAll(path, "'", `'\''`) + "' auth git-credential"
}

// ghCredentialArgs are git's leading -c options naming gh as github.com's only credential helper
// (the empty value first clears any other helper).
func ghCredentialArgs(helper string) []string {
	return []string{"-c", "credential.https://github.com.helper=", "-c", "credential.https://github.com.helper=" + helper}
}

// ghFetchError explains a failed fetch of a github.com repository. git never prompts
// (GIT_TERMINAL_PROMPT=0, GIT_ASKPASS=/bin/false), so a missing credential fails at once.
func ghFetchError(connected bool, ref string) error {
	if connected {
		return fmt.Errorf("git fetch failed: the GitHub account connected on this server cannot read this repository, or it has no %q", ref)
	}
	return fmt.Errorf("git fetch failed: the repository does not exist, has no %q, or is private — connect GitHub on this server (Servers → GitHub) to clone private repositories", ref)
}
