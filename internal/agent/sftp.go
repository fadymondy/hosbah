package agent

import (
	"errors"
	"fmt"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
sftp.user: a chrooted, SFTP-only system account, so a team can hand out upload/download access
without a shell and without root. The heavy lifting (useradd/usermod/userdel, the sshd_config.d
drop-in, `sshd -t` and its rollback) is Linux-only — see sftp_linux.go and sftp_other.go (the
same os-specific split as files_linux.go/files_other.go) — this file only decodes and orders the
steps, so both platforms share one code path for everything that is not itself a syscall.
*/

type sftpHost struct{}

func newSFTPHost() *sftpHost { return &sftpHost{} }

func (h *sftpHost) apply(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.SFTPUserParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if p.Action == "delete" {
		return h.remove(oc, p)
	}
	return h.upsert(oc, p)
}

// upsert creates or updates the group, the chroot directory, the sshd_config.d drop-in, the
// system user itself, its authorized_keys and (if given) its password — in that order, so a
// failure partway through never leaves a user pointed at a directory sshd would refuse to chroot
// into. Idempotent: re-running it (a retry, or update with nothing actually different) succeeds.
func (h *sftpHost) upsert(oc *OpContext, p agentproto.SFTPUserParams) (any, int, error) {
	if code, err := sftpEnsureSupport(oc); err != nil {
		return nil, code, err
	}
	fh := newFSHost()
	real, err := fh.resolve(p.RootDir)
	if err != nil {
		return nil, 2, fmt.Errorf("root dir: %w", err)
	}
	if code, err := sftpEnsureGroups(oc); err != nil {
		return nil, code, err
	}
	if code, err := sftpEnsureRootDir(oc, real); err != nil {
		return nil, code, err
	}
	if code, err := sftpWriteDropIn(oc); err != nil {
		return nil, code, err
	}
	existed := sftpUserExists(p.Username)
	if code, err := sftpUpsertSystemUser(oc, p.Username, real, p.ReadOnly); err != nil {
		return nil, code, err
	}
	if err := sftpVerifyEffective(oc, p.Username, p.ReadOnly); err != nil {
		if !existed {
			_, _ = sftpRemoveSystemUser(oc, p.Username)
		}
		return nil, 2, err
	}
	if code, err := sftpWriteAuthorizedKeys(oc, real, p.PublicKeys); err != nil {
		return nil, code, err
	}
	if p.PasswordSecret != "" {
		env, err := oc.OpenEnv(agentproto.SFTPSecretContext(p.ID), p.PasswordSecret)
		if err != nil {
			return nil, 2, err
		}
		pass := env["password"]
		if pass == "" {
			return nil, 2, errors.New("the sealed password was empty")
		}
		if code, err := sftpSetPassword(oc, p.Username, pass); err != nil {
			return nil, code, err
		}
	}
	return agentproto.SFTPUserResult{Username: p.Username}, 0, nil
}

// remove drops the system account and its sshd group membership. RootDir is never touched — it
// may be an app's own directory, or shared with other sftp users.
func (h *sftpHost) remove(oc *OpContext, p agentproto.SFTPUserParams) (any, int, error) {
	if code, err := sftpEnsureSupport(oc); err != nil {
		return nil, code, err
	}
	if code, err := sftpRemoveSystemUser(oc, p.Username); err != nil {
		return nil, code, err
	}
	return agentproto.SFTPUserResult{Username: p.Username}, 0, nil
}
