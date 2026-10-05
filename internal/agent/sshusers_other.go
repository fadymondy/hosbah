//go:build !linux

package agent

import "errors"

// SSH users are Linux-only, like sftp users (see sftp_other.go).

var (
	errSSHUserBox           = errors.New("the password could not be opened with this server's key")
	errSSHUserShortPassword = errors.New("password is too short")
	errSSHUserUnsupported   = errors.New("ssh users are only supported on a Linux agent")
)

func sshUserApply(oc *OpContext, username string, sudo bool, password []byte) (bool, int, error) {
	return false, 1, errSSHUserUnsupported
}

func sshUserDelete(oc *OpContext, username string) (bool, int, error) {
	return false, 1, errSSHUserUnsupported
}
