package agentproto

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// sshkey.install / sshkey.uninstall (MH-578 C10): idempotently add or remove one public key in a
// unix user's ~/.ssh/authorized_keys, marked with a Hosbah comment line so an uninstall only ever
// touches the one line it itself added — never a key with no marker (that always includes Hosbah's
// own enrollment key, installed by internal/provisioning/connect.go with no marker at all).
const (
	KindSSHKeyInstall   = "sshkey.install"
	KindSSHKeyUninstall = "sshkey.uninstall"
)

// SSHKeyMarkerPrefix begins the comment line written immediately above a key this feature manages.
// The rest of the line is the key's fingerprint, so the exact block (marker + key) is unambiguous.
const SSHKeyMarkerPrefix = "# hosbah-ssh-key:"

// SSHKeyMarker is the exact marker comment for one fingerprint.
func SSHKeyMarker(fingerprint string) string { return SSHKeyMarkerPrefix + fingerprint }

// MaxAuthorizedKeysBytes bounds how large an authorized_keys file this agent will edit.
const MaxAuthorizedKeysBytes = 1 << 20

// SSHKeyInstallParams: sshkey.install.
type SSHKeyInstallParams struct {
	User        string `json:"user"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
}

// Validate checks sshkey.install's params, on both sides of the wire.
func (p SSHKeyInstallParams) Validate() error {
	if !ValidUnixUser(p.User) {
		return errors.New("bad user")
	}
	if _, _, err := ParseAuthorizedKey(p.PublicKey); err != nil {
		return fmt.Errorf("public_key: %w", err)
	}
	if p.Fingerprint == "" || p.Fingerprint != Fingerprint(p.PublicKey) {
		return errors.New("fingerprint does not match public_key")
	}
	return nil
}

// SSHKeyInstallResult: sshkey.install / sshkey.uninstall.
type SSHKeyInstallResult struct {
	Changed bool `json:"changed"` // false: it was already in the state asked for
}

// SSHKeyUninstallParams: sshkey.uninstall.
type SSHKeyUninstallParams struct {
	User        string `json:"user"`
	Fingerprint string `json:"fingerprint"`
}

// Validate checks sshkey.uninstall's params.
func (p SSHKeyUninstallParams) Validate() error {
	if !ValidUnixUser(p.User) {
		return errors.New("bad user")
	}
	if p.Fingerprint == "" || len(p.Fingerprint) > 128 {
		return errors.New("bad fingerprint")
	}
	return nil
}

// ValidUnixUser: a plain unix login name, the same charset the ssh_key_installs.unix_user CHECK uses.
func ValidUnixUser(u string) bool {
	if u == "" || len(u) > 32 {
		return false
	}
	for i, r := range u {
		switch {
		case r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9', r == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// ParseAuthorizedKey checks that s is one well-formed "type base64 [comment]" OpenSSH public key
// line and returns its type (ed25519 | rsa | ecdsa) and the decoded key blob. It never accepts a
// private key (an authorized_keys line never is one, but a pasted -----BEGIN... block is a common
// mistake worth a clear error) or anything whose declared type does not match the blob's own type
// prefix.
func ParseAuthorizedKey(s string) (keyType string, blob []byte, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil, errors.New("empty key")
	}
	if strings.HasPrefix(s, "-----BEGIN") {
		return "", nil, errors.New("that looks like a private key, not a public key")
	}
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return "", nil, errors.New("expected \"type base64-key [comment]\"")
	}
	algo, b64 := fields[0], fields[1]
	blob, derr := base64.StdEncoding.DecodeString(b64)
	if derr != nil || len(blob) < 8 {
		return "", nil, errors.New("bad base64 key data")
	}
	// The blob itself begins with a length-prefixed algorithm name; it must agree with the field.
	if int(be32(blob)) > len(blob)-4 || int(be32(blob)) <= 0 {
		return "", nil, errors.New("malformed key blob")
	}
	inner := string(blob[4 : 4+be32(blob)])
	if inner != algo {
		return "", nil, errors.New("key type does not match key data")
	}
	switch {
	case algo == "ssh-ed25519":
		return "ed25519", blob, nil
	case algo == "ssh-rsa":
		return "rsa", blob, nil
	case strings.HasPrefix(algo, "ecdsa-sha2-"):
		return "ecdsa", blob, nil
	default:
		return "", nil, fmt.Errorf("unsupported key type %q (want ed25519, rsa or ecdsa)", algo)
	}
}

func be32(b []byte) uint32 {
	if len(b) < 4 {
		return 0
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// Fingerprint is the OpenSSH-style "SHA256:base64-no-padding" fingerprint of a public key line
// ("type base64 [comment]"). Invalid input fingerprints as "" (callers validate with
// ParseAuthorizedKey first).
func Fingerprint(publicKey string) string {
	_, blob, err := ParseAuthorizedKey(publicKey)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}
