package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
The first start after the rename from Cloudy.

A server enrolled before 2026-09-19 runs /usr/local/bin/cloudy-agent under cloudy-agent.service,
with its identity in /etc/cloudy and its work in /var/lib/cloudy. agent.update replaces that binary
in place and re-executes it, so this code first runs as cloudy-agent, in the old unit. Before it
connects, `run` does two things, both idempotent:

 1. MigrateStateDirs moves /etc/cloudy → /etc/hosbah and /var/lib/cloudy → /var/lib/hosbah and
    leaves a symlink at each old path (the gh login in /etc/cloudy/gh follows: GH_CONFIG_DIR is
    /etc/hosbah/gh now, and the old path still reaches it). A new path that exists means there is
    nothing to do.
 2. MigrateService, when this process runs inside cloudy-agent.service: installs this binary as
    /usr/local/bin/hosbah-agent (checking that it starts), writes and enables hosbah-agent.service,
    points /usr/local/bin/cloudy-agent at the new binary, disables cloudy-agent.service, and asks
    systemd — without waiting — to start hosbah-agent.service. That unit Conflicts= with the old
    one, so systemd itself stops this process and then starts the new unit: the handover does not
    depend on this process surviving it, and the two never run at the same time. Until the
    handover is accepted nothing is disabled that would leave the server without an agent at the
    next boot; if it is not accepted, the old unit is re-enabled and this process carries on.
*/

// ---- state directories --------------------------------------------------------------------------

type dirMove struct{ from, to string }

// stateDirMoves: /etc/cloudy and /var/lib/cloudy, to their new places.
var stateDirMoves = []dirMove{
	{agentproto.LegacyStateDir, agentproto.StateDir},
	{agentproto.LegacyWorkDir, agentproto.WorkDir},
}

// fsOps are the filesystem calls of the move (tests replace them).
type fsOps struct {
	lstat   func(string) (os.FileInfo, error)
	rename  func(oldpath, newpath string) error
	symlink func(oldname, newname string) error
}

var hostFS = fsOps{lstat: os.Lstat, rename: os.Rename, symlink: os.Symlink}

// MigrateStateDirs moves the pre-rename state directories, once. It returns a line per change.
func MigrateStateDirs() []string { return migrateDirs(hostFS, stateDirMoves) }

func migrateDirs(fs fsOps, moves []dirMove) []string {
	var notes []string
	for _, m := range moves {
		if n := migrateDir(fs, m.from, m.to); n != "" {
			notes = append(notes, n)
		}
	}
	return notes
}

// migrateDir moves from to to and leaves a link at from. Nothing happens when to exists (moved
// already, or a server enrolled after the rename) or from is not a real directory (never there, or
// already the link). A directory that cannot be moved (a mount point) stays, and to becomes a link
// to it, so the new paths work either way.
func migrateDir(fs fsOps, from, to string) string {
	if _, err := fs.lstat(to); err == nil {
		return ""
	}
	fi, err := fs.lstat(from)
	if err != nil || !fi.IsDir() { // Lstat: a symlink is never IsDir
		return ""
	}
	moveErr := fs.rename(from, to)
	if moveErr == nil {
		if err := fs.symlink(to, from); err == nil {
			return fmt.Sprintf("moved %s to %s; %s links to it", from, to, from)
		} else if err := fs.rename(to, from); err != nil { // put it back rather than break old paths
			return fmt.Sprintf("moved %s to %s, but could not link the old path or move it back: %v", from, to, err)
		}
		moveErr = errors.New("could not leave a link at the old path")
	}
	if err := fs.symlink(from, to); err != nil {
		return fmt.Sprintf("could not move %s to %s (%v) nor link it (%v): the agent's files are not where it looks", from, to, moveErr, err)
	}
	return fmt.Sprintf("kept %s (%v); %s links to it", from, moveErr, to)
}

// StatePath is the state file to load: path, or — when path is the default and only the
// pre-rename file exists (the directories could not be migrated) — that one.
func StatePath(path string) string {
	legacy := filepath.Join(agentproto.LegacyStateDir, filepath.Base(DefaultStatePath))
	if path != DefaultStatePath {
		return path
	}
	if _, err := os.Stat(path); err != nil {
		if _, err := os.Stat(legacy); err == nil {
			return legacy
		}
	}
	return path
}

// ---- the systemd unit and the binary -------------------------------------------------------------

// serviceHost is what MigrateService does to the host (tests replace it).
type serviceHost struct {
	self    string                 // the running binary, symlinks resolved
	systemd func() bool            // is systemd running?
	cgroup  func() (string, error) // /proc/self/cgroup
	run     func(name string, args ...string) error
	install func(src, dst string) error       // copy a binary into place, atomically, mode 0755
	write   func(path string, b []byte) error // a unit file
	link    func(target, path string) error   // replace path with a symlink to target, atomically
}

func realServiceHost() serviceHost {
	return serviceHost{
		self:    selfPath,
		systemd: systemdRunning,
		cgroup:  func() (string, error) { b, err := os.ReadFile("/proc/self/cgroup"); return string(b), err },
		run: func(name string, args ...string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //#nosec G204 -- fixed argv
			if err != nil {
				return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			}
			return nil
		},
		install: installBinary,
		write:   func(path string, b []byte) error { return os.WriteFile(path, b, 0o644) }, //#nosec G306 -- a unit file
		link:    replaceWithSymlink,
	}
}

// MigrateService moves an agent that runs as cloudy-agent.service to hosbah-agent.service (see the
// comment at the top). It reports whether systemd accepted the handover: the caller must then
// wait to be stopped, and call RevertService if that does not happen.
func MigrateService(logf func(string, ...any)) bool { return migrateService(realServiceHost(), logf) }

// RevertService makes the pre-rename unit the one that starts at boot again (a handover that
// systemd accepted but did not carry out).
func RevertService(logf func(string, ...any)) { revertService(realServiceHost(), logf) }

// inLegacyUnit: this process runs inside cloudy-agent.service.
func (h serviceHost) inLegacyUnit() bool {
	cg, err := h.cgroup()
	return err == nil && strings.Contains(cg, "/"+agentproto.LegacyAgentUnit+".service")
}

func migrateService(h serviceHost, logf func(string, ...any)) bool {
	if h.systemd == nil || !h.systemd() {
		// No systemd (the installer started the agent detached): only the binary gets its new name.
		// This process keeps running from the old path, which now leads to the new binary.
		if h.self == agentproto.LegacyAgentBin {
			if err := h.install(h.self, agentproto.AgentBin); err != nil {
				logf("could not install %s: %v", agentproto.AgentBin, err)
				return false
			}
			if err := h.link(agentproto.AgentBin, agentproto.LegacyAgentBin); err != nil {
				logf("could not point %s at %s: %v", agentproto.LegacyAgentBin, agentproto.AgentBin, err)
			}
		}
		return false
	}
	if !h.inLegacyUnit() {
		return false
	}
	logf("this server still runs the agent as %s.service; moving it to %s.service", agentproto.LegacyAgentUnit, agentproto.AgentUnit)
	if h.self != agentproto.AgentBin {
		if err := h.install(h.self, agentproto.AgentBin); err != nil {
			logf("could not install %s (staying on %s.service): %v", agentproto.AgentBin, agentproto.LegacyAgentUnit, err)
			return false
		}
	}
	if err := h.run(agentproto.AgentBin, "version"); err != nil {
		logf("%s does not start (staying on %s.service): %v", agentproto.AgentBin, agentproto.LegacyAgentUnit, err)
		return false
	}
	if err := h.write(agentproto.AgentUnitPath, []byte(agentproto.AgentUnitFile)); err != nil {
		logf("could not write %s (staying on %s.service): %v", agentproto.AgentUnitPath, agentproto.LegacyAgentUnit, err)
		return false
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", agentproto.AgentUnit + ".service"}} {
		if err := h.run("systemctl", args...); err != nil {
			logf("staying on %s.service: %v", agentproto.LegacyAgentUnit, err)
			return false
		}
	}
	// Whatever starts the old path from now on (the old unit, a script) runs the new binary.
	if err := h.link(agentproto.AgentBin, agentproto.LegacyAgentBin); err != nil {
		logf("could not point %s at %s (harmless): %v", agentproto.LegacyAgentBin, agentproto.AgentBin, err)
	}
	if err := h.run("systemctl", "disable", agentproto.LegacyAgentUnit+".service"); err != nil {
		logf("could not disable %s.service (harmless: the units conflict, only one runs): %v", agentproto.LegacyAgentUnit, err)
	}
	// --no-block: systemd queues the job and answers at once. The job stops this unit (Conflicts=),
	// then starts the new one.
	if err := h.run("systemctl", "--no-block", "start", agentproto.AgentUnit+".service"); err != nil {
		logf("systemd did not accept the handover (staying on %s.service): %v", agentproto.LegacyAgentUnit, err)
		revertService(h, logf)
		return false
	}
	logf("handed over to %s.service; systemd stops this process now", agentproto.AgentUnit)
	return true
}

func revertService(h serviceHost, logf func(string, ...any)) {
	if err := h.run("systemctl", "enable", agentproto.LegacyAgentUnit+".service"); err != nil {
		logf("could not re-enable %s.service: %v", agentproto.LegacyAgentUnit, err)
	}
	if err := h.run("systemctl", "disable", agentproto.AgentUnit+".service"); err != nil {
		logf("could not disable %s.service: %v", agentproto.AgentUnit, err)
	}
}

// installBinary copies src to dst through a temporary file next to dst, so dst is never half
// written (and a running dst keeps its old inode).
func installBinary(src, dst string) error {
	in, err := os.Open(src) //#nosec G304 -- our own binary
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".install"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755) //#nosec G302 G304 -- an executable
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil { //#nosec G302 -- an executable
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// replaceWithSymlink makes path a symlink to target (a relative one when they share a directory),
// replacing whatever is there in one rename.
func replaceWithSymlink(target, path string) error {
	if filepath.Dir(target) == filepath.Dir(path) {
		target = filepath.Base(target)
	}
	tmp := path + ".link"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
