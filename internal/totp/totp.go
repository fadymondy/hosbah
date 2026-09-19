// Package totp is RFC 6238 time-based one-time passwords: HMAC-SHA1, 30-second steps, 6
// digits — what every authenticator app expects. Hosbah's two-factor sign-in
// (internal/account) accepts the current step and one either side, and never the same step
// twice (Match's after).
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //#nosec G505 -- RFC 6238 TOTP is defined over HMAC-SHA1
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

const (
	// Period is the step length in seconds.
	Period = 30
	digits = 6
	// skew accepts the previous and next step, for clock drift between phone and server.
	skew = 1
)

var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a fresh 160-bit secret, base32 without padding.
func NewSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return encoding.EncodeToString(raw), nil
}

// Code returns the code for a base32 secret at time t.
func Code(secret string, t time.Time) (string, error) {
	key, err := decode(secret)
	if err != nil {
		return "", err
	}
	return at(key, uint64(t.Unix()/Period)), nil
}

// Valid reports whether code matches secret at now, within ±1 step.
func Valid(secret, code string, now time.Time) bool { return Match(secret, code, now, 0) != 0 }

// Match returns the step code belongs to — now or one either side — considering only steps
// greater than after, or 0 when it matches none. Storing the returned step and passing it as
// after next time makes every code single-use.
func Match(secret, code string, now time.Time, after int64) int64 {
	code = strings.TrimSpace(code)
	if len(code) != digits {
		return 0
	}
	key, err := decode(secret)
	if err != nil {
		return 0
	}
	cur := now.Unix() / Period
	var found int64
	for d := int64(-skew); d <= skew; d++ {
		step := cur + d
		if step <= after {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(at(key, uint64(step))), []byte(code)) == 1 && found == 0 {
			found = step
		}
	}
	return found
}

func at(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", bin%1_000_000)
}

func decode(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	s = strings.TrimRight(s, "=")
	return encoding.DecodeString(s)
}
