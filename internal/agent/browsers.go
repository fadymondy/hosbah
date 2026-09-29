package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// A site's browsers (browser.create, browser.remove, browser.status): headless Chromium in a
// container whose CDP port is published on 127.0.0.1 only, behind this agent's token gateway
// (browsergw.go) on the server's private address. Every argv is fixed; the names in it are derived.
//
// The token never reaches docker at all — not an argv, not an env var, not `docker inspect`: the
// image has no notion of one. The agent opens the box, keeps the token's SHA-256 (in memory, and in
// a 0600 file so the gateway comes back after an agent restart) and forgets the token.
//
// Capabilities: --cap-drop ALL and NOTHING added back. headless-shell runs Chromium with
// --no-sandbox (its entrypoint), so it needs no SYS_ADMIN for user namespaces; it binds port 9222
// (above 1024, no NET_BIND_SERVICE) and changes no file ownership. The container is the sandbox:
// memory, CPU, pids and shm limits, no-new-privileges, no capabilities, loopback-only port.

type browserHost struct {
	run    runFunc
	stream func(oc *OpContext, name string, args ...string) (int, error)
	listen func(addr string) (net.Listener, error)
	ready  func(ctx context.Context, port int) error
	dir    string

	mu       sync.Mutex
	gateways map[string]*browserGateway
}

func newBrowserHost() *browserHost {
	return &browserHost{run: execRun, listen: func(addr string) (net.Listener, error) { return net.Listen("tcp4", addr) },
		stream: func(oc *OpContext, name string, args ...string) (int, error) { return oc.Run(name, args...) },
		ready:  cdpReady, dir: filepath.Join(workRoot, "browsers"), gateways: map[string]*browserGateway{}}
}

// browserState is what survives an agent restart. TokenSHA256 is a hash: the token is not on disk.
// A desktop browser (Kind == desktop) has no token and no gateway: TokenSHA256, MaxSessions and
// IdleSeconds are zero and Restore skips it.
type browserState struct {
	BrowserID   string `json:"browser_id"`
	AppID       string `json:"app_id"`
	Kind        string `json:"kind,omitempty"` // "" reads as headless (state saved before desktop existed)
	Bind        string `json:"bind"`
	Port        int    `json:"port"`
	TokenSHA256 string `json:"token_sha256"`
	MaxSessions int    `json:"max_sessions"`
	IdleSeconds int    `json:"idle_seconds"`
}

// isDesktop reports whether the state is a desktop browser.
func (st browserState) isDesktop() bool { return st.Kind == agentproto.BrowserKindDesktop }

// cdpReady waits until Chromium answers /json/version on its loopback port.
func cdpReady(ctx context.Context, port int) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(45 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json/version", port), nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return errors.New("Chromium did not answer on its DevTools port within 45 s")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// desktopReady waits until KasmVNC answers something on its own loopback port (the desktop's own
// hostname, behind Caddy's forward_auth, is what a human actually opens — this only confirms the
// container itself came up).
func desktopReady(ctx context.Context, bind string, port int) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(60 * time.Second) // a full desktop boots slower than headless
	url := fmt.Sprintf("http://%s:%d/", bind, port)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return errors.New("the desktop did not answer within 60 s")
		}
		time.Sleep(1 * time.Second)
	}
}

// browserRunArgs is THE place a browser's `docker run` argv is built. p has passed Validate.
func browserRunArgs(p agentproto.BrowserCreateParams) []string {
	name := agentproto.BrowserContainerName(p.BrowserID)
	common := []string{"run", "-d", "--restart", "unless-stopped", "--name", name,
		"--label", agentproto.BrowserLabel + "=" + p.BrowserID, "--label", agentproto.BrowserAppLabel + "=" + p.AppID,
		"--log-opt", "max-size=10m", "--log-opt", "max-file=3",
		"--memory", strconv.Itoa(p.MemoryMB) + "m", "--cpus", strconv.FormatFloat(p.CPUs, 'f', -1, 64)}
	if p.EffectiveKind() == agentproto.BrowserKindDesktop {
		return desktopRunArgs(p, common)
	}
	args := append(common, "--pids-limit", strconv.Itoa(agentproto.BrowserPidsLimit),
		"--shm-size", strconv.Itoa(agentproto.BrowserShmMB(p.MemoryMB))+"m",
		"--security-opt", "no-new-privileges", "--cap-drop", "ALL")
	args = append(args, publishArg(agentproto.BrowserCDPPort)...)
	return append(args, p.Image)
}

// desktopRunArgs: lscr.io/linuxserver/chromium (GPL; run here unmodified — only pulled and run,
// never changed or redistributed — the same unmodified-use headless-shell's MIT/BSD needs no
// purchase for). --shm-size is fixed at DesktopShmMB (a real desktop needs more than headless's
// memory-scaled share). PUID/PGID/TZ are the image's own convention for the uid/gid it chowns
// /config to and the container's timezone. The port maps to a FIXED host port (Bind:Port, chosen by
// the control plane from the same pool as headless, not an ephemeral one docker picks) because
// there is no gateway process in front of a desktop browser to re-read a moving upstream from —
// Caddy dials Bind:Port directly, behind forward_auth. --cap-drop ALL is kept, same as headless:
// the image runs Chromium with no sandbox privileges it would need a dropped capability for.
func desktopRunArgs(p agentproto.BrowserCreateParams, common []string) []string {
	tz := p.TZ
	if tz == "" {
		tz = agentproto.DesktopDefaultTZ
	}
	args := append(common,
		"-e", "PUID="+strconv.Itoa(agentproto.DesktopPUID), "-e", "PGID="+strconv.Itoa(agentproto.DesktopPGID), "-e", "TZ="+tz,
		"--pids-limit", strconv.Itoa(agentproto.DesktopPidsLimit), "--shm-size", strconv.Itoa(agentproto.DesktopShmMB)+"m",
		"--security-opt", "no-new-privileges", "--cap-drop", "ALL",
		"-v", agentproto.DesktopVolumeName(p.BrowserID)+":/config",
		"-p", net.JoinHostPort(p.Bind, strconv.Itoa(p.Port))+":"+strconv.Itoa(agentproto.DesktopHTTPPort))
	return append(args, p.Image)
}

func (h *browserHost) upstreamPort(ctx context.Context, browserID string) (int, error) {
	out, err := h.run(ctx, nil, "docker", "port", h.containerOf(ctx, browserID), strconv.Itoa(agentproto.BrowserCDPPort)+"/tcp")
	if err != nil {
		return 0, errors.New("the container did not publish its port")
	}
	return parsePublishedPort(string(out))
}

func (h *browserHost) create(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.BrowserCreateParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if p.EffectiveKind() == agentproto.BrowserKindDesktop {
		return h.createDesktop(oc, p)
	}
	pt, err := agentproto.OpenBox(oc.priv, agentproto.BrowserTokenContext(p.BrowserID), p.Token)
	if err != nil {
		return nil, 1, errors.New("the token could not be opened with this server's key")
	}
	var sec struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(pt, &sec) != nil || !agentproto.ValidBrowserToken(sec.Token) {
		return nil, 2, errors.New("the token is malformed")
	}
	hash := sha256.Sum256([]byte(sec.Token))
	token := sec.Token // kept only to scrub it out of anything that is logged
	name := agentproto.BrowserContainerName(p.BrowserID)
	fail := func(msg string, detail error) (any, int, error) {
		if detail != nil {
			oc.Info("%s", scrub(clip(detail.Error(), 600), token))
		}
		h.closeGateway(p.BrowserID)
		_, _ = h.run(context.WithoutCancel(oc.Ctx), nil, "docker", "rm", "-f", name)
		_ = os.Remove(h.statePath(p.BrowserID))
		return nil, 1, errors.New(msg)
	}

	oc.Progress(1, 4)
	if _, err := h.run(oc.Ctx, nil, "docker", "image", "inspect", "--format", "{{.Id}}", p.Image); err != nil {
		oc.Info("pulling %s — a first pull can take a few minutes", p.Image)
		if _, err := h.stream(oc, "docker", "pull", p.Image); err != nil {
			return nil, 1, errors.New("could not pull the browser image")
		}
	}
	oc.Progress(2, 4)
	h.closeGateway(p.BrowserID)
	// Idempotent re-run (a browser made before the rename comes back under the new name).
	for _, n := range agentproto.NameCandidates(name) {
		_, _ = h.run(oc.Ctx, nil, "docker", "rm", "-f", n)
	}
	oc.Info("starting %s (%d MiB, %g CPU, CDP on 127.0.0.1 only)", name, p.MemoryMB, p.CPUs)
	if _, err := h.run(oc.Ctx, nil, "docker", browserRunArgs(p)...); err != nil {
		return fail("could not start the browser", err)
	}
	oc.Progress(3, 4)
	upstream, err := h.upstreamPort(oc.Ctx, p.BrowserID)
	if err != nil {
		return fail(err.Error(), nil)
	}
	if err := h.ready(oc.Ctx, upstream); err != nil {
		return fail(err.Error(), nil)
	}
	oc.Progress(4, 4)
	st := browserState{BrowserID: p.BrowserID, AppID: p.AppID, Kind: agentproto.BrowserKindHeadless, Bind: p.Bind, Port: p.Port,
		TokenSHA256: hex.EncodeToString(hash[:]), MaxSessions: p.MaxSessions, IdleSeconds: p.IdleTimeoutSeconds}
	if err := h.open(st, upstream); err != nil {
		return fail(fmt.Sprintf("could not listen on %s:%d — is the port taken?", p.Bind, p.Port), err)
	}
	if err := h.save(st); err != nil {
		return fail("could not record the browser on this server", err)
	}
	oc.Info("the browser is ready on %s:%d (token required; at most %d sessions, idle ones close after %d s)", p.Bind, p.Port, p.MaxSessions, p.IdleTimeoutSeconds)
	return agentproto.BrowserCreateResult{Container: name, HostPort: p.Port}, 0, nil
}

// createDesktop starts a full interactive desktop (KasmVNC + Chromium, linuxserver/chromium). There
// is no token and no gateway: the container's fixed port is dialed directly by Caddy, gated by
// forward_auth to Hosbah (proxy.set on the browser's hostname is applied separately, by the control
// plane, once this op reports the container is up).
func (h *browserHost) createDesktop(oc *OpContext, p agentproto.BrowserCreateParams) (any, int, error) {
	name := agentproto.BrowserContainerName(p.BrowserID)
	volume := agentproto.DesktopVolumeName(p.BrowserID)
	fail := func(msg string, detail error) (any, int, error) {
		if detail != nil {
			oc.Info("%s", clip(detail.Error(), 600))
		}
		_, _ = h.run(context.WithoutCancel(oc.Ctx), nil, "docker", "rm", "-f", name)
		_ = os.Remove(h.statePath(p.BrowserID))
		return nil, 1, errors.New(msg)
	}

	oc.Progress(1, 3)
	if _, err := h.run(oc.Ctx, nil, "docker", "image", "inspect", "--format", "{{.Id}}", p.Image); err != nil {
		oc.Info("pulling %s — a first pull can take a few minutes", p.Image)
		if _, err := h.stream(oc, "docker", "pull", p.Image); err != nil {
			return nil, 1, errors.New("could not pull the desktop image")
		}
	}
	oc.Progress(2, 3)
	for _, n := range agentproto.NameCandidates(name) {
		_, _ = h.run(oc.Ctx, nil, "docker", "rm", "-f", n)
	}
	oc.Info("starting %s (%d MiB, %g CPU, desktop on %s:%d)", name, p.MemoryMB, p.CPUs, p.Bind, p.Port)
	if _, err := h.run(oc.Ctx, nil, "docker", browserRunArgs(p)...); err != nil {
		return fail("could not start the desktop", err)
	}
	oc.Progress(3, 3)
	if err := desktopReady(oc.Ctx, p.Bind, p.Port); err != nil {
		return fail(err.Error(), nil)
	}
	st := browserState{BrowserID: p.BrowserID, AppID: p.AppID, Kind: agentproto.BrowserKindDesktop, Bind: p.Bind, Port: p.Port}
	if err := h.save(st); err != nil {
		return fail("could not record the browser on this server", err)
	}
	_ = volume // named on the docker run argv; kept here only for readability of what removeOne must clean up
	oc.Info("the desktop is up on %s:%d — a hostname and forward_auth get it exposed", p.Bind, p.Port)
	return agentproto.BrowserCreateResult{Container: name, HostPort: p.Port}, 0, nil
}

// open starts the gateway of a browser.
func (h *browserHost) open(st browserState, upstream int) error {
	raw, err := hex.DecodeString(st.TokenSHA256)
	if err != nil || len(raw) != sha256.Size || !agentproto.ValidBrowserBind(st.Bind) {
		return errors.New("bad browser state")
	}
	ln, err := h.listen(net.JoinHostPort(st.Bind, strconv.Itoa(st.Port)))
	if err != nil {
		return err
	}
	id := st.BrowserID
	gw := newBrowserGateway(ln, [32]byte(raw), st.MaxSessions, time.Duration(st.IdleSeconds)*time.Second, upstream, func() (int, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		p, err := h.upstreamPort(ctx, id)
		return p, err == nil
	})
	h.mu.Lock()
	h.gateways[id] = gw
	h.mu.Unlock()
	return nil
}

func (h *browserHost) closeGateway(id string) {
	h.mu.Lock()
	gw := h.gateways[id]
	delete(h.gateways, id)
	h.mu.Unlock()
	if gw != nil {
		gw.Close()
	}
}

func (h *browserHost) statePath(id string) string { return filepath.Join(h.dir, id+".json") }

func (h *browserHost) save(st browserState) error {
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(st)
	tmp := h.statePath(st.BrowserID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, h.statePath(st.BrowserID))
}

func (h *browserHost) states() []browserState {
	files, _ := filepath.Glob(filepath.Join(h.dir, "*.json"))
	var out []browserState
	for _, f := range files {
		var st browserState
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &st) == nil && agentproto.ValidID(st.BrowserID) { //#nosec G304 -- our own state dir
			out = append(out, st)
		}
	}
	return out
}

// Restore reopens the gateways of the browsers on this server (after an agent start). A container
// that is not up yet is fine: the gateway re-reads the port when a connection is refused.
func (h *browserHost) Restore(ctx context.Context) []error {
	var errs []error
	for _, st := range h.states() {
		if st.isDesktop() {
			continue // no gateway: Caddy dials the fixed port directly, nothing to reopen here
		}
		upstream, _ := h.upstreamPort(ctx, st.BrowserID)
		if err := h.open(st, upstream); err != nil {
			errs = append(errs, fmt.Errorf("browser %s: %w", st.BrowserID, err))
		}
	}
	return errs
}

func (h *browserHost) remove(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.BrowserRemoveParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	name := h.containerOf(oc.Ctx, p.BrowserID)
	removed, err := h.removeOne(oc, p.BrowserID)
	if err != nil {
		return nil, 1, err
	}
	return agentproto.BrowserRemoveResult{Container: name, Removed: removed}, 0, nil
}

// containerOf is the browser's container on this server: cloudy-browser-<id> for a browser made
// before the rename, else hosbah-browser-<id>.
func (h *browserHost) containerOf(ctx context.Context, browserID string) string {
	return resolveName(ctx, h.run, kindContainer, agentproto.BrowserContainerName(browserID))
}

func (h *browserHost) removeOne(oc *OpContext, id string) (bool, error) {
	name := h.containerOf(oc.Ctx, id)
	var wasDesktop bool
	for _, st := range h.states() {
		if st.BrowserID == id && st.isDesktop() {
			wasDesktop = true
		}
	}
	h.closeGateway(id)
	_ = os.Remove(h.statePath(id))
	_, _ = h.run(oc.Ctx, nil, "docker", "stop", "-t", "5", name)
	removed := true
	if _, err := h.run(oc.Ctx, nil, "docker", "rm", "-f", name); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such container") {
			oc.Info("%s was already gone", name)
			removed = false
		} else {
			oc.Info("%v", err)
			return false, errors.New("could not remove the browser's container")
		}
	} else {
		oc.Info("removed %s", name)
	}
	if wasDesktop {
		// best effort: the profile volume outlives the container and is only useful to this browser
		_, _ = h.run(oc.Ctx, nil, "docker", "volume", "rm", "-f", agentproto.DesktopVolumeName(id))
	}
	return removed, nil
}

// withAppRemove wraps app.remove: a deleted app's browsers go with it.
func (h *browserHost) withAppRemove(next Handler) Handler {
	return func(oc *OpContext) (any, int, error) {
		res, code, err := next(oc)
		if err != nil {
			return res, code, err
		}
		var p agentproto.RemoveParams
		if json.Unmarshal(oc.Params, &p) != nil || !agentproto.ValidID(p.AppID) {
			return res, code, err
		}
		ids := map[string]bool{}
		for _, st := range h.states() {
			if st.AppID == p.AppID {
				ids[st.BrowserID] = true
			}
		}
		// hosbah.browser-app, and cloudy.browser-app on browsers made before the rename.
		if out, err := psNames(oc.Ctx, h.run, func(l labelSet) []string {
			return []string{"-a", "--filter", "label=" + l(agentproto.BrowserAppLabel) + "=" + p.AppID,
				"--format", `{{.Label "` + l(agentproto.BrowserLabel) + `"}}`}
		}); err == nil {
			for _, id := range out {
				if agentproto.ValidID(id) {
					ids[id] = true
				}
			}
		}
		var gone []string
		for id := range ids {
			name := h.containerOf(oc.Ctx, id)
			if _, err := h.removeOne(oc, id); err == nil {
				gone = append(gone, name)
			}
		}
		if m, ok := res.(map[string]any); ok && len(gone) > 0 {
			m["browsers"] = gone
		}
		return res, code, nil
	}
}

func (h *browserHost) status(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.BrowserStatusParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	res := agentproto.BrowserStatusResult{Browsers: []agentproto.BrowserState{}}
	var running []string
	names := map[string]string{}
	for _, id := range p.BrowserIDs {
		st := agentproto.BrowserState{BrowserID: id}
		name := h.containerOf(oc.Ctx, id)
		names[id] = name
		if out, err := h.run(oc.Ctx, nil, "docker", "inspect", "--type", "container", "--format", "{{.State.Running}} {{.RestartCount}}", name); err == nil {
			if f := strings.Fields(string(out)); len(f) == 2 {
				st.Running = f[0] == "true"
				st.Restarts, _ = strconv.Atoi(f[1])
			}
		}
		if st.Running {
			running = append(running, name)
		}
		h.mu.Lock()
		if gw := h.gateways[id]; gw != nil {
			st.Sessions = int(gw.sessions.Load())
		}
		h.mu.Unlock()
		res.Browsers = append(res.Browsers, st)
	}
	if len(running) > 0 {
		args := append([]string{"stats", "--no-stream", "--format", "{{.Name}}\t{{.MemUsage}}"}, running...)
		if out, err := h.run(oc.Ctx, nil, "docker", args...); err == nil {
			mem := parseMemUsage(string(out))
			for i := range res.Browsers {
				res.Browsers[i].MemoryBytes = mem[names[res.Browsers[i].BrowserID]]
			}
		}
	}
	return res, 0, nil
}

// parseMemUsage reads `docker stats --format "{{.Name}}\t{{.MemUsage}}"`: "name\t123.4MiB / 1GiB".
func parseMemUsage(out string) map[string]*int64 {
	units := []struct {
		suffix string
		mult   float64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1}}
	mem := map[string]*int64{}
	for _, l := range strings.Split(out, "\n") {
		name, usage, ok := strings.Cut(strings.TrimSpace(l), "\t")
		if !ok {
			continue
		}
		used, _, _ := strings.Cut(usage, "/")
		used = strings.TrimSpace(used)
		for _, u := range units {
			if num, ok := strings.CutSuffix(used, u.suffix); ok {
				if f, err := strconv.ParseFloat(strings.TrimSpace(num), 64); err == nil && f >= 0 {
					n := int64(f * u.mult)
					mem[name] = &n
				}
				break
			}
		}
	}
	return mem
}
