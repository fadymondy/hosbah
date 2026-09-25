package agent

import (
	"context"
	"encoding/base64"
	"io"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// terminalHost runs terminal.open sessions: an interactive PTY streamed over term.* frames on the
// same agent connection, never through the operation log (a keystroke is never persisted, and
// PTYs run only where spawnTerminal is implemented — Linux; see terminal_linux.go /
// terminal_other.go). The control plane still signs terminal.open like any other operation, so
// the agent only ever opens a PTY it was asked to.
type terminalHost struct {
	send func(agentproto.Frame)

	mu       sync.Mutex
	sessions map[string]*termSession
}

// terminalPTY is the small surface a spawned session needs, on whichever platform provides one.
type terminalPTY interface {
	io.Reader
	io.Writer
	Resize(cols, rows int) error
	Close() error
}

type termSession struct {
	id     string
	cancel context.CancelFunc
	input  chan []byte
	resize chan [2]int
	closed chan struct{}
}

func newTerminalHost(send func(agentproto.Frame)) *terminalHost {
	return &terminalHost{send: send, sessions: map[string]*termSession{}}
}

func (h *terminalHost) register(s *termSession) {
	h.mu.Lock()
	h.sessions[s.id] = s
	h.mu.Unlock()
}

func (h *terminalHost) unregister(id string) {
	h.mu.Lock()
	delete(h.sessions, id)
	h.mu.Unlock()
}

// Handle routes a term.data / term.resize / term.close frame from the control plane to its
// session. An unknown session id is silently ignored — already closed, or never opened here.
func (h *terminalHost) Handle(f agentproto.Frame) {
	h.mu.Lock()
	s := h.sessions[f.SessionID]
	h.mu.Unlock()
	if s == nil {
		return
	}
	switch f.Type {
	case agentproto.TypeTermData:
		if !agentproto.ValidTermData(f.TermData) {
			return
		}
		raw, err := base64.StdEncoding.DecodeString(f.TermData)
		if err != nil {
			return
		}
		select {
		case s.input <- raw:
		case <-s.closed:
		default: // the shell is not keeping up (a fast paste): drop rather than block the reader
		}
	case agentproto.TypeTermResize:
		if !agentproto.ValidTermSize(f.TermCols, f.TermRows) {
			return
		}
		select {
		case s.resize <- [2]int{f.TermCols, f.TermRows}:
		case <-s.closed:
		default:
		}
	case agentproto.TypeTermClose:
		s.cancel()
	}
}

// open is the terminal.open handler: it blocks for the whole session (bounded by
// MaxTermSessionSeconds, the op's own timeout), so it must run in its own lane (see laneOf) —
// never behind, or in front of, a build or another session.
func (h *terminalHost) open(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.TerminalOpenParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}

	ctx, cancel := context.WithCancel(oc.Ctx)
	sess := &termSession{id: p.SessionID, cancel: cancel, input: make(chan []byte, 64), resize: make(chan [2]int, 8), closed: make(chan struct{})}
	h.register(sess)
	defer func() {
		h.unregister(p.SessionID)
		close(sess.closed)
		cancel()
	}()

	pty, err := spawnTerminal(ctx, p)
	if err != nil {
		h.closeFrame(p.SessionID, "could not start: "+err.Error())
		return nil, 1, err
	}
	defer pty.Close() //nolint:errcheck
	oc.Info("terminal session opened (target %s)", p.Target)

	const idleTimeout = agentproto.MaxTermIdleSeconds * time.Second
	idle := time.NewTimer(idleTimeout)
	defer idle.Stop()
	touch := func() {
		if !idle.Stop() {
			select {
			case <-idle.C:
			default:
			}
		}
		idle.Reset(idleTimeout)
	}

	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				h.send(agentproto.Frame{Type: agentproto.TypeTermData, SessionID: p.SessionID,
					TermData: base64.StdEncoding.EncodeToString(buf[:n])})
			}
			if err != nil {
				readErr <- err
				return
			}
		}
	}()

	reason := "closed"
loop:
	for {
		select {
		case <-ctx.Done():
			if oc.Ctx.Err() != nil {
				reason = "closed: the session's time limit was reached"
			}
			break loop
		case <-readErr:
			reason = "the session ended"
			break loop
		case data := <-sess.input:
			touch()
			if _, err := pty.Write(data); err != nil {
				reason = "the session ended"
				break loop
			}
		case sz := <-sess.resize:
			touch()
			_ = pty.Resize(sz[0], sz[1])
		case <-idle.C:
			reason = "closed after 15 minutes idle"
			break loop
		}
	}
	_ = pty.Close()
	h.closeFrame(p.SessionID, reason)
	return map[string]any{"closed": true}, 0, nil
}

func (h *terminalHost) closeFrame(sessionID, reason string) {
	h.send(agentproto.Frame{Type: agentproto.TypeTermClose, SessionID: sessionID, TermReason: reason})
}
