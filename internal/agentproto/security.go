package agentproto

import (
	"errors"
)

// passwd.set and deploykey.generate (MH-578 C9 / C11).
//
// passwd.set resets a unix user's password with chpasswd. The plaintext password is generated (or,
// optionally, chosen) by the control plane, sealed to the target agent's own key the same way a
// deploy env value is (internal/agentproto/box.go), carried in operations.params as ciphertext
// only that agent can open, and never persisted anywhere in the clear — not in the control plane's
// database, not in the operation's result. The caller sees it exactly once, in the REST/MCP
// response that triggered the reset.
//
// deploykey.generate creates one ed25519 keypair on the server for outbound git access (a "deploy
// key"): the private half stays under /etc/hosbah on the server (never leaves it), a known_hosts
// entry for github.com is added next to it, and only the public half is returned to the control
// plane to show the user or hand to GitHub.
const (
	KindPasswdSet        = "passwd.set"
	KindDeployKeyGenerate = "deploykey.generate"
)

// PasswordContext binds a sealed password box to the server it was generated for, so a box copied
// into another operation (or another server's queue) does not open.
func PasswordContext(serverID string) string { return "passwd:" + serverID }

// PasswdSetParams: passwd.set. PasswordBox is SealBox(agentKey, PasswordContext(serverID), password).
type PasswdSetParams struct {
	User        string `json:"user"`
	PasswordBox string `json:"password_box"`
}

// Validate checks passwd.set's params, on both sides of the wire.
func (p PasswdSetParams) Validate() error {
	if !ValidUnixUser(p.User) {
		return errors.New("bad user")
	}
	if p.PasswordBox == "" {
		return errors.New("missing password_box")
	}
	return nil
}

// PasswdSetResult: passwd.set. Deliberately holds nothing about the password itself.
type PasswdSetResult struct {
	Changed bool `json:"changed"`
}

// DeployKeyGenerateParams: deploykey.generate. Empty — always root's own key, one per server.
type DeployKeyGenerateParams struct{}

// Validate is a no-op; kept for symmetry with every other op's params.
func (DeployKeyGenerateParams) Validate() error { return nil }

// DeployKeyGenerateResult: deploykey.generate. Only ever the public half.
type DeployKeyGenerateResult struct {
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	Changed     bool   `json:"changed"` // false: a deploy key already existed and was reused
}
