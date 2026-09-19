package server

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
fakeAgent speaks the real agent protocol over the real /agent/ws: it answers the challenge with the
key the server enrolled with, VERIFIES every operation it is sent against the control plane's pinned
key (agentproto.VerifyOp — the exact gate cloudy-agent applies), records it, and answers with a
scripted result. Nothing on the host runs: it is the control plane that is under test.
*/
type fakeAgent struct {
	t        *testing.T
	serverID string
	priv     ed25519.PrivateKey
	respond  func(op agentproto.SignedOp) (result any, errMsg string)
	// events, when set, sends extra events of an op (their OpID and Seq are filled in) after its first
	// log line and before respond is called — so a test can see them while the op is still running.
	events func(op agentproto.SignedOp) []agentproto.Event

	mu       sync.Mutex
	ops      []agentproto.SignedOp
	rejected []string
	cancel   context.CancelFunc
}

// startFakeAgent connects a fake agent for an enrolled server. respond decides each op's outcome
// (errMsg != "" fails it).
func startFakeAgent(t *testing.T, base, serverID string, priv ed25519.PrivateKey, respond func(agentproto.SignedOp) (any, string)) *fakeAgent {
	t.Helper()
	cpPub, err := base64.StdEncoding.DecodeString(svcOf(t).PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	fa := &fakeAgent{t: t, serverID: serverID, priv: priv, respond: respond, cancel: cancel}
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/agent/ws", nil)
	if err != nil {
		t.Fatalf("fake agent dial: %v", err)
	}
	c.SetReadLimit(8 << 20)
	var ch agentproto.Frame
	if err := wsjson.Read(ctx, c, &ch); err != nil || ch.Type != agentproto.TypeChallenge {
		t.Fatalf("fake agent challenge: %v %+v", err, ch)
	}
	sig := ed25519.Sign(priv, agentproto.AuthSigningBytes(ch.Nonce, serverID))
	if err := wsjson.Write(ctx, c, agentproto.Frame{Type: agentproto.TypeAuth, ServerID: serverID,
		Signature: base64.StdEncoding.EncodeToString(sig), AgentVersion: "fake", Protocol: agentproto.ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	var welcome agentproto.Frame
	if err := wsjson.Read(ctx, c, &welcome); err != nil || welcome.Type != agentproto.TypeWelcome {
		t.Fatalf("fake agent welcome: %v %+v", err, welcome)
	}
	var wmu sync.Mutex
	send := func(ev agentproto.Event) {
		wmu.Lock()
		defer wmu.Unlock()
		ev.At = time.Now().UTC()
		_ = wsjson.Write(ctx, c, agentproto.Frame{Type: agentproto.TypeEvent, Event: &ev})
	}
	go func() {
		defer c.CloseNow() //nolint:errcheck
		for {
			var f agentproto.Frame
			if err := wsjson.Read(ctx, c, &f); err != nil {
				return
			}
			switch f.Type {
			case agentproto.TypeResume:
				for _, r := range f.Resume {
					send(agentproto.Event{OpID: r.OpID, Kind: agentproto.EventStatus, Status: agentproto.StatusUnknown})
				}
			case agentproto.TypeOp:
				if f.Op == nil {
					continue
				}
				op := *f.Op
				if err := agentproto.VerifyOp(ed25519.PublicKey(cpPub), &op, serverID, time.Now()); err != nil {
					fa.mu.Lock()
					fa.rejected = append(fa.rejected, op.Kind+": "+err.Error())
					fa.mu.Unlock()
					send(agentproto.Event{OpID: op.OpID, Kind: agentproto.EventStatus, Status: agentproto.StatusRejected, Error: err.Error()})
					continue
				}
				fa.mu.Lock()
				dup := false
				for _, o := range fa.ops {
					dup = dup || o.OpID == op.OpID
				}
				if !dup {
					fa.ops = append(fa.ops, op)
				}
				fa.mu.Unlock()
				if dup {
					continue
				}
				go func(op agentproto.SignedOp) {
					send(agentproto.Event{OpID: op.OpID, Seq: 1, Kind: agentproto.EventStatus, Status: agentproto.StatusRunning})
					send(agentproto.Event{OpID: op.OpID, Seq: 2, Kind: agentproto.EventLog, Stream: "info", Line: "fake " + op.Kind})
					seq := int64(3)
					fa.mu.Lock()
					extra := fa.events
					fa.mu.Unlock()
					if extra != nil {
						for _, ev := range extra(op) {
							ev.OpID, ev.Seq = op.OpID, seq
							seq++
							send(ev)
						}
					}
					res, msg := fa.respond(op)
					code := 0
					ev := agentproto.Event{OpID: op.OpID, Seq: seq, Kind: agentproto.EventStatus, Status: agentproto.StatusOK, ExitCode: &code}
					if res != nil {
						ev.Result, _ = json.Marshal(res)
					}
					if msg != "" {
						code = 1
						ev.Status, ev.Error = agentproto.StatusFailed, msg
					}
					send(ev)
				}(op)
			}
		}
	}()
	t.Cleanup(fa.stop)
	return fa
}

func (fa *fakeAgent) stop() { fa.cancel() }

// waitOp waits for the agent to have received an operation of kind (after `after` of that kind).
func (fa *fakeAgent) waitOp(t *testing.T, id string) agentproto.SignedOp {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		fa.mu.Lock()
		for _, o := range fa.ops {
			if o.OpID == id {
				fa.mu.Unlock()
				return o
			}
		}
		fa.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the agent never received operation %s (rejected: %v)", id, fa.rejectedList())
	return agentproto.SignedOp{}
}

func (fa *fakeAgent) rejectedList() []string {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return append([]string(nil), fa.rejected...)
}

func (fa *fakeAgent) kinds() []string {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	var out []string
	for _, o := range fa.ops {
		out = append(out, o.Kind)
	}
	return out
}
