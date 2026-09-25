package agentproto

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

/*
SFTP users: a chrooted system account on a server, reachable only over SFTP (OpenSSH's
internal-sftp, never a shell), so a team can hand out upload/download access without sharing
root or any interactive login. The agent manages a hosbah-sftp system group, one
/etc/ssh/sshd_config.d drop-in with a single `Match Group hosbah-sftp` block (ChrootDirectory,
ForceCommand internal-sftp), and the users themselves — never touching the rest of sshd_config,
so an operator's own SSH access is never at risk. Every change is validated with `sshd -t`
before the running sshd is asked to reload, and rolled back if that check fails.
*/

// KindSFTPUser is the one op kind for the whole lifecycle (Action decides what happens), the same
// shape as db.user.
const KindSFTPUser = "sftp.user"

// ValidSFTPUserAction: the actions sftp.user supports.
func ValidSFTPUserAction(s string) bool {
	switch s {
	case "create", "update", "delete":
		return true
	}
	return false
}

// MaxSFTPPublicKeys bounds how many keys one user can carry (an authorized_keys file, not an
// unbounded list).
const MaxSFTPPublicKeys = 16

// sftpUsernameRE mirrors sftp_users_username_check: a plain, portable Linux login name.
var sftpUsernameRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// SFTPSecretContext binds a box to the sftp user it seals a login password for.
func SFTPSecretContext(id string) string { return "sftp:" + id }

// SFTPUserParams: sftp.user — create, update or delete a chrooted SFTP-only system user.
//
//   - create / update: Username, RootDir and at least one of PublicKeys or PasswordSecret are
//     required. RootDir must already exist as (or become) a root-owned directory: OpenSSH refuses
//     to chroot into a directory writable by anyone but root, so the agent creates it 0755 root:root
//     when missing and refuses when it exists with looser ownership.
//   - delete: only Username is required; the system user, its home and the sshd_config.d rule for
//     it are removed (RootDir itself is left alone — it may be an app's directory, or shared).
//
// PasswordSecret, when set, is an env box (context SFTPSecretContext(ID)) carrying
// {"password": "…"}; the plaintext never appears in params or on an argv.
type SFTPUserParams struct {
	ID             string   `json:"id"`
	Action         string   `json:"action"`
	Username       string   `json:"username"`
	PublicKeys     []string `json:"public_keys,omitempty"`
	PasswordSecret string   `json:"password_secret,omitempty"`
	RootDir        string   `json:"root_dir,omitempty"`
	ReadOnly       bool     `json:"read_only,omitempty"`
}

// Validate checks sftp.user on both sides of the wire.
func (p SFTPUserParams) Validate() error {
	if !ValidID(p.ID) {
		return errors.New("bad sftp user id")
	}
	if !ValidSFTPUserAction(p.Action) {
		return fmt.Errorf("bad action %q", p.Action)
	}
	if !sftpUsernameRE.MatchString(p.Username) {
		return fmt.Errorf("bad username %q", p.Username)
	}
	if p.Action == "delete" {
		return nil
	}
	if err := checkFSPath(p.RootDir, true); err != nil {
		return fmt.Errorf("root dir: %w", err)
	}
	if len(p.PublicKeys) > MaxSFTPPublicKeys {
		return errors.New("too many public keys (max 16)")
	}
	for _, k := range p.PublicKeys {
		if err := validSSHPublicKey(k); err != nil {
			return err
		}
	}
	if len(p.PublicKeys) == 0 && p.PasswordSecret == "" {
		return errors.New("an sftp user needs at least one public key or a password")
	}
	return nil
}

// sshKeyTypes are the OpenSSH public key algorithms accepted in an authorized_keys line.
var sshKeyTypes = []string{"ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com"}

// validSSHPublicKey is a light, defensive check — not a full parse of the base64 body: no
// newline (an authorized_keys injection of a second line), a known key type, and a sane length.
func validSSHPublicKey(k string) error {
	k = strings.TrimSpace(k)
	if k == "" || strings.ContainsAny(k, "\n\r") {
		return errors.New("a public key must be one line")
	}
	if len(k) > 4096 {
		return errors.New("a public key is too long")
	}
	fields := strings.Fields(k)
	if len(fields) < 2 {
		return errors.New("that does not look like an OpenSSH public key")
	}
	ok := false
	for _, t := range sshKeyTypes {
		if fields[0] == t {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("unsupported key type %q", fields[0])
	}
	return nil
}

// SFTPUserResult: sftp.user.
type SFTPUserResult struct {
	Username string `json:"username"`
}
