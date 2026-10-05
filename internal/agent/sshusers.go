package agent

import (
	"github.com/fadymondy/hosbah/internal/agentproto"
)

// sshuser.set / sshuser.remove: shell logins for people to reach the server over SSH. The real work
// (useradd, usermod, chpasswd, userdel) is Linux-only, in sshusers_linux.go.

func sshUserSet(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.SSHUserSetParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	var password []byte
	if p.PasswordBox != "" {
		pt, err := agentproto.OpenBox(oc.priv, agentproto.SSHUserPasswordContext(oc.ServerID, p.User), p.PasswordBox)
		if err != nil {
			return nil, 1, errSSHUserBox
		}
		defer zero(pt)
		if len(pt) < 12 {
			return nil, 2, errSSHUserShortPassword
		}
		password = pt
	}
	created, code, err := sshUserApply(oc, p.User, p.Sudo, password)
	if err != nil {
		return nil, code, err
	}
	return agentproto.SSHUserSetResult{Created: created}, 0, nil
}

func sshUserRemove(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.SSHUserRemoveParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	removed, code, err := sshUserDelete(oc, p.User)
	if err != nil {
		return nil, code, err
	}
	return agentproto.SSHUserRemoveResult{Removed: removed}, 0, nil
}
