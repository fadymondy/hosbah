package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/catalog"
	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Installed recipes (recipe.install, recipe.remove, recipe.status): one container of an "app" recipe
// next to a site.
//
// The agent runs ITS OWN copy of the recipe. The params name a recipe by id and version; the image,
// the limits, the capabilities, the mounts and the literal env are read from the catalog embedded in
// this binary. An image reference that arrives over the wire is only compared: when it, or the
// version, differs from this agent's catalog, the answer is "update the agent" and nothing runs.
//
// Secrets (generated values, a data service's connection string) arrive in a box sealed to this
// server's key. They reach Docker through a 0600 --env-file that is removed as soon as `docker run`
// returns — never an argv, never the op log.
//
// The container: memory, CPU and pids limits, no-new-privileges, --cap-drop ALL plus only what the
// recipe declares from catalog.AllowedCaps, derived named volumes (no host path exists in the
// format), and its HTTP port published on 127.0.0.1 at a port the kernel picks.

type recipeHost struct {
	run     runFunc
	stream  func(oc *OpContext, name string, args ...string) (int, error)
	healthy func(ctx context.Context, port int, path string) error
	catalog func() (*catalog.Catalog, error)
	dir     string // where the env file lives for the moment docker reads it
}

func newRecipeHost() *recipeHost {
	return &recipeHost{run: execRun, stream: func(oc *OpContext, name string, args ...string) (int, error) { return oc.Run(name, args...) },
		healthy: httpHealthy, catalog: catalog.Builtin, dir: filepath.Join(workRoot, "installs")}
}

// installHealthWait is how long an install may take to answer its health path after it started.
const installHealthWait = 3 * time.Minute

// httpHealthy waits until the install answers 2xx on its loopback port.
func httpHealthy(ctx context.Context, port int, path string) error {
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.Now().Add(installHealthWait)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return fmt.Errorf("it did not answer 2xx on %s within %s", path, installHealthWait)
		}
		time.Sleep(time.Second)
	}
}

// recipeOf resolves the params against THIS agent's catalog.
func (h *recipeHost) recipeOf(p agentproto.RecipeInstallParams) (catalog.Recipe, error) {
	c, err := h.catalog()
	if err != nil {
		return catalog.Recipe{}, errors.New("this agent's recipe catalog did not load — update the agent")
	}
	r, ok := c.Get(p.RecipeID)
	switch {
	case !ok:
		return catalog.Recipe{}, fmt.Errorf("recipe %s is not in this agent's catalog — update the agent on this server and try again", p.RecipeID)
	case r.Kind != catalog.KindApp:
		return catalog.Recipe{}, fmt.Errorf("recipe %s is not an app recipe", p.RecipeID)
	case r.Version != p.RecipeVersion:
		return catalog.Recipe{}, fmt.Errorf("recipe %s is version %s in this agent's catalog, the control plane asked for %s — update the agent on this server and try again",
			p.RecipeID, r.Version, p.RecipeVersion)
	case r.Image != p.Image:
		return catalog.Recipe{}, fmt.Errorf("the image asked for is not the image of recipe %s %s in this agent's catalog — refused; update the agent on this server and try again",
			p.RecipeID, r.Version)
	}
	return r, nil
}

// installEnv is the container's env: the recipe's literals (templates filled from the generated
// values) plus what the box brought. Every declared variable must end up with a value, and the box
// may bring nothing the recipe does not declare as generated or from a service.
func installEnv(r catalog.Recipe, sec agentproto.InstallSecrets) (map[string]string, []string, error) {
	declared := map[string]bool{}
	for _, e := range r.Env {
		if e.Value == nil {
			declared[e.Name] = true
		}
	}
	for k := range sec.Env {
		if !declared[k] {
			return nil, nil, fmt.Errorf("the control plane sent %s, which recipe %s does not take", k, r.ID)
		}
	}
	generated := map[string]string{}
	for _, n := range r.Generated() {
		generated[n] = sec.Env[n]
	}
	env, secrets := map[string]string{}, []string{}
	for _, e := range r.Env {
		v, ok := sec.Env[e.Name]
		if e.Value != nil {
			var err error
			if v, err = catalog.Render(*e.Value, generated); err != nil {
				return nil, nil, err
			}
			ok = true
		}
		if !ok || (e.Value == nil && v == "") {
			return nil, nil, fmt.Errorf("the control plane sent no value for %s", e.Name)
		}
		if strings.ContainsAny(v, "\r\n\x00") {
			return nil, nil, fmt.Errorf("the value of %s does not fit an env file", e.Name)
		}
		env[e.Name] = v
		if e.Secret || e.Value == nil {
			secrets = append(secrets, v)
		}
	}
	return env, secrets, nil
}

// installRunArgs is THE place an install's `docker run` argv is built — from the agent's own recipe.
// Env names only reach the argv through the env file's path.
func installRunArgs(p agentproto.RecipeInstallParams, r catalog.Recipe, envFile string) []string {
	args := []string{"run", "-d", "--restart", "unless-stopped", "--name", agentproto.InstallContainerName(p.InstallID),
		"--label", agentproto.InstallLabel + "=" + p.InstallID, "--label", agentproto.InstallAppLabel + "=" + p.AppID,
		"--label", agentproto.InstallRecipeLabel + "=" + r.ID + "@" + r.Version,
		"--log-opt", "max-size=10m", "--log-opt", "max-file=3",
		"--memory", strconv.Itoa(r.Limits.MemoryMB) + "m", "--cpus", strconv.FormatFloat(r.Limits.CPUs, 'f', -1, 64),
		"--pids-limit", strconv.Itoa(agentproto.InstallPidsLimit),
		"--security-opt", "no-new-privileges", "--cap-drop", "ALL"}
	for _, c := range r.Caps { // validated against catalog.AllowedCaps when the catalog loaded
		args = append(args, "--cap-add", c)
	}
	for _, v := range r.Volumes {
		args = append(args, "--mount", "type=volume,source="+agentproto.InstallVolumeName(p.InstallID, v.Name)+",target="+v.MountPath)
	}
	if envFile != "" {
		args = append(args, "--env-file", envFile)
	}
	if r.Port > 0 {
		args = append(args, publishArg(r.Port)...)
	}
	return append(args, r.Image)
}

// writeEnvFile writes NAME=value lines, 0600, in a 0700 directory. Docker reads the file literally:
// no quoting, no expansion.
func (h *recipeHost) writeEnvFile(installID string, env map[string]string) (string, error) {
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return "", err
	}
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, k := range names {
		b.WriteString(k + "=" + env[k] + "\n")
	}
	path := filepath.Join(h.dir, installID+".env")
	_ = os.Remove(path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //#nosec G304 -- our own directory, a validated id
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(b.String())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return "", werr
	}
	return path, nil
}

func (h *recipeHost) install(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.RecipeInstallParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	r, err := h.recipeOf(p)
	if err != nil {
		return nil, 2, err
	}
	var sec agentproto.InstallSecrets
	if p.Secrets != "" {
		pt, err := agentproto.OpenBox(oc.priv, agentproto.InstallSecretsContext(p.InstallID), p.Secrets)
		if err != nil {
			return nil, 1, errors.New("the install's secrets could not be opened with this server's key")
		}
		if json.Unmarshal(pt, &sec) != nil || sec.Validate() != nil {
			return nil, 2, errors.New("the install's secrets are malformed")
		}
	}
	env, secrets, err := installEnv(r, sec)
	if err != nil {
		return nil, 2, err
	}
	name := agentproto.InstallContainerName(p.InstallID)
	fail := func(msg string, detail error) (any, int, error) {
		if detail != nil {
			oc.Info("%s", scrub(clip(detail.Error(), 600), secrets...))
		}
		if out, err := h.run(context.WithoutCancel(oc.Ctx), nil, "docker", "logs", "--tail", "20", name); err == nil && len(out) > 0 {
			oc.Info("the container's last lines:\n%s", scrub(clip(string(out), 2000), secrets...))
		}
		_, _ = h.run(context.WithoutCancel(oc.Ctx), nil, "docker", "rm", "-f", name)
		return nil, 1, errors.New(msg)
	}

	oc.Progress(1, 4)
	if _, err := h.run(oc.Ctx, nil, "docker", "image", "inspect", "--format", "{{.Id}}", r.Image); err != nil {
		oc.Info("pulling %s — a first pull can take a few minutes", r.Image)
		if _, err := h.stream(oc, "docker", "pull", r.Image); err != nil {
			return nil, 1, errors.New("could not pull " + r.Image)
		}
	}
	oc.Progress(2, 4)
	_, _ = h.run(oc.Ctx, nil, "docker", "rm", "-f", name) // idempotent re-run; the volumes stay
	envFile := ""
	if len(env) > 0 {
		if envFile, err = h.writeEnvFile(p.InstallID, env); err != nil {
			return nil, 1, errors.New("could not hand the install its variables")
		}
	}
	oc.Info("starting %s: %s %s (%d MiB, %g CPU%s)", name, r.ID, r.Version, r.Limits.MemoryMB, r.Limits.CPUs, capsNote(r.Caps))
	_, err = h.run(oc.Ctx, nil, "docker", installRunArgs(p, r, envFile)...)
	if envFile != "" {
		_ = os.Remove(envFile) // docker has read it; the values now live in the container's config only
	}
	if err != nil {
		return fail("could not start "+r.Name, err)
	}
	oc.Progress(3, 4)
	hostPort := 0
	if r.Port > 0 {
		if hostPort, err = h.publishedPort(oc.Ctx, p.InstallID, r.Port); err != nil {
			return fail(err.Error(), nil)
		}
		if r.Healthcheck != "" {
			if err := h.healthy(oc.Ctx, hostPort, r.Healthcheck); err != nil {
				return fail(r.Name+" started but is not healthy: "+err.Error(), nil)
			}
		}
	}
	oc.Progress(4, 4)
	if hostPort > 0 {
		oc.Info("%s is up on 127.0.0.1:%d (this server only)", r.Name, hostPort)
	} else {
		oc.Info("%s is up", r.Name)
	}
	return agentproto.RecipeInstallResult{Container: name, HostPort: hostPort}, 0, nil
}

func capsNote(caps []string) string {
	if len(caps) == 0 {
		return ", no capabilities"
	}
	return ", capabilities: " + strings.Join(caps, " ")
}

func (h *recipeHost) publishedPort(ctx context.Context, installID string, containerPort int) (int, error) {
	out, err := h.run(ctx, nil, "docker", "port", agentproto.InstallContainerName(installID), strconv.Itoa(containerPort)+"/tcp")
	if err != nil {
		return 0, errors.New("the container did not publish its port")
	}
	return parsePublishedPort(string(out))
}

func (h *recipeHost) remove(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.RecipeRemoveParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	removed, err := h.removeOne(oc, p.InstallID)
	if err != nil {
		return nil, 1, err
	}
	res := agentproto.RecipeRemoveResult{Container: agentproto.InstallContainerName(p.InstallID), Removed: removed, VolumesRemoved: []string{}}
	if p.DeleteData {
		// Found by the derived prefix, so this works whatever catalog this agent has. A UUID is a
		// fixed length, so one install's prefix is never the start of another's.
		prefix := agentproto.InstallVolumeName(p.InstallID, "")
		out, err := h.run(oc.Ctx, nil, "docker", "volume", "ls", "-q", "--filter", "name="+prefix)
		if err != nil {
			return nil, 1, errors.New("could not list the install's volumes")
		}
		for _, v := range strings.Fields(string(out)) {
			if !strings.HasPrefix(v, prefix) || !agentproto.ValidVolumeSlug(strings.TrimPrefix(v, prefix)) {
				continue // docker's name filter is a substring match
			}
			if _, err := h.run(oc.Ctx, nil, "docker", "volume", "rm", v); err != nil {
				oc.Info("%s", clip(err.Error(), 300))
				return nil, 1, errors.New("could not remove volume " + v)
			}
			oc.Info("removed volume %s", v)
			res.VolumesRemoved = append(res.VolumesRemoved, v)
		}
	}
	return res, 0, nil
}

func (h *recipeHost) removeOne(oc *OpContext, id string) (bool, error) {
	name := agentproto.InstallContainerName(id)
	_ = os.Remove(filepath.Join(h.dir, id+".env"))
	_, _ = h.run(oc.Ctx, nil, "docker", "stop", "-t", "10", name)
	if _, err := h.run(oc.Ctx, nil, "docker", "rm", "-f", name); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such container") {
			oc.Info("%s was already gone", name)
			return false, nil
		}
		oc.Info("%s", clip(err.Error(), 300))
		return false, errors.New("could not remove the install's container")
	}
	oc.Info("removed %s", name)
	return true, nil
}

// withAppRemove wraps app.remove: a deleted app's installs go with it (their containers; volumes are
// left, like the app's own).
func (h *recipeHost) withAppRemove(next Handler) Handler {
	return func(oc *OpContext) (any, int, error) {
		res, code, err := next(oc)
		if err != nil {
			return res, code, err
		}
		var p agentproto.RemoveParams
		if json.Unmarshal(oc.Params, &p) != nil || !agentproto.ValidID(p.AppID) {
			return res, code, err
		}
		out, lerr := h.run(oc.Ctx, nil, "docker", "ps", "-a", "--filter", "label="+agentproto.InstallAppLabel+"="+p.AppID,
			"--format", `{{.Label "`+agentproto.InstallLabel+`"}}`)
		if lerr != nil {
			return res, code, nil
		}
		var gone []string
		for _, id := range strings.Fields(string(out)) {
			if !agentproto.ValidID(id) {
				continue
			}
			if _, err := h.removeOne(oc, id); err == nil {
				gone = append(gone, agentproto.InstallContainerName(id))
			}
		}
		if m, ok := res.(map[string]any); ok && len(gone) > 0 {
			m["installs"] = gone
		}
		return res, code, nil
	}
}

func (h *recipeHost) status(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.RecipeStatusParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	c, _ := h.catalog()
	res := agentproto.RecipeStatusResult{Installs: []agentproto.InstallState{}}
	var running []string
	for i, id := range p.InstallIDs {
		st := agentproto.InstallState{InstallID: id}
		name := agentproto.InstallContainerName(id)
		if out, err := h.run(oc.Ctx, nil, "docker", "inspect", "--type", "container", "--format", "{{.State.Running}} {{.RestartCount}}", name); err == nil {
			if f := strings.Fields(string(out)); len(f) == 2 {
				st.Running = f[0] == "true"
				st.Restarts, _ = strconv.Atoi(f[1])
			}
		}
		if st.Running {
			running = append(running, name)
			if c != nil {
				if r, ok := c.Get(p.RecipeIDs[i]); ok && r.Port > 0 {
					st.HostPort, _ = h.publishedPort(oc.Ctx, id, r.Port)
				}
			}
		}
		res.Installs = append(res.Installs, st)
	}
	if len(running) > 0 {
		args := append([]string{"stats", "--no-stream", "--format", "{{.Name}}\t{{.MemUsage}}"}, running...)
		if out, err := h.run(oc.Ctx, nil, "docker", args...); err == nil {
			mem := parseMemUsage(string(out))
			for i := range res.Installs {
				res.Installs[i].MemoryBytes = mem[agentproto.InstallContainerName(res.Installs[i].InstallID)]
			}
		}
	}
	return res, 0, nil
}
