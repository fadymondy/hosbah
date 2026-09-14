// Package tokens mints bearer secrets and the SHA-256 hashes stored in their place.
//
// Only the hash is ever written to the database; the plaintext is returned once, to the
// person who minted it. Prefixes make a leaked token recognisable (secret scanners, logs):
//
//	cld_  team-bound user MCP token     (mcp_tokens)
//	cla_  platform-admin MCP token      (admin_mcp_tokens)
//	(none) invitation link token        (invitations)
package tokens

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

const (
	PrefixUserMCP  = "cld_"
	PrefixAdminMCP = "cla_"
)

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
