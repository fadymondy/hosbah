package crypto

import (
	"errors"
	"strings"
	"testing"
)

func TestPlatformSealRoundTripAndBinding(t *testing.T) {
	k := testKeyring(t)
	sealed, err := k.SealPlatform("totp:u1", []byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, platformPrefix) || strings.Contains(sealed, "JBSWY3DPEHPK3PXP") {
		t.Fatalf("sealed value looks wrong: %q", sealed)
	}
	got, err := k.OpenPlatform("totp:u1", sealed)
	if err != nil || string(got) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("round trip: %q %v", got, err)
	}
	// Moved to another account: refused.
	if _, err := k.OpenPlatform("totp:u2", sealed); !errors.Is(err, ErrOpen) {
		t.Fatalf("other context opened: %v", err)
	}
	// Another KEK: refused.
	if _, err := testKeyring(t).OpenPlatform("totp:u1", sealed); !errors.Is(err, ErrOpen) {
		t.Fatalf("other KEK opened: %v", err)
	}
	// A team value is not a platform value.
	if _, err := k.OpenPlatform("totp:u1", sealedPrefix+strings.TrimPrefix(sealed, platformPrefix)); !errors.Is(err, ErrOpen) {
		t.Fatal("wrong prefix accepted")
	}
}
