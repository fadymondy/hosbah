//go:build linux

package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// unixPTY adapts creack/pty's *os.File plus the child command to terminalPTY, and kills the whole
// process group on Close so nothing a shell started is left running behind it.
type unixPTY struct {
	f   *os.File
	cmd *exec.Cmd
}

func (u *unixPTY) Read(p []byte) (int, error)  { return u.f.Read(p) }
func (u *unixPTY) Write(p []byte) (int, error) { return u.f.Write(p) }

func (u *unixPTY) Resize(cols, rows int) error {
	return pty.Setsize(u.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}) //#nosec G115 -- bounded by ValidTermSize
}

func (u *unixPTY) Close() error {
	if u.cmd != nil && u.cmd.Process != nil {
		// Setsid (below) made the child its own process group leader: -pid signals the whole group,
		// so a shell's own children (an editor, a long-running tool) die with the session too.
		_ = syscall.Kill(-u.cmd.Process.Pid, syscall.SIGHUP)
		go func() {
			time.Sleep(2 * time.Second)
			_ = syscall.Kill(-u.cmd.Process.Pid, syscall.SIGKILL)
		}()
	}
	err := u.f.Close()
	if u.cmd != nil {
		_ = u.cmd.Wait()
	}
	return err
}

// loginShell: bash -l if present (a login shell reads the image/host's profile), else sh.
func loginShell() []string {
	for _, sh := range []string{"/bin/bash", "/usr/bin/bash"} {
		if _, err := os.Stat(sh); err == nil {
			return []string{sh, "-l"}
		}
	}
	return []string{"/bin/sh", "-l"}
}

// spawnTerminal opens a PTY running a login shell on the host, or `docker exec -it` into an app's
// live container for an "app:<id>/<process>" target.
func spawnTerminal(ctx context.Context, p agentproto.TerminalOpenParams) (terminalPTY, error) {
	var argv []string
	if p.Target == agentproto.TerminalTargetHost {
		argv = append([]string{}, loginShell()...)
	} else {
		appID, process, ok := agentproto.ParseTerminalAppTarget(p.Target)
		if !ok {
			return nil, errors.New("bad terminal target")
		}
		name, err := liveProcessContainer(ctx, appID, p.DeploymentID, process)
		if err != nil {
			return nil, err
		}
		// No shell run on the host: the argv after "exec -it <name>" is fixed, never built from a
		// string. Inside the container, try bash, fall back to sh — most images have one or the
		// other but not always both.
		argv = []string{"docker", "exec", "-it", name, "sh", "-c", "exec bash -l || exec sh -l"}
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //#nosec G204 -- a fixed login shell, or docker exec into a resolved container name
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(p.Cols), Rows: uint16(p.Rows)}) //#nosec G115 -- bounded by Validate
	if err != nil {
		return nil, err
	}
	return &unixPTY{f: f, cmd: cmd}, nil
}

// liveProcessContainer finds the running container of an app's live release for one process
// ("web" or "worker:<name>"), under the current or the pre-rename label set.
func liveProcessContainer(ctx context.Context, appID, deploymentID, process string) (string, error) {
	listed := false
	for _, l := range labelSets {
		out, err := exec.CommandContext(ctx, "docker", "ps", //#nosec G204 -- fixed argv, ids already validated
			"--filter", "label="+l(agentproto.LabelApp)+"="+appID,
			"--filter", "label="+l(agentproto.LabelProcess)+"="+process,
			"--filter", "label="+l(agentproto.LabelDeployment)+"="+deploymentID,
			"--format", "{{.Names}}").Output()
		if err != nil {
			continue
		}
		listed = true
		for _, name := range strings.Split(string(out), "\n") {
			if name = strings.TrimSpace(name); name != "" && !strings.HasPrefix(name, "-") {
				return name, nil
			}
		}
	}
	if !listed {
		return "", errors.New("could not list the app's containers")
	}
	return "", errors.New("that process has no running container on this server — deploy or restart the app first")
}
