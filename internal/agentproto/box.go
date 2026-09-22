package agentproto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"strings"

	"filippo.io/edwards25519"
)

/*
The env box: how a secret travels from the control plane to ONE agent.

Env values are sealed at rest with the team's DEK, which an agent never holds. To start a container
the agent needs them in the clear, and the operation that carries them is stored (operations.params)
and crosses relays. So the control plane seals them again, to the target agent's own key:

  - The agent's ed25519 key (generated on the server at enrolment, never sent anywhere) is mapped to
    X25519 — the standard birational map (libsodium's crypto_sign_ed25519_pk_to_curve25519 /
    _sk_to_curve25519). The control plane derives the X25519 public key from the ed25519 public key
    it pinned at enrolment, so there is no second key to register — or for a relay to substitute.
  - ECIES: an ephemeral X25519 key, HKDF-SHA256 over the shared secret (salt = both public keys),
    AES-256-GCM, with the context ("env:<app>:<deployment>:<process>") as additional data so a box
    copied into another operation does not open.

What is stored in operations.params is therefore ciphertext only this server can open; the op
signature covers it like every other param.
*/

const boxPrefix = "cab1:"

// EnvContext binds a box to what it is for.
func EnvContext(appID, deploymentID, process string) string {
	return "env:" + appID + ":" + deploymentID + ":" + process
}

// X25519FromEd25519Public maps an ed25519 public key to its X25519 (Montgomery) form.
func X25519FromEd25519Public(pub ed25519.PublicKey) (*ecdh.PublicKey, error) {
	if len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("box: not an ed25519 public key")
	}
	p, err := new(edwards25519.Point).SetBytes(pub)
	if err != nil {
		return nil, errors.New("box: invalid ed25519 public key")
	}
	return ecdh.X25519().NewPublicKey(p.BytesMontgomery())
}

// X25519FromEd25519Private maps an ed25519 private key to its X25519 scalar (SHA-512 of the seed,
// first half; crypto/ecdh clamps).
func X25519FromEd25519Private(priv ed25519.PrivateKey) (*ecdh.PrivateKey, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("box: not an ed25519 private key")
	}
	h := sha512.Sum512(priv.Seed())
	return ecdh.X25519().NewPrivateKey(h[:32])
}

// SealBox seals plaintext to the agent whose ed25519 public key (base64, as enrolled) is given.
func SealBox(agentPubB64, context string, plaintext []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(agentPubB64)
	if err != nil {
		return "", errors.New("box: agent key is not base64")
	}
	recipient, err := X25519FromEd25519Public(raw)
	if err != nil {
		return "", err
	}
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	shared, err := eph.ECDH(recipient)
	if err != nil {
		return "", err
	}
	gcm, err := boxCipher(shared, eph.PublicKey().Bytes(), recipient.Bytes())
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := append(append([]byte{}, eph.PublicKey().Bytes()...), nonce...)
	out = gcm.Seal(out, nonce, plaintext, []byte(context))
	return boxPrefix + base64.RawStdEncoding.EncodeToString(out), nil
}

// OpenBox is the agent's half.
func OpenBox(priv ed25519.PrivateKey, context, box string) ([]byte, error) {
	fail := errors.New("box: could not be opened")
	body, ok := strings.CutPrefix(box, boxPrefix)
	if !ok {
		return nil, fail
	}
	raw, err := base64.RawStdEncoding.DecodeString(body)
	if err != nil || len(raw) < 32+12+16 {
		return nil, fail
	}
	me, err := X25519FromEd25519Private(priv)
	if err != nil {
		return nil, fail
	}
	ephPub, err := ecdh.X25519().NewPublicKey(raw[:32])
	if err != nil {
		return nil, fail
	}
	shared, err := me.ECDH(ephPub)
	if err != nil {
		return nil, fail
	}
	gcm, err := boxCipher(shared, raw[:32], me.PublicKey().Bytes())
	if err != nil {
		return nil, fail
	}
	pt, err := gcm.Open(nil, raw[32:44], raw[44:], []byte(context))
	if err != nil {
		return nil, fail
	}
	return pt, nil
}

func boxCipher(shared, ephPub, recipientPub []byte) (cipher.AEAD, error) {
	salt := append(append([]byte{}, ephPub...), recipientPub...)
	// "cloudy/agent-box/v1" is frozen (pre-rename): agents on both sides of an upgrade must derive the
	// same key, and changing it would break every box sealed to an agent that has not updated.
	key, err := hkdf.Key(sha256.New, shared, salt, "cloudy/agent-box/v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
