package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

// op.cancel takes effect while its target is running — it is not queued behind it — and the
// target reports "cancelled" (exit 130).
func TestCancelStopsARunningOp(t *testing.T) {
	e, priv, s, _ := newTestExecutor(t)
	started := make(chan struct{})
	e.handlers[agentproto.KindAppBuild] = func(oc *OpContext) (any, int, error) {
		close(started)
		<-oc.Ctx.Done()
		return nil, 1, oc.Ctx.Err()
	}
	e.Submit(signedOp(t, priv, "00000000-0000-4000-8000-00000000b001", agentproto.KindAppBuild, `{"app_id":"a1"}`))
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the build never started")
	}
	e.Submit(signedOp(t, priv, "00000000-0000-4000-8000-00000000c001", agentproto.KindOpCancel, `{"op_id":"00000000-0000-4000-8000-00000000b001"}`))
	ev := s.waitFor(t, func(ev agentproto.Event) bool {
		return ev.OpID == "00000000-0000-4000-8000-00000000b001" && ev.Kind == agentproto.EventStatus && ev.Status == agentproto.StatusFailed
	})
	if ev.Error != "cancelled" || ev.ExitCode == nil || *ev.ExitCode != 130 {
		t.Fatalf("want cancelled/130, got %+v", ev)
	}
	s.waitFor(t, func(ev agentproto.Event) bool {
		return ev.OpID == "00000000-0000-4000-8000-00000000c001" && ev.Status == agentproto.StatusOK
	})
}

// Two apps' operations run side by side; one app's run one at a time, in order.
func TestLanes(t *testing.T) {
	e, priv, s, _ := newTestExecutor(t)
	var inFlight, peak atomic.Int32
	release := make(chan struct{})
	e.handlers[agentproto.KindAppStart] = func(oc *OpContext) (any, int, error) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		select {
		case <-release:
		case <-oc.Ctx.Done():
		}
		inFlight.Add(-1)
		return nil, 0, nil
	}
	e.Submit(signedOp(t, priv, "00000000-0000-4000-8000-0000000000a1", agentproto.KindAppStart, `{"app_id":"a"}`))
	e.Submit(signedOp(t, priv, "00000000-0000-4000-8000-0000000000a2", agentproto.KindAppStart, `{"app_id":"a"}`))
	e.Submit(signedOp(t, priv, "00000000-0000-4000-8000-0000000000b1", agentproto.KindAppStart, `{"app_id":"b"}`))
	deadline := time.Now().Add(3 * time.Second)
	for peak.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if got := peak.Load(); got != 2 {
		t.Fatalf("peak concurrency %d, want 2 (a1 with b1; a2 waits for a1)", got)
	}
	close(release)
	for _, id := range []string{"00000000-0000-4000-8000-0000000000a1", "00000000-0000-4000-8000-0000000000a2", "00000000-0000-4000-8000-0000000000b1"} {
		id := id
		s.waitFor(t, func(ev agentproto.Event) bool { return ev.OpID == id && ev.Status == agentproto.StatusOK })
	}
	_ = context.Background()
}
