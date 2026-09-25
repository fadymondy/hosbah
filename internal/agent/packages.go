package agent

import (
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// pkg.list / pkg.upgrade / pkg.autoupdate.set (MH-578 C8): apt (Debian/Ubuntu), dnf (RHEL family)
// or apk (Alpine) aware — everything here is a fixed argv (never a shell) picked by which package
// manager binary is on PATH.

// lookPath / rebootRequiredFile are overridable in tests.
var (
	lookPath              = exec.LookPath
	rebootRequiredFile    = "/var/run/reboot-required"
	unattendedUpgradesDir = "/etc/apt/apt.conf.d"
	dnfAutomaticConfPath  = "/etc/dnf/automatic.conf"
)

// detectPackageManager reports the OS family and package manager this host has, by which binary is
// on PATH — apt-get before dnf before apk, since a host only ever has one.
func detectPackageManager() (osFamily, manager string) {
	if _, err := lookPath("apt-get"); err == nil {
		return agentproto.OSFamilyDebian, agentproto.PkgManagerAPT
	}
	if _, err := lookPath("dnf"); err == nil {
		return agentproto.OSFamilyRHEL, agentproto.PkgManagerDNF
	}
	if _, err := lookPath("apk"); err == nil {
		return agentproto.OSFamilyAlpine, agentproto.PkgManagerAPK
	}
	return agentproto.OSFamilyOther, ""
}

// isRebootRequired: Debian/Ubuntu drop a marker file once a kernel or glibc upgrade needs a
// reboot; RHEL's dnf-utils needs-restarting -r exits 1 in the same situation (exit 0: no reboot
// needed); Alpine has no equivalent signal this agent knows.
func isRebootRequired(osFamily string) bool {
	switch osFamily {
	case agentproto.OSFamilyDebian:
		_, err := os.Stat(rebootRequiredFile)
		return err == nil
	case agentproto.OSFamilyRHEL:
		if _, err := lookPath("needs-restarting"); err != nil {
			return false
		}
		cmd := exec.Command("needs-restarting", "-r") //#nosec G204 -- fixed argv
		return cmd.Run() != nil                       // exit 1 (or anything but 0) = a reboot is wanted
	default:
		return false
	}
}

// aptUpgradableLine matches one "apt list --upgradable" line, e.g.
// "vim/jammy-updates 2:8.2.3995-1ubuntu2.15 amd64 [upgradable from: 2:8.2.3995-1ubuntu2.14]".
var aptUpgradableLine = regexp.MustCompile(`^(\S+)/\S+\s+(\S+)\s+\S+\s+\[upgradable from:\s*([^\]]+)\]`)

// parseAptUpgradable parses "apt list --upgradable" output (stable-enough for scripting, per apt's
// own warning banner, which this ignores along with any other unmatched line).
func parseAptUpgradable(out string) []agentproto.PkgInfo {
	var pkgs []agentproto.PkgInfo
	for _, line := range strings.Split(out, "\n") {
		m := aptUpgradableLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		pkgs = append(pkgs, agentproto.PkgInfo{Name: m[1], Candidate: m[2], Current: strings.TrimSpace(m[3])})
	}
	return pkgs
}

// parseDnfCheckUpdate parses "dnf check-update" output: after a blank-line-separated header, each
// update is "name.arch   version   repo" (whitespace-separated columns); dnf's own exit code is 100
// when updates exist and 0 when none — neither is an error, so the caller must not treat either as
// a failure.
func parseDnfCheckUpdate(out string) []agentproto.PkgInfo {
	var pkgs []agentproto.PkgInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Last metadata") || strings.HasPrefix(line, "Obsoleting") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if i := strings.LastIndex(name, "."); i > 0 {
			name = name[:i]
		}
		pkgs = append(pkgs, agentproto.PkgInfo{Name: name, Candidate: fields[1]})
	}
	return pkgs
}

// parseApkUpgradable parses "apk version -l '<'" output: one "pkgname-version" per line (apk has no
// separate current/candidate on this line, so Current is left blank).
func parseApkUpgradable(out string) []agentproto.PkgInfo {
	var pkgs []agentproto.PkgInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "WARNING") {
			continue
		}
		// pkgname-version-revision: split at the last "-" that starts a version (digit follows).
		i := strings.LastIndexFunc(line, func(r rune) bool { return r == '-' })
		name, ver := line, ""
		for i > 0 {
			if i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9' {
				name, ver = line[:i], line[i+1:]
				break
			}
			i = strings.LastIndex(line[:i], "-")
		}
		pkgs = append(pkgs, agentproto.PkgInfo{Name: name, Candidate: ver})
	}
	return pkgs
}

func pkgList(oc *OpContext) (any, int, error) {
	if _, err := decode[agentproto.PkgListParams](oc); err != nil {
		return nil, 2, err
	}
	osFamily, manager := detectPackageManager()
	res := agentproto.PkgListResult{OSFamily: osFamily, PackageManager: manager}
	var pkgs []agentproto.PkgInfo
	switch manager {
	case agentproto.PkgManagerAPT:
		_ = commandContext(oc, "apt-get", "-qq", "update").Run() //#nosec G204 -- fixed argv; refreshing the index best-effort
		out, _ := commandContext(oc, "apt", "list", "--upgradable").CombinedOutput()
		pkgs = parseAptUpgradable(string(out))
	case agentproto.PkgManagerDNF:
		out, _ := commandContext(oc, "dnf", "-q", "check-update").CombinedOutput() // exit 100 = updates exist; not an error
		pkgs = parseDnfCheckUpdate(string(out))
	case agentproto.PkgManagerAPK:
		_ = commandContext(oc, "apk", "update", "-q").Run() //#nosec G204 -- fixed argv
		out, _ := commandContext(oc, "apk", "version", "-l", "<").CombinedOutput()
		pkgs = parseApkUpgradable(string(out))
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	res.UpgradableCount = len(pkgs)
	if len(pkgs) > agentproto.MaxPkgListPackages {
		pkgs = pkgs[:agentproto.MaxPkgListPackages]
	}
	res.Packages = pkgs
	res.RebootRequired = isRebootRequired(osFamily)
	return res, 0, nil
}

func pkgUpgrade(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.PkgUpgradeParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	osFamily, manager := detectPackageManager()
	res := agentproto.PkgUpgradeResult{OSFamily: osFamily}
	code := 0
	switch manager {
	case agentproto.PkgManagerAPT:
		if c, _ := oc.Run("apt-get", "-qq", "update"); c != 0 {
			oc.Info("apt-get update failed (continuing with the current index)")
		}
		// apt has no portable "security only" release flag across distributions (unlike dnf
		// --security), so SecurityOnly is a no-op here: a full upgrade also carries security
		// updates, just not exclusively.
		args := []string{"-y", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "upgrade"}
		code, err = oc.Run("apt-get", args...)
	case agentproto.PkgManagerDNF:
		args := []string{"-y"}
		if p.SecurityOnly {
			args = append(args, "--security")
		}
		args = append(args, "upgrade")
		code, err = oc.Run("dnf", args...)
	case agentproto.PkgManagerAPK:
		if c, _ := oc.Run("apk", "update"); c != 0 {
			oc.Info("apk update failed (continuing with the current index)")
		}
		code, err = oc.Run("apk", "upgrade")
	default:
		oc.Info("no supported package manager (apt, dnf or apk) was found on this server")
	}
	if err != nil {
		return nil, code, err
	}
	res.ExitCode = code
	res.RebootRequired = isRebootRequired(osFamily)
	return res, code, nil
}

func pkgAutoupdateSet(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.PkgAutoupdateSetParams](oc)
	if err != nil {
		return nil, 2, err
	}
	osFamily, manager := detectPackageManager()
	res := agentproto.PkgAutoupdateSetResult{OSFamily: osFamily}
	switch manager {
	case agentproto.PkgManagerAPT:
		res.Supported = true
		if code, err := setUnattendedUpgrades(oc, p.Enabled); err != nil {
			res.Error = err.Error()
			return res, code, nil
		}
		res.Enabled = p.Enabled
	case agentproto.PkgManagerDNF:
		res.Supported = true
		if code, err := setDNFAutomatic(oc, p.Enabled); err != nil {
			res.Error = err.Error()
			return res, code, nil
		}
		res.Enabled = p.Enabled
	default:
		res.Supported = false
		res.Error = "this distribution has no automatic security-update mechanism Hosbah knows"
	}
	return res, 0, nil
}

// unattendedUpgradesConfig is the exact content Hosbah writes for a Debian/Ubuntu host — enabling
// only the security pocket, which is what "automatic security updates" means here.
const unattendedUpgradesConfig = `// Managed by Hosbah (MH-578 C8). Do not edit by hand; toggle it from the team's server page.
Unattended-Upgrade::Allowed-Origins {
    "${distro_id}:${distro_codename}-security";
};
`

const aptPeriodicConfig = `// Managed by Hosbah (MH-578 C8).
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
`

func setUnattendedUpgrades(oc *OpContext, enabled bool) (int, error) {
	if !enabled {
		// Disable at the source: 0 turns both periodic actions off; the packages stay installed.
		return oc.RunStdin("APT::Periodic::Update-Package-Lists \"0\";\nAPT::Periodic::Unattended-Upgrade \"0\";\n",
			"tee", unattendedUpgradesDir+"/20auto-upgrades")
	}
	if code, err := oc.Run("apt-get", "-y", "install", "unattended-upgrades"); err != nil {
		return code, err
	}
	if code, err := oc.RunStdin(unattendedUpgradesConfig, "tee", unattendedUpgradesDir+"/51unattended-upgrades-hosbah"); err != nil {
		return code, err
	}
	return oc.RunStdin(aptPeriodicConfig, "tee", unattendedUpgradesDir+"/20auto-upgrades")
}

func setDNFAutomatic(oc *OpContext, enabled bool) (int, error) {
	if !enabled {
		return oc.Run("systemctl", "disable", "--now", "dnf-automatic.timer")
	}
	if code, err := oc.Run("dnf", "-y", "install", "dnf-automatic"); err != nil {
		return code, err
	}
	// upgrade_type = security: only security updates are applied automatically.
	conf := "[commands]\nupgrade_type = security\napply_updates = yes\n"
	if code, err := oc.RunStdin(conf, "tee", dnfAutomaticConfPath); err != nil {
		return code, err
	}
	return oc.Run("systemctl", "enable", "--now", "dnf-automatic.timer")
}
