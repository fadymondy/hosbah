package saasify

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// SignWebhook is exactly HMAC-SHA256(secret, body) hex-encoded — the receiver-side verification
// a team's billing system does against X-Hosbah-Signature — so it must match a plain
// crypto/hmac computation byte for byte, be deterministic, and change with either input.
func TestSignWebhookMatchesPlainHMAC(t *testing.T) {
	secret := "whsec_test_123"
	body := []byte(`{"event":"instance.provisioned","instance":{"id":"i1"}}`)

	got := SignWebhook(secret, body)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))

	if got != want {
		t.Fatalf("SignWebhook = %s, want %s", got, want)
	}
	if len(got) != sha256.Size*2 {
		t.Fatalf("SignWebhook produced %d hex chars, want %d (a %d-byte digest)", len(got), sha256.Size*2, sha256.Size)
	}
}

func TestSignWebhookIsDeterministic(t *testing.T) {
	secret, body := "s3cret", []byte("same body every time")
	a := SignWebhook(secret, body)
	b := SignWebhook(secret, body)
	if a != b {
		t.Fatalf("SignWebhook is not deterministic: %s != %s", a, b)
	}
}

func TestSignWebhookChangesWithBodyOrSecret(t *testing.T) {
	base := SignWebhook("secret-a", []byte("body-1"))
	if base == SignWebhook("secret-b", []byte("body-1")) {
		t.Fatal("SignWebhook did not change when the secret changed")
	}
	if base == SignWebhook("secret-a", []byte("body-2")) {
		t.Fatal("SignWebhook did not change when the body changed")
	}
}

// A webhook secret sealed for one team round-trips through the same team's DEK, and fails to
// open under another team's id or another DEK entirely — the same binding every other sealed
// field in Hosbah gets (internal/crypto's context binding), applied to this one.
func TestWebhookSecretRoundTripsAndIsBound(t *testing.T) {
	dek := randomDEK(t)
	otherDEK := randomDEK(t)
	const teamID, otherTeamID = "team-a", "team-b"
	const plain = "whsec_abcdef0123456789"

	sealed, err := sealWebhookSecret(dek, teamID, plain)
	if err != nil {
		t.Fatalf("sealWebhookSecret: %v", err)
	}
	if sealed == plain {
		t.Fatal("sealWebhookSecret returned the plaintext unchanged")
	}

	opened, err := openWebhookSecret(dek, teamID, sealed)
	if err != nil {
		t.Fatalf("openWebhookSecret: %v", err)
	}
	if opened != plain {
		t.Fatalf("openWebhookSecret = %q, want %q", opened, plain)
	}

	if _, err := openWebhookSecret(dek, otherTeamID, sealed); err == nil {
		t.Fatal("openWebhookSecret opened a value sealed for a different team")
	}
	if _, err := openWebhookSecret(otherDEK, teamID, sealed); err == nil {
		t.Fatal("openWebhookSecret opened a value sealed under a different DEK")
	}
}

func randomDEK(t *testing.T) []byte {
	t.Helper()
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return dek
}
