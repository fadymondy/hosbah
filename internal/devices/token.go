// Package devices holds the refresh tokens of the phone and desktop apps (docs/apps-plan.md).
//
// A signed-in app keeps one hbr_… refresh token. Only its SHA-256 is stored (devices.refresh_hash);
// POST /api/auth/device/refresh trades it for a fresh 24-hour session and a new refresh token, so a
// stolen copy stops working the next time the real app refreshes.
package devices

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"
)

// Prefix marks a device refresh token (secret scanners and support can recognise one).
const Prefix = "hbr_"

// Window is how long a device stays signed in without opening the app: each refresh restarts it.
const Window = 90 * 24 * time.Hour

// NewRefresh returns a new refresh token and the hash to store.
func NewRefresh() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = Prefix + base64.RawURLEncoding.EncodeToString(b)
	return token, Hash(token), nil
}

// Hash is the stored form of a refresh token.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

// Valid reports whether token looks like a refresh token at all (cheap check before a lookup).
func Valid(token string) bool {
	return strings.HasPrefix(token, Prefix) && len(token) > len(Prefix)+20 && len(token) < 200
}

// Platforms and Apps are the values devices.platform and devices.app accept.
var (
	Platforms = []string{"ios", "android", "windows", "macos", "linux"}
	Apps      = []string{"mobile", "desktop"}
)
