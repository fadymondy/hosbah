//go:build linux

package agent

import (
	"os/exec"
	"syscall"
	"time"
)

// configureGroup makes cmd (a script.run's bash) the leader of its own process group, and arranges
// for the whole group to be killed when its context ends — so `apt-get`, a downloaded installer, or
// anything else the script started is gone at the timeout too, not just the bash that ran it.
func configureGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		go func(pid int) {
			time.Sleep(5 * time.Second)
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}(cmd.Process.Pid)
		return nil
	}
}
