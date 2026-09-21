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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// App operations (P2). Everything here is a fixed argv for git / docker / railpack, or Caddy JSON —
// no host shell. The two strings a user controls that do run are the release command and a process
// command from cloudy.toml, and both run INSIDE the app's own container (sh -c), never on the host.

const workRoot = "/var/lib/cloudy"

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
	if ghRepo != "" {
		helper = ghCredentialHelper()
	}
	if helper != "" {
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
		if code, err := oc.RunEnv(env, "git", append(cred, gitArgs(argv...)...)...); err != nil {
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

func gitOut(oc *OpContext, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(oc.Ctx, "git", gitArgs(append([]string{"-C", dir}, args...)...)...) //#nosec G204 -- fixed argv
	cmd.Env = append(os.Environ(), gitEnv...)
	out, err := cmd.Output()
	return string(out), err
}

// ---- detection -----------------------------------------------------------------------------------

// detect decides how dir builds: its own Dockerfile first, else Railpack (whose `prepare` writes
// the build plan and says what it found). cloudy.toml is returned raw for the control plane to
// parse.
func detect(oc *OpContext, src, workdir string) (agentproto.DetectResult, int, error) {
	var r agentproto.DetectResult
	if b, err := os.ReadFile(filepath.Join(src, "cloudy.toml")); err == nil { //#nosec G304 -- inside our checkout
		if len(b) > agentproto.MaxHosbahToml {
			return r, 1, errors.New("cloudy.toml is larger than 64 KiB")
		}
		r.HosbahToml = string(b)
		oc.Info("found cloudy.toml")
	}
	if st, err := os.Stat(filepath.Join(src, "Dockerfile")); err == nil && st.Mode().IsRegular() {
		r.Runtime = "dockerfile"
		oc.Info("build: the repository's Dockerfile")
		return r, 0, nil
	}
	if _, err := os.Stat(railpackBin); err != nil {
		return r, 1, errors.New("no Dockerfile, and Railpack is not installed — run the railpack.install provisioning step")
	}
	r.Runtime = "railpack"
	plan, info := filepath.Join(workdir, "railpack-plan.json"), filepath.Join(workdir, "railpack-info.json")
	var code int
	var err error
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
				StartCmd     string `json:"startCommand"`
				StartCmdAlt  string `json:"startCmd"`
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
	r, code, err := detect(oc, src, work)
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
		p.Image != agentproto.ImageRepo(p.App)+":"+p.DeploymentID {
		return nil, 2, errors.New("bad app, deployment or image")
	}
	if code, err := dockerReady(oc); err != nil {
		return nil, code, err
	}
	work := filepath.Join(workRoot, "builds", p.DeploymentID)
	defer os.RemoveAll(work)
	src := filepath.Join(work, "src")

	oc.Progress(1, 4)
	commit, msg, code, err := checkout(oc, p.GitSource, src)
	if err != nil {
		return nil, code, err
	}
	oc.Progress(2, 4)
	d, code, err := detect(oc, src, work)
	if err != nil {
		return nil, code, err
	}
	d.Commit, d.Message = commit, msg

	oc.Progress(3, 4)
	labels := []string{"--label", "cloudy.app=" + p.AppID, "--label", "cloudy.deployment=" + p.DeploymentID}
	args := []string{"buildx", "build", "--progress=plain", "--load", "-t", p.Image}
	args = append(args, labels...)
	if d.Runtime == "dockerfile" {
		args = append(args, "-f", filepath.Join(src, "Dockerfile"), src)
	} else {
		if p.RailpackFrontend == "" {
			return nil, 2, errors.New("no Railpack frontend image in the params")
		}
		args = append(args, "--build-arg", "BUILDKIT_SYNTAX="+p.RailpackFrontend,
			"-f", filepath.Join(work, "railpack-plan.json"), src)
	}
	if code, err := oc.RunEnv([]string{"DOCKER_BUILDKIT=1"}, "docker", args...); err != nil {
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

func dockerReady(oc *OpContext) (int, error) {
	if d := DockerInfo(oc.Ctx); !d.Running {
		return 1, errors.New("Docker is not running on this server — run the docker.install provisioning step")
	}
	if exec.CommandContext(oc.Ctx, "docker", "buildx", "version").Run() != nil { //#nosec G204 -- fixed argv
		return 1, errors.New("docker buildx is missing — run the build.tools provisioning step")
	}
	return 0, nil
}

// pruneImages keeps the newest `keep` images of cloudy/<app> plus the protected ones.
func pruneImages(oc *OpContext, app string, keep int, protect []string) []string {
	out, err := execOutput(oc, "docker", "image", "ls", agentproto.ImageRepo(app), "--format", "{{.Repository}}:{{.Tag}}\t{{.CreatedAt}}")
	if err != nil {
		return nil
	}
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
		!strings.HasPrefix(p.Image, agentproto.ImageRepo(p.App)+":") || strings.TrimSpace(p.Command) == "" {
		return nil, 2, errors.New("bad release params")
	}
	env, err := oc.OpenEnv(agentproto.EnvContext(p.AppID, p.DeploymentID, "release"), p.Env)
	if err != nil {
		return nil, 1, err
	}
	name := agentproto.ContainerName(p.App, "release", p.DeploymentID)
	_ = exec.CommandContext(oc.Ctx, "docker", "rm", "-f", name).Run() //#nosec G204 -- a retry after a crash
	args := []string{"run", "--rm", "--name", name, "--label", "cloudy.app=" + p.AppID, "--label", "cloudy.process=release"}
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
		return res, 0, nil
	}

	out, err := execOutput(oc, "docker", "port", name, strconv.Itoa(p.Port)+"/tcp")
	if err != nil {
		return fail(1, errors.New("the container did not publish its port"))
	}
	hostPort := 0
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if i := strings.LastIndex(l, ":"); i >= 0 && strings.HasPrefix(l, "127.0.0.1") {
			hostPort, _ = strconv.Atoi(strings.TrimSpace(l[i+1:]))
		}
	}
	if hostPort == 0 {
		return fail(1, fmt.Errorf("could not read the published port from %q", strings.TrimSpace(out)))
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

// startArgs is THE place a release container's `docker run` argv is built — web and workers alike,
// for app.start and app.rollback (restart) both. p has passed Validate: the mounts are this app's
// derived volume names and clean, allowed paths, so neither can smuggle a comma-separated option
// into --mount. Docker creates a named volume that does not exist yet.
func startArgs(p agentproto.StartParams, name string, env map[string]string) (args, pairs []string) {
	args = []string{"run", "-d", "--name", name, "--restart", "unless-stopped",
		"--label", "cloudy.app=" + p.AppID, "--label", "cloudy.process=" + p.Process, "--label", "cloudy.deployment=" + p.DeploymentID,
		"--log-opt", "max-size=10m", "--log-opt", "max-file=3"}
	pairs = envPairs(env)
	if p.Process == "web" {
		port := strconv.Itoa(p.Port)
		args = append(args, "-p", "127.0.0.1::"+port, "-e", "PORT")
		pairs = append(pairs, "PORT="+port)
	}
	for _, m := range p.Mounts {
		args = append(args, "--mount", "type=volume,source="+m.Volume+",target="+m.MountPath)
	}
	args = append(args, envArgs(env)...)
	if p.Command != "" {
		args = append(args, "--entrypoint", "/bin/sh", p.Image, "-c", p.Command)
	} else {
		args = append(args, p.Image)
	}
	return args, pairs
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
		if !agentproto.ValidHostname(h.Hostname) || !agentproto.ValidTLS(h.TLS) {
			return 2, fmt.Errorf("bad host %q", h.Hostname)
		}
	}
	if p.Upstream != "" && !strings.HasPrefix(p.Upstream, "127.0.0.1:") {
		return 2, errors.New("upstreams are local ports (127.0.0.1:<port>)")
	}
	err := applyRoutes(oc, func(t *routeTable) {
		if p.Upstream == "" {
			delete(t.Apps, p.AppID)
			return
		}
		t.Apps[p.AppID] = routeEntry{Hosts: p.Hosts, Upstream: p.Upstream}
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
		_, _ = oc.Run("docker", "stop", "-t", strconv.Itoa(grace), n)
		if _, err := oc.Run("docker", "rm", "-f", n); err == nil {
			stopped = append(stopped, n)
		}
	}
	if len(stopped) == 0 {
		oc.Info("nothing to drain")
	}
	return stopped, 0, nil
}

// appContainers lists the app's containers (all = include stopped ones), newest first.
func appContainers(oc *OpContext, appID, process string, all bool) ([]string, error) {
	args := []string{"ps", "--filter", "label=cloudy.app=" + appID, "--format", "{{.Names}}"}
	if all {
		args = append(args, "-a")
	}
	if process != "" {
		args = append(args, "--filter", "label=cloudy.process="+process)
	}
	out, err := execOutput(oc, "docker", args...)
	if err != nil {
		return nil, errors.New("could not list the app's containers")
	}
	var names []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			names = append(names, l)
		}
	}
	return names, nil
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
	if code, err := setRoute(oc, agentproto.RouteParams{AppID: p.AppID, Hosts: p.Hosts, Upstream: fmt.Sprintf("127.0.0.1:%d", web.HostPort)}, probePath); err != nil {
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
			_, _ = oc.Run("docker", "stop", "-t", strconv.Itoa(max(p.GraceSeconds, 1)), n)
			_, _ = oc.Run("docker", "rm", "-f", n)
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
	out, _ := execOutput(oc, "docker", "image", "ls", agentproto.ImageRepo(p.App), "--format", "{{.Repository}}:{{.Tag}}")
	var removed []string
	for _, ref := range strings.Fields(out) {
		if _, err := oc.Run("docker", "image", "rm", "-f", ref); err == nil {
			removed = append(removed, ref)
		}
	}
	return map[string]any{"containers": stopped, "images": removed}, 0, nil
}
