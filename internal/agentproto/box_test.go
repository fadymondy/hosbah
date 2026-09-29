package agentproto

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

func TestBoxRoundTripAndBinding(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	ctx := EnvContext("app-1", "dep-1", "web")
	secret := []byte(`{"DATABASE_URL":"postgres://u:p@db/app"}`)

	box, err := SealBox(pubB64, ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(box, "postgres") || !strings.HasPrefix(box, "cab1:") {
		t.Fatalf("box leaks plaintext or has no prefix: %s", box)
	}
	got, err := OpenBox(priv, ctx, box)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("round trip: %q, %v", got, err)
	}

	// Another context (the same box pasted into another op / process) does not open.
	if _, err := OpenBox(priv, EnvContext("app-1", "dep-1", "worker:queue"), box); err == nil {
		t.Error("a box opened under another context")
	}
	// Another agent cannot open it.
	_, other, _ := ed25519.GenerateKey(nil)
	if _, err := OpenBox(other, ctx, box); err == nil {
		t.Error("another agent's key opened the box")
	}
	// Tampering is detected.
	raw := []byte(box)
	raw[len(raw)-3] ^= 1
	if _, err := OpenBox(priv, ctx, string(raw)); err == nil {
		t.Error("a tampered box opened")
	}
	// Each seal is fresh (ephemeral key + nonce).
	box2, _ := SealBox(pubB64, ctx, secret)
	if box2 == box {
		t.Error("two seals of the same value are identical")
	}
}

// The box primitive is direction-agnostic: an agent can seal to the control plane's own op-signing
// key (SealToControlPlane, container.inspect's EnvBox) exactly the way the control plane seals env
// to an agent's key. Round-trips the same way, and is bound to op id + container name so a box
// copied into another op's result does not open.
func TestAdoptEnvBoxRoundTripAndBinding(t *testing.T) {
	cpPub, cpPriv, _ := ed25519.GenerateKey(nil)
	cpPubB64 := base64.StdEncoding.EncodeToString(cpPub)
	ctx := AdoptEnvContext("op-1", "shop-web")
	secret := []byte(`{"SECRET_TOKEN":"s3cret!"}`)

	box, err := SealBox(cpPubB64, ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(box, "s3cret") {
		t.Fatalf("box leaks plaintext: %s", box)
	}
	got, err := OpenBox(cpPriv, ctx, box)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("round trip: %q, %v", got, err)
	}
	// A different op id, or a different container name, does not open it.
	if _, err := OpenBox(cpPriv, AdoptEnvContext("op-2", "shop-web"), box); err == nil {
		t.Error("a box opened under another op id")
	}
	if _, err := OpenBox(cpPriv, AdoptEnvContext("op-1", "other-container"), box); err == nil {
		t.Error("a box opened under another container name")
	}
}

// The public-key map (control plane) and the private-key map (agent) must agree, or no box opens.
func TestX25519MapsAgree(t *testing.T) {
	for i := 0; i < 20; i++ {
		pub, priv, _ := ed25519.GenerateKey(nil)
		xp, err := X25519FromEd25519Public(pub)
		if err != nil {
			t.Fatal(err)
		}
		xs, err := X25519FromEd25519Private(priv)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(xs.PublicKey().Bytes(), xp.Bytes()) {
			t.Fatalf("maps disagree for key %d", i)
		}
	}
	if _, err := X25519FromEd25519Public(make([]byte, 5)); err == nil {
		t.Error("a short key was accepted")
	}
}

func TestRepoURLAndNames(t *testing.T) {
	for _, ok := range []string{"https://github.com/o/r", "https://github.com/o/r.git", "http://host.docker.internal:3791/cloudy/go.git", "git://example.com/r"} {
		if err := CheckRepoURL(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"ext::sh -c touch% /tmp/x", "file:///etc", "ssh://git@github.com/o/r", "https://u:p@github.com/o/r",
		"https://github.com", "https://github.com/o/r?x=1", "-uhttps://x/y", "git@github.com:o/r.git"} {
		if err := CheckRepoURL(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if got := NormalizeRepoURL("https://GitHub.com/o/r.git/"); got != "https://github.com/o/r" {
		t.Errorf("normalize: %s", got)
	}
	for _, r := range []string{"main", "release/1.2", "v1.0.0", "a1b2c3d"} {
		if !ValidRef(r) {
			t.Errorf("ref %s refused", r)
		}
	}
	for _, r := range []string{"-x", "--upload-pack=sh", "a..b", "", "a b"} {
		if ValidRef(r) {
			t.Errorf("ref %q accepted", r)
		}
	}
	if ContainerName("go-sample", "worker:queue", "0123456789ab") != "hosbah-go-sample-worker-queue-01234567" {
		t.Error("container name")
	}
}
