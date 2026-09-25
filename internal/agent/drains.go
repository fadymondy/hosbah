package agent

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// ---- H8: team log drains, agent side -----------------------------------------------------------
//
// The control plane never ships a log line itself — log.drains.set only pushes ONE drain's
// configuration to the server that runs the matching app(s) (like proxy.set), sealed to this
// agent's own key. This host persists the set of configured drains under
// /var/lib/hosbah/log-drains.json (so they survive an agent restart) and runs one background
// "tailer" per drain: it polls for the app's current containers (so a redeploy's new containers
// are picked up the next poll, no control-plane round trip needed), tails each with
// `docker logs --follow --since <cursor>`, and hands lines to a bounded buffer a shipping
// goroutine flushes to the sink with backoff.

const drainsPath = agentproto.WorkDir + "/log-drains.json"

// drainsFile is overridable in tests.
var drainsFile = func() string { return drainsPath }

var drainsMu sync.Mutex

func loadDrainConfigs() (map[string]agentproto.DrainConfig, error) {
	out := map[string]agentproto.DrainConfig{}
	b, err := os.ReadFile(drainsFile())
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, fmt.Errorf("%s is corrupt: %w", drainsFile(), err)
	}
	return out, nil
}

func saveDrainConfigs(m map[string]agentproto.DrainConfig) error {
	if err := os.MkdirAll(filepath.Dir(drainsFile()), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := drainsFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, drainsFile())
}

// drainRuntime is one running drain: its buffer, its cancel func, and the status a
// log.drains.status query reads.
type drainRuntime struct {
	cancel context.CancelFunc
	buf    *lineBuffer

	mu      sync.Mutex
	status  agentproto.DrainStatus
	cursors map[string]time.Time // container name -> last line's time, so a restart does not resend
}

func (r *drainRuntime) recordSuccess(shipped int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	r.status.LastSuccessAt = &now
	r.status.Shipped += int64(shipped)
}

func (r *drainRuntime) recordError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	r.status.LastErrorAt = &now
	r.status.LastError = clip(err.Error(), 300)
}

func (r *drainRuntime) recordDrop(n int64) {
	if n <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.Dropped += n
}

func (r *drainRuntime) snapshot(drainID string) agentproto.DrainStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.status
	s.DrainID = drainID
	s.Configured = true
	return s
}

// drainHost owns every drain configured on this server.
type drainHost struct {
	mu       sync.Mutex
	baseCtx  context.Context
	priv     ed25519.PrivateKey // this agent's own key: opens auth-header boxes sealed to it
	serverID string
	runtimes map[string]*drainRuntime
}

func newDrainHost() *drainHost {
	return &drainHost{runtimes: map[string]*drainRuntime{}, baseCtx: context.Background()}
}

// Run starts every persisted drain. Called once, with the agent's own long-lived context (it must
// outlive any single operation — a drain tails containers for as long as it is configured, not for
// the few seconds log.drains.set itself runs) and this agent's key (set on the executor slightly
// earlier than Run, at connect time — see Executor.SetKey / Executor.Work).
func (h *drainHost) Run(ctx context.Context, priv ed25519.PrivateKey, serverID string) {
	h.mu.Lock()
	h.baseCtx, h.priv, h.serverID = ctx, priv, serverID
	h.mu.Unlock()
	configs, err := loadDrainConfigs()
	if err != nil {
		return
	}
	for id, cfg := range configs {
		h.start(id, cfg)
	}
}

func (h *drainHost) start(id string, cfg agentproto.DrainConfig) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if rt, ok := h.runtimes[id]; ok {
		rt.cancel()
	}
	cctx, cancel := context.WithCancel(h.baseCtx)
	rt := &drainRuntime{cancel: cancel, buf: newLineBuffer(2000), cursors: map[string]time.Time{}}
	rt.status.DrainID, rt.status.Configured = id, true
	h.runtimes[id] = rt
	go runDrain(cctx, id, cfg, rt, h.priv, h.serverID)
}

func (h *drainHost) stop(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if rt, ok := h.runtimes[id]; ok {
		rt.cancel()
		delete(h.runtimes, id)
	}
}

// ---- log.drains.set --------------------------------------------------------------------------

func (h *drainHost) set(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DrainConfig](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	drainsMu.Lock()
	configs, err := loadDrainConfigs()
	if err != nil {
		drainsMu.Unlock()
		return nil, 1, err
	}
	if p.Remove {
		delete(configs, p.DrainID)
	} else {
		configs[p.DrainID] = p
	}
	err = saveDrainConfigs(configs)
	drainsMu.Unlock()
	if err != nil {
		return nil, 1, err
	}
	if p.Remove {
		h.stop(p.DrainID)
		oc.Info("drain %s removed", p.DrainID)
		return map[string]any{"removed": true}, 0, nil
	}
	h.start(p.DrainID, p)
	oc.Info("drain %s (%s) configured", p.DrainID, p.Kind)
	return map[string]any{"drain_id": p.DrainID, "kind": p.Kind}, 0, nil
}

// ---- log.drains.status -----------------------------------------------------------------------

func (h *drainHost) status(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DrainStatusParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	h.mu.Lock()
	rt, ok := h.runtimes[p.DrainID]
	h.mu.Unlock()
	if !ok {
		return agentproto.DrainStatus{DrainID: p.DrainID, Configured: false}, 0, nil
	}
	return rt.snapshot(p.DrainID), 0, nil
}

// ---- log.drains.test -------------------------------------------------------------------------

func (h *drainHost) test(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DrainTestParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	authVal, err := openDrainAuth(oc, p.Drain)
	if err != nil {
		return nil, 1, err
	}
	line := DrainLine{At: time.Now().UTC(), Server: oc.ServerID, App: "test", Process: "test", Container: "test",
		Stream: "stdout", Line: "hosbah log drain test — " + time.Now().UTC().Format(time.RFC3339)}
	ctx, cancel := context.WithTimeout(oc.Ctx, 20*time.Second)
	defer cancel()
	if err := shipBatch(ctx, p.Drain, authVal, []DrainLine{line}); err != nil {
		return agentproto.DrainTestResult{OK: false, Detail: err.Error()}, 1, fmt.Errorf("the sink refused the test line: %w", err)
	}
	return agentproto.DrainTestResult{OK: true, Detail: "the sink accepted one test line"}, 0, nil
}

// openDrainAuth opens the sealed auth-header value for an http/loki sink (empty when there is
// none — a drain need not have one).
func openDrainAuth(oc *OpContext, cfg agentproto.DrainConfig) (string, error) {
	var sealed string
	switch cfg.Kind {
	case agentproto.DrainKindHTTP:
		if cfg.HTTP != nil {
			sealed = cfg.HTTP.AuthSealed
		}
	case agentproto.DrainKindLoki:
		if cfg.Loki != nil {
			sealed = cfg.Loki.AuthSealed
		}
	}
	if sealed == "" {
		return "", nil
	}
	pt, err := agentproto.OpenBox(oc.priv, agentproto.DrainSecretContext(cfg.DrainID), sealed)
	if err != nil {
		return "", errors.New("the drain's auth header could not be opened with this server's key")
	}
	return string(pt), nil
}

// ---- the tailer + shipper loop ----------------------------------------------------------------

// discoverInterval: how often a drain re-lists the app's containers (picks up a redeploy's new
// ones, drops the old ones' tailers once their `docker logs -f` process exits on its own).
const discoverInterval = 10 * time.Second

// shipInterval: how often a drain's buffer is flushed to its sink.
const shipInterval = 5 * time.Second

// runDrain is one drain's whole lifecycle: discover, tail, ship, until ctx ends (log.drains.set
// removed it, or the agent is shutting down).
func runDrain(ctx context.Context, drainID string, cfg agentproto.DrainConfig, rt *drainRuntime, priv ed25519.PrivateKey, serverID string) {
	authVal, err := openDrainAuthPriv(cfg, priv)
	if err != nil {
		rt.recordError(err)
	}
	go shipLoop(ctx, cfg, authVal, rt)

	tailing := map[string]context.CancelFunc{}
	defer func() {
		for _, cancel := range tailing {
			cancel()
		}
	}()
	t := time.NewTicker(discoverInterval)
	defer t.Stop()
	for {
		names, labels := discoverDrainContainers(ctx, cfg)
		for _, name := range names {
			if _, ok := tailing[name]; ok {
				continue
			}
			cctx, cancel := context.WithCancel(ctx)
			tailing[name] = cancel
			app, process := labels[name][0], labels[name][1]
			go tailContainer(cctx, name, app, process, serverID, rt)
		}
		for name, cancel := range tailing {
			if !drainHasContainer(names, name) {
				cancel()
				delete(tailing, name)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// openDrainAuthPriv opens a drain's auth secret using the agent's own key (given by drainHost.Run,
// which received it from Executor.SetKey before the tailing daemon ever starts).
func openDrainAuthPriv(cfg agentproto.DrainConfig, priv ed25519.PrivateKey) (string, error) {
	var sealed string
	switch cfg.Kind {
	case agentproto.DrainKindHTTP:
		if cfg.HTTP != nil {
			sealed = cfg.HTTP.AuthSealed
		}
	case agentproto.DrainKindLoki:
		if cfg.Loki != nil {
			sealed = cfg.Loki.AuthSealed
		}
	}
	if sealed == "" {
		return "", nil
	}
	if priv == nil {
		return "", errors.New("this agent has no key loaded yet")
	}
	pt, err := agentproto.OpenBox(priv, agentproto.DrainSecretContext(cfg.DrainID), sealed)
	if err != nil {
		return "", errors.New("the drain's auth header could not be opened with this server's key")
	}
	return string(pt), nil
}

func drainHasContainer(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// discoverDrainContainers lists the running containers a drain covers: every app's, or one app's
// when AppID is set. labels maps a container name to [app_id, process].
func discoverDrainContainers(ctx context.Context, cfg agentproto.DrainConfig) (names []string, labels map[string][2]string) {
	args := []string{"ps", "--filter", "label=" + agentproto.LabelApp, "--format", "{{.Names}}"}
	if cfg.AppID != "" {
		args = []string{"ps", "--filter", "label=" + agentproto.LabelApp + "=" + cfg.AppID, "--format", "{{.Names}}"}
	}
	out, err := execRun(ctx, nil, "docker", args...)
	if err != nil {
		return nil, nil
	}
	for _, n := range strings.Fields(string(out)) {
		if !agentproto.IsHosbahOwned(n) {
			continue // an imported container carries no hosbah.app label to begin with, but be defensive
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return nil, nil
	}
	labels = map[string][2]string{}
	raw, err := execRun(ctx, nil, "docker", append([]string{"inspect"}, names...)...)
	if err != nil {
		return names, labels
	}
	var raws []processInspect
	if json.Unmarshal(raw, &raws) == nil {
		for _, r := range raws {
			name := strings.TrimPrefix(r.Name, "/")
			app := r.Config.Labels[agentproto.LabelApp]
			proc := r.Config.Labels[agentproto.LabelProcess]
			labels[name] = [2]string{app, proc}
		}
	}
	return names, labels
}

// tailContainer runs `docker logs --follow --since <cursor>` and pushes every line into the
// drain's buffer until ctx ends (the container is gone, or the drain was removed).
func tailContainer(ctx context.Context, name, app, process, serverID string, rt *drainRuntime) {
	rt.mu.Lock()
	cursor, ok := rt.cursors[name]
	rt.mu.Unlock()
	args := []string{"logs", "--timestamps", "--follow"}
	if ok {
		args = append(args, "--since", cursor.UTC().Format(time.RFC3339Nano))
	} else {
		args = append(args, "--tail", "0") // a freshly discovered container: ship only what happens from now on
	}
	args = append(args, name)
	cmd := exec.CommandContext(ctx, "docker", args...) //#nosec G204 -- fixed argv, validated container name
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if cmd.Start() != nil {
		return
	}
	pump := func(r io.Reader, stream string) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			at, msg, ok := splitDockerTimestamp(sc.Text())
			if !ok {
				at = time.Now().UTC()
			}
			rt.mu.Lock()
			rt.cursors[name] = at
			rt.mu.Unlock()
			rt.buf.Push(DrainLine{At: at, Server: serverID, App: app, Process: process, Container: name, Stream: stream, Line: msg})
		}
	}
	done := make(chan struct{}, 2)
	go func() { pump(stdout, "stdout"); done <- struct{}{} }()
	go func() { pump(stderr, "stderr"); done <- struct{}{} }()
	<-done
	<-done
	_ = cmd.Wait()
}

// shipLoop flushes a drain's buffer on shipInterval, backing off after consecutive failures so a
// down collector is not hammered (the buffer keeps accepting lines meanwhile, dropping the oldest
// once full).
func shipLoop(ctx context.Context, cfg agentproto.DrainConfig, authVal string, rt *drainRuntime) {
	t := time.NewTicker(shipInterval)
	defer t.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		lines, dropped := rt.buf.Drain()
		rt.recordDrop(dropped)
		if len(lines) == 0 {
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := shipBatch(sctx, cfg, authVal, lines)
		cancel()
		if err != nil {
			failures++
			rt.recordError(err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoffDelay(failures)):
			}
			continue
		}
		failures = 0
		rt.recordSuccess(len(lines))
	}
}
