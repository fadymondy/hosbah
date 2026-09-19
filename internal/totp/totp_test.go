package totp

import (
	"encoding/base32"
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA-1 column: secret "12345678901234567890", 8 digits. We use 6 digits,
// which are the last 6 of the 8-digit values.
func TestRFC6238Vectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for _, v := range []struct {
		unix int64
		want string
	}{
		{59, "287082"},          // 94287082
		{1111111109, "081804"},  // 07081804
		{1111111111, "050471"},  // 14050471
		{1234567890, "005924"},  // 89005924
		{2000000000, "279037"},  // 69279037
		{20000000000, "353130"}, // 65353130
	} {
		got, err := Code(secret, time.Unix(v.unix, 0))
		if err != nil {
			t.Fatal(err)
		}
		if got != v.want {
			t.Errorf("T=%d: got %s want %s", v.unix, got, v.want)
		}
	}
}

func TestValidWindow(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	now := time.Unix(1_700_000_000, 0)
	cur, _ := Code(secret, now)
	prev, _ := Code(secret, now.Add(-30*time.Second))
	next, _ := Code(secret, now.Add(30*time.Second))
	old, _ := Code(secret, now.Add(-90*time.Second))

	if !Valid(secret, cur, now) || !Valid(secret, prev, now) || !Valid(secret, next, now) {
		t.Fatal("a code within ±1 step was rejected")
	}
	if Valid(secret, old, now) {
		t.Fatal("a code 3 steps old was accepted")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if Valid(secret, bad, now) {
			t.Fatalf("malformed code %q was accepted", bad)
		}
	}
	if Valid("not base32!!", cur, now) {
		t.Fatal("an undecodable secret validated")
	}
}

func TestMatchIsSingleUse(t *testing.T) {
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	code, _ := Code(secret, now)
	step := Match(secret, code, now, 0)
	if step != now.Unix()/Period {
		t.Fatalf("step = %d", step)
	}
	if Match(secret, code, now, step) != 0 {
		t.Fatal("a used step matched again")
	}
	prev, _ := Code(secret, now.Add(-Period*time.Second))
	if Match(secret, prev, now, 0) == 0 {
		t.Fatal("previous step should be within skew")
	}
	old, _ := Code(secret, now.Add(-3*Period*time.Second))
	if Match(secret, old, now, 0) != 0 {
		t.Fatal("a code three steps old matched")
	}
}
