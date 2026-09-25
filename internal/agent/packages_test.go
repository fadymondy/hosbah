package agent

import (
	"errors"
	"os"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func TestParseAptUpgradable(t *testing.T) {
	out := `Listing... Done
vim/jammy-updates 2:8.2.3995-1ubuntu2.15 amd64 [upgradable from: 2:8.2.3995-1ubuntu2.14]
curl/jammy-updates 7.81.0-1ubuntu1.15 amd64 [upgradable from: 7.81.0-1ubuntu1.14]
`
	pkgs := parseAptUpgradable(out)
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages, want 2: %+v", len(pkgs), pkgs)
	}
	if pkgs[0].Name != "vim" || pkgs[0].Candidate != "2:8.2.3995-1ubuntu2.15" || pkgs[0].Current != "2:8.2.3995-1ubuntu2.14" {
		t.Fatalf("bad parse: %+v", pkgs[0])
	}
	if pkgs[1].Name != "curl" {
		t.Fatalf("bad parse: %+v", pkgs[1])
	}
}

func TestParseAptUpgradableEmpty(t *testing.T) {
	if pkgs := parseAptUpgradable("Listing... Done\n"); len(pkgs) != 0 {
		t.Fatalf("got %d, want 0", len(pkgs))
	}
}

func TestParseDnfCheckUpdate(t *testing.T) {
	out := `Last metadata expiration check: 0:12:34 ago.

bash.x86_64                    5.1.8-4.el8_5                    updates
kernel.x86_64                  4.18.0-348.el8                   updates

Obsoleting Packages
`
	pkgs := parseDnfCheckUpdate(out)
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages, want 2: %+v", len(pkgs), pkgs)
	}
	if pkgs[0].Name != "bash" || pkgs[0].Candidate != "5.1.8-4.el8_5" {
		t.Fatalf("bad parse: %+v", pkgs[0])
	}
	if pkgs[1].Name != "kernel" {
		t.Fatalf("bad parse: %+v", pkgs[1])
	}
}

func TestParseApkUpgradable(t *testing.T) {
	out := "apk-tools-2.12.9-r3\nbusybox-1.35.0-r29\n"
	pkgs := parseApkUpgradable(out)
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages, want 2: %+v", len(pkgs), pkgs)
	}
	if pkgs[0].Name != "apk-tools" || pkgs[0].Candidate != "2.12.9-r3" {
		t.Fatalf("bad parse: %+v", pkgs[0])
	}
	if pkgs[1].Name != "busybox" || pkgs[1].Candidate != "1.35.0-r29" {
		t.Fatalf("bad parse: %+v", pkgs[1])
	}
}

func TestDetectPackageManager(t *testing.T) {
	orig := lookPath
	defer func() { lookPath = orig }()

	lookPath = func(name string) (string, error) {
		if name == "apt-get" {
			return "/usr/bin/apt-get", nil
		}
		return "", errors.New("not found")
	}
	if fam, mgr := detectPackageManager(); fam != agentproto.OSFamilyDebian || mgr != agentproto.PkgManagerAPT {
		t.Fatalf("got %s/%s", fam, mgr)
	}

	lookPath = func(name string) (string, error) {
		if name == "dnf" {
			return "/usr/bin/dnf", nil
		}
		return "", errors.New("not found")
	}
	if fam, mgr := detectPackageManager(); fam != agentproto.OSFamilyRHEL || mgr != agentproto.PkgManagerDNF {
		t.Fatalf("got %s/%s", fam, mgr)
	}

	lookPath = func(name string) (string, error) {
		if name == "apk" {
			return "/sbin/apk", nil
		}
		return "", errors.New("not found")
	}
	if fam, mgr := detectPackageManager(); fam != agentproto.OSFamilyAlpine || mgr != agentproto.PkgManagerAPK {
		t.Fatalf("got %s/%s", fam, mgr)
	}

	lookPath = func(name string) (string, error) { return "", errors.New("not found") }
	if fam, mgr := detectPackageManager(); fam != agentproto.OSFamilyOther || mgr != "" {
		t.Fatalf("got %s/%s", fam, mgr)
	}
}

func TestIsRebootRequired(t *testing.T) {
	origFile := rebootRequiredFile
	defer func() { rebootRequiredFile = origFile }()
	rebootRequiredFile = t.TempDir() + "/does-not-exist"
	if isRebootRequired(agentproto.OSFamilyDebian) {
		t.Fatal("want false: no marker file")
	}
	f := t.TempDir() + "/reboot-required"
	if err := os.WriteFile(f, []byte("reboot"), 0o644); err != nil {
		t.Fatal(err)
	}
	rebootRequiredFile = f
	if !isRebootRequired(agentproto.OSFamilyDebian) {
		t.Fatal("want true: marker file present")
	}
	if isRebootRequired(agentproto.OSFamilyAlpine) {
		t.Fatal("want false: alpine has no signal")
	}
}
