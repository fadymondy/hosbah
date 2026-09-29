package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Two apps deploying together must not build at once: the second waits for the first's slot, and
// gives up with a clear error if its op times out while waiting.
func TestBuildSlotRunsOneBuildAtATime(t *testing.T) {
	var logs []string
	oc := func(ctx context.Context) *OpContext {
		return &OpContext{Ctx: ctx, log: func(_, line string) { logs = append(logs, line) },
			notify: func(string, json.RawMessage) {}, progress: func(int, int) {}}
	}
	release, err := acquireBuildSlot(oc(t.Context()))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := acquireBuildSlot(oc(ctx)); err == nil || !strings.Contains(err.Error(), "waiting for another build") {
		t.Fatalf("a second build while the slot is held: err = %v, want a wait timeout", err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "waiting") {
		t.Fatalf("logs = %q, want one waiting line", logs)
	}

	got := make(chan error, 1)
	go func() {
		r, err := acquireBuildSlot(oc(t.Context()))
		if err == nil {
			r()
		}
		got <- err
	}()
	release()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting build never got the slot after it was released")
	}
}
