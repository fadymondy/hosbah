package agentproto

import "errors"

// sshuser.set / sshuser.remove: a shell login on the server for a person to reach it over SSH (the
// SSH page). sshuser.set creates the user (home directory, /bin/bash) or updates one Hosbah made
// before: its sudo membership and, when PasswordBox is set, its password. Keys are not part of this
// op; they go on with sshkey.install for the same user. The agent marks every user it creates with
// SSHUserGECOS and refuses to edit or remove any user without that mark, so root, system users and
// logins someone made by hand are never touched.
const (
	KindSSHUserSet    = "sshuser.set"
	KindSSHUserRemove = "sshuser.remove"
)

// SSHUserGECOS is the comment field of every login sshuser.set creates.
const SSHUserGECOS = "hosbah-ssh-user"

// SSHUserPasswordContext binds a sealed SSH-user password to its server and login.
func SSHUserPasswordContext(serverID, user string) string { return "sshuser:" + serverID + ":" + user }

// reservedSSHUsers can never be created or removed through sshuser.*, whatever the GECOS says.
var reservedSSHUsers = map[string]bool{
	"root": true, "daemon": true, "bin": true, "sys": true, "sync": true, "games": true, "man": true, "lp": true,
	"mail": true, "news": true, "uucp": true, "proxy": true, "www-data": true, "backup": true, "list": true,
	"irc": true, "nobody": true, "systemd-network": true, "systemd-resolve": true, "messagebus": true,
	"sshd": true, "syslog": true, "postgres": true, "mysql": true, "redis": true, "caddy": true, "docker": true,
	"hosbah": true, "cloudy": true, "ubuntu": true, "debian": true, "admin": true,
}

// ValidSSHUser: a plain unix login that is not reserved and not an SFTP login's prefix.
func ValidSSHUser(u string) bool {
	return ValidUnixUser(u) && !reservedSSHUsers[u]
}

// SSHUserSetParams: sshuser.set. PasswordBox, when set, is SealBox(agentKey,
// SSHUserPasswordContext(serverID, User), password).
type SSHUserSetParams struct {
	User        string `json:"user"`
	Sudo        bool   `json:"sudo"`
	PasswordBox string `json:"password_box,omitempty"`
}

// Validate checks sshuser.set's params, on both sides of the wire.
func (p SSHUserSetParams) Validate() error {
	if !ValidSSHUser(p.User) {
		return errors.New("bad or reserved user name")
	}
	if len(p.PasswordBox) > 4096 {
		return errors.New("password_box is too large")
	}
	return nil
}

// SSHUserSetResult: sshuser.set.
type SSHUserSetResult struct {
	Created bool `json:"created"` // false: the user already existed (and was Hosbah's)
}

// SSHUserRemoveParams: sshuser.remove.
type SSHUserRemoveParams struct {
	User string `json:"user"`
}

// Validate checks sshuser.remove's params.
func (p SSHUserRemoveParams) Validate() error {
	if !ValidSSHUser(p.User) {
		return errors.New("bad or reserved user name")
	}
	return nil
}

// SSHUserRemoveResult: sshuser.remove.
type SSHUserRemoveResult struct {
	Removed bool `json:"removed"` // false: there was no such user
}
