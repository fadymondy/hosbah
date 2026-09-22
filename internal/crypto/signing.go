package crypto

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
)

// opsSigningInfo is the HKDF context string. Changing it changes the key every agent has pinned.
// Frozen (pre-rename): it derives the key every enrolled agent pinned. A new label is a new key,
// and every agent would refuse every operation.
const opsSigningInfo = "cloudy/ops-signing/v1"

// OpsSigningKey is the control plane's ed25519 key for signing agent operations.
//
// It is derived from the KEK with HKDF-SHA256 under its own context string, so it needs no table
// and no second secret, and it is as protected as the KEK itself (HOSBAH_KEK_FILE in production).
// The consequence, stated plainly: rotating the KEK rotates this key, and every enrolled agent
// must then be given the new public key (a re-enrolment, or a key-rotation op signed with the old
// key — not built in P1). A KMS-held signing key can replace this behind the same method.
func (k *Keyring) OpsSigningKey() (ed25519.PrivateKey, error) {
	seed, err := hkdf.Key(sha256.New, k.kek, nil, opsSigningInfo, ed25519.SeedSize)
	if err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// OpsPublicKey is the base64 public half agents pin at enrolment.
func (k *Keyring) OpsPublicKey() (string, error) {
	priv, err := k.OpsSigningKey()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)), nil
}
