//go:build linux

package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// sftpDropIn is the one file this feature ever writes under /etc/ssh/sshd_config.d — Debian's and
// RHEL's stock sshd_config both `Include`/glob that directory, so this alone is enough; the rest
// of sshd_config (and any drop-in an operator wrote themselves) is never touched. Two Match Group
// blocks cover every sftp user this control plane manages, whatever their read_only flag — no
// per-user Match block, so adding a user never rewrites the file's shape, only useradd/usermod.
const sftpDropIn = "/etc/ssh/sshd_config.d/hosbah-sftp.conf"

// sftpGroupRW / sftpGroupRO: which Match block a user's group membership puts it in.
const (
	sftpGroupRW = "hosbah-sftp"
	sftpGroupRO = "hosbah-sftp-ro"
)

func sftpEnsureSupport(oc *OpContext) (int, error) {
	if _, err := exec.LookPath("sshd"); err != nil {
		return 1, errors.New("sshd is not installed on this server")
	}
	return 0, nil
}

func renderSFTPDropIn() string {
	return fmt.Sprintf(`# Managed by Hosbah (internal/agent/sftp.go) — do not edit by hand, it is overwritten on the
# next SFTP user change. ChrootDirectory %%h: each user's own home (set to its root dir at
# creation) is its chroot, so this one file covers every SFTP user regardless of which directory
# it is rooted at.
Match Group %s
    ChrootDirectory %%h
    ForceCommand internal-sftp
    AllowTcpForwarding no
    AllowAgentForwarding no
    X11Forwarding no
    PermitTunnel no

Match Group %s
    ChrootDirectory %%h
    ForceCommand internal-sftp -R
    AllowTcpForwarding no
    AllowAgentForwarding no
    X11Forwarding no
    PermitTunnel no
`, sftpGroupRW, sftpGroupRO)
}

// sftpWriteDropIn writes the drop-in only if it changed, validates the result with `sshd -t`, and
// rolls back to what was there before (or removes the file, if there was nothing before) the
// moment that check fails — the running sshd, and any existing SSH session, is never at risk.
func sftpWriteDropIn(oc *OpContext) (int, error) {
	want := renderSFTPDropIn()
	prev, hadPrev := "", false
	if b, err := os.ReadFile(sftpDropIn); err == nil { //#nosec G304 -- fixed, managed path
		prev, hadPrev = string(b), true
	}
	if hadPrev && prev == want {
		return 0, nil
	}
	if err := os.MkdirAll(filepath.Dir(sftpDropIn), 0o755); err != nil {
		return 1, err
	}
	if err := os.WriteFile(sftpDropIn, []byte(want), 0o644); err != nil { //#nosec G306 -- sshd config, world-readable by design
		return 1, err
	}
	if code, err := oc.Run("sshd", "-t"); err != nil {
		if hadPrev {
			_ = os.WriteFile(sftpDropIn, []byte(prev), 0o644)
		} else {
			_ = os.Remove(sftpDropIn)
		}
		return code, fmt.Errorf("the sshd configuration was rejected and rolled back: %w", err)
	}
	return sftpReloadSSHD(oc)
}

// sftpReloadSSHD reloads whichever of the two conventional unit names (Debian/Ubuntu: ssh,
// RHEL/derivatives: sshd) is active. Existing sessions are unaffected by a reload; only new
// connections see the change. If neither unit is found running under systemd (an unusual host),
// the file is left validated and in place — the next sshd restart, whenever that is, picks it up.
func sftpReloadSSHD(oc *OpContext) (int, error) {
	for _, unit := range []string{"ssh", "sshd"} {
		if exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil { //#nosec G204 -- fixed argv
			return oc.Run("systemctl", "reload", unit)
		}
	}
	oc.Info("no active ssh/sshd systemd unit found — the drop-in is valid and in place, applied on the next sshd restart")
	return 0, nil
}

// sftpEnsureRootDir makes sure real exists as a directory the agent can chroot into. A directory
// the agent creates itself is made root:root 0755. An existing one is left exactly as it is
// (never chowned or chmodded out from under an app or an operator's own files) and only verified.
func sftpEnsureRootDir(oc *OpContext, real string) (int, error) {
	info, err := os.Lstat(real)
	if err != nil {
		if !os.IsNotExist(err) {
			return 1, err
		}
		if err := os.MkdirAll(real, 0o755); err != nil {
			return 1, err
		}
		if err := os.Chown(real, 0, 0); err != nil { //#nosec G302 -- root:root is exactly the chroot requirement
			oc.Info("could not chown %s to root: %v", real, err)
		}
		info, err = os.Lstat(real)
		if err != nil {
			return 1, err
		}
	}
	if !info.IsDir() {
		return 2, fmt.Errorf("%s is not a directory", real)
	}
	if err := sftpVerifyChrootOwnership(real); err != nil {
		return 2, err
	}
	return 0, nil
}

// sftpVerifyChrootOwnership walks real up to "/": OpenSSH refuses ChrootDirectory unless every
// component (not just the leaf) is root-owned and not group- or other-writable. This only reports
// the problem — fixing an existing ancestor's ownership is an operator decision, not the agent's
// to make silently on a directory it did not create.
func sftpVerifyChrootOwnership(real string) error {
	dir := real
	for {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Uid != 0 {
			return fmt.Errorf("%s is not owned by root — OpenSSH refuses to chroot into a directory (or any of its parents) "+
				"that root does not own; chown it to root first", dir)
		}
		if info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("%s is writable by group or other — OpenSSH refuses to chroot there; chmod g-w,o-w first", dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func sftpGroupOf(readOnly bool) string {
	if readOnly {
		return sftpGroupRO
	}
	return sftpGroupRW
}

func sftpEnsureGroups(oc *OpContext) (int, error) {
	for _, g := range []string{sftpGroupRW, sftpGroupRO} {
		if exec.Command("getent", "group", g).Run() == nil { //#nosec G204 -- fixed argv
			continue
		}
		if code, err := oc.Run("groupadd", "--system", g); err != nil {
			return code, err
		}
	}
	return 0, nil
}

// sftpUpsertSystemUser creates or updates the login itself: no home directory of its own making
// (home IS the chroot root, already prepared), no shell (nologin — ForceCommand is what actually
// runs, but a stray password-only login attempt outside sftp still gets nothing to execute).
func sftpUpsertSystemUser(oc *OpContext, username, home string, readOnly bool) (int, error) {
	group := sftpGroupOf(readOnly)
	if exec.Command("id", "-u", username).Run() != nil { //#nosec G204 -- fixed argv
		return oc.Run("useradd", "--system", "--no-create-home", "--home-dir", home, "--shell", "/usr/sbin/nologin", "--gid", group, username)
	}
	if err := sftpManaged(username); err != nil {
		return 2, err
	}
	return oc.Run("usermod", "--home", home, "--shell", "/usr/sbin/nologin", "--gid", group, username)
}

// sftpManaged refuses to touch an existing login whose primary group is not one of the two
// hosbah-sftp groups: a username like root, ubuntu or www-data passes the name check but belongs
// to the server, and usermod (nologin, new home) or userdel on it would lock people out.
func sftpManaged(username string) error {
	out, err := exec.Command("id", "-gn", username).Output() //#nosec G204 -- fixed argv, validated name
	if err != nil {
		return fmt.Errorf("could not read the groups of %s: %w", username, err)
	}
	if g := strings.TrimSpace(string(out)); g != sftpGroupRW && g != sftpGroupRO {
		return fmt.Errorf("a system user named %s already exists on this server and is not an SFTP user Hosbah manages; pick another username", username)
	}
	return nil
}

func sftpRemoveSystemUser(oc *OpContext, username string) (int, error) {
	if exec.Command("id", "-u", username).Run() != nil { //#nosec G204 -- fixed argv
		return 0, nil // already gone: delete is idempotent
	}
	if err := sftpManaged(username); err != nil {
		return 2, err
	}
	return oc.Run("userdel", username)
}

// sftpWriteAuthorizedKeys: sshd resolves ~/.ssh/authorized_keys against the user's real home
// (from /etc/passwd) BEFORE it chroots — which here is exactly the chroot root, so the same file
// also appears at /.ssh/authorized_keys once inside. Root-owned and not group/other-writable, the
// same StrictModes rule sshd applies to every authorized_keys file. sshd opens the file with the
// user's own privileges, so the directory is 0755 and the file 0644: readable, but only root writes.
func sftpWriteAuthorizedKeys(oc *OpContext, home string, keys []string) (int, error) {
	sshDir := filepath.Join(home, ".ssh")
	akPath := filepath.Join(sshDir, "authorized_keys")
	if len(keys) == 0 {
		_ = os.Remove(akPath) // password-only: no stale keys left granting access
		return 0, nil
	}
	if err := os.MkdirAll(sshDir, 0o755); err != nil {
		return 1, err
	}
	if err := os.Chown(sshDir, 0, 0); err != nil { //#nosec G302 -- StrictModes requires root ownership
		oc.Info("could not chown %s: %v", sshDir, err)
	}
	if err := os.Chmod(sshDir, 0o755); err != nil {
		oc.Info("could not chmod %s: %v", sshDir, err)
	}
	content := strings.Join(keys, "\n") + "\n"
	if err := os.WriteFile(akPath, []byte(content), 0o644); err != nil { //#nosec G306 -- public keys; sshd reads the file as the user, so it must be readable
		return 1, err
	}
	if err := os.Chmod(akPath, 0o644); err != nil {
		oc.Info("could not chmod %s: %v", akPath, err)
	}
	if err := os.Chown(akPath, 0, 0); err != nil {
		oc.Info("could not chown %s: %v", akPath, err)
	}
	return 0, nil
}

// sftpSetPassword feeds chpasswd its "user:password" line over stdin — never an argv, never an
// environment variable, never a file — so the plaintext never appears anywhere this operation's
// own logging could capture it.
func sftpSetPassword(oc *OpContext, username, password string) (int, error) {
	return oc.RunStdin(username+":"+password+"\n", "chpasswd")
}

// sftpVerifyEffective asks sshd itself (sshd -T with the user's connection spec) what it would
// apply to this login. If the server's sshd_config does not Include sshd_config.d, or an earlier
// Match wins, the drop-in is silently ignored and the user would get plain, unchrooted sftp to the
// whole filesystem, so that is refused.
func sftpVerifyEffective(oc *OpContext, username string, readOnly bool) error {
	out, err := exec.Command("sshd", "-T", "-C", "user="+username+",host=localhost,addr=127.0.0.1").Output() //#nosec G204 -- validated name
	if err != nil {
		return fmt.Errorf("could not check the effective sshd settings: %w", err)
	}
	return sftpCheckEffective(string(out), readOnly)
}

func sftpCheckEffective(cfg string, readOnly bool) error {
	chroot, force := "", ""
	for _, line := range strings.Split(cfg, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch strings.ToLower(k) {
		case "chrootdirectory":
			chroot = strings.TrimSpace(v)
		case "forcecommand":
			force = strings.TrimSpace(v)
		}
	}
	want := "internal-sftp"
	if readOnly {
		want = "internal-sftp -R"
	}
	if chroot == "" || chroot == "none" || force != want {
		return errors.New("sshd does not apply Hosbah's SFTP rule to this user (does sshd_config Include /etc/ssh/sshd_config.d/*.conf before any Match block?); the user was not created")
	}
	return nil
}

func sftpUserExists(username string) bool {
	return exec.Command("id", "-u", username).Run() == nil //#nosec G204 -- validated name
}
