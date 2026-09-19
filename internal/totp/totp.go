// Package totp verifies RFC 6238 time-based one-time passwords (HMAC-SHA1, 30 s,
// 6 digits) — the format the togo auth plugin's /api/auth/2fa/enroll issues.
//
// It exists because the plugin enrolls and verifies TOTP but its login never asks
// for it: an account with 2FA "enabled" still signed in with a password alone.
// Hosbah's login wrapper (internal/server/login_2fa.go) uses this to require it.
package totp

import (
	"crypto/hmac"
	"crypto/sha1" //#nosec G505 -- RFC 6238 TOTP is defined over HMAC-SHA1
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

const (
	period = 30
	digits = 6
	// skew accepts the previous and next step, for clock drift between phone and server.
	skew = 1
)

// Code returns the code for a base32 secret at time t.
func Code(secret string, t time.Time) (string, error) {
	key, err := decode(secret)
	if err != nil {
		return "", err
	}
	return at(key, uint64(t.Unix()/period)), nil
}

// Valid reports whether code matches secret at now, within ±1 step.
func Valid(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != digits {
		return false
	}
	key, err := decode(secret)
	if err != nil {
		return false
	}
	step := now.Unix() / period
	ok := 0
	for d := -skew; d <= skew; d++ {
		ok |= subtle.ConstantTimeCompare([]byte(at(key, uint64(step+int64(d)))), []byte(code))
	}
	return ok == 1
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
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
}
