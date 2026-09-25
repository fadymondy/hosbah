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
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Handler runs one kind of operation. It logs through oc and returns a JSON-able result.
type Handler func(oc *OpContext) (result any, exitCode int, err error)

// Executor verifies, de-duplicates, queues and runs operations, and keeps a journal of every event
// each one emitted so a reconnect can resume exactly where the control plane's copy stops.
//
// Operations run in lanes (P2): the host lane runs system.info, docker.check, provisioning steps
// and agent updates one at a time (they share apt and dpkg locks); each app has its own lane, so
// one app's deploy steps never interleave while two apps build side by side; app.logs and
// app.detect run on their own, so a long build never holds up a log relay. op.cancel is handled
// the moment it arrives.
type Executor struct {
	serverID     string
	cpPub        ed25519.PublicKey
	priv         ed25519.PrivateKey // this agent's own key: opens env boxes sealed to it
	version      string
	controlPlane string
	now          func() time.Time
	emit         func(agentproto.Event)
	// termSend delivers a term.* frame straight to the connection, bypassing the event journal
	// (set by the Agent that owns this executor; nil, and dropped, until then).
	termSend  func(agentproto.Frame)
	handlers  map[string]Handler
	browsers  *browserHost  // owns the token gateways of this server's browsers
	recipes   *recipeHost   // installed catalog recipes; runs this binary's own copy of a recipe
	terminals *terminalHost // interactive terminal.open sessions
	// Restart is called after an operation that asked for it (agent.update) has reported its
	// final status.
	Restart func()

	mu      sync.Mutex
	journal map[string]*record
	order   []string
	queue   chan *record
	running map[string]context.CancelFunc

	lanesMu sync.Mutex
	lanes   map[string]chan *record
}

type record struct {
	op        agentproto.SignedOp
	events    []agentproto.Event
	restart   bool
	cancelled bool
}

// journalSize bounds memory: the last N operations are remembered for de-duplication and resume.
// The journal is in memory only — after an agent restart, the control plane's resume list gets
// "unknown" for in-flight ops and re-queues them; every kind is safe to re-run (provision steps
// are idempotent, agent.update checks the running version first, app.start replaces a container
// of the same name, route.set renders the whole route table).
const journalSize = 256

// NewExecutor builds an executor for one server, trusting one control-plane key.
func NewExecutor(serverID string, cpPub ed25519.PublicKey, version, controlPlane string) *Executor {
	e := &Executor{
		serverID: serverID, cpPub: cpPub, version: version, controlPlane: controlPlane,
		now: time.Now, emit: func(agentproto.Event) {},
		journal: map[string]*record{}, queue: make(chan *record, 64),
		running: map[string]context.CancelFunc{}, lanes: map[string]chan *record{},
	}
	e.handlers = map[string]Handler{
		agentproto.KindSystemInfo:      systemInfo,
		agentproto.KindDockerCheck:     dockerCheck,
		agentproto.KindProvisionStep:   provisionStep,
		agentproto.KindAgentUpdate:     e.agentUpdate,
		agentproto.KindAppDetect:       appDetect,
		agentproto.KindAppBuild:        appBuild,
		agentproto.KindAppFastBuild:    appFastBuild,
		agentproto.KindAppRelease:      appRelease,
		agentproto.KindAppStart:        appStart,
		agentproto.KindRouteSet:        routeSet,
		agentproto.KindProxySet:        proxySet,
		agentproto.KindRealtimeRoute:   realtimeRoute,
		agentproto.KindAppDrain:        appDrain,
		agentproto.KindAppRollback:     appRollback,
		agentproto.KindAppLogs:         appLogs,
		agentproto.KindAppRemove:       appRemove,
		agentproto.KindDBProvision:     dbProvision,
		agentproto.KindDBCreate:        dbCreate,
		agentproto.KindDBDrop:          dbDrop,
		agentproto.KindTunnelFrp:       tunnelFrp,
		agentproto.KindTunnelTailscale: tunnelTailscale,
		agentproto.KindDBBackup:        dbBackup,
		agentproto.KindDBRestore:       dbRestore,
		agentproto.KindDBRemove:        dbRemove,
		agentproto.KindDBUser:          dbUser,
		agentproto.KindDBQuery:         dbQuery,
		agentproto.KindCaddyCerts:      caddyCerts,
		agentproto.KindAppExec:         newExecHost().appExec,
		agentproto.KindAppProcesses:    appProcesses,
		agentproto.KindAppLimits:       appLimits,
		agentproto.KindDBLimits:        dbLimits,
	}
	wl := newWorkloads()
	for kind, h := range map[string]Handler{
		agentproto.KindWorkloadPower: wl.power, agentproto.KindWorkloadLogs: wl.logs, agentproto.KindWorkloadStats: wl.stats,
		agentproto.KindGuestResize: wl.resize, agentproto.KindGuestSnapshot: wl.snapshot, agentproto.KindGuestBackup: wl.backup,
		agentproto.KindContainerRecreate: wl.recreate, agentproto.KindWorkloadDestroy: wl.destroy, agentproto.KindDBProbe: wl.probe,
	} {
		e.handlers[kind] = h
	}
	st := newStorageHost()
	for kind, h := range map[string]Handler{
		agentproto.KindVolumeUsage: st.volumeUsage, agentproto.KindVolumeRemove: st.volumeRemove,
		agentproto.KindBucketCreate: st.bucketCreate, agentproto.KindBucketRemove: st.bucketRemove,
	} {
		e.handlers[kind] = h
	}
	e.browsers = newBrowserHost()
	for kind, h := range map[string]Handler{
		agentproto.KindBrowserCreate: e.browsers.create, agentproto.KindBrowserRemove: e.browsers.remove, agentproto.KindBrowserStatus: e.browsers.status,
	} {
		e.handlers[kind] = h
	}
	e.recipes = newRecipeHost()
	for kind, h := range map[string]Handler{
		agentproto.KindRecipeInstall: e.recipes.install, agentproto.KindRecipeRemove: e.recipes.remove, agentproto.KindRecipeStatus: e.recipes.status,
		// a deleted app's browsers and installed recipes go with it
		agentproto.KindAppRemove: e.recipes.withAppRemove(e.browsers.withAppRemove(appRemove)),
	} {
		e.handlers[kind] = h
	}
	gh := newGHHost()
	for kind, h := range map[string]Handler{
		agentproto.KindGitHubInstall: gh.install, agentproto.KindGitHubLogin: gh.login, agentproto.KindGitHubStatus: gh.statusOp,
		agentproto.KindGitHubLogout: gh.logout, agentproto.KindGitHubRepos: gh.repos, agentproto.KindGitHubBranches: gh.branches,
		agentproto.KindGitHubCommits: gh.commits, agentproto.KindGitHubCompare: gh.compare, agentproto.KindGitHubPulls: gh.pulls,
	} {
		e.handlers[kind] = h
	}
	fs := newFSHost()
	for kind, h := range map[string]Handler{
		agentproto.KindFSList: fs.list, agentproto.KindFSRead: fs.read, agentproto.KindFSWrite: fs.write,
		agentproto.KindFSMkdir: fs.mkdir, agentproto.KindFSDelete: fs.delete, agentproto.KindFSRename: fs.rename,
	} {
		e.handlers[kind] = h
	}
	e.handlers[agentproto.KindSFTPUser] = newSFTPHost().apply
	e.terminals = newTerminalHost(func(f agentproto.Frame) {
		if e.termSend != nil {
			e.termSend(f)
		}
	})
	e.handlers[agentproto.KindTerminalOpen] = e.terminals.open
	return e
}

// TermFrame routes a term.data / term.resize / term.close frame from the control plane to its
// session (the agent's reader calls this for every such frame).
func (e *Executor) TermFrame(f agentproto.Frame) { e.terminals.Handle(f) }

// SetKey gives the executor this agent's private key (to open env boxes sealed to it).
func (e *Executor) SetKey(priv ed25519.PrivateKey) { e.priv = priv }

// Submit is where every operation from the network enters. Nothing runs unless VerifyOp accepts
// it: a tampered, unsigned, expired, misaddressed or unknown-kind operation is refused and the
// refusal reported. A re-sent op_id is never run twice — its recorded events are replayed.
func (e *Executor) Submit(op *agentproto.SignedOp) {
	if op == nil {
		return
	}
	if err := agentproto.VerifyOp(e.cpPub, op, e.serverID, e.now()); err != nil {
		// Seq 0: a control event, not a line of the operation (it may not be a real operation).
		e.emit(agentproto.Event{OpID: op.OpID, Kind: agentproto.EventStatus, Status: agentproto.StatusRejected,
			Error: err.Error(), At: e.now().UTC()})
		return
	}
	e.mu.Lock()
	if rec, ok := e.journal[op.OpID]; ok {
		evs := append([]agentproto.Event(nil), rec.events...)
		e.mu.Unlock()
		for _, ev := range evs {
			e.emit(ev)
		}
		return
	}
	rec := &record{op: *op}
	e.remember(rec)
	e.mu.Unlock()

	if op.Kind == agentproto.KindOpCancel {
		go e.cancel(rec)
		return
	}
	select {
	case e.queue <- rec:
	default:
		e.event(rec, agentproto.Event{Kind: agentproto.EventStatus, Status: agentproto.StatusFailed,
			Error: "the agent's operation queue is full; try again shortly"})
	}
}

// cancel handles op.cancel at once: the target's context is cancelled (its handler sees ctx.Done,
// its commands are killed) and it reports "failed: cancelled". Cancelling something that is not
// running is not an error — it may have just finished.
func (e *Executor) cancel(rec *record) {
	e.event(rec, agentproto.Event{Kind: agentproto.EventStatus, Status: agentproto.StatusRunning})
	var p agentproto.CancelParams
	_ = json.Unmarshal(rec.op.Params, &p)
	e.mu.Lock()
	stop, ok := e.running[p.OpID]
	if target := e.journal[p.OpID]; ok && target != nil {
		target.cancelled = true
	}
	e.mu.Unlock()
	if ok {
		stop()
		e.event(rec, agentproto.Event{Kind: agentproto.EventLog, Stream: "info", Line: "cancelled operation " + p.OpID})
	} else {
		e.event(rec, agentproto.Event{Kind: agentproto.EventLog, Stream: "info", Line: "operation " + p.OpID + " is not running here — nothing to cancel"})
	}
	code := 0
	raw, _ := json.Marshal(map[string]any{"cancelled": ok, "op_id": p.OpID})
	e.event(rec, agentproto.Event{Kind: agentproto.EventStatus, Status: agentproto.StatusOK, ExitCode: &code, Result: raw})
}

// Resume answers the control plane's resume list after a reconnect: the events it has not seen,
// or "unknown" for an operation this agent never received (so the server re-queues it).
func (e *Executor) Resume(entries []agentproto.ResumeEntry) {
	for _, en := range entries {
		e.mu.Lock()
		rec, ok := e.journal[en.OpID]
		var evs []agentproto.Event
		if ok {
			for _, ev := range rec.events {
				if ev.Seq > en.LastSeq {
					evs = append(evs, ev)
				}
			}
		}
		e.mu.Unlock()
		if !ok {
			e.emit(agentproto.Event{OpID: en.OpID, Kind: agentproto.EventStatus, Status: agentproto.StatusUnknown, At: e.now().UTC()})
			continue
		}
		for _, ev := range evs {
			e.emit(ev)
		}
	}
}

// Work hands queued operations to their lanes until ctx ends.
func (e *Executor) Work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case rec := <-e.queue:
			e.dispatch(ctx, rec)
		}
	}
}

// laneOf: "" runs concurrently; anything else runs one at a time within its lane.
func laneOf(op agentproto.SignedOp) string {
	switch op.Kind {
	case agentproto.KindAppLogs, agentproto.KindWorkloadLogs, agentproto.KindWorkloadStats, agentproto.KindDBProbe, agentproto.KindCaddyCerts,
		agentproto.KindVolumeUsage, agentproto.KindBrowserStatus, agentproto.KindRecipeStatus:
		return "" // read-only: never queued behind a long action
	case agentproto.KindBrowserCreate, agentproto.KindBrowserRemove:
		// One lane per browser: its create and remove never interleave, and a first image pull of
		// minutes holds up neither the app's deploys nor the host lane.
		var p struct {
			BrowserID string `json:"browser_id"`
		}
		if json.Unmarshal(op.Params, &p) == nil && p.BrowserID != "" {
			return "browser:" + p.BrowserID
		}
		return "browser:?"
	case agentproto.KindRecipeInstall, agentproto.KindRecipeRemove:
		// One lane per install, for the same reasons.
		var p struct {
			InstallID string `json:"install_id"`
		}
		if json.Unmarshal(op.Params, &p) == nil && p.InstallID != "" {
			return "install:" + p.InstallID
		}
		return "install:?"
	case agentproto.KindAppExec:
		// A cron job never waits behind a 45-minute build, nor holds one up. The control plane lets one
		// run of a schedule go at a time; different schedules may overlap.
		return ""
	case agentproto.KindGitHubStatus, agentproto.KindGitHubRepos, agentproto.KindGitHubBranches,
		agentproto.KindGitHubCommits, agentproto.KindGitHubCompare, agentproto.KindGitHubPulls:
		return "" // read-only gh calls: a pending device login must not hold up the repository picker
	case agentproto.KindFSList, agentproto.KindFSRead, agentproto.KindFSWrite, agentproto.KindFSMkdir,
		agentproto.KindFSDelete, agentproto.KindFSRename:
		return "" // browsing and editing files never waits behind a build or a provisioning step
	case agentproto.KindSFTPUser:
		// Its own lane: two sftp.user ops writing the same sshd_config.d drop-in must never
		// interleave, but a slow one must never hold up an app's build or a provisioning step.
		return "sftp"
	case agentproto.KindTerminalOpen:
		// One lane per session: it runs for as long as someone is typing, so it must never share a
		// lane with another session or with a build — but it also must not block one.
		var p struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(op.Params, &p) == nil && p.SessionID != "" {
			return "term:" + p.SessionID
		}
		return "term:?"
	case agentproto.KindGitHubInstall, agentproto.KindGitHubLogin, agentproto.KindGitHubLogout:
		// Their own lane: a device login waits up to 15 minutes for a human and must not block
		// provisioning; install then login (queued in that order) still run in order. apt's lock is
		// shared with the host lane, so the install waits for it (DPkg::Lock::Timeout).
		return "github"
	case agentproto.KindWorkloadPower, agentproto.KindGuestResize, agentproto.KindGuestSnapshot, agentproto.KindGuestBackup,
		agentproto.KindContainerRecreate, agentproto.KindWorkloadDestroy:
		// One lane per workload: a restart never interleaves with a snapshot or a destroy of the same one.
		var p struct {
			VMID string `json:"vmid"`
			Name string `json:"name"`
		}
		if json.Unmarshal(op.Params, &p) == nil && (p.VMID != "" || p.Name != "") {
			if p.VMID != "" {
				return "guest:" + p.VMID
			}
			return "container:" + p.Name
		}
		return "workload:?"
	case agentproto.KindAppDetect:
		// Host lane: it needs git and Railpack, which the provisioning steps queued just before it
		// (same lane, so in order) install on a fresh server.
		return "host"
	case agentproto.KindAppBuild, agentproto.KindAppFastBuild, agentproto.KindAppRelease, agentproto.KindAppStart, agentproto.KindRouteSet,
		agentproto.KindAppDrain, agentproto.KindAppRollback, agentproto.KindAppRemove, agentproto.KindVolumeRemove,
		agentproto.KindRealtimeRoute, agentproto.KindAppProcesses, agentproto.KindAppLimits:
		var p struct {
			AppID string `json:"app_id"`
		}
		if json.Unmarshal(op.Params, &p) == nil && p.AppID != "" {
			return "app:" + p.AppID
		}
		return "app:?"
	case agentproto.KindDBProvision, agentproto.KindDBCreate, agentproto.KindDBDrop, agentproto.KindDBBackup,
		agentproto.KindDBRestore, agentproto.KindDBRemove, agentproto.KindDBUser, agentproto.KindDBQuery, agentproto.KindBucketCreate, agentproto.KindBucketRemove,
		agentproto.KindDBLimits:
		var p struct {
			ServiceID string `json:"service_id"`
		}
		if json.Unmarshal(op.Params, &p) == nil && p.ServiceID != "" {
			return "db:" + p.ServiceID
		}
		return "db:?"
	}
	return "host"
}

func (e *Executor) dispatch(ctx context.Context, rec *record) {
	lane := laneOf(rec.op)
	if lane == "" {
		go e.run(ctx, rec)
		return
	}
	e.lanesMu.Lock()
	ch, ok := e.lanes[lane]
	if !ok {
		ch = make(chan *record, 64)
		e.lanes[lane] = ch
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case r := <-ch:
					e.run(ctx, r)
				}
			}
		}()
	}
	e.lanesMu.Unlock()
	select {
	case ch <- rec:
	default:
		e.event(rec, agentproto.Event{Kind: agentproto.EventStatus, Status: agentproto.StatusFailed,
			Error: "too many operations are waiting for this app; try again shortly"})
	}
}

func (e *Executor) run(ctx context.Context, rec *record) {
	h := e.handlers[rec.op.Kind]
	timeout := time.Duration(rec.op.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	octx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	e.mu.Lock()
	e.running[rec.op.OpID] = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.running, rec.op.OpID)
		e.mu.Unlock()
	}()

	e.event(rec, agentproto.Event{Kind: agentproto.EventStatus, Status: agentproto.StatusRunning})
	oc := &OpContext{
		Ctx: octx, OpID: rec.op.OpID, ServerID: e.serverID, Params: rec.op.Params, Version: e.version, ControlPlane: e.controlPlane, priv: e.priv,
		notify: func(line string, data json.RawMessage) {
			e.event(rec, agentproto.Event{Kind: agentproto.EventLog, Stream: "info", Line: line, Data: data})
		},
		log: func(stream, line string) {
			e.event(rec, agentproto.Event{Kind: agentproto.EventLog, Stream: stream, Line: line})
		},
		progress: func(step, total int) {
			e.event(rec, agentproto.Event{Kind: agentproto.EventProgress, Step: step, Total: total})
		},
	}

	var (
		result any
		code   int
		err    error
	)
	if h == nil { // VerifyOp already enforces the allowlist; belt and braces
		err, code = fmt.Errorf("no handler for %q", rec.op.Kind), 2
	} else {
		func() {
			defer func() {
				if r := recover(); r != nil {
					err, code = fmt.Errorf("the operation panicked: %v", r), 70
				}
			}()
			result, code, err = h(oc)
		}()
	}
	e.mu.Lock()
	cancelled := rec.cancelled
	e.mu.Unlock()
	switch {
	case err != nil && cancelled:
		err, code = errors.New("cancelled"), 130
	case err != nil && errors.Is(octx.Err(), context.DeadlineExceeded):
		err, code = fmt.Errorf("timed out after %s", timeout), 124
	}
	final := agentproto.Event{Kind: agentproto.EventStatus, Status: agentproto.StatusOK, ExitCode: &code}
	if result != nil {
		if raw, mErr := json.Marshal(result); mErr == nil {
			final.Result = raw
		}
	}
	if err != nil {
		final.Status = agentproto.StatusFailed
		final.Error = err.Error()
		if code == 0 {
			code = 1
		}
	}
	e.event(rec, final)
	if oc.restart && err == nil && e.Restart != nil {
		go e.Restart()
	}
}

// event appends to the operation's journal (assigning the next seq) and sends it.
func (e *Executor) event(rec *record, ev agentproto.Event) {
	e.mu.Lock()
	ev.OpID = rec.op.OpID
	ev.Seq = int64(len(rec.events)) + 1
	ev.At = e.now().UTC()
	rec.events = append(rec.events, ev)
	e.mu.Unlock()
	e.emit(ev)
}

// remember adds to the journal, forgetting the oldest beyond journalSize. Caller holds e.mu.
func (e *Executor) remember(rec *record) {
	e.journal[rec.op.OpID] = rec
	e.order = append(e.order, rec.op.OpID)
	for len(e.order) > journalSize {
		delete(e.journal, e.order[0])
		e.order = e.order[1:]
	}
}

// OpContext is what a handler gets: its params, a deadline, and ways to report.
type OpContext struct {
	Ctx          context.Context
	OpID         string
	ServerID     string
	Params       json.RawMessage
	Version      string
	ControlPlane string
	priv         ed25519.PrivateKey
	log          func(stream, line string)
	progress     func(step, total int)
	notify       func(line string, data json.RawMessage)
	restart      bool
}

// Info logs a line on the "info" stream.
func (o *OpContext) Info(format string, args ...any) { o.log("info", fmt.Sprintf(format, args...)) }

// Progress reports step of total.
func (o *OpContext) Progress(step, total int) { o.progress(step, total) }

// RequestRestart asks the agent to re-exec itself after this operation reports success.
func (o *OpContext) RequestRestart() { o.restart = true }

// OpenEnv opens an env box sealed to this agent for context, into NAME → value.
func (o *OpContext) OpenEnv(context, box string) (map[string]string, error) {
	if box == "" {
		return map[string]string{}, nil
	}
	pt, err := agentproto.OpenBox(o.priv, context, box)
	if err != nil {
		return nil, errors.New("the environment could not be opened with this server's key")
	}
	env := map[string]string{}
	if err := json.Unmarshal(pt, &env); err != nil {
		return nil, errors.New("the environment is malformed")
	}
	return env, nil
}

const maxLine = 4096

// Run executes a fixed argv (never a shell), streaming stdout and stderr line by line, and
// returns the exit code.
func (o *OpContext) Run(name string, args ...string) (int, error) {
	return o.RunEnv(nil, name, args...)
}

// RunEnv is Run with extra environment variables for the child (never logged). Docker's `-e NAME`
// (no value) takes the value from here, so a secret never appears in an argv or a file.
func (o *OpContext) RunEnv(extra []string, name string, args ...string) (int, error) {
	o.Info("$ %s", shellQuote(append([]string{name}, args...)))
	cmd := exec.CommandContext(o.Ctx, name, args...)
	cmd.Env = append(append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "LC_ALL=C"), extra...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 127, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 127, err
	}
	if err := cmd.Start(); err != nil {
		return 127, fmt.Errorf("%s: %w", name, err)
	}
	var wg sync.WaitGroup
	pump := func(r io.Reader, stream string) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if len(line) > maxLine {
				line = line[:maxLine] + "…"
			}
			o.log(stream, line)
		}
	}
	wg.Add(2)
	go pump(stdout, "stdout")
	go pump(stderr, "stderr")
	wg.Wait()
	err = cmd.Wait()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), fmt.Errorf("%s exited with status %d", name, exitErr.ExitCode())
	default:
		return 1, err
	}
}

// RunStdin is Run with a fixed string fed to the child's stdin — never logged, and never an argv
// or an environment variable, for a secret like a login password (chpasswd's own stdin format).
func (o *OpContext) RunStdin(stdin, name string, args ...string) (int, error) {
	o.Info("$ %s", shellQuote(append([]string{name}, args...)))
	cmd := exec.CommandContext(o.Ctx, name, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "LC_ALL=C")
	cmd.Stdin = strings.NewReader(stdin)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 127, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 127, err
	}
	if err := cmd.Start(); err != nil {
		return 127, fmt.Errorf("%s: %w", name, err)
	}
	var wg sync.WaitGroup
	pump := func(r io.Reader, stream string) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if len(line) > maxLine {
				line = line[:maxLine] + "…"
			}
			o.log(stream, line)
		}
	}
	wg.Add(2)
	go pump(stdout, "stdout")
	go pump(stderr, "stderr")
	wg.Wait()
	err = cmd.Wait()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), fmt.Errorf("%s exited with status %d", name, exitErr.ExitCode())
	default:
		return 1, err
	}
}

func shellQuote(argv []string) string {
	out := ""
	for i, a := range argv {
		if i > 0 {
			out += " "
		}
		safe := a != ""
		for _, r := range a {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == '/' || r == '=' || r == ':' || r == ',') {
				safe = false
				break
			}
		}
		if safe {
			out += a
		} else {
			out += "'" + a + "'"
		}
	}
	return out
}

// Notify logs line on the "info" stream with a typed payload (agentproto.Event.Data) a client can
// act on while the operation is still running — the device flow's one-time code, for one.
func (o *OpContext) Notify(line string, data any) {
	raw, err := json.Marshal(data)
	if err != nil || o.notify == nil {
		o.log("info", line)
		return
	}
	o.notify(line, raw)
}
