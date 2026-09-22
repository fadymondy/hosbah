package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"strings"
)

/*
Platform values: secrets that belong to an account rather than a team (an authenticator secret),
so there is no team DEK to seal them with.

They are sealed under a key derived from the KEK (HMAC-SHA256 over a fixed label), never with the
KEK itself, and bound to a caller-chosen context ("totp:<user_id>") exactly as team values are
bound to their field. The "cpv1:" prefix keeps them distinguishable from team values ("csv1:").
Rotating the KEK means re-sealing these too.
*/

const platformPrefix = "cpv1:"

func (k *Keyring) platformKey() []byte {
	m := hmac.New(sha256.New, k.kek)
	m.Write([]byte("cloudy/platform-key/v1")) // frozen (pre-rename): it derives the key of everything sealed so far
	return m.Sum(nil)
}

// platformAAD's label is frozen for the same reason: it is the additional data of every sealed value.
func platformAAD(context string) []byte { return []byte("cloudy/platform/v1\x00" + context) }

// SealPlatform encrypts an account-level secret. OpenPlatform must be given the same context.
func (k *Keyring) SealPlatform(context string, plaintext []byte) (string, error) {
	ct, err := seal(k.platformKey(), plaintext, platformAAD(context))
	if err != nil {
		return "", err
	}
	return platformPrefix + ct, nil
}

// OpenPlatform decrypts a value produced by SealPlatform.
func (k *Keyring) OpenPlatform(context, sealed string) ([]byte, error) {
	body, ok := strings.CutPrefix(sealed, platformPrefix)
	if !ok {
		return nil, ErrOpen
	}
	pt, err := open(k.platformKey(), body, platformAAD(context))
	if err != nil {
		return nil, ErrOpen
	}
	return pt, nil
}
