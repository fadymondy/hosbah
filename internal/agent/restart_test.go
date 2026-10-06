package agent

import (
	"context"
	"testing"
	"time"
)

// An agent.update must not re-exec over a build that is still running (MH-1254).
func TestWaitIdleWaitsForRunningOperations(t *testing.T) {
	e := NewExecutor("srv", nil, "v1", "https://cp.example")
	e.mu.Lock()
	e.running["build"] = context.CancelFunc(func() {})
	e.mu.Unlock()
	if e.Running() != 1 {
		t.Fatalf("Running() = %d, want 1", e.Running())
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		e.mu.Lock()
		delete(e.running, "build")
		e.mu.Unlock()
	}()
	waits := 0
	if !waitIdle(e.Running, 5*time.Second, 5*time.Millisecond, func(int) { waits++ }) {
		t.Fatal("waitIdle gave up while the operation was about to finish")
	}
	if waits == 0 {
		t.Fatal("waitIdle returned before the running operation finished")
	}

	e.mu.Lock()
	e.running["stuck"] = context.CancelFunc(func() {})
	e.mu.Unlock()
	if waitIdle(e.Running, 30*time.Millisecond, 5*time.Millisecond, func(int) {}) {
		t.Fatal("waitIdle reported idle with an operation still running")
	}
}
