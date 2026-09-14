//go:build linux

package agent

import (
	"errors"
	"os"
	"syscall"
)

// reexec replaces this process with the binary now at selfPath (same pid, so systemd keeps
// supervising it). Under systemd a plain exit would also do — Restart=always brings the new
// binary up — but re-exec works without systemd too.
func reexec() error {
	if selfPath == "" {
		return errors.New("cannot locate the agent binary")
	}
	return syscall.Exec(selfPath, os.Args, os.Environ()) //#nosec G204 -- our own, just-verified binary
}
