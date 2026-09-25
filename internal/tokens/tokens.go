// Package tokens mints bearer secrets and the SHA-256 hashes stored in their place.
//
// Only the hash is ever written to the database; the plaintext is returned once, to the
// person who minted it. Prefixes make a leaked token recognisable (secret scanners, logs):
//
//	hbu_  team-bound user MCP token     (mcp_tokens)          was cld_ before the rename
//	hba_  platform-admin MCP token      (admin_mcp_tokens)    was cla_
//	hbe_  agent enrolment token         (agent_enrollments)   was cle_  (internal/ops)
//	hbh_  app deploy-hook token         (apps)                was cdh_  (internal/deploy)
//	hbp_  heartbeat ping token          (monitors)             new in MH-575 H3
//	hbr_  RUM ingest key (public, not a secret — travels in page source)  new in MH-575 H5
//	(none) invitation link token        (invitations)
//
// New tokens get the hb*_ prefix. Tokens minted before the rename keep working: the prefix is
// part of the hashed plaintext, so a stored hash only ever matches the exact token it was made
// from, and the checks below accept either prefix of a family. No two prefixes share a family.
package tokens

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	PrefixUserMCP  = "hbu_"
	PrefixAdminMCP = "hba_"
	PrefixEnrol    = "hbe_"
	PrefixHook     = "hbh_"
	PrefixPing     = "hbp_"
	PrefixRum      = "hbr_"

	// Before the rename from Cloudy. Accepted, never minted.
	LegacyPrefixUserMCP  = "cld_"
	LegacyPrefixAdminMCP = "cla_"
	LegacyPrefixEnrol    = "cle_"
	LegacyPrefixHook     = "cdh_"
)

// Families: the prefixes a token of each kind may carry, the current one first.
var (
	UserMCP  = []string{PrefixUserMCP, LegacyPrefixUserMCP}
	AdminMCP = []string{PrefixAdminMCP, LegacyPrefixAdminMCP}
	Enrol    = []string{PrefixEnrol, LegacyPrefixEnrol}
	Hook     = []string{PrefixHook, LegacyPrefixHook}
	Ping     = []string{PrefixPing}
	Rum      = []string{PrefixRum}
)

// HasPrefix reports whether token starts with one of the family's prefixes.
func HasPrefix(token string, family []string) bool {
	for _, p := range family {
		if strings.HasPrefix(token, p) {
			return true
		}
	}
	return false
}

// New returns a fresh token with the given prefix and its hash.
func New(prefix string) (plain, hash string) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("tokens: crypto/rand failed: " + err.Error())
	}
	plain = prefix + hex.EncodeToString(b)
	return plain, Hash(plain)
}

// Hash is the SHA-256 hex digest stored for a plaintext token.
func Hash(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
