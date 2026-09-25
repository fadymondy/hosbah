package agentproto

import "fmt"

// pkg.list / pkg.upgrade / pkg.autoupdate.set (MH-578 C8): apt (Debian/Ubuntu), dnf (RHEL/Fedora/
// Alma/Rocky) or apk (Alpine) aware upgrade checks, an actual upgrade run, and the toggle for the
// distribution's own unattended/automatic security-update mechanism (unattended-upgrades on
// Debian/Ubuntu, dnf-automatic on RHEL; apk and anything else report unsupported).
const (
	KindPkgList          = "pkg.list"
	KindPkgUpgrade       = "pkg.upgrade"
	KindPkgAutoupdateSet = "pkg.autoupdate.set"
)

// OS families pkg.list/pkg.upgrade report (PkgListResult.OSFamily / PkgUpgradeResult.OSFamily).
const (
	OSFamilyDebian = "debian"
	OSFamilyRHEL   = "rhel"
	OSFamilyAlpine = "alpine"
	OSFamilyOther  = "other"
)

// Package managers.
const (
	PkgManagerAPT = "apt"
	PkgManagerDNF = "dnf"
	PkgManagerAPK = "apk"
)

// MaxPkgListPackages bounds how many upgradable packages a pkg.list result carries (the agent
// truncates; UpgradableCount still reports the true total).
const MaxPkgListPackages = 500

// PkgListParams: pkg.list. No fields — it inspects the server it runs on.
type PkgListParams struct{}

// Validate: pkg.list takes no parameters.
func (PkgListParams) Validate() error { return nil }

// PkgInfo is one upgradable package.
type PkgInfo struct {
	Name      string `json:"name"`
	Current   string `json:"current,omitempty"`
	Candidate string `json:"candidate,omitempty"`
}

// PkgListResult: pkg.list.
type PkgListResult struct {
	OSFamily        string    `json:"os_family"`                 // debian | rhel | alpine | other
	PackageManager  string    `json:"package_manager,omitempty"` // apt | dnf | apk, empty for "other"
	UpgradableCount int       `json:"upgradable_count"`
	Packages        []PkgInfo `json:"packages,omitempty"` // first MaxPkgListPackages, by name
	RebootRequired  bool      `json:"reboot_required"`
}

// PkgUpgradeParams: pkg.upgrade — a non-interactive full upgrade with the detected manager.
// SecurityOnly, when true, upgrades only packages carrying a security update where the manager can
// tell (apt: the -security pocket; dnf: --security; apk has no such distinction and always upgrades
// everything upgradable).
type PkgUpgradeParams struct {
	SecurityOnly bool `json:"security_only,omitempty"`
}

// Validate: pkg.upgrade takes no required parameters.
func (PkgUpgradeParams) Validate() error { return nil }

// PkgUpgradeResult: pkg.upgrade.
type PkgUpgradeResult struct {
	OSFamily       string `json:"os_family"`
	ExitCode       int    `json:"exit_code"`
	RebootRequired bool   `json:"reboot_required"`
	Upgraded       int    `json:"upgraded"` // best-effort count of packages actually changed
}

// PkgAutoupdateSetParams: pkg.autoupdate.set — switch the distribution's own automatic security
// updates on or off.
type PkgAutoupdateSetParams struct {
	Enabled bool `json:"enabled"`
}

// Validate: pkg.autoupdate.set takes no required parameters beyond Enabled (a bool, always valid).
func (PkgAutoupdateSetParams) Validate() error { return nil }

// PkgAutoupdateSetResult: pkg.autoupdate.set.
type PkgAutoupdateSetResult struct {
	OSFamily  string `json:"os_family"`
	Supported bool   `json:"supported"` // false: the distribution has no unattended mechanism this agent knows; Enabled is always reported false
	Enabled   bool   `json:"enabled"`
	Error     string `json:"error,omitempty"`
}

// MaxUpgradeExitCode / MaxUpgraded bound reported values so a malformed result cannot spoof huge
// numbers into a client. Not enforced strictly (informational fields) but used by tests.
func validatePkgResult(exitCode int) error {
	if exitCode < 0 || exitCode > 255 {
		return fmt.Errorf("exit code out of range: %d", exitCode)
	}
	return nil
}
