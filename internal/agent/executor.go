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
	"sync"
	"time"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

// Handler runs one kind of operation. It logs through oc and returns a JSON-able result.
type Handler func(oc *OpContext) (result any, exitCode int, err error)

// Executor verifies, de-duplicates, queues and runs operations, one at a time, and keeps a
// journal of every event each one emitted so a reconnect can resume exactly where the control
// plane's copy stops.
type Executor struct {
	serverID     string
	cpPub        ed25519.PublicKey
	version      string
	controlPlane string
	now          func() time.Time
	emit         func(agentproto.Event)
	handlers     map[string]Handler
	// Restart is called after an operation that asked for it (agent.update) has reported its
	// final status.
	Restart func()

	mu      sync.Mutex
	journal map[string]*record
	order   []string
	queue   chan *record
}

type record struct {
	op      agentproto.SignedOp
	events  []agentproto.Event
	restart bool
}

// journalSize bounds memory: the last N operations are remembered for de-duplication and resume.
// The journal is in memory only — after an agent restart, the control plane's resume list gets
// "unknown" for in-flight ops and re-queues them; every kind is safe to re-run (provision steps
// are idempotent, agent.update checks the running version first).
const journalSize = 256

// NewExecutor builds an executor for one server, trusting one control-plane key.
func NewExecutor(serverID string, cpPub ed25519.PublicKey, version, controlPlane string) *Executor {
	e := &Executor{
		serverID: serverID, cpPub: cpPub, version: version, controlPlane: controlPlane,
		now: time.Now, emit: func(agentproto.Event) {},
		journal: map[string]*record{}, queue: make(chan *record, 64),
	}
	e.handlers = map[string]Handler{
		agentproto.KindSystemInfo:    systemInfo,
		agentproto.KindDockerCheck:   dockerCheck,
		agentproto.KindProvisionStep: provisionStep,
		agentproto.KindAgentUpdate:   e.agentUpdate,
	}
	return e
}

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

	select {
	case e.queue <- rec:
	default:
		e.event(rec, agentproto.Event{Kind: agentproto.EventStatus, Status: agentproto.StatusFailed,
			Error: "the agent's operation queue is full; try again shortly"})
	}
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

// Work runs queued operations one at a time (provisioning steps share apt and dpkg locks).
func (e *Executor) Work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case rec := <-e.queue:
			e.run(ctx, rec)
		}
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

	e.event(rec, agentproto.Event{Kind: agentproto.EventStatus, Status: agentproto.StatusRunning})
	oc := &OpContext{
		Ctx: octx, Params: rec.op.Params, Version: e.version, ControlPlane: e.controlPlane,
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
	if err != nil && errors.Is(octx.Err(), context.DeadlineExceeded) {
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
	Params       json.RawMessage
	Version      string
	ControlPlane string
	log          func(stream, line string)
	progress     func(step, total int)
	restart      bool
}

// Info logs a line on the "info" stream.
func (o *OpContext) Info(format string, args ...any) { o.log("info", fmt.Sprintf(format, args...)) }

// Progress reports step of total.
func (o *OpContext) Progress(step, total int) { o.progress(step, total) }

// RequestRestart asks the agent to re-exec itself after this operation reports success.
func (o *OpContext) RequestRestart() { o.restart = true }

const maxLine = 4096

// Run executes a fixed argv (never a shell), streaming stdout and stderr line by line, and
// returns the exit code.
func (o *OpContext) Run(name string, args ...string) (int, error) {
	o.Info("$ %s", shellQuote(append([]string{name}, args...)))
	cmd := exec.CommandContext(o.Ctx, name, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "LC_ALL=C")
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
