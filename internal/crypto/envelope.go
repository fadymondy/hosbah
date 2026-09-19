// Package crypto is Hosbah's envelope encryption.
//
// One key-encryption key (KEK) per deployment, loaded from CLOUDY_KEK (base64, 32
// bytes) or CLOUDY_KEK_FILE (a service-only file holding the same). It never
// encrypts data directly: it wraps one data-encryption key (DEK) per team, and
// the wrapped DEK is stored on the team row (teams.dek_wrapped). Every secret a
// team owns — provider credentials, env values, database passwords, SSH private
// keys, operation scripts and logs — is sealed with that team's DEK.
//
// Both layers are AES-256-GCM with a fresh 96-bit random nonce per seal, and both
// bind additional authenticated data:
//
//   - a wrapped DEK is bound to its team id, so copying one team's dek_wrapped
//     onto another team's row does not decrypt;
//   - a sealed value is bound to the team id and to a caller-chosen context
//     ("env:<app_id>:<name>", "credential:<id>"…), so a ciphertext moved to a
//     different row or field fails to open instead of being silently accepted.
//
// Rotating the KEK means re-wrapping every team's DEK (cheap: one small
// ciphertext per team); rotating a DEK means re-sealing that team's values. The
// "v1" prefixes leave room for both. A KMS can replace the local KEK later
// behind the same Keyring methods.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	keySize = 32 // AES-256

	wrappedPrefix = "cdk1:" // a wrapped team DEK
	sealedPrefix  = "csv1:" // a value sealed with a team DEK
)

var (
	// ErrNoKEK means no key-encryption key is configured. The server refuses to
	// start without one rather than storing secrets in the clear.
	ErrNoKEK = errors.New("crypto: CLOUDY_KEK (or CLOUDY_KEK_FILE) is not set")
	// ErrKEKSize means the configured KEK is not exactly 32 bytes.
	ErrKEKSize = errors.New("crypto: the KEK must decode to exactly 32 bytes")
	// ErrOpen is returned for any failure to decrypt: wrong key, wrong team or
	// context, tampering, truncation. Deliberately one error, so a caller cannot
	// use the difference as an oracle.
	ErrOpen = errors.New("crypto: ciphertext could not be opened")
)

// Keyring holds the deployment's KEK.
type Keyring struct {
	kek []byte
}

// NewKeyring builds a keyring from raw KEK bytes (32).
func NewKeyring(kek []byte) (*Keyring, error) {
	if len(kek) != keySize {
		return nil, ErrKEKSize
	}
	k := make([]byte, keySize)
	copy(k, kek)
	return &Keyring{kek: k}, nil
}

// LoadKeyring reads the KEK from CLOUDY_KEK_FILE (preferred: a file only the
// service user can read) or CLOUDY_KEK, both base64-encoded.
func LoadKeyring() (*Keyring, error) {
	raw := ""
	if path := strings.TrimSpace(os.Getenv("CLOUDY_KEK_FILE")); path != "" {
		b, err := os.ReadFile(path) //#nosec G304 -- operator-configured key file
		if err != nil {
			return nil, fmt.Errorf("crypto: read CLOUDY_KEK_FILE: %w", err)
		}
		raw = string(b)
	} else {
		raw = os.Getenv("CLOUDY_KEK")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrNoKEK
	}
	kek, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("crypto: CLOUDY_KEK is not valid base64: %w", err)
	}
	return NewKeyring(kek)
}

// NewDEK returns a fresh random 32-byte data-encryption key.
func NewDEK() ([]byte, error) {
	dek := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, err
	}
	return dek, nil
}

// WrapDEK encrypts a team's DEK under the KEK, bound to the team id.
func (k *Keyring) WrapDEK(teamID string, dek []byte) (string, error) {
	if len(dek) != keySize {
		return "", errors.New("crypto: a DEK must be 32 bytes")
	}
	ct, err := seal(k.kek, dek, dekAAD(teamID))
	if err != nil {
		return "", err
	}
	return wrappedPrefix + ct, nil
}

// UnwrapDEK recovers a team's DEK. It fails if the wrapped key belongs to
// another team, was produced under another KEK, or was altered.
func (k *Keyring) UnwrapDEK(teamID, wrapped string) ([]byte, error) {
	body, ok := strings.CutPrefix(wrapped, wrappedPrefix)
	if !ok {
		return nil, ErrOpen
	}
	dek, err := open(k.kek, body, dekAAD(teamID))
	if err != nil || len(dek) != keySize {
		return nil, ErrOpen
	}
	return dek, nil
}

// NewTeamKey creates a DEK for a new team and returns it with its wrapped form
// (the value to store in teams.dek_wrapped).
func (k *Keyring) NewTeamKey(teamID string) (dek []byte, wrapped string, err error) {
	dek, err = NewDEK()
	if err != nil {
		return nil, "", err
	}
	wrapped, err = k.WrapDEK(teamID, dek)
	if err != nil {
		return nil, "", err
	}
	return dek, wrapped, nil
}

// Seal encrypts plaintext with a team DEK. context names the field the value
// belongs to; Open must be given the same team id and context.
func Seal(dek []byte, teamID, context string, plaintext []byte) (string, error) {
	if len(dek) != keySize {
		return "", errors.New("crypto: a DEK must be 32 bytes")
	}
	ct, err := seal(dek, plaintext, valueAAD(teamID, context))
	if err != nil {
		return "", err
	}
	return sealedPrefix + ct, nil
}

// Open decrypts a value produced by Seal.
func Open(dek []byte, teamID, context, sealed string) ([]byte, error) {
	body, ok := strings.CutPrefix(sealed, sealedPrefix)
	if !ok || len(dek) != keySize {
		return nil, ErrOpen
	}
	pt, err := open(dek, body, valueAAD(teamID, context))
	if err != nil {
		return nil, ErrOpen
	}
	return pt, nil
}

// IsSealed reports whether a stored string is a sealed value (so list
// endpoints can refuse to echo it and migrations can find plaintext left over).
func IsSealed(s string) bool { return strings.HasPrefix(s, sealedPrefix) }

func dekAAD(teamID string) []byte { return []byte("cloudy/dek/v1\x00" + teamID) }

func valueAAD(teamID, context string) []byte {
	return []byte("cloudy/value/v1\x00" + teamID + "\x00" + context)
}

// seal = base64(nonce || AES-256-GCM(key, nonce, plaintext, aad)).
func seal(key, plaintext, aad []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, plaintext, aad)
	return base64.RawStdEncoding.EncodeToString(out), nil
}

func open(key []byte, body string, aad []byte) ([]byte, error) {
	raw, err := base64.RawStdEncoding.DecodeString(body)
	if err != nil {
		return nil, ErrOpen
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrOpen
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, ErrOpen
	}
	return pt, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
