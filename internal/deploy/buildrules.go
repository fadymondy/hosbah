package deploy

import (
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/brand"
)

/*
Build rules: which pushes build at all, and how long an automatic deploy waits for more pushes
before it starts. Both come from hosbah.toml's [build] table:

	[build]
	watch    = ["cmd/**", "internal/**", "web/**", "go.mod", "go.sum", "Dockerfile"]
	ignore   = ["apps/desktop/**", "docs/**", "*.md"]
	debounce = 90                       # seconds an automatic deploy waits for the pushes after it

A push whose changed paths are all outside watch (when watch is set) or all inside ignore is
recorded as a skipped deployment ("no image changes") and builds nothing. Neither set = every push
builds, as before. A change to hosbah.toml (or cloudy.toml) always builds: it may change the rules.

The rules are read from the hosbah.toml of the last build (stored on apps.build_rules), because a
push is decided before anything is checked out. A push that edits the rules builds, so the new
rules apply from the push after it.

Patterns are gitignore-like: "*" and "?" stay inside one path segment, "**" spans any number of
them, a trailing "/" means everything under that directory, a pattern without a "/" matches the
file name at any depth ("*.md", "Dockerfile"), and a leading "/" anchors it to the root ("/TODO.md").
*/

// BuildRules is hosbah.toml's [build], as stored on apps.build_rules.
type BuildRules struct {
	Watch    []string `toml:"watch" json:"watch,omitempty"`
	Ignore   []string `toml:"ignore" json:"ignore,omitempty"`
	Debounce int      `toml:"debounce" json:"debounce,omitempty"` // seconds; 0 = the platform default
}

// Limits on [build], so a stored rule set stays small.
const (
	maxBuildPatterns  = 100
	maxBuildPattern   = 200
	MaxBuildDebounceS = 1800
)

// Check validates [build] (file names the config file in errors).
func (b BuildRules) Check(file string) error {
	for _, set := range []struct {
		key  string
		pats []string
	}{{"watch", b.Watch}, {"ignore", b.Ignore}} {
		if len(set.pats) > maxBuildPatterns {
			return fmt.Errorf("%s: build.%s has more than %d patterns", file, set.key, maxBuildPatterns)
		}
		for _, p := range set.pats {
			if strings.TrimSpace(p) == "" || len(p) > maxBuildPattern || strings.HasPrefix(p, "//") || strings.Contains(p, "..") {
				return fmt.Errorf("%s: build.%s pattern %q — use a path relative to the repository root, like \"web/**\" or \"*.md\"", file, set.key, p)
			}
		}
	}
	if b.Debounce < 0 || b.Debounce > MaxBuildDebounceS {
		return fmt.Errorf("%s: build.debounce must be 0 to %d seconds", file, MaxBuildDebounceS)
	}
	return nil
}

// ParseBuildRules reads apps.build_rules; anything unreadable is "no rules" (build every push).
func ParseBuildRules(raw string) BuildRules {
	var b BuildRules
	_ = json.Unmarshal([]byte(raw), &b)
	return b
}

// JSON is the stored form of the rules.
func (b BuildRules) JSON() string {
	raw, err := json.Marshal(b)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// alwaysBuild are paths whose change builds whatever the rules say: they may change the rules.
var alwaysBuild = map[string]bool{ConfigFile: true, LegacyConfigFile: true}

// Decide reports whether a push that changed these paths needs a build. known is false when the
// changed paths are not known (a force push, a new branch, a payload without file lists): that
// always builds.
func (b BuildRules) Decide(changed []string, known bool) (build bool, reason string) {
	if len(b.Watch) == 0 && len(b.Ignore) == 0 {
		return true, "no build rules"
	}
	if !known || len(changed) == 0 {
		return true, "the changed files are not known"
	}
	for _, p := range changed {
		p = strings.TrimPrefix(strings.TrimSpace(p), "./")
		if p == "" {
			continue
		}
		if alwaysBuild[p] {
			return true, p + " changed"
		}
		if len(b.Watch) > 0 && !matchAny(b.Watch, p) {
			continue
		}
		if matchAny(b.Ignore, p) {
			continue
		}
		return true, p + " changed"
	}
	if len(changed) == 1 {
		return false, "no image changes (" + changed[0] + ")"
	}
	return false, fmt.Sprintf("no image changes (%d files, none watched)", len(changed))
}

func matchAny(patterns []string, p string) bool {
	for _, pat := range patterns {
		if MatchPath(pat, p) {
			return true
		}
	}
	return false
}

// MatchPath matches a repository-relative path against one gitignore-like pattern (see above).
func MatchPath(pattern, p string) bool {
	pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "./")
	anchored := strings.HasPrefix(pattern, "/")
	pattern = strings.TrimPrefix(pattern, "/")
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	if !anchored && !strings.Contains(pattern, "/") {
		pattern = "**/" + pattern // a bare name matches at any depth
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true // a trailing ** is everything below, at any depth
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], segs[0]); err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// ---- debounce ----------------------------------------------------------------------------------------

// Automatic deploys (a push, a deploy hook) wait DefaultDebounce after the latest push before they
// start, so a burst of merges builds once, at its last commit. A steady stream of pushes still
// builds at least every DefaultMaxWait. A deploy started by a person (web, MCP, CLI) never waits.
const (
	DefaultDebounce = 75 * time.Second
	DefaultMaxWait  = 10 * time.Minute
)

// Debounce is the platform's debounce settings: HOSBAH_BUILD_DEBOUNCE and HOSBAH_BUILD_MAX_WAIT,
// in seconds (0 turns debouncing off).
type Debounce struct {
	Wait    time.Duration
	MaxWait time.Duration
}

// DebounceFromEnv reads the debounce settings (defaults when unset or unreadable).
func DebounceFromEnv() Debounce {
	d := Debounce{Wait: DefaultDebounce, MaxWait: DefaultMaxWait}
	if v, ok := envSeconds("BUILD_DEBOUNCE"); ok {
		d.Wait = v
	}
	if v, ok := envSeconds("BUILD_MAX_WAIT"); ok {
		d.MaxWait = v
	}
	return d
}

func envSeconds(name string) (time.Duration, bool) {
	raw := strings.TrimSpace(brand.Getenv(name))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 86400 {
		return 0, false
	}
	return time.Duration(n) * time.Second, true
}

// NotBefore is when an automatic deploy queued at now may start: wait after the latest push, but
// never later than maxWait after the first push it coalesces (since; zero = this one). A zero wait
// means no debounce (the zero time: start at once).
func NotBefore(now, since time.Time, wait, maxWait time.Duration) time.Time {
	if wait <= 0 {
		return time.Time{}
	}
	at := now.Add(wait)
	if since.IsZero() || since.After(now) {
		since = now
	}
	if maxWait > 0 {
		if latest := since.Add(maxWait); latest.Before(at) {
			at = latest
		}
	}
	if at.Before(now) {
		at = now
	}
	return at
}

// For the app: its hosbah.toml debounce, if any, over the platform's.
func (d Debounce) For(rules BuildRules) time.Duration {
	if rules.Debounce > 0 {
		return time.Duration(rules.Debounce) * time.Second
	}
	return d.Wait
}

// ---- build CPU share ---------------------------------------------------------------------------------

// DefaultBuildCPUShare is the part of a server's CPUs its image builds may use, so the apps it
// serves keep the rest (HOSBAH_BUILD_CPU_SHARE, 0.1 .. 1; 1 = no cap).
const DefaultBuildCPUShare = 0.5

// BuildCPUShareFromEnv reads HOSBAH_BUILD_CPU_SHARE.
func BuildCPUShareFromEnv() float64 {
	raw := strings.TrimSpace(brand.Getenv("BUILD_CPU_SHARE"))
	if raw == "" {
		return DefaultBuildCPUShare
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0.1 || v > 1 {
		return DefaultBuildCPUShare
	}
	return v
}

// ---- push payloads -----------------------------------------------------------------------------------

// PushPayload is the part of a git host's push event the build rules read. GitHub, Gitea/Forgejo
// and GitLab share this shape (GitLab adds total_commits_count and caps commits at 20).
type PushPayload struct {
	Ref          string `json:"ref"`
	Before       string `json:"before"`
	After        string `json:"after"`
	Created      bool   `json:"created"`
	Forced       bool   `json:"forced"`
	Deleted      bool   `json:"deleted"`
	TotalCommits *int   `json:"total_commits_count"`
	HeadCommit   *struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"head_commit"`
	Commits []PushCommit `json:"commits"`
}

// PushCommit is one pushed commit and the paths it touched.
type PushCommit struct {
	ID       string   `json:"id"`
	Message  string   `json:"message"`
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	Modified []string `json:"modified"`
}

// maxPushCommits is GitHub's cap on a push payload's commits: a push that long may have more.
const maxPushCommits = 2048

// Changed is every path the push touched, and whether that list is complete: a new branch, a force
// push, a push with no commit list or one the host truncated is not known (so it builds).
func (p PushPayload) Changed() (paths []string, known bool) {
	zero := strings.Trim(p.Before, "0") == ""
	if p.Created || p.Forced || zero || len(p.Commits) == 0 || len(p.Commits) >= maxPushCommits {
		return nil, false
	}
	if p.TotalCommits != nil && *p.TotalCommits > len(p.Commits) {
		return nil, false
	}
	seen := map[string]bool{}
	for _, c := range p.Commits {
		for _, set := range [][]string{c.Added, c.Removed, c.Modified} {
			for _, f := range set {
				if f != "" && !seen[f] {
					seen[f] = true
					paths = append(paths, f)
				}
			}
		}
	}
	return paths, true
}

// Head is the pushed commit and its message's first line ("" when the payload has none).
func (p PushPayload) Head() (sha, message string) {
	sha = p.After
	if p.HeadCommit != nil {
		sha, message = p.HeadCommit.ID, p.HeadCommit.Message
	} else if n := len(p.Commits); n > 0 {
		message = p.Commits[n-1].Message
	}
	if strings.Trim(sha, "0") == "" {
		sha = ""
	}
	if i := strings.IndexByte(message, '\n'); i >= 0 {
		message = message[:i]
	}
	return sha, message
}
