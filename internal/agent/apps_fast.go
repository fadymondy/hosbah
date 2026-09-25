package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// ---- app.fast_build (MH-573 A7, redesigned MH-573 A7b) -----------------------------------------
//
// The fast track skips a from-scratch rebuild, but what "fast" means depends on the app
// (agentproto.FastBuildParams.Mode, deploy.FastBuildMode):
//
//   - dockerfile: the app has its own Dockerfile — fastBuildDockerfile runs the exact same build
//     as app.build, just with BuildKit's local cache kept between deploys instead of thrown away.
//     Multi-stage Dockerfiles (a golang builder, a node builder, a slim runtime with no toolchain)
//     work exactly as a full build would, because it IS that build, only warmer.
//   - builder: no Dockerfile, but a configured builder image (a preset's golang/node/composer/
//     python image, or a hand-picked one) — fastBuildWithBuilder runs the steps in a throwaway
//     `docker run` of that image with the checkout bind-mounted, then copies the configured output
//     paths onto the app's current runtime image with a generated `FROM <base>\nCOPY …` build. The
//     runtime image itself never needs a toolchain, so this also fixes multi-stage Dockerfile apps
//     once they get a matching builder_image/outputs configuration instead of a Dockerfile.
//   - runtime: the original MH-573 A7 behaviour, kept as the fallback for an app with neither a
//     Dockerfile nor a builder image configured (nothing to build, e.g. static) —
//     fastBuildInRuntime runs the steps directly inside BaseImage and `docker commit`s the result.
//
// Every mode keeps a persistent checkout per app under the agent's work dir (fetches the requested
// ref, same credentials as app.build/app.detect) and diffs the new commit against the one last
// built there. If a build-affecting path changed (agentproto.FullTrackMatch — the same rule
// deploy.ChooseTrack applies before dispatch), it aborts before running any mode: the result says
// so, and the control plane falls back to a full app.build.

// fastCheckoutDir is where a persistent, per-app checkout lives (kept between deploys, unlike
// app.build's and app.detect's one-shot dirs under builds/ and detect/).
func fastCheckoutDir(app string) string { return filepath.Join(workRoot, "apps", app, "src") }

// fastCacheVolume is the named Docker volume that carries an app's package/build caches across
// fast deploys.
func fastCacheVolume(app string) string { return agentproto.NamePrefix + "fastcache-" + app }

func appFastBuild(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.FastBuildParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if p.Mode == "" {
		p.Mode = agentproto.FastModeRuntime // an old control plane that predates the mode field
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if d := DockerInfo(oc.Ctx); !d.Running {
		return nil, 1, errors.New("Docker is not running on this server — run the docker.install provisioning step")
	}

	dir := fastCheckoutDir(p.App)
	oc.Progress(1, 5)
	commit, prevCommit, msg, code, err := fastCheckout(oc, p.GitSource, dir)
	if err != nil {
		return nil, code, err
	}
	files := topLevelFiles(dir)

	var changed []string
	if prevCommit != "" && prevCommit != commit {
		if out, derr := gitOut(oc, dir, "diff", "--name-only", prevCommit, commit); derr == nil {
			for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
				if l = strings.TrimSpace(l); l != "" {
					changed = append(changed, l)
				}
			}
		} else {
			oc.Info("could not diff against the previous build (%v) — proceeding without a changed-files list", derr)
		}
	}
	if path, ok := agentproto.FullTrackMatch(changed); ok {
		oc.Info("%s changed — a full rebuild is required", path)
		return agentproto.FastBuildResult{Commit: commit, PrevCommit: prevCommit, Message: msg, Changed: changed,
			Aborted: true, AbortPath: path, AbortReason: path + " changed", DetectedFiles: files}, 0, nil
	}
	oc.Progress(2, 5)

	var image, imageID string
	switch p.Mode {
	case agentproto.FastModeDockerfile:
		image, imageID, code, err = fastBuildDockerfile(oc, p, dir)
	case agentproto.FastModeBuilder:
		image, imageID, code, err = fastBuildWithBuilder(oc, p, dir)
	default: // FastModeRuntime
		image, imageID, code, err = fastBuildInRuntime(oc, p, dir)
	}
	if err != nil {
		return nil, code, err
	}
	oc.Progress(5, 5)

	return agentproto.FastBuildResult{Commit: commit, PrevCommit: prevCommit, Message: msg, Changed: changed,
		Image: image, ImageID: imageID, DetectedFiles: files}, 0, nil
}

// fastBuildDockerfile (MH-573 A7b) is the fast track for an app with a Dockerfile: the exact image
// a full build would produce, reusing the Docker daemon's own layer cache, so unchanged layers
// (dependencies, base images) never rebuild. The default docker driver cannot export a
// type=local cache, so the daemon cache is the one every Docker install has. Kept as a normal
// `docker build`/buildx invocation, not app.build's — app.build always ran without --cache-to/from,
// and keeps doing so, so `full` stays a from-scratch, cache-free rebuild by design.
func fastBuildDockerfile(oc *OpContext, p agentproto.FastBuildParams, dir string) (image, imageID string, code int, err error) {
	if exec.CommandContext(oc.Ctx, "docker", "buildx", "version").Run() != nil { //#nosec G204 -- fixed argv
		return "", "", 1, errors.New("docker buildx is missing — run the build.tools provisioning step")
	}
	labels := []string{"--label", agentproto.LabelApp + "=" + p.AppID, "--label", agentproto.LabelDeployment + "=" + p.DeploymentID}
	args := []string{"buildx", "build", "--progress=plain", "--load", "-t", p.Image}
	args = append(args, labels...)
	args = append(args, "-f", filepath.Join(dir, "Dockerfile"), dir)
	oc.Progress(3, 5)
	if code, err := oc.RunEnv([]string{"DOCKER_BUILDKIT=1"}, "docker", args...); err != nil {
		return "", "", code, errors.New("the cached build failed — see the log above; the live release is untouched")
	}
	oc.Progress(4, 5)
	id, _ := execOutput(oc, "docker", "image", "inspect", "--format", "{{.Id}}", p.Image)
	return p.Image, strings.TrimSpace(id), 0, nil
}

// fastBuildWithBuilder (MH-573 A7b) is the fast track for an app with no Dockerfile but a
// configured builder image: the steps run in a `docker run` of BuilderImage with the checkout
// bind-mounted (so their output lands back on disk, unlike app.fast_build's original docker-cp —
// there is no live container to commit here), then Outputs are layered onto BaseImage with a
// generated `FROM <base>\nCOPY …` build. The runtime image never needs the builder's toolchain.
func fastBuildWithBuilder(oc *OpContext, p agentproto.FastBuildParams, dir string) (image, imageID string, code int, err error) {
	cache := fastCacheVolume(p.App)
	env, err := oc.OpenEnv(agentproto.EnvContext(p.AppID, p.DeploymentID, "fast_build"), p.Env)
	if err != nil {
		return "", "", 2, err
	}
	runArgs := []string{"run", "--rm", "-v", dir + ":/app", "-w", "/app", "-v", cache + ":/cache",
		"-e", "HOME=/cache", "-e", "XDG_CACHE_HOME=/cache", "-e", "GOCACHE=/cache/go-build",
		"-e", "GOMODCACHE=/cache/go-mod", "-e", "npm_config_cache=/cache/npm", "-e", "COMPOSER_HOME=/cache/composer"}
	for k, v := range env {
		runArgs = append(runArgs, "-e", k+"="+v)
	}
	oc.Progress(3, 5)
	stepErr, stepCode := runFastSteps(p.Steps, func(args ...string) (int, error) {
		full := append(append([]string{}, runArgs...), p.BuilderImage)
		full = append(full, args...) // args is always ["sh", "-c", run]
		return oc.Run("docker", full...)
	}, oc.Info)
	if stepErr != nil {
		return "", "", stepCode, stepErr
	}
	oc.Progress(4, 5)

	genDir, err := os.MkdirTemp("", "hosbah-fastbuild-*")
	if err != nil {
		return "", "", 1, err
	}
	defer os.RemoveAll(genDir)
	dockerfile := "FROM " + p.BaseImage + "\n"
	for _, o := range p.Outputs {
		dockerfile += "COPY " + fastCopyArgs(o) + "\n"
	}
	dockerfilePath := filepath.Join(genDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0o600); err != nil {
		return "", "", 1, err
	}
	labels := []string{"--label", agentproto.LabelApp + "=" + p.AppID, "--label", agentproto.LabelDeployment + "=" + p.DeploymentID}
	args := append([]string{"build", "--progress=plain", "-t", p.Image, "-f", dockerfilePath}, labels...)
	args = append(args, dir)
	if code, err := oc.RunEnv([]string{"DOCKER_BUILDKIT=1"}, "docker", args...); err != nil {
		return "", "", code, errors.New("could not layer the build output onto the runtime image")
	}
	id, _ := execOutput(oc, "docker", "image", "inspect", "--format", "{{.Id}}", p.Image)
	return p.Image, strings.TrimSpace(id), 0, nil
}

// fastCopyArgs turns one output path into a `COPY <src> <dest>` pair (relative to the checkout,
// which is also the generated Dockerfile's build context): the same relative path in the image,
// under /app — the working directory app.build's own Dockerfile-mode images already use by
// convention. "." copies the whole checkout.
func fastCopyArgs(out string) string {
	if out == "." {
		return ". /app/"
	}
	clean := strings.TrimPrefix(out, "/")
	return clean + " /app/" + clean
}

// fastBuildInRuntime is the original MH-573 A7 fast track: the steps run inside a container started
// from BaseImage with the checkout docker-cp'd in, then the container is committed — the fallback
// for an app with no Dockerfile and no configured builder image.
func fastBuildInRuntime(oc *OpContext, p agentproto.FastBuildParams, dir string) (image, imageID string, code int, err error) {
	env, err := oc.OpenEnv(agentproto.EnvContext(p.AppID, p.DeploymentID, "fast_build"), p.Env)
	if err != nil {
		return "", "", 2, err
	}
	ctr := agentproto.NamePrefix + "fastbuild-" + p.App
	cache := fastCacheVolume(p.App)
	_, _ = dockerStep(oc, "rm", "-f", ctr) // best-effort: a stale container from a previous failed attempt

	createArgs := []string{"create", "--name", ctr, "-v", cache + ":/cache",
		"-e", "HOME=/cache", "-e", "XDG_CACHE_HOME=/cache", "-e", "GOCACHE=/cache/go-build",
		"-e", "GOMODCACHE=/cache/go-mod", "-e", "npm_config_cache=/cache/npm", "-e", "COMPOSER_HOME=/cache/composer"}
	for k, v := range env {
		createArgs = append(createArgs, "-e", k+"="+v)
	}
	createArgs = append(createArgs, p.BaseImage, "tail", "-f", "/dev/null")
	if code, err := dockerStep(oc, createArgs...); err != nil {
		return "", "", code, fmt.Errorf("could not create the build container from %s", p.BaseImage)
	}
	defer func() { _, _ = dockerStep(oc, "rm", "-f", ctr) }()
	if code, err := dockerStep(oc, "start", ctr); err != nil {
		return "", "", code, errors.New("could not start the build container")
	}
	if code, err := dockerStep(oc, "exec", ctr, "mkdir", "-p", "/app"); err != nil {
		return "", "", code, err
	}
	if code, err := oc.Run("docker", "cp", dir+"/.", ctr+":/app"); err != nil {
		return "", "", code, errors.New("could not copy the checkout into the build container")
	}
	oc.Progress(3, 5)

	stepErr, code := runFastSteps(p.Steps, func(args ...string) (int, error) {
		return oc.Run("docker", append([]string{"exec", "-w", "/app", ctr}, args...)...)
	}, oc.Info)
	if stepErr != nil {
		return "", "", code, stepErr
	}
	oc.Progress(4, 5)

	if code, err := dockerStep(oc, "stop", ctr); err != nil {
		return "", "", code, errors.New("could not stop the build container")
	}
	if code, err := dockerStep(oc, "commit", ctr, p.Image); err != nil {
		return "", "", code, errors.New("could not commit the new image")
	}
	id, _ := execOutput(oc, "docker", "image", "inspect", "--format", "{{.Id}}", p.Image)
	return p.Image, strings.TrimSpace(id), 0, nil
}

// runFastSteps runs an app's enabled install/build/migrate steps in order (each `sh -c <run>`,
// via exec — the container the caller already started and copied the checkout into), skipping
// on_failure, which it instead runs, best-effort, once some other step fails. It is pulled out of
// appFastBuild so tests can drive it with a fake exec, without a real Docker daemon.
func runFastSteps(steps []agentproto.FastBuildStep, exec func(args ...string) (int, error), info func(format string, args ...any)) (error, int) {
	var onFailure string
	for _, st := range steps {
		if st.Key == "on_failure" {
			onFailure = st.Run // read up front: on_failure may be listed before the step that fails
		}
	}
	for _, st := range steps {
		if st.Key == "on_failure" {
			continue
		}
		if strings.TrimSpace(st.Run) == "" {
			continue
		}
		info("step %s: %s", st.Key, st.Name)
		if code, err := exec("sh", "-c", st.Run); err != nil {
			if onFailure != "" {
				info("running the on_failure step")
				_, _ = exec("sh", "-c", onFailure)
			}
			return fmt.Errorf("step %q failed", st.Key), code
		}
	}
	return nil, 0
}

// fastCheckout is checkout (apps.go) for a persistent directory: an existing .git is fetched and
// checked out in place rather than re-cloned, and it reports the commit that was checked out
// there before this fetch (prevCommit, "" the first time), for the caller to diff against.
func fastCheckout(oc *OpContext, src agentproto.GitSource, dir string) (commit, prevCommit, message string, code int, err error) {
	if err := agentproto.CheckRepoURL(src.RepoURL); err != nil {
		return "", "", "", 2, err
	}
	if !agentproto.ValidRef(src.Ref) {
		return "", "", "", 2, fmt.Errorf("bad ref %q", src.Ref)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", "", "", 1, errors.New("git is not installed — run the build.tools provisioning step")
	}
	fresh := true
	if st, err := os.Stat(filepath.Join(dir, ".git")); err == nil && st.IsDir() {
		fresh = false
	}
	if fresh {
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", "", "", 1, err
		}
	} else {
		out, _ := gitOut(oc, dir, "rev-parse", "HEAD")
		prevCommit = strings.TrimSpace(out)
	}

	env, cred, ghRepo := gitEnv, []string(nil), agentproto.GitHubRepoOf(src.RepoURL)
	helper := ""
	var fetchEnv []string
	if src.Token != "" {
		tok, err := openCloneToken(oc, src)
		if err != nil {
			return "", prevCommit, "", 2, err
		}
		cred = []string{"-c", "credential.https://github.com.helper=", "-c", "credential.https://github.com.helper=" + appTokenHelper}
		fetchEnv = []string{appTokenEnv + "=" + tok}
		helper = "app"
		oc.Info("using the team's GitHub App installation for %s", ghRepo)
	} else if ghRepo != "" {
		helper = ghCredentialHelper()
	}
	if helper != "" && src.Token == "" {
		cred = ghCredentialArgs(helper)
		env = append(append([]string{}, gitEnv...), ghEnv()...)
		oc.Info("using this server's GitHub connection (gh) for %s", ghRepo)
	}

	if fresh {
		if code, err := oc.RunEnv(env, "git", gitArgs("init", "--quiet", dir)...); err != nil {
			return "", prevCommit, "", code, errors.New("git init failed")
		}
		if code, err := oc.RunEnv(env, "git", gitArgs("-C", dir, "remote", "add", "origin", src.RepoURL)...); err != nil {
			return "", prevCommit, "", code, errors.New("git remote add failed")
		}
	}
	runEnv := env
	if len(fetchEnv) > 0 {
		runEnv = append(append([]string{}, env...), fetchEnv...)
	}
	// A deeper (not --depth 1) fetch: the previously built commit needs to stay reachable so the
	// diff above can compare against it. 50 commits is enough for any deploy cadence a fast track
	// would run between; the checkout is still shallow, never the whole history.
	if code, err := oc.RunEnv(runEnv, "git", append(cred, gitArgs("-C", dir, "fetch", "--depth", "50", "--no-tags", "origin", src.Ref)...)...); err != nil {
		if src.Token != "" {
			return "", prevCommit, "", code, fmt.Errorf("git fetch failed: the team's GitHub App installation cannot read this repository, or it has no %q", src.Ref)
		}
		if ghRepo != "" {
			return "", prevCommit, "", code, ghFetchError(helper != "", src.Ref)
		}
		return "", prevCommit, "", code, errors.New("git fetch failed")
	}
	if code, err := oc.RunEnv(env, "git", gitArgs("-C", dir, "checkout", "--quiet", "--force", "--detach", "FETCH_HEAD")...); err != nil {
		return "", prevCommit, "", code, errors.New("git checkout failed")
	}
	out, err := gitOut(oc, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", prevCommit, "", 1, err
	}
	commit = strings.TrimSpace(out)
	m, _ := gitOut(oc, dir, "log", "-1", "--format=%s")
	oc.Info("checked out %s at %s", src.Ref, commit)
	return commit, prevCommit, strings.TrimSpace(m), 0, nil
}
