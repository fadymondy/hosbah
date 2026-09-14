package providers

import (
	"context"
	"strings"
	"testing"
)

type stubProvider struct{ Provider }

func (stubProvider) Key() string { return "stub-test" }

func TestRegistry(t *testing.T) {
	Register("stub-test", func(cred Credential) (Provider, error) { return stubProvider{}, nil })
	p, err := New("stub-test", Credential{})
	if err != nil || p.Key() != "stub-test" {
		t.Fatalf("New(stub-test) = %v, %v", p, err)
	}
	if _, err := New("nope", Credential{}); err == nil {
		t.Fatal("New(unknown) succeeded")
	}
	found := false
	for _, k := range Keys() {
		found = found || k == "stub-test"
	}
	if !found {
		t.Fatal("Keys() misses stub-test")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate Register did not panic")
		}
	}()
	Register("stub-test", func(Credential) (Provider, error) { return nil, context.Canceled })
}

func TestAgentUserData(t *testing.T) {
	got := AgentUserData(`curl -fsSL "https://x/install/tok" | sh`, []string{"ssh-ed25519 AAAA one", "  ", "ssh-ed25519 BBBB two\n"})
	want := `#cloud-config
users:
  - name: cloudy
    groups: [sudo]
    shell: /bin/bash
    sudo: "ALL=(ALL) NOPASSWD:ALL"
    lock_passwd: true
    ssh_authorized_keys:
      - "ssh-ed25519 AAAA one"
      - "ssh-ed25519 BBBB two"
package_update: false
runcmd:
  - ["sh", "-c", "curl -fsSL \"https://x/install/tok\" | sh"]
`
	if got != want {
		t.Fatalf("AgentUserData:\n%s\nwant:\n%s", got, want)
	}
}

func TestAgentUserDataEscapesStructure(t *testing.T) {
	got := AgentUserData("echo a\nruncmd: evil", nil)
	if strings.Contains(got, "ssh_authorized_keys") {
		t.Fatal("empty key list still emitted ssh_authorized_keys")
	}
	if strings.Count(got, "\nruncmd:") != 1 || !strings.Contains(got, `"echo a\nruncmd: evil"`) {
		t.Fatalf("newline in the command was not escaped:\n%s", got)
	}
	if strings.Contains(AgentUserData("", nil), "runcmd") {
		t.Fatal("empty command still emitted runcmd")
	}
}
