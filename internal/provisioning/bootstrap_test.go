package provisioning

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestSSHInstallCommandRunsTheScriptAndFailsLoudly(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/install/ok" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("echo installed-the-agent\n"))
	}))
	defer srv.Close()

	out, err := exec.Command("sh", "-c", sshInstallCommand(srv.URL+"/install/ok", "")).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "installed-the-agent") {
		t.Fatalf("ok script: err=%v out=%q", err, out)
	}
	// The old `curl … | sh` exits 0 here; this must not.
	if out, err := exec.Command("sh", "-c", sshInstallCommand(srv.URL+"/install/gone", "")).CombinedOutput(); err == nil {
		t.Fatalf("a failed download succeeded: %q", out)
	}
}

func TestSSHInstallCommandSudo(t *testing.T) {
	c := sshInstallCommand("https://x.test/install/t", "sudo -n ")
	if !strings.Contains(c, `sudo -n sh "$f"`) || !strings.Contains(c, "sudo -n sh -c 'if command -v apt-get") {
		t.Fatalf("sudo not applied to the install and the curl bootstrap: %s", c)
	}
}
