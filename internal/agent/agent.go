package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Agent is one enrolled agent's connection loop.
type Agent struct {
	State          *State
	Version        string
	Log            *slog.Logger
	HeartbeatEvery time.Duration
	// MetricsEvery is the cadence of the light "metrics" readings between heartbeats (live
	// charts). 0 sends none.
	MetricsEvery time.Duration
	// TrafficPollEvery is how often the Caddy access log is tailed; finished minutes are sent as
	// they complete regardless of this cadence. 0 disables traffic analytics entirely.
	TrafficPollEvery time.Duration
	Exec             *Executor

	priv    ed25519.PrivateKey
	sampler *Sampler
	traffic *trafficCollector
	// live carries events to whichever connection is up. It is only a fast path: every event is
	// also in the executor's journal, and the server's resume list after a reconnect replays
	// whatever did not arrive.
	live chan agentproto.Event
	// termOut carries terminal.open sessions' term.data/term.close frames straight to the wire —
	// never journalled, never replayed: a session a reconnect interrupts is simply over.
	termOut chan agentproto.Frame
}

// New builds an agent from its enrolled state.
func New(st *State, version string, log *slog.Logger) (*Agent, error) {
	priv, cpPub, err := st.Keys()
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	a := &Agent{
		State: st, Version: version, Log: log, HeartbeatEvery: 15 * time.Second, MetricsEvery: 5 * time.Second,
		TrafficPollEvery: 20 * time.Second,
		priv:             priv, sampler: NewSampler(), traffic: newTrafficCollector(), live: make(chan agentproto.Event, 4096),
		termOut: make(chan agentproto.Frame, 256),
	}
	a.Exec = NewExecutor(st.ServerID, cpPub, version, st.ControlPlane)
	a.Exec.SetKey(priv)
	a.Exec.emit = a.enqueue
	a.Exec.termSend = a.enqueueTerm
	a.Exec.Restart = a.restart
	return a, nil
}

func (a *Agent) enqueue(ev agentproto.Event) {
	select {
	case a.live <- ev:
	default:
		// Full (a long disconnect with a chatty operation): the journal still holds the event and
		// the next resume replays it.
	}
}

// enqueueTerm hands a term.* frame to whichever connection is up. Unlike enqueue, there is no
// journal behind it: a frame dropped here (a full buffer, or no connection) is simply gone — the
// session itself notices (its reads keep failing, or the control plane closes it after the agent
// reconnects with no matching session) and a fresh session is how the user recovers.
func (a *Agent) enqueueTerm(f agentproto.Frame) {
	select {
	case a.termOut <- f:
	default:
	}
}

// Run connects, and reconnects with capped, jittered backoff, until ctx ends. Operations keep
// running while disconnected; their events are replayed on reconnect.
func (a *Agent) Run(ctx context.Context) error {
	go a.Exec.Work(ctx)
	go func() { // the token gateways of this server's browsers come back with the agent
		for _, err := range a.Exec.browsers.Restore(ctx) {
			a.Log.Warn("a browser's gateway did not come back", "error", err)
		}
	}()
	backoff := time.Second
	for {
		started := time.Now()
		err := a.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(started) > 30*time.Second {
			backoff = time.Second // it was a healthy session; start the backoff over
		}
		wait := backoff/2 + time.Duration(rand.Int64N(int64(backoff)))
		a.Log.Warn("disconnected from the control plane", "err", err, "retry_in", wait.Round(time.Millisecond))
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		if backoff *= 2; backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

// session is one connection: handshake, then a reader and a writer until either fails.
func (a *Agent) session(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	dctx, dcancel := context.WithTimeout(ctx, 20*time.Second)
	conn, _, err := websocket.Dial(dctx, a.State.WebSocketURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"User-Agent": {"hosbah-agent/" + a.Version}},
	})
	dcancel()
	if err != nil {
		return err
	}
	defer conn.CloseNow() //nolint:errcheck
	conn.SetReadLimit(4 << 20)

	if err := a.handshake(ctx, conn); err != nil {
		return err
	}
	a.Log.Info("connected to the control plane", "server", a.State.ServerName, "server_id", a.State.ServerID)

	errc := make(chan error, 2)
	go func() { errc <- a.writer(ctx, conn) }()
	go func() { errc <- a.reader(ctx, conn) }()
	err = <-errc
	cancel()
	return err
}

func (a *Agent) handshake(ctx context.Context, conn *websocket.Conn) error {
	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var ch agentproto.Frame
	if err := wsjson.Read(hctx, conn, &ch); err != nil {
		return fmt.Errorf("read challenge: %w", err)
	}
	if ch.Type != agentproto.TypeChallenge || ch.Nonce == "" {
		return errors.New("the control plane did not send a challenge")
	}
	sig := ed25519.Sign(a.priv, agentproto.AuthSigningBytes(ch.Nonce, a.State.ServerID))
	if err := wsjson.Write(hctx, conn, agentproto.Frame{
		Type: agentproto.TypeAuth, ServerID: a.State.ServerID, Signature: base64.StdEncoding.EncodeToString(sig),
		AgentVersion: a.Version, Protocol: agentproto.ProtocolVersion,
	}); err != nil {
		return err
	}
	var w agentproto.Frame
	if err := wsjson.Read(hctx, conn, &w); err != nil {
		return fmt.Errorf("read welcome: %w", err)
	}
	switch w.Type {
	case agentproto.TypeWelcome:
		return nil
	case agentproto.TypeError:
		return fmt.Errorf("the control plane refused this agent: %s", w.Error)
	default:
		return fmt.Errorf("unexpected %q frame during the handshake", w.Type)
	}
}

func (a *Agent) writer(ctx context.Context, conn *websocket.Conn) error {
	hb := time.NewTicker(a.HeartbeatEvery)
	defer hb.Stop()
	send := func(f agentproto.Frame) error {
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return wsjson.Write(wctx, conn, f)
	}
	// The first (immediate) beat skips the Docker probe so a reconnect is fast; later beats carry
	// a fresh reading at a slower cadence, which the control plane merges into facts.docker.
	nextDocker := time.Now().Add(a.HeartbeatEvery)
	// NVIDIA GPUs are read once a minute; the beats between repeat the last reading.
	var lastGPUs []agentproto.GPUMetric
	nextGPU := time.Now()
	heartbeat := func() error {
		m := a.sampler.Sample()
		if now := time.Now(); !now.Before(nextDocker) {
			d := DockerInfo(ctx)
			m.Docker = &d
			nextDocker = now.Add(2 * time.Minute)
		}
		if now := time.Now(); !now.Before(nextGPU) {
			lastGPUs = nvidiaMetrics(ctx)
			nextGPU = now.Add(time.Minute)
		}
		m.GPUs = lastGPUs
		return send(agentproto.Frame{Type: agentproto.TypeHeartbeat, Metrics: &m, AgentVersion: a.Version})
	}
	// Between heartbeats, light readings for live charts: CPU, memory, disk, load, network and the
	// last GPU reading, no Docker probe. The control plane keeps one history row per minute.
	var mt <-chan time.Time
	if a.MetricsEvery > 0 {
		t := time.NewTicker(a.MetricsEvery)
		defer t.Stop()
		mt = t.C
	}
	// Traffic: the access log is polled at TrafficPollEvery, but a finished minute is shipped as
	// soon as its bucket closes, whichever poll notices first — no waiting a whole extra cycle.
	var tt <-chan time.Time
	if a.TrafficPollEvery > 0 && a.traffic != nil {
		t := time.NewTicker(a.TrafficPollEvery)
		defer t.Stop()
		tt = t.C
	}
	if err := heartbeat(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-hb.C:
			if err := heartbeat(); err != nil {
				return err
			}
		case <-mt:
			m := a.sampler.Sample()
			m.GPUs = lastGPUs
			if err := send(agentproto.Frame{Type: agentproto.TypeMetrics, Metrics: &m}); err != nil {
				return err
			}
		case <-tt:
			a.traffic.poll()
			if buckets := a.traffic.collect(time.Now()); len(buckets) > 0 {
				if err := send(agentproto.Frame{Type: agentproto.TypeTraffic, Traffic: buckets}); err != nil {
					return err
				}
			}
		case ev := <-a.live:
			if err := send(agentproto.Frame{Type: agentproto.TypeEvent, Event: &ev}); err != nil {
				return err // the event is in the journal; the resume after reconnecting replays it
			}
		case f := <-a.termOut:
			if err := send(f); err != nil {
				return err // the session's next read/write fails and it closes itself
			}
		}
	}
}

func (a *Agent) reader(ctx context.Context, conn *websocket.Conn) error {
	for {
		var f agentproto.Frame
		if err := wsjson.Read(ctx, conn, &f); err != nil {
			return err
		}
		switch f.Type {
		case agentproto.TypeOp:
			a.Exec.Submit(f.Op)
		case agentproto.TypeResume:
			a.Exec.Resume(f.Resume)
		case agentproto.TypeTermData, agentproto.TypeTermResize, agentproto.TypeTermClose:
			a.Exec.TermFrame(f)
		case agentproto.TypeError:
			return fmt.Errorf("control plane: %s", f.Error)
		}
	}
}

// restart re-executes the (just replaced) binary once the final status of the update operation
// has had a chance to reach the control plane. If it has not, the next session's resume reports
// the op as unknown, the server re-queues it, and the new binary answers "already at version".
func (a *Agent) restart() {
	deadline := time.Now().Add(5 * time.Second)
	for len(a.live) > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	a.Log.Info("restarting into the updated binary")
	if err := reexec(); err != nil {
		a.Log.Error("restart failed; the new binary takes over at the next service restart", "err", err)
	}
}
