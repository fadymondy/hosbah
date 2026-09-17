package deploy

import (
	"strings"
	"testing"
)

func TestParseAndResolveConfig(t *testing.T) {
	c, err := ParseConfig(`
port = 3000
release = "npm run migrate"

[healthcheck]
path = "/healthz"
timeout = 30

[processes]
web = "node server.js"
"worker:queue" = "node worker.js"
`)
	if err != nil {
		t.Fatal(err)
	}
	e := Resolve(c, Defaults{Port: 8080, HealthPath: "/", HealthTimeout: 60})
	if e.Port != 3000 || e.HealthPath != "/healthz" || e.HealthTimeout != 30 || e.Release != "npm run migrate" {
		t.Fatalf("resolved: %+v", e)
	}
	if e.Processes["web"] != "node server.js" || e.Processes["worker:queue"] != "node worker.js" {
		t.Fatalf("processes: %+v", e.Processes)
	}
	if w := e.Workers(); len(w) != 1 || w[0] != "worker:queue" {
		t.Fatalf("workers: %v", w)
	}

	// No file: the app's settings, and web with the image's own command.
	e = Resolve(AppConfig{}, Defaults{Port: 8080, HealthPath: "/up", HealthTimeout: 45})
	if e.Port != 8080 || e.HealthPath != "/up" || e.HealthTimeout != 45 || e.Processes["web"] != "" || len(e.Workers()) != 0 {
		t.Fatalf("defaults: %+v", e)
	}
}

func TestParseConfigRefusesMistakes(t *testing.T) {
	for raw, want := range map[string]string{
		`prot = 80`:                         "unknown key",
		`port = 70000`:                      "port",
		"[healthcheck]\npath = \"health\"":  "start with /",
		"[healthcheck]\ntimeout = 1":        "timeout",
		"[processes]\nqueue = \"x\"":        `"web" or "worker:<name>"`,
		"[processes]\n\"worker:q\" = \" \"": "empty command",
		`port = `:                           "cloudy.toml",
	} {
		if _, err := ParseConfig(raw); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want an error mentioning %q, got %v", raw, want, err)
		}
	}
}

func TestPinnedTools(t *testing.T) {
	c, err := CaddyTool("amd64")
	if err != nil || !strings.HasSuffix(c.URL, "caddy_2.11.4_linux_amd64.tar.gz") || len(c.SHA512) != 128 {
		t.Fatalf("caddy: %+v %v", c, err)
	}
	r, err := RailpackTool("arm64")
	if err != nil || !strings.HasSuffix(r.URL, "railpack-v0.39.0-arm64-unknown-linux-musl.tar.gz") || len(r.SHA256) != 64 {
		t.Fatalf("railpack: %+v %v", r, err)
	}
	if _, err := CaddyTool("riscv64"); err == nil {
		t.Error("an unpinned arch was accepted")
	}
}
