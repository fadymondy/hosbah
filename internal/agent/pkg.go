package agent

import (
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// pkgManager hides apt versus dnf. Hosbah names packages the Debian way; rpmNames maps the ones
// that differ on the RHEL family (an empty name means "nothing to install there").
type pkgManager struct{ rpm bool }

var rpmNames = map[string]string{
	"gnupg":         "gnupg2",
	"docker.io":     "docker-ce",
	"docker-buildx": "docker-buildx-plugin",
}

func hostPkgManager() pkgManager {
	return pkgManager{rpm: agentproto.RHELFamily(readOSReleaseID())}
}

func (m pkgManager) name(p string) string {
	if m.rpm {
		if n, ok := rpmNames[p]; ok {
			return n
		}
	}
	return p
}

// missing returns the packages (Debian names) that are not installed.
func (m pkgManager) missing(oc *OpContext, pkgs []string) []string {
	var out []string
	for _, p := range pkgs {
		if m.rpm {
			if _, err := execOutput(oc, "rpm", "-q", m.name(p)); err != nil {
				out = append(out, p)
			}
			continue
		}
		s, err := execOutput(oc, "dpkg-query", "-W", "-f=${Status}", p)
		if err != nil || !strings.Contains(s, "install ok installed") {
			out = append(out, p)
		}
	}
	return out
}

// install refreshes the index and installs pkgs (Debian names). Docker CE on the RHEL family comes
// from Docker's own repository: the distributions ship only podman.
func (m pkgManager) install(oc *OpContext, pkgs []string) (int, error) {
	if !m.rpm {
		if code, err := oc.Run("apt-get", "update"); err != nil {
			return code, err
		}
		return oc.Run("apt-get", append([]string{"install", "-y", "--no-install-recommends"}, pkgs...)...)
	}
	names := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		n := m.name(p)
		if strings.HasPrefix(n, "docker-") {
			if code, err := m.dockerRepo(oc); err != nil {
				return code, err
			}
		}
		if n == "restic" {
			if code, err := oc.Run("dnf", "install", "-y", "epel-release"); err != nil {
				return code, err
			}
		}
		names = append(names, n)
	}
	return oc.Run("dnf", append([]string{"install", "-y"}, names...)...)
}

func (m pkgManager) dockerRepo(oc *OpContext) (int, error) {
	if _, err := execOutput(oc, "test", "-f", "/etc/yum.repos.d/docker-ce.repo"); err == nil {
		return 0, nil
	}
	if code, err := oc.Run("dnf", "install", "-y", "dnf-plugins-core"); err != nil {
		return code, err
	}
	return oc.Run("dnf", "config-manager", "--add-repo", "https://download.docker.com/linux/centos/docker-ce.repo")
}
