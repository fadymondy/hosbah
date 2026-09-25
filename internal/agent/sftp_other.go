//go:build !linux

package agent

import "errors"

// sftp.user's real work (useradd/usermod/userdel, sshd_config.d, sshd -t) is Linux-only: the
// agent only ever ships for Linux servers. These stubs exist so the package still builds and its
// portable tests still run on a developer's own machine, whatever that machine is — the same
// reason files_other.go exists for ownerOf/preserveOwner.

var errSFTPUnsupported = errors.New("sftp users are only supported on a Linux agent")

func sftpEnsureSupport(oc *OpContext) (int, error) { return 1, errSFTPUnsupported }

func sftpEnsureGroups(oc *OpContext) (int, error) { return 1, errSFTPUnsupported }

func sftpEnsureRootDir(oc *OpContext, real string) (int, error) { return 1, errSFTPUnsupported }

func sftpWriteDropIn(oc *OpContext) (int, error) { return 1, errSFTPUnsupported }

func sftpUpsertSystemUser(oc *OpContext, username, home string, readOnly bool) (int, error) {
	return 1, errSFTPUnsupported
}

func sftpWriteAuthorizedKeys(oc *OpContext, home string, keys []string) (int, error) {
	return 1, errSFTPUnsupported
}

func sftpSetPassword(oc *OpContext, username, password string) (int, error) {
	return 1, errSFTPUnsupported
}

func sftpRemoveSystemUser(oc *OpContext, username string) (int, error) { return 1, errSFTPUnsupported }

func sftpVerifyEffective(oc *OpContext, username string, readOnly bool) error {
	return errSFTPUnsupported
}

func sftpUserExists(username string) bool { return false }
