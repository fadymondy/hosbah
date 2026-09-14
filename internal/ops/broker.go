package ops

import (
	"sync"

	"github.com/coder/websocket"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

// Broker is an in-process wake-up bus: publishers say "topic X changed", subscribers re-read
// from the database. It carries no data, deliberately — every SSE stream reads through its own
// team-scoped transaction, so a stream can only ever show what row-level security lets its team
// see, and a lost or coalesced wake-up costs latency, never correctness (streams also poll).
// This is also why Cloudy never uses togo's global /events broadcast.
type Broker struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

// NewBroker returns an empty broker.
func NewBroker() *Broker { return &Broker{subs: map[string]map[chan struct{}]struct{}{}} }

// Subscribe returns a channel that receives (coalesced) wake-ups for topic, and an unsubscribe.
func (b *Broker) Subscribe(topic string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	if b.subs[topic] == nil {
		b.subs[topic] = map[chan struct{}]struct{}{}
	}
	b.subs[topic][ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs[topic], ch)
		if len(b.subs[topic]) == 0 {
			delete(b.subs, topic)
		}
		b.mu.Unlock()
	}
}

// Publish wakes every subscriber of topic without blocking.
func (b *Broker) Publish(topics ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range topics {
		for ch := range b.subs[t] {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// ---- the agent connection hub ------------------------------------------------------------------

type agentConn struct {
	serverID string
	teamID   string
	dek      []byte
	remoteIP string
	version  string
	ws       *websocket.Conn
	send     chan agentproto.Frame
}

type hub struct {
	mu    sync.RWMutex
	conns map[string]*agentConn
}

func newHub() *hub { return &hub{conns: map[string]*agentConn{}} }

// add registers c, closing an earlier connection of the same server (one agent, one socket).
func (h *hub) add(c *agentConn) {
	h.mu.Lock()
	old := h.conns[c.serverID]
	h.conns[c.serverID] = c
	h.mu.Unlock()
	if old != nil {
		_ = old.ws.Close(websocket.StatusPolicyViolation, "replaced by a newer connection")
	}
}

// remove unregisters c if it is still the current connection of its server.
func (h *hub) remove(c *agentConn) {
	h.mu.Lock()
	if h.conns[c.serverID] == c {
		delete(h.conns, c.serverID)
	}
	h.mu.Unlock()
}

func (h *hub) get(serverID string) *agentConn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns[serverID]
}

func (h *hub) ids() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.conns))
	for id := range h.conns {
		out = append(out, id)
	}
	return out
}

func (h *hub) closeAll() {
	h.mu.Lock()
	conns := h.conns
	h.conns = map[string]*agentConn{}
	h.mu.Unlock()
	for _, c := range conns {
		_ = c.ws.Close(websocket.StatusGoingAway, "control plane shutting down")
	}
}
