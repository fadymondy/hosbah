package ops

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func distServer(t *testing.T, dir string) (*Service, *httptest.Server) {
	t.Helper()
	s := &Service{cfg: Config{DistDir: dir, PublicURL: "https://cp.example"}}
	r := chi.NewRouter()
	r.Get("/agent/download/{file}", s.handleDownload)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return s, srv
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url) //#nosec G107 -- a test server
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// A dist dir built after the rename (hosbah-agent-linux-*) is served under both names: the new one,
// and the one an install script or agent.update from a control plane before the rename asks for.
func TestAgentBuildsAreServedUnderBothNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hosbah-agent-linux-amd64"), []byte("new build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("v9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, srv := distServer(t, dir)
	for _, name := range []string{"hosbah-agent-linux-amd64", "cloudy-agent-linux-amd64"} {
		if code, body := get(t, srv.URL+"/agent/download/"+name); code != 200 || body != "new build" {
			t.Errorf("%s: %d %q", name, code, body)
		}
	}
	for _, name := range []string{"hosbah-agent-linux-arm64", "hosbah-agent-linux-386", "other-agent-linux-amd64", "VERSION"} {
		if code, _ := get(t, srv.URL+"/agent/download/"+name); code != 404 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if _, sums := get(t, srv.URL+"/agent/download/SHA256SUMS"); !strings.Contains(sums, "  hosbah-agent-linux-amd64\n") {
		t.Errorf("SHA256SUMS: %q", sums)
	}
	raw, version, err := s.UpdateParams("amd64")
	if err != nil || version != "v9" {
		t.Fatal(version, err)
	}
	var p map[string]string
	_ = json.Unmarshal(raw, &p)
	if p["url"] != "https://cp.example/agent/download/hosbah-agent-linux-amd64" || len(p["sha256"]) != 64 {
		t.Fatalf("agent.update params: %v", p)
	}
}

// A dist dir from before the rename (only cloudy-agent-linux-*) keeps working.
func TestAPreRenameDistDirIsServed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cloudy-agent-linux-arm64"), []byte("old build"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, srv := distServer(t, dir)
	for _, name := range []string{"hosbah-agent-linux-arm64", "cloudy-agent-linux-arm64"} {
		if code, body := get(t, srv.URL+"/agent/download/"+name); code != 200 || body != "old build" {
			t.Errorf("%s: %d %q", name, code, body)
		}
	}
	if d := s.dist.load(dir); d.sha["arm64"] == "" || d.file["arm64"] != "cloudy-agent-linux-arm64" {
		t.Fatalf("dist: %+v", d)
	}
}

// The install script installs hosbah-agent under hosbah-agent.service, retires a pre-rename unit
// and leaves the old binary path leading to the new binary.
func TestTheInstallScriptInstallsHosbahAgent(t *testing.T) {
	sh := installScript(installVars{ControlPlane: "https://cp.example", Token: "hbe_x", ServerName: "web", ExpiresAt: "soon",
		Version: "v9", SHA256AMD64: strings.Repeat("a", 64)})
	for _, want := range []string{
		"HOSBAH_URL='https://cp.example'",
		`"$HOSBAH_URL/agent/download/hosbah-agent-linux-$ARCH"`,
		"install -m 0755 \"$TMP/hosbah-agent\" /usr/local/bin/hosbah-agent",
		"ln -sfn hosbah-agent /usr/local/bin/cloudy-agent",
		"cat > /etc/systemd/system/hosbah-agent.service <<'UNIT'\n" + agentproto.AgentUnitFile + "UNIT\n",
		"systemctl disable cloudy-agent",
		"systemctl restart hosbah-agent",
		"nohup /usr/local/bin/hosbah-agent run",
	} {
		if !strings.Contains(sh, want) {
			t.Errorf("the script lacks %q", want)
		}
	}
	if strings.Contains(sh, "CLOUDY_URL") || strings.Contains(sh, "systemctl restart cloudy-agent") {
		t.Error("the script still installs the pre-rename agent")
	}
}
