package agent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// guestEnrolOpCtx builds an OpContext for guest.enrol, with the params sealed to priv's own public
// key the way the control plane would seal them to the agent's key.
func guestEnrolOpCtx(t *testing.T, priv ed25519.PrivateKey, vmid, command string) *OpContext {
	t.Helper()
	pub := priv.Public().(ed25519.PublicKey)
	box, err := agentproto.SealBox(base64.StdEncoding.EncodeToString(pub), agentproto.GuestEnrolContext(vmid), []byte(command))
	if err != nil {
		t.Fatalf("seal command: %v", err)
	}
	raw, err := json.Marshal(agentproto.GuestEnrolParams{VMID: vmid, CommandBox: box})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return &OpContext{Ctx: context.Background(), OpID: "op", ServerID: "srv", priv: priv, Params: raw,
		log: func(_, _ string) {}, progress: func(int, int) {}}
}

// TestGuestEnrolRefusesWithoutPct: this node has no pct on PATH — it is not a Proxmox node — so
// guest.enrol must refuse before ever touching the guest, with a message that says so.
func TestGuestEnrolRefusesWithoutPct(t *testing.T) {
	dir := t.TempDir() // empty: no pct, no anything
	prevPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	t.Cleanup(func() { _ = os.Setenv("PATH", prevPath) })

	pub, priv, err := ed25519.GenerateKey(nil)
	_ = pub
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	oc := guestEnrolOpCtx(t, priv, "101", "curl -fsSL https://example.test/install/secret-token | sh")

	_, code, err := guestEnrol(oc)
	if err == nil {
		t.Fatal("expected an error when pct is not on PATH")
	}
	if code == 0 {
		t.Fatalf("expected a non-zero exit code, got 0")
	}
	if !strings.Contains(err.Error(), "pct") {
		t.Fatalf("expected the refusal to mention pct, got: %v", err)
	}
}

// TestGuestEnrolRejectsBadBox: a command_box that does not open with this agent's key (wrong
// server, or tampered) must be refused, never run.
func TestGuestEnrolRejectsBadBox(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	_, wrongPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	// Sealed to priv's key, but the OpContext opens it with wrongPriv.
	oc := guestEnrolOpCtx(t, priv, "101", "curl -fsSL https://example.test/install/secret-token | sh")
	oc.priv = wrongPriv

	_, code, err := guestEnrol(oc)
	if err == nil {
		t.Fatal("expected an error opening a box sealed to a different key")
	}
	if code == 0 {
		t.Fatalf("expected a non-zero exit code, got 0")
	}
}

func TestRedactInstallToken(t *testing.T) {
	in := "connecting to https://example.test/install/AbC123-token_xyz ...\ndone"
	out := redactInstallToken(in)
	if strings.Contains(out, "AbC123-token_xyz") {
		t.Fatalf("token leaked in output: %q", out)
	}
	if !strings.Contains(out, "/install/[redacted]") {
		t.Fatalf("expected redaction marker, got: %q", out)
	}
}
