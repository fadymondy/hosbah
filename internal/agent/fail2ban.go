package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// writeFileAtomic writes b to path via a temp file in the same directory, then renames it into
// place, so a reader (or fail2ban itself, reloading its config) never sees a half-written file.
func writeFileAtomic(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

/*
MH-580 J2: fail2ban with one jail this feature owns, sshd, in its own drop-in file
(/etc/fail2ban/jail.d/hosbah-sshd.conf) — never touching jail.local or any other jail a server
operator may already have configured. Installing the package follows the same apt/dnf/apk
detection as pkg.go (extended here with apk, which pkg.go's Debian/RHEL split does not cover:
fail2ban's package name is "fail2ban" on all three).
*/

// fail2banJailWritePath is where the sshd jail's drop-in config is written; overridable in tests.
var fail2banJailWritePath = "/etc/fail2ban/jail.d/hosbah-sshd.conf"

// fail2banPkgManager reports which package manager this box uses, purely from /etc/os-release —
// apk (Alpine), dnf (the RHEL family) or apt (Debian family, the default).
func fail2banPkgManager() string {
	id := readOSReleaseID()
	switch {
	case id == "alpine":
		return "apk"
	case agentproto.RHELFamily(id):
		return "dnf"
	default:
		return "apt"
	}
}

// renderFail2banJail renders the sshd jail's drop-in config: the pure, unit-tested part.
func renderFail2banJail(p agentproto.Fail2banInstallParams) string {
	enabled := "false"
	if p.Enabled {
		enabled = "true"
	}
	var b strings.Builder
	b.WriteString("# Managed by Hosbah (MH-580 J2) — do not edit by hand, changes are overwritten on the next apply.\n")
	b.WriteString("[sshd]\n")
	b.WriteString("enabled = " + enabled + "\n")
	b.WriteString("bantime = " + strconv.Itoa(p.Bantime) + "\n")
	b.WriteString("findtime = " + strconv.Itoa(p.Findtime) + "\n")
	b.WriteString("maxretry = " + strconv.Itoa(p.Maxretry) + "\n")
	return b.String()
}

type fail2banHost struct{ run runFunc }

func newFail2banHost() *fail2banHost { return &fail2banHost{run: execRun} }

func (h *fail2banHost) install(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.Fail2banInstallParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	installed := true
	if _, err := h.run(oc.Ctx, nil, "fail2ban-client", "--version"); err != nil {
		installed = false
		switch fail2banPkgManager() {
		case "apk":
			if _, err := h.run(oc.Ctx, nil, "apk", "add", "--no-cache", "fail2ban"); err != nil {
				return nil, 1, err
			}
		case "dnf":
			if _, err := h.run(oc.Ctx, nil, "dnf", "install", "-y", "epel-release"); err != nil {
				oc.Info("warning: could not ensure epel-release (%v); continuing", err)
			}
			if _, err := h.run(oc.Ctx, nil, "dnf", "install", "-y", "fail2ban"); err != nil {
				return nil, 1, err
			}
		default:
			if _, err := h.run(oc.Ctx, nil, "apt-get", "update"); err != nil {
				return nil, 1, err
			}
			if _, err := h.run(oc.Ctx, nil, "apt-get", "install", "-y", "--no-install-recommends", "fail2ban"); err != nil {
				return nil, 1, err
			}
		}
		installed = true
	}
	if err := writeFileAtomic(fail2banJailWritePath, renderFail2banJail(p)); err != nil {
		return nil, 1, fmt.Errorf("write jail config: %w", err)
	}
	if _, err := h.run(oc.Ctx, nil, "systemctl", "enable", "fail2ban"); err != nil {
		oc.Info("warning: could not enable fail2ban at boot (%v)", err)
	}
	action := "restart"
	if _, err := h.run(oc.Ctx, nil, "systemctl", action, "fail2ban"); err != nil {
		return nil, 1, err
	}
	active := false
	if out, err := h.run(oc.Ctx, nil, "systemctl", "is-active", "fail2ban"); err == nil {
		active = strings.TrimSpace(string(out)) == "active"
	}
	oc.Info("fail2ban sshd jail: enabled=%v bantime=%ds findtime=%ds maxretry=%d", p.Enabled, p.Bantime, p.Findtime, p.Maxretry)
	return agentproto.Fail2banInstallResult{Installed: installed, Active: active}, 0, nil
}

func (h *fail2banHost) banned(oc *OpContext) (any, int, error) {
	res := agentproto.Fail2banBannedResult{Jail: agentproto.SSHJail}
	out, err := h.run(oc.Ctx, nil, "fail2ban-client", "status", agentproto.SSHJail)
	if err != nil {
		return res, 0, nil // fail2ban not installed/running: no jail, no bans — not an error
	}
	res.Active = true
	res.Banned = parseBannedIPs(string(out))
	return res, 0, nil
}

// parseBannedIPs pulls the IPs off fail2ban-client's "Banned IP list:" line.
func parseBannedIPs(status string) []string {
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "Banned IP list:"); idx >= 0 {
			rest := strings.TrimSpace(line[idx+len("Banned IP list:"):])
			if rest == "" {
				return nil
			}
			return strings.Fields(rest)
		}
	}
	return nil
}

func (h *fail2banHost) unban(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.Fail2banUnbanParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	_, err = h.run(oc.Ctx, nil, "fail2ban-client", "set", agentproto.SSHJail, "unbanip", p.IP)
	if err != nil {
		return agentproto.Fail2banUnbanResult{IP: p.IP, Unbanned: false}, 0, nil
	}
	oc.Info("unbanned %s from the %s jail", p.IP, agentproto.SSHJail)
	return agentproto.Fail2banUnbanResult{IP: p.IP, Unbanned: true}, 0, nil
}
