package agent

import (
	"context"
	"encoding/base64"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// TestTerminalHostHandleRouting checks the frame router on its own, with a fake session — this
// runs on every OS (spawnTerminal, the real PTY, is Linux-only; see TestSpawnTerminalHostEcho).
func TestTerminalHostHandleRouting(t *testing.T) {
	h := newTerminalHost(func(agentproto.Frame) {})
	sess := &termSession{id: "s1", input: make(chan []byte, 4), resize: make(chan [2]int, 4), closed: make(chan struct{})}
	h.register(sess)
	t.Cleanup(func() { h.unregister(sess.id) })

	// An unknown session id is silently ignored.
	h.Handle(agentproto.Frame{Type: agentproto.TypeTermData, SessionID: "unknown", TermData: "aGk="})

	// Bad base64: dropped, not delivered.
	h.Handle(agentproto.Frame{Type: agentproto.TypeTermData, SessionID: "s1", TermData: "not base64!!"})
	select {
	case b := <-sess.input:
		t.Fatalf("bad base64 should not reach input, got %q", b)
	default:
	}

	// A valid frame is decoded and delivered.
	h.Handle(agentproto.Frame{Type: agentproto.TypeTermData, SessionID: "s1", TermData: base64.StdEncoding.EncodeToString([]byte("hello"))})
	select {
	case b := <-sess.input:
		if string(b) != "hello" {
			t.Fatalf("input = %q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("no input delivered")
	}

	// An out-of-range resize is dropped.
	h.Handle(agentproto.Frame{Type: agentproto.TypeTermResize, SessionID: "s1", TermCols: 1, TermRows: 1})
	select {
	case sz := <-sess.resize:
		t.Fatalf("out-of-range resize should not deliver, got %v", sz)
	default:
	}

	// A valid resize is delivered.
	h.Handle(agentproto.Frame{Type: agentproto.TypeTermResize, SessionID: "s1", TermCols: 100, TermRows: 30})
	select {
	case sz := <-sess.resize:
		if sz != [2]int{100, 30} {
			t.Fatalf("resize = %v", sz)
		}
	case <-time.After(time.Second):
		t.Fatal("no resize delivered")
	}
}

// TestSpawnTerminalHostEcho: MH-574 E11. A real PTY, Linux only (the agent only ever ships for
// Linux; see terminal_linux.go / terminal_other.go) — open a login shell, echo something through
// it, and confirm Close ends the process.
func TestSpawnTerminalHostEcho(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PTYs run on Linux only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := spawnTerminal(ctx, agentproto.TerminalOpenParams{SessionID: "11111111-1111-1111-1111-111111111111",
		Target: agentproto.TerminalTargetHost, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("spawnTerminal: %v", err)
	}

	marker := "hosbah-terminal-test-echo"
	if _, err := p.Write([]byte("echo " + marker + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	found := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		var out strings.Builder
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			n, rerr := p.Read(buf)
			if n > 0 {
				out.Write(buf[:n])
				if strings.Contains(out.String(), marker) {
					close(found)
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()
	select {
	case <-found:
	case <-time.After(8 * time.Second):
		t.Fatal("the echoed marker never came back through the PTY")
	}

	if err := p.Resize(100, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
