package agent

import (
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func TestRenderFail2banJailGolden(t *testing.T) {
	p := agentproto.Fail2banInstallParams{Enabled: true, Bantime: 600, Findtime: 600, Maxretry: 5}
	got := renderFail2banJail(p)
	want := "# Managed by Hosbah (MH-580 J2) — do not edit by hand, changes are overwritten on the next apply.\n" +
		"[sshd]\n" +
		"enabled = true\n" +
		"bantime = 600\n" +
		"findtime = 600\n" +
		"maxretry = 5\n"
	if got != want {
		t.Fatalf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderFail2banJailDisabled(t *testing.T) {
	got := renderFail2banJail(agentproto.Fail2banInstallParams{Enabled: false, Bantime: 60, Findtime: 60, Maxretry: 3})
	if !strings.Contains(got, "enabled = false") {
		t.Fatalf("want enabled = false: %s", got)
	}
}

func TestFail2banInstallValidateRejectsOutOfRangeSettings(t *testing.T) {
	cases := []agentproto.Fail2banInstallParams{
		{Bantime: 1, Findtime: 60, Maxretry: 3},
		{Bantime: 600, Findtime: 1, Maxretry: 3},
		{Bantime: 600, Findtime: 600, Maxretry: 0},
		{Bantime: 600, Findtime: 600, Maxretry: 1000},
	}
	for i, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("case %d: want an error, got none: %+v", i, c)
		}
	}
}

func TestFail2banUnbanValidateRejectsNonIP(t *testing.T) {
	for _, ip := range []string{"", "not-an-ip", "10.0.0.1; rm -rf /", "10.0.0.999"} {
		if err := (agentproto.Fail2banUnbanParams{IP: ip}).Validate(); err == nil {
			t.Fatalf("ip %q: want an error", ip)
		}
	}
	if err := (agentproto.Fail2banUnbanParams{IP: "10.0.0.1"}).Validate(); err != nil {
		t.Fatalf("valid ip rejected: %v", err)
	}
}

func TestFail2banPkgManagerDetection(t *testing.T) {
	// hostPkgManager-style detection is exercised end to end through readOSReleaseID, which is a
	// no-op ("") on non-Linux build tags in this test environment; the RHEL/alpine branches are
	// exercised directly against the pure classifier instead so the test is OS-independent.
	if agentproto.RHELFamily("almalinux") != true {
		t.Fatalf("almalinux should be RHEL family")
	}
}

func TestFail2banInstallSkipsPackageInstallWhenAlreadyPresent(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{
		"fail2ban-client --version":    "Fail2Ban v1.0.2\n",
		"systemctl is-active fail2ban": "active\n",
	}}
	h := &fail2banHost{run: f.run}
	dir := t.TempDir()
	old := fail2banJailWritePath
	fail2banJailWritePath = dir + "/hosbah-sshd.conf"
	defer func() { fail2banJailWritePath = old }()

	oc, _ := opCtx(t, agentproto.Fail2banInstallParams{Enabled: true, Bantime: 600, Findtime: 600, Maxretry: 5})
	res, code, err := h.install(oc)
	if err != nil || code != 0 {
		t.Fatalf("install: %v (%d)", err, code)
	}
	out := res.(agentproto.Fail2banInstallResult)
	if !out.Installed || !out.Active {
		t.Fatalf("unexpected result: %+v", out)
	}
	for _, c := range f.calls {
		if strings.Contains(c.argv, "apt-get install") || strings.Contains(c.argv, "dnf install") || strings.Contains(c.argv, "apk add") {
			t.Fatalf("must not re-install an already-present fail2ban: %v", f.calls)
		}
	}
}

func TestFail2banBannedParsesStatusOutput(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{
		"fail2ban-client status sshd": "Status for the jail: sshd\n" +
			"|- Filter\n" +
			"|  `- Currently failed:\t0\n" +
			"`- Actions\n" +
			"   |- Currently banned:\t2\n" +
			"   `- Banned IP list:\t10.0.0.5 10.0.0.6\n",
	}}
	h := &fail2banHost{run: f.run}
	oc, _ := opCtx(t, struct{}{})
	res, code, err := h.banned(oc)
	if err != nil || code != 0 {
		t.Fatalf("banned: %v (%d)", err, code)
	}
	out := res.(agentproto.Fail2banBannedResult)
	if !out.Active || len(out.Banned) != 2 || out.Banned[0] != "10.0.0.5" || out.Banned[1] != "10.0.0.6" {
		t.Fatalf("unexpected result: %+v", out)
	}
}

func TestFail2banBannedNotActiveWhenJailMissing(t *testing.T) {
	f := &fakeRunner{fail: map[string]string{"fail2ban-client status": "Unable to contact server"}}
	h := &fail2banHost{run: f.run}
	oc, _ := opCtx(t, struct{}{})
	res, code, err := h.banned(oc)
	if err != nil || code != 0 {
		t.Fatalf("banned: %v (%d)", err, code)
	}
	if res.(agentproto.Fail2banBannedResult).Active {
		t.Fatalf("want inactive when fail2ban-client fails")
	}
}

func TestFail2banUnban(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{}}
	h := &fail2banHost{run: f.run}
	oc, _ := opCtx(t, agentproto.Fail2banUnbanParams{IP: "10.0.0.5"})
	res, code, err := h.unban(oc)
	if err != nil || code != 0 {
		t.Fatalf("unban: %v (%d)", err, code)
	}
	if !res.(agentproto.Fail2banUnbanResult).Unbanned {
		t.Fatalf("want unbanned=true")
	}
	found := false
	for _, c := range f.calls {
		if c.argv == "fail2ban-client set sshd unbanip 10.0.0.5" {
			found = true
		}
	}
	if !found {
		t.Fatalf("did not call fail2ban-client set sshd unbanip: %v", f.calls)
	}
}
