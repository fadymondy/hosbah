package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A clone only falls back to the deploy key once one was generated here, and then it goes over
// SSH with that key alone and github.com's pinned host key.
func TestDeployKeyGit(t *testing.T) {
	dir := t.TempDir()
	deployKeyDirOverride = dir
	defer func() { deployKeyDirOverride = "" }()

	if _, _, ok := deployKeyGit(); ok {
		t.Fatal("no key was generated, yet the clone would use one")
	}
	priv := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(priv, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, cfg, ok := deployKeyGit()
	if !ok {
		t.Fatal("a generated key is not used")
	}
	ssh := ""
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			ssh = e
		}
	}
	for _, want := range []string{"-i " + priv, "IdentitiesOnly=yes", "StrictHostKeyChecking=yes", "UserKnownHostsFile=" + filepath.Join(dir, "known_hosts")} {
		if !strings.Contains(ssh, want) {
			t.Fatalf("GIT_SSH_COMMAND %q lacks %q", ssh, want)
		}
	}
	if got := strings.Join(cfg, " "); !strings.Contains(got, "protocol.ssh.allow=always") || !strings.Contains(got, "url.ssh://git@github.com/.insteadOf=https://github.com/") {
		t.Fatalf("git options: %q", got)
	}
	known, _ := os.ReadFile(filepath.Join(dir, "known_hosts"))
	if !strings.Contains(string(known), githubKnownHostsLine) {
		t.Fatal("github.com's host key is not pinned")
	}
}
