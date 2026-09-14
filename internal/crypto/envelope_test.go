package crypto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKeyring(t *testing.T) *Keyring {
	t.Helper()
	kek, err := NewDEK() // any 32 random bytes
	if err != nil {
		t.Fatal(err)
	}
	k, err := NewKeyring(kek)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestWrapUnwrapRoundTrip(t *testing.T) {
	k := testKeyring(t)
	dek, wrapped, err := k.NewTeamKey("team-a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(wrapped, wrappedPrefix) {
		t.Fatalf("wrapped DEK lacks its version prefix: %q", wrapped)
	}
	if strings.Contains(wrapped, base64.RawStdEncoding.EncodeToString(dek)) {
		t.Fatal("wrapped DEK contains the plaintext DEK")
	}
	got, err := k.UnwrapDEK("team-a", wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, dek) {
		t.Fatal("unwrapped DEK differs from the original")
	}
}

// A wrapped DEK copied onto another team's row must not open.
func TestUnwrapBoundToTeam(t *testing.T) {
	k := testKeyring(t)
	_, wrapped, _ := k.NewTeamKey("team-a")
	if _, err := k.UnwrapDEK("team-b", wrapped); !errors.Is(err, ErrOpen) {
		t.Fatalf("expected ErrOpen unwrapping team-a's DEK as team-b, got %v", err)
	}
}

func TestUnwrapWrongKEK(t *testing.T) {
	k1, k2 := testKeyring(t), testKeyring(t)
	_, wrapped, _ := k1.NewTeamKey("team-a")
	if _, err := k2.UnwrapDEK("team-a", wrapped); !errors.Is(err, ErrOpen) {
		t.Fatalf("expected ErrOpen under a different KEK, got %v", err)
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	dek, _ := NewDEK()
	sealed, err := Seal(dek, "team-a", "env:app1:DATABASE_URL", []byte("postgres://secret"))
	if err != nil {
		t.Fatal(err)
	}
	if !IsSealed(sealed) || strings.Contains(sealed, "secret") {
		t.Fatalf("sealed value looks wrong: %q", sealed)
	}
	pt, err := Open(dek, "team-a", "env:app1:DATABASE_URL", sealed)
	if err != nil || string(pt) != "postgres://secret" {
		t.Fatalf("round trip failed: %q %v", pt, err)
	}
}

func TestSealIsRandomised(t *testing.T) {
	dek, _ := NewDEK()
	a, _ := Seal(dek, "t", "c", []byte("same"))
	b, _ := Seal(dek, "t", "c", []byte("same"))
	if a == b {
		t.Fatal("two seals of the same plaintext are identical — nonce reuse")
	}
}

// Ciphertext moved to another team, another field, or opened with another
// team's key must fail — and fail with the same error each time.
func TestOpenBoundToTeamAndContext(t *testing.T) {
	dekA, _ := NewDEK()
	dekB, _ := NewDEK()
	sealed, _ := Seal(dekA, "team-a", "credential:1", []byte("token"))

	cases := map[string]func() error{
		"other team id": func() error { _, err := Open(dekA, "team-b", "credential:1", sealed); return err },
		"other context": func() error { _, err := Open(dekA, "team-a", "credential:2", sealed); return err },
		"other DEK":     func() error { _, err := Open(dekB, "team-a", "credential:1", sealed); return err },
	}
	for name, fn := range cases {
		if err := fn(); !errors.Is(err, ErrOpen) {
			t.Errorf("%s: expected ErrOpen, got %v", name, err)
		}
	}
}

func TestOpenRejectsTampering(t *testing.T) {
	dek, _ := NewDEK()
	sealed, _ := Seal(dek, "t", "c", []byte("hello world"))
	body := strings.TrimPrefix(sealed, sealedPrefix)
	raw, _ := base64.RawStdEncoding.DecodeString(body)

	flipped := append([]byte(nil), raw...)
	flipped[len(flipped)-1] ^= 0x01
	for name, candidate := range map[string]string{
		"flipped bit":  sealedPrefix + base64.RawStdEncoding.EncodeToString(flipped),
		"truncated":    sealedPrefix + base64.RawStdEncoding.EncodeToString(raw[:10]),
		"no prefix":    body,
		"not base64":   sealedPrefix + "!!!",
		"empty":        "",
		"wrapped form": wrappedPrefix + body,
	} {
		if _, err := Open(dek, "t", "c", candidate); !errors.Is(err, ErrOpen) {
			t.Errorf("%s: expected ErrOpen, got %v", name, err)
		}
	}
}

func TestKeyringRejectsBadKEK(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := NewKeyring(make([]byte, n)); !errors.Is(err, ErrKEKSize) {
			t.Errorf("KEK of %d bytes: expected ErrKEKSize, got %v", n, err)
		}
	}
}

func TestLoadKeyringFromEnvAndFile(t *testing.T) {
	kek, _ := NewDEK()
	enc := base64.StdEncoding.EncodeToString(kek)

	t.Setenv("CLOUDY_KEK_FILE", "")
	t.Setenv("CLOUDY_KEK", "")
	if _, err := LoadKeyring(); !errors.Is(err, ErrNoKEK) {
		t.Fatalf("expected ErrNoKEK with nothing configured, got %v", err)
	}

	t.Setenv("CLOUDY_KEK", enc)
	k1, err := LoadKeyring()
	if err != nil {
		t.Fatal(err)
	}

	// The file wins over the variable, and trailing newlines are tolerated.
	path := filepath.Join(t.TempDir(), "kek")
	other, _ := NewDEK()
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(other)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLOUDY_KEK_FILE", path)
	k2, err := LoadKeyring()
	if err != nil {
		t.Fatal(err)
	}
	_, wrapped, _ := k1.NewTeamKey("t")
	if _, err := k2.UnwrapDEK("t", wrapped); !errors.Is(err, ErrOpen) {
		t.Fatal("CLOUDY_KEK_FILE did not take precedence over CLOUDY_KEK")
	}

	t.Setenv("CLOUDY_KEK_FILE", "")
	t.Setenv("CLOUDY_KEK", "not base64!")
	if _, err := LoadKeyring(); err == nil {
		t.Fatal("expected an error for a non-base64 KEK")
	}
}
