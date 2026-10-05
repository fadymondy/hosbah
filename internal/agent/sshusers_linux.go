//go:build linux

package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

var (
	errSSHUserBox           = errors.New("the password could not be opened with this server's key")
	errSSHUserShortPassword = errors.New("password is too short")
)

// sshUserManaged reports whether username exists and, if it does, refuses any login whose comment
// field is not Hosbah's mark: those belong to the server (or to a person) and are never edited.
func sshUserManaged(username string) (exists bool, err error) {
	out, gerr := exec.Command("getent", "passwd", username).Output() //#nosec G204 -- fixed argv, validated name
	if gerr != nil {
		return false, nil // getent exits 2 for "no such user"
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(fields) < 5 || strings.Split(fields[4], ",")[0] != agentproto.SSHUserGECOS {
		return true, fmt.Errorf("a user named %s already exists on this server and was not created by Hosbah; pick another username", username)
	}
	return true, nil
}

// sudoGroup is the admin group of this distribution: sudo (Debian, Ubuntu) or wheel (RHEL family).
func sudoGroup() (string, error) {
	for _, g := range []string{"sudo", "wheel"} {
		if exec.Command("getent", "group", g).Run() == nil { //#nosec G204 -- fixed argv
			return g, nil
		}
	}
	return "", errors.New("this server has neither a sudo nor a wheel group (is sudo installed?)")
}

func sshUserApply(oc *OpContext, username string, sudo bool, password []byte) (bool, int, error) {
	exists, err := sshUserManaged(username)
	if err != nil {
		return false, 2, err
	}
	if !exists {
		if code, err := oc.Run("useradd", "--create-home", "--shell", "/bin/bash", "--comment", agentproto.SSHUserGECOS, username); err != nil {
			return false, code, err
		}
	}
	group, gerr := sudoGroup()
	switch {
	case sudo && gerr != nil:
		return !exists, 1, gerr
	case sudo:
		if code, err := oc.Run("usermod", "--append", "--groups", group, username); err != nil {
			return !exists, code, err
		}
	case gerr == nil:
		// Not an admin (any more): gpasswd fails when the user was never in the group, which is fine.
		_ = exec.Command("gpasswd", "--delete", username, group).Run() //#nosec G204 -- fixed argv, validated name
	}
	if password != nil {
		cmd := commandContext(oc, "chpasswd")
		cmd.Stdin = bytes.NewReader(append(append([]byte(username+":"), password...), '\n'))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return !exists, 1, fmt.Errorf("chpasswd: %w: %s", err, stderr.String())
		}
		oc.Info("password set for %s", username)
	}
	oc.Info("ssh user %s ready (sudo: %t)", username, sudo)
	return !exists, 0, nil
}

func sshUserDelete(oc *OpContext, username string) (bool, int, error) {
	exists, err := sshUserManaged(username)
	if err != nil {
		return false, 2, err
	}
	if !exists {
		return false, 0, nil
	}
	// A login still in use keeps userdel from removing it: end its sessions first.
	_ = exec.Command("pkill", "--uid", username).Run() //#nosec G204 -- fixed argv, validated name
	if code, err := oc.Run("userdel", "--remove", username); err != nil {
		return false, code, err
	}
	oc.Info("ssh user %s removed", username)
	return true, 0, nil
}
