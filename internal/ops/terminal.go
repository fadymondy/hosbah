package ops

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// A web terminal session relayed browser <-> control plane <-> agent. The control plane never
// stores a byte of it: it only signs the terminal.open op (so the agent opens a PTY it was asked
// for) and then forwards term.* frames between the browser's websocket handler (internal/api) and
// the agent's connection by session id, refusing anything for another team or user.

// MaxTerminalsPerServer caps concurrent sessions (a runaway browser tab, or a forgotten one).
const MaxTerminalsPerServer = 4

// ErrTerminalLimit: too many sessions already open on this server.
var ErrTerminalLimit = errors.New("this server already has the maximum number of terminal sessions open")

// ErrServerOffline: the agent is not connected right now.
var ErrServerOffline = errors.New("the server's agent is not connected")

// TerminalSession is the control plane's record of one open session, held only in memory.
type TerminalSession struct {
	ID       string
	ServerID string
	TeamID   string
	UserID   string
	Target   string
	Opened   time.Time
	// Out carries term_data / term_close frames the agent sent, for the browser handler to relay.
	Out chan agentproto.Frame

	closeOnce sync.Once
}

type terminalRegistry struct {
	mu       sync.Mutex
	sessions map[string]*TerminalSession
}

func newTerminalRegistry() *terminalRegistry {
	return &terminalRegistry{sessions: map[string]*TerminalSession{}}
}

func (r *terminalRegistry) add(s *TerminalSession) {
	r.mu.Lock()
	r.sessions[s.ID] = s
	r.mu.Unlock()
}

func (r *terminalRegistry) remove(id string) {
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
}

func (r *terminalRegistry) get(id string) *TerminalSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[id]
}

func (r *terminalRegistry) countForServer(serverID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sessions {
		if s.ServerID == serverID {
			n++
		}
	}
	return n
}

// OpenTerminal signs and sends a terminal.open op to serverID's live agent connection, and
// registers the session so agent frames for it can be relayed back. The caller (internal/api) has
// already checked team membership and the ability for target (AbilityManageServers for the host,
// AbilityDeploy for an app container).
func (s *Service) OpenTerminal(teamID, serverID, userID, target, deploymentID string, cols, rows int) (*TerminalSession, error) {
	ac := s.hub.get(serverID)
	if ac == nil || ac.teamID != teamID {
		return nil, ErrServerOffline
	}
	if s.terminals.countForServer(serverID) >= MaxTerminalsPerServer {
		return nil, ErrTerminalLimit
	}
	sess := &TerminalSession{ID: newUUID(), ServerID: serverID, TeamID: teamID, UserID: userID, Target: target,
		Opened: time.Now(), Out: make(chan agentproto.Frame, 64)}

	p := agentproto.TerminalOpenParams{SessionID: sess.ID, Target: target, Cols: cols, Rows: rows, DeploymentID: deploymentID}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	params, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	so := &agentproto.SignedOp{OpID: newUUID(), ServerID: serverID, Kind: agentproto.KindTerminalOpen,
		Params: params, TimeoutSeconds: agentproto.MaxTermSessionSeconds}
	so.Exp = time.Now().Add(agentproto.MaxTermSessionSeconds * time.Second).Unix()
	if err := agentproto.SignOp(s.signer, so); err != nil {
		return nil, err
	}

	s.terminals.add(sess)
	select {
	case ac.send <- agentproto.Frame{Type: agentproto.TypeOp, Op: so}:
	default:
		s.terminals.remove(sess.ID)
		return nil, errors.New("the agent's connection is backed up — try again")
	}
	return sess, nil
}

// SendTerminalInput relays a term_data / term_resize / term_close frame from the browser to
// sess's agent, refusing silently if the agent has since disconnected (the browser handler's own
// read loop then errors out and closes).
func (s *Service) SendTerminalInput(sess *TerminalSession, f agentproto.Frame) {
	ac := s.hub.get(sess.ServerID)
	if ac == nil {
		return
	}
	f.SessionID = sess.ID
	select {
	case ac.send <- f:
	default:
	}
}

// CloseTerminal asks the agent to end sess (best effort) and forgets it. Safe to call more than
// once (a browser disconnect and an agent-reported close racing each other).
func (s *Service) CloseTerminal(sess *TerminalSession) {
	sess.closeOnce.Do(func() {
		s.terminals.remove(sess.ID)
		if ac := s.hub.get(sess.ServerID); ac != nil {
			select {
			case ac.send <- agentproto.Frame{Type: agentproto.TypeTermClose, SessionID: sess.ID}:
			default:
			}
		}
		close(sess.Out)
	})
}

// TerminalSession looks up an open session by id, or nil.
func (s *Service) TerminalSessionByID(id string) *TerminalSession { return s.terminals.get(id) }

// routeTerminalFrame relays a term_data / term_close frame the agent sent to its session's Out
// channel, for the browser handler to forward. ac.serverID must match the session's — an agent
// can only ever speak for its own server, so this is really just the "unknown session" filter.
func (s *Service) routeTerminalFrame(ac *agentConn, f agentproto.Frame) {
	sess := s.terminals.get(f.SessionID)
	if sess == nil || sess.ServerID != ac.serverID {
		return
	}
	select {
	case sess.Out <- f:
	default: // the browser is not keeping up: drop rather than block the agent's reader
	}
	if f.Type == agentproto.TypeTermClose {
		s.CloseTerminal(sess)
	}
}

// closeTerminalsOfServer ends every open session of serverID (its agent just disconnected: there
// is nothing left to relay to).
func (s *Service) closeTerminalsOfServer(serverID string) {
	s.terminals.mu.Lock()
	var sessions []*TerminalSession
	for _, sess := range s.terminals.sessions {
		if sess.ServerID == serverID {
			sessions = append(sessions, sess)
		}
	}
	s.terminals.mu.Unlock()
	for _, sess := range sessions {
		sess.closeOnce.Do(func() {
			s.terminals.remove(sess.ID)
			close(sess.Out)
		})
	}
}
