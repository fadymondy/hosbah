package agentproto

import (
	"errors"
	"net"
)

// MH-580 J2: fail2ban, with a single sshd jail the team controls (bantime/findtime/maxretry).
// fail2ban.install installs the package (apt/dnf/apk, whichever the box has), writes the jail
// config and enables the service; fail2ban.banned lists the sshd jail's currently banned IPs;
// fail2ban.unban clears one.

const (
	// KindFail2banInstall installs (if needed) and (re)configures fail2ban's sshd jail.
	KindFail2banInstall = "fail2ban.install"
	// KindFail2banBanned lists the IPs currently banned in the sshd jail.
	KindFail2banBanned = "fail2ban.banned"
	// KindFail2banUnban clears one IP from the sshd jail.
	KindFail2banUnban = "fail2ban.unban"
)

// SSHJail is the one jail this feature manages.
const SSHJail = "sshd"

// Fail2banInstallParams: fail2ban.install. Enabled=false stops and disables the jail (fail2ban
// itself is left installed: nothing here ever uninstalls a package).
type Fail2banInstallParams struct {
	Enabled  bool `json:"enabled"`
	Bantime  int  `json:"bantime"`  // seconds
	Findtime int  `json:"findtime"` // seconds
	Maxretry int  `json:"maxretry"`
}

// Fail2banInstallResult: fail2ban.install.
type Fail2banInstallResult struct {
	Installed bool `json:"installed"`
	Active    bool `json:"active"`
}

// Fail2banBannedResult: fail2ban.banned.
type Fail2banBannedResult struct {
	Jail   string   `json:"jail"`
	Active bool     `json:"active"`
	Banned []string `json:"banned"`
}

// Fail2banUnbanParams: fail2ban.unban.
type Fail2banUnbanParams struct {
	IP string `json:"ip"`
}

// Fail2banUnbanResult: fail2ban.unban.
type Fail2banUnbanResult struct {
	IP       string `json:"ip"`
	Unbanned bool   `json:"unbanned"`
}

// Validate checks fail2ban.install's params, on both sides of the wire. The ranges match the
// fail2ban_settings table's CHECK constraints (internal/db/schema/fail2ban_setting.sql).
func (p Fail2banInstallParams) Validate() error {
	if p.Bantime < 30 || p.Bantime > 31536000 {
		return errors.New("bantime must be 30-31536000 seconds")
	}
	if p.Findtime < 10 || p.Findtime > 31536000 {
		return errors.New("findtime must be 10-31536000 seconds")
	}
	if p.Maxretry < 1 || p.Maxretry > 100 {
		return errors.New("maxretry must be 1-100")
	}
	return nil
}

// Validate checks fail2ban.unban's params, on both sides of the wire.
func (p Fail2banUnbanParams) Validate() error {
	if net.ParseIP(p.IP) == nil {
		return errors.New("not a valid IP address")
	}
	return nil
}
