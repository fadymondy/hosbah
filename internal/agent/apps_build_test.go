package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// safeJoin resolves a dockerfile_path/build_context against the checkout root, refusing anything
// that escapes it — including through a symlink planted inside the checkout.
func TestSafeJoinStaysInsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docker", "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := safeJoin(root, ""); !ok {
		t.Fatal("empty rel (root itself) should resolve")
	}
	if p, ok := safeJoin(root, "docker/Dockerfile"); !ok || !strings.HasSuffix(filepath.ToSlash(p), "docker/Dockerfile") {
		t.Fatalf("docker/Dockerfile: %q %v", p, ok)
	}
	for _, bad := range []string{"..", "../etc/passwd", "/etc/passwd", "a/../../etc/passwd"} {
		if _, ok := safeJoin(root, bad); ok {
			t.Errorf("%q should not resolve inside root", bad)
		}
	}

	if runtime.GOOS == "windows" {
		// os.Symlink needs a privilege Windows CI runners do not reliably grant; the traversal
		// cases above already cover the charset/".." half of safeJoin's contract.
		return
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink not supported here: %v", err)
	}
	if _, ok := safeJoin(root, "escape/secret"); ok {
		t.Error("a symlink planted inside the checkout should not let a path escape it")
	}
}

// detect(), given a dockerfilePath, looks for the Dockerfile there instead of the checkout root.
func TestDetectHonorsDockerfilePath(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "docker", "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oc, _ := opCtx(t, agentproto.DetectParams{})

	// No Dockerfile at the default location, and no dockerfile_path override: falls through to
	// Railpack, which is not installed in this test environment — a clear error, not a false
	// "dockerfile" runtime.
	if _, _, err := detect(oc, src, t.TempDir(), ""); err == nil {
		t.Fatal("expected an error when the default Dockerfile does not exist and Railpack is unavailable")
	}

	r, code, err := detect(oc, src, t.TempDir(), "docker/Dockerfile")
	if err != nil || code != 0 || r.Runtime != "dockerfile" {
		t.Fatalf("docker/Dockerfile: %+v %d %v", r, code, err)
	}
}

// buildArgArgs never carries a value — only --build-arg NAME, sorted. The value only ever reaches
// docker through envPairs, fed to the child's own process environment (oc.RunEnv), never an argv.
func TestBuildArgArgsCarriesNoSecretValue(t *testing.T) {
	env := map[string]string{
		"B_NAME": "super-secret-value",
		"A_NAME": "another-secret",
	}
	args := buildArgArgs(env)
	if got, want := strings.Join(args, " "), "--build-arg A_NAME --build-arg B_NAME"; got != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for _, secret := range env {
		if strings.Contains(strings.Join(args, " "), secret) {
			t.Fatalf("a secret value leaked into the argv: %v", args)
		}
	}
	// envPairs is where the values actually go — as NAME=VALUE pairs for the child's environment,
	// never as an argv.
	pairs := envPairs(env)
	found := map[string]bool{}
	for _, p := range pairs {
		for k, v := range env {
			if p == k+"="+v {
				found[k] = true
			}
		}
	}
	if len(found) != len(env) {
		t.Fatalf("envPairs did not carry every value: %v (got %v)", env, pairs)
	}
}

func TestOrDefault(t *testing.T) {
	if orDefault("", "Dockerfile") != "Dockerfile" {
		t.Fatal("empty should default")
	}
	if orDefault("custom/Dockerfile", "Dockerfile") != "custom/Dockerfile" {
		t.Fatal("non-empty should pass through")
	}
}
