package agentproto

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func signed(t *testing.T, priv ed25519.PrivateKey, kind, params string, exp time.Time) *SignedOp {
	t.Helper()
	op := &SignedOp{OpID: "op-1", ServerID: "srv-1", Kind: kind, Params: json.RawMessage(params), Exp: exp.Unix(), TimeoutSeconds: 60}
	if err := SignOp(priv, op); err != nil {
		t.Fatal(err)
	}
	return op
}

func TestVerifyOp(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	otherPub, otherPriv, _ := ed25519.GenerateKey(nil)
	now := time.Now()
	later := now.Add(time.Minute)

	good := signed(t, priv, KindProvisionStep, `{"step": "packages.base"}`, later)
	if err := VerifyOp(pub, good, "srv-1", now); err != nil {
		t.Fatalf("a genuine op must verify: %v", err)
	}
	// A relay re-encoding the JSON (whitespace) does not break it: the hash is over the compact form.
	respaced := *good
	respaced.Params = json.RawMessage("{ \"step\" :\n \"packages.base\" }")
	if err := VerifyOp(pub, &respaced, "srv-1", now); err != nil {
		t.Fatalf("whitespace-only re-encoding must still verify: %v", err)
	}

	cases := map[string]func() (*SignedOp, ed25519.PublicKey, string){
		"tampered params": func() (*SignedOp, ed25519.PublicKey, string) {
			op := *good
			op.Params = json.RawMessage(`{"step":"docker.install"}`)
			return &op, pub, "params do not match"
		},
		"tampered params with a recomputed hash": func() (*SignedOp, ed25519.PublicKey, string) {
			op := *good
			op.Params = json.RawMessage(`{"step":"docker.install"}`)
			op.ParamsHash, _ = ParamsHash(op.Params)
			return &op, pub, "signature does not verify"
		},
		"tampered kind": func() (*SignedOp, ed25519.PublicKey, string) {
			op := *good
			op.Kind = KindAgentUpdate
			return &op, pub, "signature does not verify"
		},
		"extended expiry": func() (*SignedOp, ed25519.PublicKey, string) {
			op := *good
			op.Exp += 3600
			return &op, pub, "signature does not verify"
		},
		"unsigned": func() (*SignedOp, ed25519.PublicKey, string) {
			op := *good
			op.Sig = ""
			return &op, pub, "unsigned"
		},
		"signed by another key": func() (*SignedOp, ed25519.PublicKey, string) {
			return signed(t, otherPriv, KindSystemInfo, `{}`, later), pub, "signature does not verify"
		},
		"for another server": func() (*SignedOp, ed25519.PublicKey, string) {
			return good, pub, "another server"
		},
		"expired": func() (*SignedOp, ed25519.PublicKey, string) {
			return signed(t, priv, KindSystemInfo, `{}`, now.Add(-time.Second)), pub, "expired"
		},
		"unknown kind, validly signed": func() (*SignedOp, ed25519.PublicKey, string) {
			return signed(t, priv, "run_command", `{"cmd":"rm -rf /"}`, later), pub, "unknown operation kind"
		},
		"no pinned key": func() (*SignedOp, ed25519.PublicKey, string) {
			return good, nil, "no pinned"
		},
	}
	_ = otherPub
	for name, mk := range cases {
		op, key, want := mk()
		server := "srv-1"
		if name == "for another server" {
			server = "srv-2"
		}
		err := VerifyOp(key, op, server, now)
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want a refusal mentioning %q, got %v", name, want, err)
		}
	}
}

func TestVerifyAuth(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, AuthSigningBytes("nonce", "srv-1")))
	if !VerifyAuth(pubB64, "nonce", "srv-1", sig) {
		t.Fatal("a genuine challenge answer must verify")
	}
	for name, ok := range map[string]bool{
		"other nonce":  VerifyAuth(pubB64, "nonce-2", "srv-1", sig),
		"other server": VerifyAuth(pubB64, "nonce", "srv-2", sig),
		"garbage":      VerifyAuth(pubB64, "nonce", "srv-1", "AAAA"),
	} {
		if ok {
			t.Errorf("%s: must not verify", name)
		}
	}
}

func TestEvaluateChecks(t *testing.T) {
	good := Facts{OSID: "ubuntu", OSVersion: "24.04", Arch: "amd64", MemTotalBytes: 2 << 30, Systemd: true,
		Docker: DockerFacts{Installed: true, Running: true, Version: "27", StorageDriver: "overlay2"}, Ports: map[string]bool{"80": true}}
	if b := Blocking(EvaluateChecks(good)); len(b) != 0 {
		t.Fatalf("a supported host must not be blocked: %+v", b)
	}
	bad := good
	bad.OSID, bad.MemTotalBytes = "alpine", 256<<20
	if b := Blocking(EvaluateChecks(bad)); len(b) != 2 {
		t.Fatalf("unsupported OS and too little memory must both block: %+v", b)
	}
	vfs := good
	vfs.Docker.StorageDriver = "vfs"
	for _, c := range EvaluateChecks(vfs) {
		if c.Name == "docker" && c.Status != CheckWarn {
			t.Fatalf("vfs must warn: %+v", c)
		}
	}
}
