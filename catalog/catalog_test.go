package catalog_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fadymondy/hosbah/catalog"
	"github.com/fadymondy/hosbah/internal/deploy"
)

// The whole embedded catalog loads: a bad recipe fails here, in the build's tests, not in production.
func TestTheWholeEmbeddedCatalogIsValid(t *testing.T) {
	c, err := catalog.Builtin()
	if err != nil {
		t.Fatalf("the embedded catalog does not load: %v", err)
	}
	all := c.List("", "")
	if len(all) == 0 {
		t.Fatal("the embedded catalog is empty")
	}
	apps, stacks := 0, 0
	for _, r := range all {
		switch r.Kind {
		case catalog.KindApp:
			apps++
			if err := catalog.ValidImage(r.Image); err != nil {
				t.Errorf("%s: %v", r.ID, err)
			}
			if r.HasWebUI && r.Healthcheck == "" {
				t.Errorf("%s has a web port but no healthcheck: the agent could not tell when it is up", r.ID)
			}
		case catalog.KindStack:
			stacks++
			// The suggested cloudy.toml must be one Hosbah's own parser accepts, and must say what
			// the suggested processes say.
			cfg, err := deploy.ParseConfig(r.Stack.CloudyTOML)
			if err != nil {
				t.Errorf("%s: its cloudy.toml does not parse: %v", r.ID, err)
			}
			for name, cmd := range r.Stack.Processes {
				if cfg.Processes[name] != cmd {
					t.Errorf("%s: process %s differs between processes and cloudy_toml", r.ID, name)
				}
			}
		}
		if got, ok := c.Get(r.ID); !ok || got.ID != r.ID {
			t.Errorf("Get(%s) does not find it", r.ID)
		}
	}
	if apps == 0 || stacks == 0 {
		t.Errorf("want at least one app and one stack, got %d and %d", apps, stacks)
	}
	if got := c.List(catalog.KindApp, "MAILPIT"); len(got) != 1 || got[0].ID != "mailpit" {
		t.Errorf("List(app, MAILPIT): %+v", got)
	}
	if got := c.List(catalog.KindStack, "mailpit"); len(got) != 0 {
		t.Errorf("a kind filter must apply: %+v", got)
	}
}

const goodApp = `
id: demo
name: Demo
summary: A demo.
description: A demo recipe.
homepage: https://example.com
licence: MIT
kind: app
version: "1.0.0"
verified: false
image: example/demo:1.2.3
port: 8080
healthcheck: /healthz
env:
  - name: KEY
    generate: { length: 32, charset: alnum }
    secret: true
    inject_as: DEMO_KEY
  - name: AUTH
    value: "user:{KEY}"
    secret: true
volumes:
  - name: data
    mount_path: /data
limits: { memory_mb: 256, cpus: 0.5 }
caps: [CHOWN]
exposes:
  - env: DEMO_URL
    template: "https://{host}:{port}/?k={KEY}"
`

func TestParseAcceptsAGoodRecipeAndRefusesEverythingTheFormatForbids(t *testing.T) {
	if _, err := catalog.Parse([]byte(goodApp)); err != nil {
		t.Fatalf("the good recipe: %v", err)
	}
	for name, c := range map[string]struct{ old, new, want string }{
		"latest":               {"example/demo:1.2.3", "example/demo:latest", "floating tag"},
		"no tag":               {"example/demo:1.2.3", "example/demo", "not pinned"},
		"port is not a tag":    {"example/demo:1.2.3", "registry.example:5000/demo", "not pinned"},
		"bare name":            {"example/demo:1.2.3", "demo:1.2.3", "full reference"},
		"image with a flag":    {"example/demo:1.2.3", "'--privileged example/demo:1.2.3'", "full reference"},
		"bad digest":           {"example/demo:1.2.3", "example/demo@sha256:abc", "digest"},
		"unknown template var": {"user:{KEY}", "user:{NOPE}", "{NOPE}, which is not declared"},
		"host in env":          {"user:{KEY}", "http://{host}", "{host}, which is not declared"},
		"unknown expose var":   {"?k={KEY}", "?k={SECRET}", "{SECRET}, which is not declared"},
		"stray brace":          {"?k={KEY}", "?k={KEY", "unbalanced brace"},
		"mount under /etc":     {"mount_path: /data", "mount_path: /etc/demo", "/etc"},
		"mount escapes":        {"mount_path: /data", "mount_path: /data/../proc", "/proc"},
		"mount at root":        {"mount_path: /data", "mount_path: /", "mounted at /"},
		"relative mount":       {"mount_path: /data", "mount_path: data", "absolute"},
		"mount with an option": {"mount_path: /data", "mount_path: '/data,readonly'", "absolute"},
		"unknown cap":          {"caps: [CHOWN]", "caps: [SYS_ADMIN]", `"SYS_ADMIN" is not allowed`},
		"net admin":            {"caps: [CHOWN]", "caps: [NET_ADMIN]", "not allowed"},
		"unknown field":        {"port: 8080", "port: 8080\nprivileged: true", "field privileged not found"},
		"a command":            {"port: 8080", "port: 8080\ncommand: sh -c id", "field command not found"},
		"host mount field":     {"  - name: data", "  - name: data\n    host_path: /", "field host_path not found"},
		"bad id":               {"id: demo", "id: Demo_1", "id must match"},
		"http homepage":        {"https://example.com", "http://example.com", "https"},
		"generated not secret": {"    secret: true\n    inject_as", "    inject_as", "secret: true"},
		"injects PORT":         {"inject_as: DEMO_KEY", "inject_as: PORT", "never PORT"},
		"injects twice":        {"env: DEMO_URL", "env: DEMO_KEY", "twice"},
		"no limits":            {"limits: { memory_mb: 256, cpus: 0.5 }", "", "limits.memory_mb"},
		"from without needs":   {`value: "user:{KEY}"`, "from: postgres_url", "needs"},
		"bad kind":             {"kind: app", "kind: plugin", "kind must be"},
		"stack block on app":   {"kind: app", "kind: app\nstack: { repo: 'https://github.com/a/b', branch: main, runtime: go }", "no stack block"},
		"two documents":        {"verified: false", "verified: false\n---\nid: second", "one recipe per file"},
	} {
		doc := strings.Replace(goodApp, c.old, c.new, 1)
		if doc == goodApp {
			t.Fatalf("%s: the replacement did not apply", name)
		}
		_, err := catalog.Parse([]byte(doc))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, c.want)
		}
	}
	for _, ok := range []string{"ghcr.io/owner/name:v1.0.0", "registry.example:5000/owner/name:2", "owner/name@sha256:" + strings.Repeat("a", 64),
		"owner/name:latest@sha256:" + strings.Repeat("b", 64)} {
		if err := catalog.ValidImage(ok); err != nil {
			t.Errorf("ValidImage(%s): %v", ok, err)
		}
	}
}

func TestAStackIsOnlyData(t *testing.T) {
	stack := `
id: demo-stack
name: Demo stack
summary: A stack.
description: A stack.
homepage: https://example.com
licence: MIT
kind: stack
version: "1"
stack:
  repo: https://github.com/example/demo
  branch: main
  runtime: go
  env: [APP_KEY]
`
	if _, err := catalog.Parse([]byte(stack)); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ old, new, want string }{
		"an image":       {"kind: stack", "kind: stack\nimage: example/demo:1", "no app fields"},
		"not github":     {"https://github.com/example/demo", "https://gitlab.com/example/demo", "github.com"},
		"ssh":            {"https://github.com/example/demo", "git@github.com:example/demo", "github.com"},
		"credentials":    {"https://github.com/example/demo", "https://tok@github.com/example/demo", "github.com"},
		"PORT suggested": {"env: [APP_KEY]", "env: [PORT]", "never PORT"},
		"bad process":    {"runtime: go", "runtime: go\n  processes: { cron: 'x' }", "worker:<name>"},
		"no stack block": {"stack:\n  repo: https://github.com/example/demo\n  branch: main\n  runtime: go\n  env: [APP_KEY]\n", "", "needs its stack block"},
	} {
		doc := strings.Replace(stack, c.old, c.new, 1)
		if _, err := catalog.Parse([]byte(doc)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, c.want)
		}
	}
}

func TestLoadRefusesDuplicateIDsAndMisnamedFiles(t *testing.T) {
	fs := fstest.MapFS{"r/demo.yaml": {Data: []byte(goodApp)}, "r/other.yaml": {Data: []byte(goodApp)}}
	if _, err := catalog.LoadFS(fs, "r"); err == nil || !strings.Contains(err.Error(), "already in the catalog") {
		t.Errorf("duplicate ids: %v", err)
	}
	fs = fstest.MapFS{"r/renamed.yaml": {Data: []byte(goodApp)}}
	if _, err := catalog.LoadFS(fs, "r"); err == nil || !strings.Contains(err.Error(), "is named demo.yaml") {
		t.Errorf("a misnamed file: %v", err)
	}
	fs = fstest.MapFS{"r/demo.yaml": {Data: []byte(goodApp)}, "r/bad.yaml": {Data: []byte("id: bad\n")}}
	if _, err := catalog.LoadFS(fs, "r"); err == nil || !strings.Contains(err.Error(), "bad.yaml") {
		t.Errorf("one bad recipe fails the load and is named: %v", err)
	}
	c, err := catalog.LoadFS(fstest.MapFS{"r/demo.yaml": {Data: []byte(goodApp)}}, "r")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := c.Get("demo")
	got, err := catalog.Render(r.Exposes[0].Template, map[string]string{"host": "h.example.com", "port": "443", "KEY": "k"})
	if err != nil || got != "https://h.example.com:443/?k=k" {
		t.Errorf("Render: %q %v", got, err)
	}
	if _, err := catalog.Render("{host}", map[string]string{}); err == nil {
		t.Error("Render with a missing value must fail")
	}
}
