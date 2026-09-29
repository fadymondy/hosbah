package agent

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// App operations (P2). Everything here is a fixed argv for git / docker / railpack, or Caddy JSON —
// no host shell. The two strings a user controls that do run are the release command and a process
// command from hosbah.toml (or cloudy.toml), and both run INSIDE the app's own container (sh -c), never on the host.

// workRoot was /var/lib/cloudy before the rename; migrateStateDirs moves it on the first start.
const workRoot = agentproto.WorkDir

// appDocker runs the docker commands that only read (listing an app's containers); dockerStep
// the ones that change something, logged to the operation. Tests replace both.
var (
	appDocker  runFunc = execRun
	dockerStep         = func(oc *OpContext, args ...string) (int, error) { return oc.Run("docker", args...) }
)

func decode[T any](oc *OpContext) (T, error) {
	var p T
	if err := json.Unmarshal(oc.Params, &p); err != nil {
		return p, fmt.Errorf("bad params: %w", err)
	}
	return p, nil
}

// ---- git -----------------------------------------------------------------------------------------

// gitEnv: never prompt for credentials; git's config and hooks of the host user never apply.
var gitEnv = []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ASKPASS=/bin/false"}

// gitArgs pins the transports to the ones CheckRepoURL admits (no ext::, no file://).
func gitArgs(args ...string) []string {
	return append([]string{"-c", "protocol.allow=never", "-c", "protocol.https.allow=always",
		"-c", "protocol.http.allow=always", "-c", "protocol.git.allow=always", "-c", "core.hooksPath=/dev/null"}, args...)
}

// checkout fetches exactly one ref (a branch, tag or commit) at depth 1 into dir.
func checkout(oc *OpContext, src agentproto.GitSource, dir string) (commit, message string, code int, err error) {
	if err := agentproto.CheckRepoURL(src.RepoURL); err != nil {
		return "", "", 2, err
	}
	if !agentproto.ValidRef(src.Ref) {
		return "", "", 2, fmt.Errorf("bad ref %q", src.Ref)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", "", 1, errors.New("git is not installed — run the build.tools provisioning step")
	}
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", 1, err
	}
	env, cred, ghRepo := gitEnv, []string(nil), agentproto.GitHubRepoOf(src.RepoURL)
	helper := ""
	var fetchEnv []string // added to the fetch only: the one command that talks to the remote
	if src.Token != "" {
		tok, err := openCloneToken(oc, src)
		if err != nil {
			return "", "", 2, err
		}
		// The team's GitHub App installation: git asks the helper, which reads the token from the
		// environment of the fetch. The argv (logged) names only the variable.
		cred = []string{"-c", "credential.https://github.com.helper=", "-c", "credential.https://github.com.helper=" + appTokenHelper}
		fetchEnv = []string{appTokenEnv + "=" + tok}
		helper = "app"
		oc.Info("using the team's GitHub App installation for %s", ghRepo)
	} else if ghRepo != "" {
		helper = ghCredentialHelper()
	}
	if helper != "" && src.Token == "" {
		// A github.com repository and a gh login on this server: git asks gh for credentials, so a
		// private repository the connected account can read clones. Nothing is written to disk.
		cred = ghCredentialArgs(helper)
		env = append(append([]string{}, gitEnv...), ghEnv()...)
		oc.Info("using this server's GitHub connection (gh) for %s", ghRepo)
	}
	for _, argv := range [][]string{
		{"init", "--quiet", dir},
		{"-C", dir, "remote", "add", "origin", src.RepoURL},
		{"-C", dir, "fetch", "--depth", "1", "--no-tags", "origin", src.Ref},
		{"-C", dir, "checkout", "--quiet", "--detach", "FETCH_HEAD"},
	} {
		runEnv := env
		if argv[2] == "fetch" && len(fetchEnv) > 0 {
			runEnv = append(append([]string{}, env...), fetchEnv...)
		}
		if code, err := oc.RunEnv(runEnv, "git", append(cred, gitArgs(argv...)...)...); err != nil {
			if argv[2] == "fetch" && src.Token != "" {
				return "", "", code, fmt.Errorf("git fetch failed: the team's GitHub App installation cannot read this repository, or it has no %q", src.Ref)
			}
			if argv[2] == "fetch" && ghRepo != "" {
				return "", "", code, ghFetchError(helper != "", src.Ref)
			}
			return "", "", code, fmt.Errorf("git %s failed", argv[len(argv)-2])
		}
	}
	out, err := gitOut(oc, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", 1, err
	}
	msg, _ := gitOut(oc, dir, "log", "-1", "--format=%s")
	commit = strings.TrimSpace(out)
	oc.Info("checked out %s at %s", src.Ref, commit)
	return commit, strings.TrimSpace(msg), 0, nil
}

// appTokenEnv carries a GitHub App installation token to the one git fetch that needs it.
const appTokenEnv = "HOSBAH_GIT_TOKEN"

// appTokenHelper is git's credential helper for that fetch: on "get" it answers the installation
// token (username x-access-token, as GitHub expects) from the environment; "store" and "erase" do
// nothing, so nothing is ever written anywhere.
const appTokenHelper = `!f() { test "$1" = get || exit 0; echo username=x-access-token; echo "password=$` + appTokenEnv + `"; }; f`

// openCloneToken opens a clone token sealed to this server for exactly this repository.
func openCloneToken(oc *OpContext, src agentproto.GitSource) (string, error) {
	if agentproto.GitHubRepoOf(src.RepoURL) == "" {
		return "", errors.New("a GitHub App token is only used for github.com repositories")
	}
	pt, err := agentproto.OpenBox(oc.priv, agentproto.GitTokenContext(src.RepoURL), src.Token)
	if err != nil {
		return "", errors.New("the GitHub App token could not be opened with this server's key")
	}
	tok := strings.TrimSpace(string(pt))
	if tok == "" || len(tok) > 512 || !tokenChars(tok) {
		return "", errors.New("the GitHub App token is malformed")
	}
	return tok, nil
}

// tokenChars: GitHub tokens are ASCII letters, digits, "_" and "-". Anything else could not be a
// token and has no business reaching a shell-run helper.
func tokenChars(s string) bool {
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func gitOut(oc *OpContext, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(oc.Ctx, "git", gitArgs(append([]string{"-C", dir}, args...)...)...) //#nosec G204 -- fixed argv
	cmd.Env = append(os.Environ(), gitEnv...)
	out, err := cmd.Output()
	return string(out), err
}

// ---- detection -----------------------------------------------------------------------------------

// readConfigToml reads hosbah.toml, or the pre-rename cloudy.toml, from a checkout. name is empty
// when the repository has neither.
func readConfigToml(src string) (toml, name string, err error) {
	for _, name := range []string{"hosbah.toml", "cloudy.toml"} {
		b, err := os.ReadFile(filepath.Join(src, name)) //#nosec G304 -- inside our checkout
		if err != nil {
			continue
		}
		if len(b) > agentproto.MaxHosbahToml {
			return "", "", errors.New(name + " is larger than 64 KiB")
		}
		return string(b), name, nil
	}
	return "", "", nil
}

// safeJoin resolves rel (a dockerfile_path or build_context, already charset/".."-checked by
// agentproto.ValidBuildPath at the control plane) against root and reports whether the result is
// both real (root and the target both exist, symlinks resolved with filepath.EvalSymlinks) and
// still inside root (filepath.Rel has no ".." component) — refusing a symlink planted inside the
// checkout that would otherwise let a build step reach outside it. rel == "" means root itself.
// ok is false for any problem; callers treat that as "does not apply" (detect) or a hard failure
// (build) as appropriate — never a path used as-is.
func safeJoin(root, rel string) (string, bool) {
	if rel != "" && !agentproto.ValidBuildPath(rel) {
		return "", false
	}
	joined := root
	if rel != "" {
		joined = filepath.Join(root, rel)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	resolvedJoined, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", false
	}
	relCheck, err := filepath.Rel(resolvedRoot, resolvedJoined)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", false
	}
	return resolvedJoined, true
}

// detect decides how dir builds: its own Dockerfile first (at dockerfilePath, relative to src,
// default "Dockerfile" when empty), else Railpack (whose `prepare` writes the build plan and says
// what it found). hosbah.toml — or, when a repository has none, the pre-rename cloudy.toml — is
// returned raw for the control plane to parse.
func detect(oc *OpContext, src, workdir, dockerfilePath string) (agentproto.DetectResult, int, error) {
	var r agentproto.DetectResult
	r.Files = topLevelFiles(src)
	toml, name, err := readConfigToml(src)
	if err != nil {
		return r, 1, err
	}
	if name != "" {
		// Both keys: a control plane from before the rename reads only cloudy_toml.
		r.HosbahToml, r.LegacyToml, r.TomlFile = toml, toml, name
		oc.Info("found %s", name)
	}
	df := dockerfilePath
	if df == "" {
		df = "Dockerfile"
	}
	if dfPath, ok := safeJoin(src, df); ok {
		if st, err := os.Stat(dfPath); err == nil && st.Mode().IsRegular() {
			r.Runtime = "dockerfile"
			oc.Info("build: the repository's Dockerfile (%s)", df)
			return r, 0, nil
		}
	}
	if _, err := os.Stat(railpackBin); err != nil {
		return r, 1, errors.New("no Dockerfile, and Railpack is not installed — run the railpack.install provisioning step")
	}
	r.Runtime = "railpack"
	plan, info := filepath.Join(workdir, "railpack-plan.json"), filepath.Join(workdir, "railpack-info.json")
	var code int
	for attempt := 1; attempt <= 3; attempt++ {
		code, err = oc.Run(railpackBin, "prepare", src, "--plan-out", plan, "--info-out", info)
		if code != 75 { // 75 = transient (a download), worth retrying; anything else is final
			break
		}
		oc.Info("railpack prepare: transient failure, retrying (%d/3)", attempt)
		time.Sleep(time.Duration(attempt) * 2 * time.Second)
	}
	r.Provider, r.StartCommand = readRailpackInfo(info, plan)
	if err != nil {
		return r, code, errors.New("Railpack could not plan this repository (no supported language detected, or no start command) — add a Dockerfile, or see the log above")
	}
	if r.Provider != "" {
		oc.Info("build: Railpack, detected %s", r.Provider)
	}
	return r, 0, nil
}

// composerRequireRE finds a top-level "require": { ... } object in a composer.json (composer.json
// never nests another object inside "require" — every value is a version constraint string — so a
// non-greedy match to the first "}" is safe).
var composerRequireRE = regexp.MustCompile(`(?s)("require"\s*:\s*\{)(.*?)(\})`)

// composerPHPLineRE matches an existing "php": "..." entry inside a require object.
var composerPHPLineRE = regexp.MustCompile(`"php"\s*:\s*"[^"]*"`)

// composerIndentRE pulls the indentation composer.json already uses for its require entries, so an
// inserted "php" line matches the file's own style instead of guessing one.
var composerIndentRE = regexp.MustCompile(`\n([ \t]*)"`)

// pinComposerPHPVersion (MH-576 D2) rewrites "require"."php" in the checkout's composer.json to an
// exact version, because Railpack's PHP provider (railwayapp/railpack core/providers/php) reads the
// version only from that constraint — it does not honor a RAILPACK_PHP_VERSION env var. A no-op for
// "" or "auto" (Railpack detects it itself). When the repository has no composer.json at all, this
// leaves it alone and logs a clear note instead of creating one: a PHP pin only makes sense for a
// Composer-managed app.
func pinComposerPHPVersion(oc *OpContext, src, version string) error {
	if version == "" || version == "auto" {
		return nil
	}
	path := filepath.Join(src, "composer.json")
	b, err := os.ReadFile(path) //#nosec G304 -- our own checkout
	if err != nil {
		if os.IsNotExist(err) {
			oc.Info("PHP version pin %s: no composer.json in this repository — Railpack reads the PHP version from composer.json's \"require\".\"php\"; add one to apply the pin", version)
			return nil
		}
		return fmt.Errorf("reading composer.json: %w", err)
	}
	out, changed := setComposerPHPVersion(string(b), version)
	if !changed {
		oc.Info("PHP version pin %s: composer.json has no \"require\" object to add a \"php\" constraint to — add one to apply the pin", version)
		return nil
	}
	if !json.Valid([]byte(out)) {
		oc.Info("PHP version pin %s: rewriting composer.json's \"require\".\"php\" would produce invalid JSON — leaving composer.json untouched", version)
		return nil
	}
	oc.Info("pinned composer.json's \"require\".\"php\" to %s", version)
	return os.WriteFile(path, []byte(out), 0o644) //#nosec G306 -- world-readable manifest, not a secret
}

// setComposerPHPVersion edits composer.json's first top-level "require" object to require exactly
// version, preserving the rest of the file (including key order and formatting) untouched. It
// replaces an existing "php" entry in place, or inserts one as the first entry in "require" when
// there isn't one yet. Reports false, unchanged, when there is no "require" object at all.
func setComposerPHPVersion(src, version string) (string, bool) {
	loc := composerRequireRE.FindStringSubmatchIndex(src)
	if loc == nil {
		return src, false
	}
	body := src[loc[4]:loc[5]]
	var newBody string
	if composerPHPLineRE.MatchString(body) {
		newBody = composerPHPLineRE.ReplaceAllString(body, `"php": "`+version+`"`)
	} else {
		indent := "    "
		if m := composerIndentRE.FindStringSubmatch(body); m != nil {
			indent = m[1]
		}
		if strings.TrimSpace(body) == "" {
			newBody = "\n" + indent + `"php": "` + version + `"` + "\n"
		} else {
			newBody = "\n" + indent + `"php": "` + version + `",` + body
		}
	}
	return src[:loc[4]] + newBody + src[loc[5]:], true
}

// writePHPIniOverrides (MH-576 D2) writes settings as a php.ini at the checkout root, for
// Railpack's PHP provider to merge into the image it builds. A no-op when there is nothing to
// write, or when the repository already has its own php.ini — Hosbah never overwrites an app's
// own file. The control plane validates keys and values (deploy.ValidatePHPIni) before they ever
// reach a signed op, so this only renders; it never trusts them as shell input.
func writePHPIniOverrides(src string, settings map[string]string) error {
	if len(settings) == 0 {
		return nil
	}
	dst := filepath.Join(src, "php.ini")
	if _, err := os.Stat(dst); err == nil {
		return nil // the app ships its own — leave it alone
	}
	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("; Written by Hosbah (MH-576 D2) — per-app php.ini overrides.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, settings[k])
	}
	return os.WriteFile(dst, []byte(b.String()), 0o644) //#nosec G306 -- world-readable ini, not a secret
}

// topLevelFiles lists a checkout's root entries (files and directories), for deploy.DetectPreset —
// best-effort: an unreadable directory just yields no files, never a build failure.
func topLevelFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// readRailpackInfo pulls the detected provider and start command out of Railpack's outputs. It
// reads them loosely (only the two fields), so a Railpack upgrade that adds fields does not break it.
func readRailpackInfo(infoPath, planPath string) (provider, start string) {
	if b, err := os.ReadFile(infoPath); err == nil { //#nosec G304 -- our own work dir
		var info struct {
			DetectedProviders []string `json:"detectedProviders"`
		}
		if json.Unmarshal(b, &info) == nil && len(info.DetectedProviders) > 0 {
			provider = info.DetectedProviders[0]
		}
	}
	if b, err := os.ReadFile(planPath); err == nil { //#nosec G304 -- our own work dir
		var plan struct {
			Deploy struct {
				StartCmd    string `json:"startCommand"`
				StartCmdAlt string `json:"startCmd"`
			} `json:"deploy"`
		}
		if json.Unmarshal(b, &plan) == nil {
			start = plan.Deploy.StartCmd
			if start == "" {
				start = plan.Deploy.StartCmdAlt
			}
		}
	}
	return provider, start
}

// ---- app.detect ----------------------------------------------------------------------------------

func appDetect(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DetectParams](oc)
	if err != nil {
		return nil, 2, err
	}
	work := filepath.Join(workRoot, "detect", oc.OpID)
	defer os.RemoveAll(work)
	src := filepath.Join(work, "src")
	commit, msg, code, err := checkout(oc, p.GitSource, src)
	if err != nil {
		return nil, code, err
	}
	r, code, err := detect(oc, src, work, p.DockerfilePath)
	r.Commit, r.Message = commit, msg
	if err != nil {
		return r, code, err
	}
	return r, 0, nil
}

// ---- app.build -----------------------------------------------------------------------------------

func appBuild(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.BuildParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if !agentproto.ValidID(p.AppID) || !agentproto.ValidAppName(p.App) || !agentproto.ValidID(p.DeploymentID) ||
		(p.Image != agentproto.ImageRepo(p.App)+":"+p.DeploymentID && p.Image != agentproto.LegacyImageRepo(p.App)+":"+p.DeploymentID) {
		return nil, 2, errors.New("bad app, deployment or image")
	}
	if p.DockerfilePath != "" && !agentproto.ValidBuildPath(p.DockerfilePath) {
		return nil, 2, errors.New("bad dockerfile path")
	}
	if p.BuildContext != "" && !agentproto.ValidBuildPath(p.BuildContext) {
		return nil, 2, errors.New("bad build context")
	}
	if code, err := dockerReady(oc); err != nil {
		return nil, code, err
	}
	release, err := acquireBuildSlot(oc)
	if err != nil {
		return nil, 1, err
	}
	defer release()
	work := filepath.Join(workRoot, "builds", p.DeploymentID)
	defer os.RemoveAll(work)
	src := filepath.Join(work, "src")

	oc.Progress(1, 4)
	commit, msg, code, err := checkout(oc, p.GitSource, src)
	if err != nil {
		return nil, code, err
	}
	oc.Progress(2, 4)
	// MH-576 D2: a pinned PHP version is written into composer.json's "require"."php" before
	// `railpack prepare` runs (Railpack reads the version from there, nowhere else); ini overrides
	// are written to a php.ini at the checkout root, unless the app ships its own (never
	// overwritten — it owns that file). Both are no-ops for a Dockerfile app.
	if err := pinComposerPHPVersion(oc, src, p.PHPVersion); err != nil {
		oc.Info("PHP version pin: %v — continuing without it", err)
	}
	if err := writePHPIniOverrides(src, p.PHPIni); err != nil {
		oc.Info("php.ini overrides: %v — continuing without them", err)
	}
	d, code, err := detect(oc, src, work, p.DockerfilePath)
	if err != nil {
		return nil, code, err
	}
	d.Commit, d.Message = commit, msg

	// MH-579 gap A: build-time env — an EnvBox of the values of the app's build_env-listed
	// variable names, opened here and passed as `--build-arg NAME` with the VALUE only ever
	// reaching docker through its own process environment (buildEnvArgs/envPairs), never an argv
	// and never logged. A name with no value in the box (the control plane skips one with no
	// matching env var) is simply not passed.
	buildEnv, err := oc.OpenEnv(agentproto.EnvContext(p.AppID, p.DeploymentID, "build"), p.Env)
	if err != nil {
		return nil, 1, err
	}

	oc.Progress(3, 4)
	labels := []string{"--label", agentproto.LabelApp + "=" + p.AppID, "--label", agentproto.LabelDeployment + "=" + p.DeploymentID}
	args := []string{"buildx", "build", "--progress=plain", "--load", "-t", p.Image}
	if p.NoCache {
		args = append(args, "--no-cache")
	}
	args = append(args, labels...)
	args = append(args, buildArgArgs(buildEnv)...)
	args = append(args, buildSecretArgs(buildEnv)...)
	if d.Runtime == "dockerfile" {
		dfPath, ok := safeJoin(src, orDefault(p.DockerfilePath, "Dockerfile"))
		if !ok {
			return nil, 2, errors.New("the dockerfile path does not resolve inside the checkout")
		}
		ctxPath, ok := safeJoin(src, p.BuildContext)
		if !ok {
			return nil, 2, errors.New("the build context does not resolve inside the checkout")
		}
		args = append(args, "-f", dfPath, ctxPath)
	} else {
		if p.RailpackFrontend == "" {
			return nil, 2, errors.New("no Railpack frontend image in the params")
		}
		// Railpack's generated plan encodes paths relative to the checkout root, so a custom
		// build_context does not apply here — only to a Dockerfile build.
		args = append(args, "--build-arg", "BUILDKIT_SYNTAX="+p.RailpackFrontend,
			"-f", filepath.Join(work, "railpack-plan.json"), src)
	}
	runEnv := append([]string{"DOCKER_BUILDKIT=1"}, envPairs(buildEnv)...)
	if code, err := oc.RunEnv(runEnv, "docker", args...); err != nil {
		return nil, code, errors.New("the build failed — see the log above; the live release is untouched")
	}
	id, _ := execOutput(oc, "docker", "image", "inspect", "--format", "{{.Id}}", p.Image)

	oc.Progress(4, 4)
	keep := p.Keep
	if keep <= 0 {
		keep = agentproto.DefaultKeepImages
	}
	pruned := pruneImages(oc, p.App, keep, append(p.Protect, p.Image))
	return agentproto.BuildResult{DetectResult: d, Image: p.Image, ImageID: strings.TrimSpace(id), Pruned: pruned}, 0, nil
}

// buildSlot lets one image build run on a server at a time. Ops run in a lane per app, so two apps
// deploying together used to build in parallel — on a 3 GB guest, two no-cache Vite builds pushed
// the load past 90 and neither finished before its timeout.
var buildSlot = make(chan struct{}, 1)

// acquireBuildSlot waits for the server's build slot (logging once that it is waiting) and returns
// its release; it gives up when the op's context ends.
func acquireBuildSlot(oc *OpContext) (func(), error) {
	select {
	case buildSlot <- struct{}{}:
		return func() { <-buildSlot }, nil
	default:
	}
	oc.Info("another app is building on this server — waiting for it to finish")
	select {
	case buildSlot <- struct{}{}:
		return func() { <-buildSlot }, nil
	case <-oc.Ctx.Done():
		return nil, errors.New("timed out waiting for another build on this server to finish")
	}
}

func dockerReady(oc *OpContext) (int, error) {
	if d := DockerInfo(oc.Ctx); !d.Running {
		return 1, errors.New("Docker is not running on this server — run the docker.install provisioning step")
	}
	if exec.CommandContext(oc.Ctx, "docker", "buildx", "version").Run() != nil { //#nosec G204 -- fixed argv
		return 1, errors.New("docker buildx is missing — run the build.tools provisioning step")
	}
	return 0, nil
}

// pruneImages keeps the newest `keep` images of hosbah/<app> — counting the app's images from before
// the rename, cloudy/<app>, with them — plus the protected ones.
func pruneImages(oc *OpContext, app string, keep int, protect []string) []string {
	var out string
	for _, repo := range []string{agentproto.ImageRepo(app), agentproto.LegacyImageRepo(app)} {
		b, err := execOutput(oc, "docker", "image", "ls", repo, "--format", "{{.Repository}}:{{.Tag}}\t{{.CreatedAt}}")
		if err != nil {
			return nil
		}
		out += b + "\n"
	}
	out = strings.TrimSpace(out)
	type img struct{ ref, created string }
	var imgs []img
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(l, "\t", 2)
		if len(parts) == 2 && !strings.HasSuffix(parts[0], ":<none>") {
			imgs = append(imgs, img{parts[0], parts[1]})
		}
	}
	sort.Slice(imgs, func(i, j int) bool { return imgs[i].created > imgs[j].created })
	protected := map[string]bool{}
	for _, p := range protect {
		protected[p] = true
	}
	var pruned []string
	for i, im := range imgs {
		if i < keep || protected[im.ref] {
			continue
		}
		if _, err := oc.Run("docker", "image", "rm", im.ref); err == nil {
			pruned = append(pruned, im.ref)
		}
	}
	if len(pruned) > 0 {
		oc.Info("pruned %d old image(s); kept the newest %d", len(pruned), keep)
	}
	return pruned
}

// ---- app.release ---------------------------------------------------------------------------------

func appRelease(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.ReleaseParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if !agentproto.ValidID(p.AppID) || !agentproto.ValidAppName(p.App) || !agentproto.ValidID(p.DeploymentID) ||
		!agentproto.IsAppImage(p.App, p.Image) || strings.TrimSpace(p.Command) == "" {
		return nil, 2, errors.New("bad release params")
	}
	env, err := oc.OpenEnv(agentproto.EnvContext(p.AppID, p.DeploymentID, "release"), p.Env)
	if err != nil {
		return nil, 1, err
	}
	name := agentproto.ContainerName(p.App, "release", p.DeploymentID)
	_ = exec.CommandContext(oc.Ctx, "docker", "rm", "-f", name).Run() //#nosec G204 -- a retry after a crash
	if err := ensureAppNetwork(oc); err != nil {
		return nil, 1, err
	}
	args := []string{"run", "--rm", "--name", name, "--label", agentproto.LabelApp + "=" + p.AppID, "--label", agentproto.LabelProcess + "=release",
		"--network", appNetwork}
	args = append(args, envArgs(env)...)
	args = append(args, "--entrypoint", "/bin/sh", p.Image, "-c", p.Command)
	oc.Info("release command: %s", p.Command)
	if code, err := oc.RunEnv(envPairs(env), "docker", args...); err != nil {
		return nil, code, errors.New("the release command failed — the live release is untouched")
	}
	return map[string]any{"ok": true}, 0, nil
}

// envArgs is `-e NAME` for each variable — docker reads the value from its own environment
// (envPairs), so no value is ever on a command line.
func envArgs(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []string
	for _, k := range names {
		out = append(out, "-e", k)
	}
	return out
}

func envPairs(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// buildArgArgs is `--build-arg NAME` for each build-time variable — docker reads the value from
// its own process environment (envPairs, passed to oc.RunEnv), so no value is ever on a command
// line or in a log. Same idea as envArgs/-e for a container's runtime environment.
func buildArgArgs(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []string
	for _, k := range names {
		out = append(out, "--build-arg", k)
	}
	return out
}

// buildSecretArgs is `--secret id=NAME,env=NAME` for each build-time variable, so a Dockerfile can
// read a token with `RUN --mount=type=secret,id=NAME` (a private Go module or npm package) without
// declaring it as an ARG, which would leave it in the image's history. The value comes from the
// same process environment as buildArgArgs.
func buildSecretArgs(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []string
	for _, k := range names {
		out = append(out, "--secret", "id="+k+",env="+k)
	}
	return out
}

// orDefault returns s, or def when s is empty — dockerfile_path/build_context's "" means "the
// default", never a literal empty path.
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ---- app.start -----------------------------------------------------------------------------------

func appStart(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.StartParams](oc)
	if err != nil {
		return nil, 2, err
	}
	res, code, err := startProcess(oc, p)
	if err != nil {
		return nil, code, err
	}
	return res, 0, nil
}

// startProcess starts one process from an image. web publishes its port on 127.0.0.1 at a port
// the kernel picks (ephemeral, so old and new run side by side) and must pass the health gate; a
// worker must still be running a few seconds later. A failed start leaves nothing behind.
func startProcess(oc *OpContext, p agentproto.StartParams) (agentproto.StartResult, int, error) {
	var res agentproto.StartResult
	if err := p.Validate(); err != nil {
		return res, 2, err
	}
	env, err := oc.OpenEnv(agentproto.EnvContext(p.AppID, p.DeploymentID, p.Process), p.Env)
	if err != nil {
		return res, 1, err
	}
	name := agentproto.ContainerName(p.App, p.Process, p.DeploymentID)
	_ = exec.CommandContext(oc.Ctx, "docker", "rm", "-f", name).Run() //#nosec G204 -- idempotent re-run
	// A volume made before the rename keeps its name (and its data): cloudy-vol-… when that exists.
	p.Mounts = resolveMounts(oc.Ctx, appDocker, p.Mounts)
	if err := ensureAppNetwork(oc); err != nil {
		return res, 1, err
	}
	args, pairs := startArgs(p, name, env)
	oc.Info("starting %s (%s)", p.Process, name)
	for _, m := range p.Mounts {
		oc.Info("volume %s at %s", m.Volume, m.MountPath)
	}
	if code, err := oc.RunEnv(pairs, "docker", args...); err != nil {
		return res, code, fmt.Errorf("could not start %s", p.Process)
	}
	res.Container = name
	fail := func(code int, err error) (agentproto.StartResult, int, error) {
		dumpLogs(oc, name, 40)
		_ = exec.Command("docker", "rm", "-f", name).Run() //#nosec G204 -- cleanup, even after the op's deadline
		oc.Info("removed %s — the live release is untouched", name)
		return agentproto.StartResult{}, code, err
	}

	if p.Process != "web" {
		time.Sleep(5 * time.Second)
		if !containerRunning(oc, name) {
			return fail(1, fmt.Errorf("%s exited right after starting", p.Process))
		}
		oc.Info("%s is running", p.Process)
		if p.Realtime == nil {
			return res, 0, nil
		}
		// The realtime worker: its port was published the way web's is; the deploy routes to it.
		hostPort, err := publishedPort(oc, name, p.Realtime.Port)
		if err != nil {
			return fail(1, err)
		}
		res.HostPort = hostPort
		oc.Info("%s (realtime, path %s) listens on 127.0.0.1:%d", name, p.Realtime.Path, hostPort)
		return res, 0, nil
	}

	hostPort, err := publishedPort(oc, name, p.Port)
	if err != nil {
		return fail(1, err)
	}
	res.HostPort = hostPort
	oc.Info("%s listens on 127.0.0.1:%d", name, hostPort)
	if p.Health != nil {
		if err := healthGate(oc, name, hostPort, *p.Health); err != nil {
			return fail(1, err)
		}
	}
	return res, 0, nil
}

// publishedPort reads which 127.0.0.1 port the kernel picked for a container port (publishArg) —
// web's, and the realtime worker's.
func publishedPort(oc *OpContext, name string, containerPort int) (int, error) {
	out, err := execOutput(oc, "docker", "port", name, strconv.Itoa(containerPort)+"/tcp")
	if err != nil {
		return 0, errors.New("the container did not publish its port")
	}
	return parsePublishedPort(out)
}

// parsePublishedPort reads the loopback port out of `docker port`'s answer. A port published
// anywhere but 127.0.0.1 is not one of ours and is not read.
func parsePublishedPort(out string) (int, error) {
	hostPort := 0
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if i := strings.LastIndex(l, ":"); i >= 0 && strings.HasPrefix(l, "127.0.0.1") {
			hostPort, _ = strconv.Atoi(strings.TrimSpace(l[i+1:]))
		}
	}
	if hostPort == 0 {
		return 0, fmt.Errorf("could not read the published port from %q", strings.TrimSpace(out))
	}
	return hostPort, nil
}

// publishArg is the one way a release container publishes a port: on loopback only, at a host port
// the kernel picks, so an old and a new release run side by side.
func publishArg(containerPort int) []string {
	return []string{"-p", "127.0.0.1::" + strconv.Itoa(containerPort)}
}

// startArgs is THE place a release container's `docker run` argv is built — web and workers alike,
// for app.start and app.rollback (restart) both. p has passed Validate: the mounts are this app's
// derived volume names and clean, allowed paths, so neither can smuggle a comma-separated option
// into --mount. Docker creates a named volume that does not exist yet.
func startArgs(p agentproto.StartParams, name string, env map[string]string) (args, pairs []string) {
	args = []string{"run", "-d", "--name", name, "--restart", "unless-stopped",
		"--label", agentproto.LabelApp + "=" + p.AppID, "--label", agentproto.LabelProcess + "=" + p.Process, "--label", agentproto.LabelDeployment + "=" + p.DeploymentID,
		"--log-opt", "max-size=10m", "--log-opt", "max-file=3", "--network", appNetwork}
	if p.Process == "web" {
		// Other apps on this server reach it by name: API_ORIGIN=http://<app>:<port>.
		args = append(args, "--network-alias", p.App)
	}
	pairs = envPairs(env)
	if p.Process == "web" {
		args = append(append(args, publishArg(p.Port)...), "-e", "PORT")
		pairs = append(pairs, "PORT="+strconv.Itoa(p.Port))
	} else if p.Realtime != nil {
		// The realtime worker only (Validate refuses it on web): an int, so nothing to smuggle. PORT is
		// not set — a websocket server reads its own variable (REVERB_SERVER_PORT, …).
		args = append(args, publishArg(p.Realtime.Port)...)
	}
	for _, m := range p.Mounts {
		args = append(args, "--mount", "type=volume,source="+m.Volume+",target="+m.MountPath)
	}
	args = append(args, p.Resources.DockerArgs()...)
	args = append(args, envArgs(env)...)
	if p.Command != "" {
		args = append(args, "--entrypoint", "/bin/sh", p.Image, "-c", p.Command)
	} else {
		args = append(args, p.Image)
	}
	return args, pairs
}

// appNetwork is the bridge network every app container joins, so the apps on one server reach each
// other by name (web carries the app's name as an alias) without publishing beyond 127.0.0.1.
const appNetwork = "hosbah"

// ensureAppNetwork creates appNetwork unless it exists.
func ensureAppNetwork(oc *OpContext) error {
	if exec.CommandContext(oc.Ctx, "docker", "network", "inspect", appNetwork).Run() == nil { //#nosec G204 -- constant args
		return nil
	}
	if out, err := exec.CommandContext(oc.Ctx, "docker", "network", "create", appNetwork).CombinedOutput(); err != nil && //#nosec G204 -- constant args
		!strings.Contains(string(out), "already exists") {
		return fmt.Errorf("could not create the %s network: %s", appNetwork, strings.TrimSpace(string(out)))
	}
	return nil
}

// healthGate: GET the path until it answers 2xx, the container dies, or the time is up.
func healthGate(oc *OpContext, container string, hostPort int, h agentproto.HealthSpec) error {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", hostPort, h.Path)
	oc.Info("health gate: GET %s must answer 2xx within %ds", h.Path, h.TimeoutSeconds)
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.Now().Add(time.Duration(h.TimeoutSeconds) * time.Second)
	last := ""
	for attempt := 1; ; attempt++ {
		if oc.Ctx.Err() != nil {
			return oc.Ctx.Err()
		}
		req, _ := http.NewRequestWithContext(oc.Ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		switch {
		case err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300:
			resp.Body.Close()
			oc.Info("health gate passed: HTTP %d after %d attempt(s)", resp.StatusCode, attempt)
			return nil
		case err == nil:
			resp.Body.Close()
			last = fmt.Sprintf("HTTP %d", resp.StatusCode)
		default:
			last = "no answer yet"
		}
		if attempt == 1 || attempt%5 == 0 {
			oc.Info("health gate: attempt %d — %s", attempt, last)
		}
		if !containerRunning(oc, container) {
			return fmt.Errorf("health gate failed: the container exited (last: %s)", last)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("health gate failed: GET %s did not answer 2xx within %ds (last: %s)", h.Path, h.TimeoutSeconds, last)
		}
		time.Sleep(time.Second)
	}
}

func containerRunning(oc *OpContext, name string) bool {
	out, err := execOutput(oc, "docker", "inspect", "--format", "{{.State.Running}}", name)
	return err == nil && strings.TrimSpace(out) == "true"
}

func dumpLogs(oc *OpContext, name string, lines int) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "docker", "logs", "--tail", strconv.Itoa(lines), name).CombinedOutput() //#nosec G204 -- fixed argv
	if s := strings.TrimSpace(string(out)); s != "" {
		oc.Info("last %d lines of %s:", lines, name)
		for _, l := range strings.Split(s, "\n") {
			oc.log("stderr", clip(l, maxLine))
		}
	}
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ---- route.set -----------------------------------------------------------------------------------

func routeSet(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.RouteParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if code, err := setRoute(oc, p, ""); err != nil {
		return nil, code, err
	}
	return map[string]any{"hosts": len(p.Hosts), "upstream": p.Upstream}, 0, nil
}

// setRoute points the app's hostnames at upstream (or removes them), then probes each plain-HTTP
// and internal-TLS hostname through Caddy itself. probePath "" = "/".
func setRoute(oc *OpContext, p agentproto.RouteParams, probePath string) (int, error) {
	if !agentproto.ValidID(p.AppID) {
		return 2, errors.New("bad app id")
	}
	for _, h := range p.Hosts {
		if !agentproto.ValidRouteHost(h) {
			return 2, fmt.Errorf("bad host %q (tls %s)", h.Hostname, h.TLS)
		}
	}
	if p.Upstream != "" && !strings.HasPrefix(p.Upstream, "127.0.0.1:") {
		return 2, errors.New("upstreams are local ports (127.0.0.1:<port>)")
	}
	if p.Realtime != nil {
		if err := p.Realtime.Validate(); err != nil {
			return 2, err
		}
	}
	err := applyRoutes(oc, func(t *routeTable) {
		if p.Upstream == "" {
			delete(t.Apps, p.AppID)
			return
		}
		// The cache toggle (MH-576 D8) is set separately (cache.set); route.set must never clear it.
		prev := t.Apps[p.AppID]
		t.Apps[p.AppID] = routeEntry{Hosts: p.Hosts, Upstream: p.Upstream, Realtime: p.Realtime, Cache: prev.Cache}
	})
	if err != nil {
		return 1, err
	}
	if p.Upstream == "" {
		oc.Info("route removed")
		return 0, nil
	}
	names := make([]string, 0, len(p.Hosts))
	for _, h := range p.Hosts {
		names = append(names, h.Hostname+" ("+h.TLS+")")
	}
	if len(names) == 0 {
		oc.Info("Caddy updated; this app has no domains yet, so nothing routes to %s", p.Upstream)
		return 0, nil
	}
	oc.Info("Caddy now routes %s → %s", strings.Join(names, ", "), p.Upstream)
	if p.Realtime != nil {
		oc.Info("realtime: %s* of the same hostnames → %s", p.Realtime.Path, p.Realtime.Upstream)
	}
	if probePath == "" {
		probePath = "/"
	}
	for _, h := range p.Hosts {
		if h.TLS == "auto" {
			oc.Info("probe %s skipped: its public certificate is issued on the first request, once the domain is verified", h.Hostname)
			continue
		}
		if err := probe(oc, h, probePath); err != nil {
			return 1, err
		}
	}
	return 0, nil
}

// probe requests the app through Caddy on this server, as a visitor to hostname would.
func probe(oc *OpContext, h agentproto.Route, path string) error {
	scheme, addr := "http", "127.0.0.1:80"
	if h.TLS == "internal" {
		scheme, addr = "https", "127.0.0.1:443"
	}
	if rest, ok := strings.CutPrefix(h.Hostname, "*."); ok {
		// A wildcard is probed through one name it covers: any answer below 500 (the app's own 404
		// for an unknown tenant included) shows the route reaches the app.
		h.Hostname = "hosbah-probe." + rest
	}
	tr := &http.Transport{
		// Dial Caddy on this host whatever the name resolves to; for internal TLS the certificate is
		// from Caddy's own CA, which this probe does not need to trust to see the route works.
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{ServerName: h.Hostname, InsecureSkipVerify: true}, //#nosec G402 -- local probe of our own route
	}
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var last string
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequestWithContext(oc.Ctx, http.MethodGet, scheme+"://"+h.Hostname+path, nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				oc.Info("probe %s://%s%s through Caddy: HTTP %d", scheme, h.Hostname, path, resp.StatusCode)
				return nil
			}
			last = fmt.Sprintf("HTTP %d", resp.StatusCode)
		} else {
			last = err.Error()
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("probe of %s through Caddy failed: %s", h.Hostname, last)
}

// ---- app.drain -----------------------------------------------------------------------------------

func appDrain(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DrainParams](oc)
	if err != nil {
		return nil, 2, err
	}
	stopped, code, err := drain(oc, p)
	if err != nil {
		return nil, code, err
	}
	return map[string]any{"stopped": stopped}, 0, nil
}

// drain stops (SIGTERM, then the grace period, then SIGKILL) and removes every container of the
// app — of one process, if given — except Keep.
func drain(oc *OpContext, p agentproto.DrainParams) ([]string, int, error) {
	if !agentproto.ValidID(p.AppID) || (p.Process != "" && !agentproto.ValidProcess(p.Process)) {
		return nil, 2, errors.New("bad drain params")
	}
	grace := p.GraceSeconds
	if grace <= 0 || grace > 300 {
		grace = 10
	}
	names, err := appContainers(oc, p.AppID, p.Process, true)
	if err != nil {
		return nil, 1, err
	}
	keep := map[string]bool{}
	for _, k := range p.Keep {
		keep[k] = true
	}
	only := map[string]bool{}
	for _, o := range p.Only {
		only[o] = true
	}
	var stopped []string
	for _, n := range names {
		if keep[n] || (len(only) > 0 && !only[n]) {
			continue
		}
		oc.Info("draining %s (SIGTERM, %ds grace)", n, grace)
		_, _ = dockerStep(oc, "stop", "-t", strconv.Itoa(grace), n)
		if _, err := dockerStep(oc, "rm", "-f", n); err == nil {
			stopped = append(stopped, n)
		}
	}
	if len(stopped) == 0 {
		oc.Info("nothing to drain")
	}
	return stopped, 0, nil
}

// appContainers lists the app's containers (all = include stopped ones), newest first: those
// labelled hosbah.app, then those an agent from before the rename labelled cloudy.app.
func appContainers(oc *OpContext, appID, process string, all bool) ([]string, error) {
	names, err := psNames(oc.Ctx, appDocker, func(l labelSet) []string {
		args := []string{"--filter", "label=" + l(agentproto.LabelApp) + "=" + appID, "--format", "{{.Names}}"}
		if all {
			args = append(args, "-a")
		}
		if process != "" {
			args = append(args, "--filter", "label="+l(agentproto.LabelProcess)+"="+process)
		}
		return args
	})
	if err != nil {
		return nil, errors.New("could not list the app's containers")
	}
	return names, nil
}

// resolveMounts points each mount at the volume as it exists on this host (resolveName).
func resolveMounts(ctx context.Context, run runFunc, mounts []agentproto.Mount) []agentproto.Mount {
	if len(mounts) == 0 {
		return mounts
	}
	out := make([]agentproto.Mount, len(mounts))
	for i, m := range mounts {
		m.Volume = resolveName(ctx, run, kindVolume, m.Volume)
		out[i] = m
	}
	return out
}

// ---- app.rollback --------------------------------------------------------------------------------

// appRollback re-points the app at a kept image in one round trip: start its web process, gate it,
// switch the route, drain the old web containers, then the same for workers. The build is skipped
// entirely — that is what makes it seconds. If the kept image fails its gate, nothing switched.
func appRollback(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.RollbackParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if p.Web.AppID != p.AppID {
		return nil, 2, errors.New("bad rollback params")
	}
	var res agentproto.RollbackResult
	oc.Progress(1, 4)
	web, code, err := startProcess(oc, p.Web)
	if err != nil {
		return nil, code, err
	}
	res.Web = web
	oc.Progress(2, 4)
	probePath := "/"
	if p.Web.Health != nil {
		probePath = p.Web.Health.Path
	}
	// Web switches first; the workers start after it. Until the new realtime worker is up, the
	// realtime route the app has now (the old release's socket server, still running) is kept.
	if code, err := setRoute(oc, agentproto.RouteParams{AppID: p.AppID, Hosts: p.Hosts, Upstream: fmt.Sprintf("127.0.0.1:%d", web.HostPort),
		Realtime: currentRealtime(p.AppID)}, probePath); err != nil {
		_ = exec.Command("docker", "rm", "-f", web.Container).Run() //#nosec G204 -- undo the start
		return nil, code, err
	}
	oc.Progress(3, 4)
	if _, code, err := drain(oc, agentproto.DrainParams{AppID: p.AppID, Process: "web", Keep: []string{web.Container}, GraceSeconds: p.GraceSeconds}); err != nil {
		return nil, code, err
	}
	oc.Progress(4, 4)
	keep := map[string][]string{}
	for _, w := range p.Workers {
		r, code, err := startProcess(oc, w)
		if err != nil {
			return res, code, fmt.Errorf("web is back on the kept release, but worker %s did not start: %w", w.Process, err)
		}
		res.Workers = append(res.Workers, r)
		keep[w.Process] = append(keep[w.Process], r.Container)
	}
	// The realtime route now points at the new worker — or goes, when this release has none —
	// before the old workers stop.
	if code, err := switchRealtime(oc, p.AppID, p.Workers, res.Workers); err != nil {
		return res, code, fmt.Errorf("web is back on the kept release, but its realtime route could not be set: %w", err)
	}
	// Workers the rolled-back release does not have, or older copies of the ones it does, stop.
	names, _ := appContainers(oc, p.AppID, "", true)
	for _, n := range names {
		if n == web.Container || strings.Contains(n, "-web-") || strings.Contains(n, "-release-") {
			continue
		}
		kept := false
		for _, cs := range keep {
			for _, c := range cs {
				kept = kept || c == n
			}
		}
		if !kept {
			_, _ = dockerStep(oc, "stop", "-t", strconv.Itoa(max(p.GraceSeconds, 1)), n)
			_, _ = dockerStep(oc, "rm", "-f", n)
		}
	}
	return res, 0, nil
}

// ---- app.logs ------------------------------------------------------------------------------------

// appLogs relays `docker logs --follow` for a bounded time and line count. Runtime logs are not
// collected continuously; they are relayed when someone asks.
func appLogs(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.LogsParams](oc)
	if err != nil {
		return nil, 2, err
	}
	process := p.Process
	if process == "" {
		process = "web"
	}
	if !agentproto.ValidID(p.AppID) || !agentproto.ValidProcess(process) {
		return nil, 2, errors.New("bad logs params")
	}
	tail := min(max(p.Tail, 0), agentproto.MaxLogTail)
	follow := min(max(p.FollowSeconds, 0), agentproto.MaxLogFollowSeconds)
	names, err := appContainers(oc, p.AppID, process, false)
	if err != nil {
		return nil, 1, err
	}
	if len(names) == 0 {
		return nil, 1, fmt.Errorf("no running %s container for this app", process)
	}
	name := names[0]
	ctx, cancel := context.WithTimeout(oc.Ctx, time.Duration(follow)*time.Second+2*time.Second)
	defer cancel()
	args := []string{"logs", "--timestamps", "--tail", strconv.Itoa(tail)}
	if follow > 0 {
		args = append(args, "--follow")
	}
	args = append(args, name)
	oc.Info("logs of %s: the last %d lines, then following for %ds", name, tail, follow)
	cmd := exec.CommandContext(ctx, "docker", args...) //#nosec G204 -- fixed argv
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, 1, err
	}
	lines := 0
	done := make(chan struct{}, 2)
	pump := func(r io.Reader, stream string) {
		defer func() { done <- struct{}{} }()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			if lines >= agentproto.MaxLogLines {
				cancel()
				continue
			}
			lines++
			oc.log(stream, clip(sc.Text(), maxLine))
		}
	}
	go pump(stdout, "stdout")
	go pump(stderr, "stderr")
	<-done
	<-done
	_ = cmd.Wait()
	if lines >= agentproto.MaxLogLines {
		oc.Info("stopped after %d lines", agentproto.MaxLogLines)
	} else {
		oc.Info("end of the follow window")
	}
	return map[string]any{"container": name, "lines": lines}, 0, nil
}

// ---- app.remove ----------------------------------------------------------------------------------

func appRemove(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.RemoveParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if !agentproto.ValidID(p.AppID) || !agentproto.ValidAppName(p.App) {
		return nil, 2, errors.New("bad app")
	}
	if caddyUp(oc.Ctx) {
		if code, err := setRoute(oc, agentproto.RouteParams{AppID: p.AppID}, ""); err != nil {
			return nil, code, err
		}
	}
	stopped, code, err := drain(oc, agentproto.DrainParams{AppID: p.AppID, GraceSeconds: 10})
	if err != nil {
		return nil, code, err
	}
	var removed []string
	for _, repo := range []string{agentproto.ImageRepo(p.App), agentproto.LegacyImageRepo(p.App)} {
		out, _ := execOutput(oc, "docker", "image", "ls", repo, "--format", "{{.Repository}}:{{.Tag}}")
		removed = append(removed, removeImages(oc, strings.Fields(out))...)
	}
	return map[string]any{"containers": stopped, "images": removed}, 0, nil
}

func removeImages(oc *OpContext, refs []string) []string {
	var removed []string
	for _, ref := range refs {
		if _, err := oc.Run("docker", "image", "rm", "-f", ref); err == nil {
			removed = append(removed, ref)
		}
	}
	return removed
}

// appLimits: app.limits — a live `docker update` of the named containers (no restart). A container
// that no longer exists (already drained by a deploy that raced this) is skipped, not a failure.
func appLimits(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.AppLimitsParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	args := append([]string{"update"}, p.Resources.DockerArgs()...)
	if len(args) == 1 {
		return agentproto.AppLimitsResult{}, 0, nil // unlimited on both — nothing to change
	}
	var applied []string
	for _, name := range p.Containers {
		if !containerRunning(oc, name) {
			oc.Info("%s is not running — skipped", name)
			continue
		}
		if _, err := oc.Run("docker", append(append([]string{}, args...), name)...); err != nil {
			return nil, 1, fmt.Errorf("could not update %s", name)
		}
		applied = append(applied, name)
		oc.Info("updated limits on %s", name)
	}
	return agentproto.AppLimitsResult{Applied: applied}, 0, nil
}
