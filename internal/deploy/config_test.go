package deploy

import (
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
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

// TestResolveResourcesDefaults: no [resources] in hosbah.toml — the app's own settings (Defaults)
// stand, base and per-process overrides alike.
func TestResolveResourcesDefaults(t *testing.T) {
	c, err := ParseConfig(`port = 3000`)
	if err != nil {
		t.Fatal(err)
	}
	d := Defaults{
		Port: 8080, Resources: agentproto.ResourceLimits{MemoryMB: 512, CPUs: 1},
		ProcessResources: map[string]agentproto.ResourceLimits{"worker:queue": {MemoryMB: 256, CPUs: 0.5}},
	}
	e := Resolve(c, d)
	if e.Resources != d.Resources {
		t.Fatalf("base resources: got %+v, want %+v", e.Resources, d.Resources)
	}
	if e.ResourcesFor("web") != d.Resources {
		t.Fatalf("ResourcesFor(web): got %+v, want the base %+v", e.ResourcesFor("web"), d.Resources)
	}
	if got := e.ResourcesFor("worker:queue"); got != d.ProcessResources["worker:queue"] {
		t.Fatalf("ResourcesFor(worker:queue): got %+v, want %+v", got, d.ProcessResources["worker:queue"])
	}
}

// TestResolveResourcesMerge: hosbah.toml's [resources] wins over the app's own base and process
// settings, key by key — a key it leaves out keeps the app's setting.
func TestResolveResourcesMerge(t *testing.T) {
	c, err := ParseConfig(`
[resources]
memory = "1G"
cpus = 2

[resources.processes."worker:mail"]
memory = "256M"
`)
	if err != nil {
		t.Fatal(err)
	}
	d := Defaults{
		Resources:        agentproto.ResourceLimits{MemoryMB: 512, CPUs: 0.5},
		ProcessResources: map[string]agentproto.ResourceLimits{"worker:mail": {MemoryMB: 128, CPUs: 0.25}},
	}
	e := Resolve(c, d)

	// The repository's [resources] replaces both the base app's memory and cpus.
	want := agentproto.ResourceLimits{MemoryMB: 1024, CPUs: 2}
	if e.Resources != want {
		t.Fatalf("base resources: got %+v, want %+v", e.Resources, want)
	}
	// [resources.processes."worker:mail"] only sets memory, so it starts from the app's own
	// worker:mail setting (not the new base) and keeps that setting's cpus.
	wantWorker := agentproto.ResourceLimits{MemoryMB: 256, CPUs: 0.25}
	if got := e.ResourcesFor("worker:mail"); got != wantWorker {
		t.Fatalf("ResourcesFor(worker:mail): got %+v, want %+v", got, wantWorker)
	}
	// A process the file never mentions falls back to the new base.
	if got := e.ResourcesFor("web"); got != want {
		t.Fatalf("ResourcesFor(web): got %+v, want the base %+v", got, want)
	}
}

// TestResolveResourcesProcessOverrideStartsFromBase: a hosbah.toml process override for a process
// the app itself has no override for starts from the (possibly just-replaced) base, not zero.
func TestResolveResourcesProcessOverrideStartsFromBase(t *testing.T) {
	c, err := ParseConfig(`
[resources]
memory = "512M"

[resources.processes."worker:queue"]
cpus = 0.25
`)
	if err != nil {
		t.Fatal(err)
	}
	e := Resolve(c, Defaults{Resources: agentproto.ResourceLimits{CPUs: 1}})
	want := agentproto.ResourceLimits{MemoryMB: 512, CPUs: 0.25}
	if got := e.ResourcesFor("worker:queue"); got != want {
		t.Fatalf("ResourcesFor(worker:queue): got %+v, want %+v", got, want)
	}
}

func TestParseConfigResourcesValidation(t *testing.T) {
	for raw, want := range map[string]string{
		"[resources]\nmemory = \"1x\"":                     "bad memory size",
		"[resources]\nmemory = \"8M\"":                      "memory must be",
		"[resources]\ncpus = 999":                           "cpus must be",
		"[resources.processes]\nqueue = {}":                 `"web" or "worker:<name>"`,
		"[resources.processes.\"worker:q\"]\nmemory = \"1x\"": "bad memory size",
	} {
		if _, err := ParseConfig(raw); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want an error mentioning %q, got %v", raw, want, err)
		}
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
		`port = `:                           "hosbah.toml",
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

// A fast build with no hosbah.toml keeps the live release's processes and release command, but the
// app's current port and health check: changing the health path must apply on the next deploy.
func TestKeepLiveProcessesKeepsCurrentHealth(t *testing.T) {
	eff := Resolve(AppConfig{}, Defaults{Port: 5190, HealthPath: "/en", HealthTimeout: 90})
	live := Effective{Port: 3000, HealthPath: "/", HealthTimeout: 30, Release: "migrate",
		Processes: map[string]string{"web": "node server.js", "worker:jobs": "node jobs.js"}}
	got := keepLiveProcesses(eff, live)
	if got.Port != 5190 || got.HealthPath != "/en" || got.HealthTimeout != 90 {
		t.Fatalf("port/health = %d %q %d, want the app's 5190 /en 90", got.Port, got.HealthPath, got.HealthTimeout)
	}
	if got.Release != "migrate" || got.Processes["worker:jobs"] != "node jobs.js" || got.Processes["web"] != "node server.js" {
		t.Fatalf("processes/release not kept: %+v", got)
	}
}
