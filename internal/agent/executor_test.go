package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

type sink struct {
	mu  sync.Mutex
	evs []agentproto.Event
}

func (s *sink) add(ev agentproto.Event) { s.mu.Lock(); s.evs = append(s.evs, ev); s.mu.Unlock() }
func (s *sink) all() []agentproto.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agentproto.Event(nil), s.evs...)
}

func (s *sink) waitFor(t *testing.T, pred func(agentproto.Event) bool) agentproto.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range s.all() {
			if pred(ev) {
				return ev
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no matching event; got %+v", s.all())
	return agentproto.Event{}
}

func newTestExecutor(t *testing.T) (*Executor, ed25519.PrivateKey, *sink, *atomic.Int32) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	e := NewExecutor("srv-1", pub, "v1", "http://cp.test")
	s := &sink{}
	e.emit = s.add
	var runs atomic.Int32
	e.handlers[agentproto.KindSystemInfo] = func(oc *OpContext) (any, int, error) {
		runs.Add(1)
		oc.Info("hello")
		return map[string]string{"ok": "yes"}, 0, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go e.Work(ctx)
	return e, priv, s, &runs
}

func signedOp(t *testing.T, priv ed25519.PrivateKey, id, kind, params string) *agentproto.SignedOp {
	op := &agentproto.SignedOp{OpID: id, ServerID: "srv-1", Kind: kind, Params: json.RawMessage(params),
		Exp: time.Now().Add(time.Minute).Unix(), TimeoutSeconds: 30}
	if err := agentproto.SignOp(priv, op); err != nil {
		t.Fatal(err)
	}
	return op
}

// The P1 exit test: a tampered or unsigned operation is refused and never executed.
func TestTamperedOrUnsignedOpIsRefusedAndNotRun(t *testing.T) {
	e, priv, s, runs := newTestExecutor(t)
	_, foreign, _ := ed25519.GenerateKey(nil)

	tampered := signedOp(t, priv, "op-t", agentproto.KindSystemInfo, `{}`)
	tampered.Params = json.RawMessage(`{"x":1}`)
	unsigned := signedOp(t, priv, "op-u", agentproto.KindSystemInfo, `{}`)
	unsigned.Sig = ""
	forged := signedOp(t, foreign, "op-f", agentproto.KindSystemInfo, `{}`)

	for _, op := range []*agentproto.SignedOp{tampered, unsigned, forged} {
		e.Submit(op)
		ev := s.waitFor(t, func(ev agentproto.Event) bool { return ev.OpID == op.OpID })
		if ev.Status != agentproto.StatusRejected || ev.Seq != 0 {
			t.Fatalf("%s: want a rejected control event, got %+v", op.OpID, ev)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if n := runs.Load(); n != 0 {
		t.Fatalf("a refused operation ran %d time(s)", n)
	}
}

func TestOpRunsOnceAndResumes(t *testing.T) {
	e, priv, s, runs := newTestExecutor(t)
	op := signedOp(t, priv, "op-1", agentproto.KindSystemInfo, `{}`)
	e.Submit(op)
	final := s.waitFor(t, func(ev agentproto.Event) bool { return ev.OpID == "op-1" && ev.Status == agentproto.StatusOK })
	if final.Seq != 3 { // running, log "hello", ok
		t.Fatalf("want seq 3 for the final status, got %+v", final)
	}
	// Re-sent (same op_id): replayed, not re-run.
	e.Submit(op)
	time.Sleep(100 * time.Millisecond)
	if runs.Load() != 1 {
		t.Fatalf("a re-sent op ran %d times", runs.Load())
	}
	// Resume after seq 1: exactly seqs 2 and 3 again. Unknown op: status unknown.
	before := len(s.all())
	e.Resume([]agentproto.ResumeEntry{{OpID: "op-1", LastSeq: 1}, {OpID: "never-seen", LastSeq: 0}})
	got := s.all()[before:]
	if len(got) != 3 || got[0].Seq != 2 || got[1].Seq != 3 || got[2].Status != agentproto.StatusUnknown {
		t.Fatalf("resume replay wrong: %+v", got)
	}
}

// agent.update installs only bytes whose SHA-256 is the one inside the signed params.
func TestAgentUpdateVerifiesChecksum(t *testing.T) {
	good := []byte("new agent binary")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("evil bytes")) }))
	defer srv.Close()

	dir := t.TempDir()
	self := filepath.Join(dir, "cloudy-agent")
	if err := os.WriteFile(self, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := executablePath
	executablePath = func() string { return self }
	defer func() { executablePath = orig }()

	pub, priv, _ := ed25519.GenerateKey(nil)
	e := NewExecutor("srv-1", pub, "v1", srv.URL)
	s := &sink{}
	e.emit = s.add
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Work(ctx)

	sum := sha256.Sum256(good)
	params, _ := json.Marshal(map[string]string{"version": "v2", "url": srv.URL + "/agent/download/cloudy-agent-linux-amd64", "sha256": hex.EncodeToString(sum[:])})
	e.Submit(signedOp(t, priv, "op-up", agentproto.KindAgentUpdate, string(params)))
	final := s.waitFor(t, func(ev agentproto.Event) bool {
		return ev.OpID == "op-up" && (ev.Status == agentproto.StatusOK || ev.Status == agentproto.StatusFailed)
	})
	if final.Status != agentproto.StatusFailed || !strings.Contains(final.Error, "checksum mismatch") {
		t.Fatalf("a download that does not match the signed hash must fail: %+v", final)
	}
	if b, _ := os.ReadFile(self); string(b) != "old" {
		t.Fatal("the binary was replaced despite the checksum mismatch")
	}
}
