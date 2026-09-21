package agentproto

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

/*
GitHub through the GitHub CLI on each server.

A server connects to GitHub with `gh`, installed from GitHub's own package repository and logged in
either with the device flow (a human enters a one-time code at github.com/login/device) or with a
token. The agent keeps gh's configuration in GHConfigDir (root-only), and every gh call and every
deploy clone of a github.com repository uses it, so a private repository the account can read
clones without a token ever reaching the control plane's database in the clear.

A token for the token mode travels like deploy env values: sealed to the target agent's own key
(SealBox with GitHubTokenContext), so operations.params holds ciphertext only that server opens.
*/

// Operation kinds.
const (
	KindGitHubInstall  = "github.install"
	KindGitHubLogin    = "github.login"
	KindGitHubStatus   = "github.status"
	KindGitHubLogout   = "github.logout"
	KindGitHubRepos    = "github.repos"
	KindGitHubBranches = "github.branches"
	KindGitHubCommits  = "github.commits" // one repository's recent commits on a branch
	KindGitHubCompare  = "github.compare" // base...head: ahead/behind, commits, changed files (no patches)
	KindGitHubPulls    = "github.pulls"   // one repository's open pull requests
)

// GitHubKinds are the github.* operations, for the control plane's bookkeeping.
var GitHubKinds = []string{KindGitHubInstall, KindGitHubLogin, KindGitHubStatus, KindGitHubLogout, KindGitHubRepos, KindGitHubBranches,
	KindGitHubCommits, KindGitHubCompare, KindGitHubPulls}

// GHConfigDir is where gh keeps its hosts file on a server (GH_CONFIG_DIR for every gh the agent runs).
const GHConfigDir = "/etc/cloudy/gh"

// GitHubHost is the only host Hosbah logs gh into.
const GitHubHost = "github.com"

// Login modes.
const (
	GitHubModeDevice = "device"
	GitHubModeToken  = "token"
)

// MaxGitHubRepos bounds github.repos.
const MaxGitHubRepos = 1000

// GitHubTokenContext binds a sealed token to the server it is for.
func GitHubTokenContext(serverID string) string { return "github-token:" + serverID }

// GitHubLoginParams: github.login. Token is a box sealed to the agent (token mode only).
type GitHubLoginParams struct {
	Mode  string `json:"mode"`
	Token string `json:"token,omitempty"`
}

// Validate checks github.login on both sides of the wire. A token must arrive sealed.
func (p GitHubLoginParams) Validate() error {
	switch p.Mode {
	case GitHubModeDevice:
		if p.Token != "" {
			return errors.New("device mode takes no token")
		}
	case GitHubModeToken:
		if !strings.HasPrefix(p.Token, boxPrefix) {
			return errors.New("token mode needs a token sealed to the server")
		}
	default:
		return errors.New("mode is device or token")
	}
	return nil
}

// GitHubReposParams: github.repos. An empty owner lists the account's own repositories and those
// of every organisation it belongs to.
type GitHubReposParams struct {
	Owner string `json:"owner,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

var (
	ghOwnerRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	ghRepoRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// ValidGitHubOwner: a GitHub user or organisation login.
func ValidGitHubOwner(s string) bool { return ghOwnerRe.MatchString(s) }

// ValidGitHubRepoName: a repository name (not "." or "..").
func ValidGitHubRepoName(s string) bool { return ghRepoRe.MatchString(s) && s != "." && s != ".." }

// Validate checks github.repos on both sides.
func (p GitHubReposParams) Validate() error {
	if p.Owner != "" && !ValidGitHubOwner(p.Owner) {
		return errors.New("owner must be a GitHub user or organisation name")
	}
	if p.Limit < 0 || p.Limit > MaxGitHubRepos {
		return errors.New("limit is 1–1000")
	}
	return nil
}

// GitHubBranchesParams: github.branches.
type GitHubBranchesParams struct {
	Repo string `json:"repo"` // owner/name
}

// Validate checks github.branches on both sides.
func (p GitHubBranchesParams) Validate() error {
	owner, name, ok := strings.Cut(p.Repo, "/")
	if !ok || !ValidGitHubOwner(owner) || !ValidGitHubRepoName(name) {
		return errors.New("repo must be owner/name")
	}
	return nil
}

// Bounds of the repository reads.
const (
	MaxGitHubCommits      = 100 // one page of GitHub's API
	MaxGitHubPulls        = 100
	MaxGitHubCompareFiles = 300 // what GitHub's compare returns at most
)

var ghSHARe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// ValidGitHubSHA: a full or short commit sha.
func ValidGitHubSHA(s string) bool { return ghSHARe.MatchString(s) }

// ValidGitHubRepo: owner/name.
func ValidGitHubRepo(s string) bool {
	owner, name, ok := strings.Cut(s, "/")
	return ok && ValidGitHubOwner(owner) && ValidGitHubRepoName(name)
}

// ValidGitHubRev: a branch, tag or sha as it may appear in a `gh api` path or query — ValidRef's
// charset (letters, digits, . _ / -; no leading dash, no ".."), so no ?, &, #, %, braces or spaces.
func ValidGitHubRev(s string) bool {
	return ValidRef(s) && !strings.Contains(s, "//") && !strings.HasSuffix(s, "/")
}

// GitHubCommitsParams: github.commits.
type GitHubCommitsParams struct {
	Repo   string `json:"repo"`   // owner/name
	Branch string `json:"branch"` // a branch, tag or sha to list from
	Limit  int    `json:"limit,omitempty"`
}

// Validate checks github.commits on both sides.
func (p GitHubCommitsParams) Validate() error {
	switch {
	case !ValidGitHubRepo(p.Repo):
		return errors.New("repo must be owner/name")
	case !ValidGitHubRev(p.Branch):
		return errors.New("branch must be a branch, tag or commit")
	case p.Limit < 0 || p.Limit > MaxGitHubCommits:
		return errors.New("limit is 1–100")
	}
	return nil
}

// GitHubCompareParams: github.compare (base...head).
type GitHubCompareParams struct {
	Repo string `json:"repo"`
	Base string `json:"base"`
	Head string `json:"head"`
}

// Validate checks github.compare on both sides.
func (p GitHubCompareParams) Validate() error {
	switch {
	case !ValidGitHubRepo(p.Repo):
		return errors.New("repo must be owner/name")
	case !ValidGitHubRev(p.Base):
		return errors.New("base must be a branch, tag or commit")
	case !ValidGitHubRev(p.Head):
		return errors.New("head must be a branch, tag or commit")
	}
	return nil
}

// GitHubPullsParams: github.pulls.
type GitHubPullsParams struct {
	Repo  string `json:"repo"`
	Limit int    `json:"limit,omitempty"`
}

// Validate checks github.pulls on both sides.
func (p GitHubPullsParams) Validate() error {
	switch {
	case !ValidGitHubRepo(p.Repo):
		return errors.New("repo must be owner/name")
	case p.Limit < 0 || p.Limit > MaxGitHubPulls:
		return errors.New("limit is 1–100")
	}
	return nil
}

// GitHubCommit is one commit of github.commits and github.compare.
type GitHubCommit struct {
	SHA             string    `json:"sha"`
	ShortSHA        string    `json:"short_sha"`
	Message         string    `json:"message"` // first line
	AuthorName      string    `json:"author_name,omitempty"`
	AuthorLogin     string    `json:"author_login,omitempty"`
	AuthorAvatarURL string    `json:"author_avatar_url,omitempty"`
	CommittedAt     time.Time `json:"committed_at"`
	URL             string    `json:"url"`
}

// GitHubCommitsResult: github.commits, newest first.
type GitHubCommitsResult struct {
	Commits []GitHubCommit `json:"commits"`
}

// GitHubFile is one changed file of a comparison — never its patch.
type GitHubFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// GitHubCompareResult: github.compare. Commits are newest first. Truncated: more commits or files
// exist than are listed.
type GitHubCompareResult struct {
	AheadBy      int            `json:"ahead_by"`
	BehindBy     int            `json:"behind_by"`
	TotalCommits int            `json:"total_commits"`
	Commits      []GitHubCommit `json:"commits"`
	Files        []GitHubFile   `json:"files"`
	Truncated    bool           `json:"truncated"`
}

// GitHubPull is one open pull request.
type GitHubPull struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	AuthorLogin string    `json:"author_login,omitempty"`
	HeadRef     string    `json:"head_ref"`
	BaseRef     string    `json:"base_ref"`
	Draft       bool      `json:"draft"`
	URL         string    `json:"url"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GitHubPullsResult: github.pulls.
type GitHubPullsResult struct {
	Pulls []GitHubPull `json:"pulls"`
}

// GitHubStatus is the result of github.status, github.login, github.logout and github.install.
type GitHubStatus struct {
	Installed bool     `json:"installed"`
	Version   string   `json:"version,omitempty"`
	LoggedIn  bool     `json:"logged_in"`
	User      string   `json:"user,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
}

// GitHubRepo is one repository github.repos returns.
type GitHubRepo struct {
	Name          string    `json:"name"`
	NameWithOwner string    `json:"name_with_owner"`
	IsPrivate     bool      `json:"is_private"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	URL           string    `json:"url"`
	UpdatedAt     time.Time `json:"updated_at"`
	Description   string    `json:"description,omitempty"`
}

// GitHubReposResult: github.repos.
type GitHubReposResult struct {
	Repos     []GitHubRepo `json:"repos"`
	Owners    []string     `json:"owners,omitempty"` // whose repositories were listed
	Truncated bool         `json:"truncated,omitempty"`
}

// GitHubBranchesResult: github.branches.
type GitHubBranchesResult struct {
	Default  string   `json:"default"`
	Branches []string `json:"branches"`
}

// Event data: an event may carry a typed payload in Event.Data. The device flow's one-time code is
// the first: the agent sends it as soon as gh prints it, while github.login keeps running.
const DataGitHubDeviceCode = "github.device_code"

// GitHubDeviceCode is the Data of a log event of github.login in device mode.
type GitHubDeviceCode struct {
	Type            string    `json:"type"` // DataGitHubDeviceCode
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// GitHubRepoOf returns owner/name when repoURL is a github.com repository — https://github.com/o/n(.git)
// or the ssh forms git@github.com:o/n.git and ssh://git@github.com/o/n.git — else "".
func GitHubRepoOf(repoURL string) string {
	u := strings.TrimSpace(repoURL)
	lower := strings.ToLower(u)
	for _, p := range []string{"https://github.com/", "http://github.com/", "https://www.github.com/", "git@github.com:", "ssh://git@github.com/"} {
		if strings.HasPrefix(lower, p) {
			rest := strings.TrimSuffix(strings.Trim(u[len(p):], "/"), ".git")
			owner, name, ok := strings.Cut(rest, "/")
			if ok && ValidGitHubOwner(owner) && ValidGitHubRepoName(name) {
				return owner + "/" + name
			}
		}
	}
	return ""
}
