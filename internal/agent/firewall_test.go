package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func TestRenderFirewallNFTGoldenAcceptDefaultKeepsWebAndSSH(t *testing.T) {
	p := agentproto.FirewallApplyParams{
		DefaultPolicy: agentproto.FirewallPolicyAccept,
		KeepWebPorts:  true,
		Rules: []agentproto.FirewallRuleSpec{
			{Action: agentproto.FirewallActionAllow, Proto: agentproto.FirewallProtoTCP, PortStart: 5432, PortEnd: 5432,
				SourceCIDR: "10.0.0.0/24", Comment: "postgres"},
		},
	}
	got := renderFirewallNFT(p, 22)
	want := `add table inet hosbah
delete table inet hosbah
table inet hosbah {
    chain input {
        type filter hook input priority filter; policy accept;
        ct state established,related accept
        iif "lo" accept
        meta l4proto { icmp, ipv6-icmp } accept comment "hosbah: icmp (ping, ipv6 neighbour discovery)"
        udp dport { 68, 546 } accept comment "hosbah: dhcp client"
        iifname "docker0" accept comment "hosbah: containers to host"
        iifname "br-*" accept comment "hosbah: containers to host"
        tcp dport 22 accept comment "hosbah: ssh (always allowed)"
        tcp dport {80, 443} accept comment "hosbah: web (caddy)"
        ip saddr 10.0.0.0/24 tcp dport 5432 accept comment "postgres"
    }
}
`
	if got != want {
		t.Fatalf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderFirewallNFTGoldenDropPolicyWithoutWebPorts(t *testing.T) {
	p := agentproto.FirewallApplyParams{
		DefaultPolicy: agentproto.FirewallPolicyDrop,
		KeepWebPorts:  false,
		SSHPort:       2222,
		Rules: []agentproto.FirewallRuleSpec{
			{Action: agentproto.FirewallActionDeny, Proto: agentproto.FirewallProtoAny, PortStart: 23, PortEnd: 23, SourceCIDR: "any"},
		},
	}
	got := renderFirewallNFT(p, 2222)
	want := `add table inet hosbah
delete table inet hosbah
table inet hosbah {
    chain input {
        type filter hook input priority filter; policy drop;
        ct state established,related accept
        iif "lo" accept
        meta l4proto { icmp, ipv6-icmp } accept comment "hosbah: icmp (ping, ipv6 neighbour discovery)"
        udp dport { 68, 546 } accept comment "hosbah: dhcp client"
        iifname "docker0" accept comment "hosbah: containers to host"
        iifname "br-*" accept comment "hosbah: containers to host"
        tcp dport 2222 accept comment "hosbah: ssh (always allowed)"
        tcp dport 23 drop
        udp dport 23 drop
    }
}
`
	if got != want {
		t.Fatalf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestRenderFirewallNFTAlwaysAllowsSSHRegardlessOfRules: even a "deny everything" rule set, and
// even a default policy of drop, never removes the SSH accept line — it is written unconditionally
// before any team rule, and nft's first-match semantics mean nothing after it can undo it for the
// SSH port itself (a later "drop 22" rule would never be reached).
func TestRenderFirewallNFTAlwaysAllowsSSHRegardlessOfRules(t *testing.T) {
	p := agentproto.FirewallApplyParams{
		DefaultPolicy: agentproto.FirewallPolicyDrop,
		Rules: []agentproto.FirewallRuleSpec{
			{Action: agentproto.FirewallActionDeny, Proto: agentproto.FirewallProtoTCP, PortStart: 1, PortEnd: 65535, SourceCIDR: "any"},
		},
	}
	got := renderFirewallNFT(p, 22)
	sshLine := `tcp dport 22 accept comment "hosbah: ssh (always allowed)"`
	if !strings.Contains(got, sshLine) {
		t.Fatalf("ssh accept line missing:\n%s", got)
	}
	if strings.Index(got, sshLine) > strings.Index(got, "dport 1-65535") {
		t.Fatalf("ssh accept must come before the team's catch-all deny:\n%s", got)
	}
}

func TestRenderFirewallRuleCommentInjectionIsNeutralized(t *testing.T) {
	r := agentproto.FirewallRuleSpec{Action: agentproto.FirewallActionAllow, Proto: agentproto.FirewallProtoTCP,
		PortStart: 80, PortEnd: 80, SourceCIDR: "any", Comment: `"; flush ruleset; #`}
	lines := renderFirewallRule(r)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	line := lines[0]
	// The quoted comment string must stay balanced (exactly the two quotes wrapping it) and
	// contain no raw quote, backslash or semicolon that could close it early and start a new nft
	// statement — the content itself (e.g. the words "flush ruleset") is harmless once it can
	// never break out of the string.
	if strings.Count(line, `"`) != 2 {
		t.Fatalf("comment quoting broken (more than the wrapping pair): %q", line)
	}
	start := strings.Index(line, `"`)
	end := strings.LastIndex(line, `"`)
	inner := line[start+1 : end]
	if strings.ContainsAny(inner, `";\`) {
		t.Fatalf("comment still contains an unescaped quote/backslash/semicolon: %q", line)
	}
}

func TestFirewallRuleValidateRejectsBadInput(t *testing.T) {
	cases := []agentproto.FirewallRuleSpec{
		{Action: "smash", Proto: "tcp", PortStart: 1, PortEnd: 1, SourceCIDR: "any"},
		{Action: "allow", Proto: "sctp", PortStart: 1, PortEnd: 1, SourceCIDR: "any"},
		{Action: "allow", Proto: "tcp", PortStart: 0, PortEnd: 1, SourceCIDR: "any"},
		{Action: "allow", Proto: "tcp", PortStart: 100, PortEnd: 1, SourceCIDR: "any"},
		{Action: "allow", Proto: "tcp", PortStart: 1, PortEnd: 70000, SourceCIDR: "any"},
		{Action: "allow", Proto: "tcp", PortStart: 1, PortEnd: 1, SourceCIDR: "not a cidr; rm -rf /"},
	}
	for i, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("case %d: want an error, got none: %+v", i, c)
		}
	}
}

func TestDetectSSHPortReadsSshdConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sshd_config")
	old := sshdConfigPath
	sshdConfigPath = path
	defer func() { sshdConfigPath = old }()

	if err := os.WriteFile(path, []byte("# comment\nPort 2200\nProtocol 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := detectSSHPort(); got != 2200 {
		t.Fatalf("got %d, want 2200", got)
	}
}

func TestDetectSSHPortDefaultsTo22WhenMissing(t *testing.T) {
	old := sshdConfigPath
	sshdConfigPath = filepath.Join(t.TempDir(), "does-not-exist")
	defer func() { sshdConfigPath = old }()
	if got := detectSSHPort(); got != 22 {
		t.Fatalf("got %d, want 22", got)
	}
}

func TestFirewallApplyChecksBeforeApplying(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{}, fail: map[string]string{"nft -c -f": "syntax error"}}
	h := &firewallHost{run: f.run}
	oc, _ := opCtx(t, agentproto.FirewallApplyParams{DefaultPolicy: "accept", KeepWebPorts: true})
	_, code, err := h.apply(oc)
	if err == nil || code == 0 {
		t.Fatalf("want a failure when nft -c rejects the ruleset, got code=%d err=%v", code, err)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c.argv, "nft -f") {
			t.Fatalf("nft -f (apply) must never run after nft -c (check) failed: %v", f.calls)
		}
	}
}

func TestFirewallApplyRunsCheckThenApply(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{}, fail: map[string]string{}}
	h := &firewallHost{run: f.run}
	oc, _ := opCtx(t, agentproto.FirewallApplyParams{DefaultPolicy: "accept", KeepWebPorts: true})
	res, code, err := h.apply(oc)
	if err != nil || code != 0 {
		t.Fatalf("apply: %v (%d)", err, code)
	}
	out := res.(agentproto.FirewallApplyResult)
	if out.SSHPort == 0 || !strings.Contains(out.Ruleset, "table inet hosbah") {
		t.Fatalf("unexpected result: %+v", out)
	}
	if len(f.calls) != 2 || !strings.HasPrefix(f.calls[0].argv, "nft -c -f") || !strings.HasPrefix(f.calls[1].argv, "nft -f") {
		t.Fatalf("want check then apply, got: %v", f.calls)
	}
}

func TestFirewallStatusNotActiveWhenTableMissing(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{}, fail: map[string]string{"nft list table": "No such file or directory"}}
	h := &firewallHost{run: f.run}
	oc, _ := opCtx(t, struct{}{})
	res, code, err := h.status(oc)
	if err != nil || code != 0 {
		t.Fatalf("status: %v (%d)", err, code)
	}
	if res.(agentproto.FirewallStatusResult).Active {
		t.Fatalf("want inactive when nft list fails")
	}
}

func TestFirewallStatusReturnsRuleset(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"nft list table": "table inet hosbah {\n}\n"}}
	h := &firewallHost{run: f.run}
	oc, _ := opCtx(t, struct{}{})
	res, code, err := h.status(oc)
	if err != nil || code != 0 {
		t.Fatalf("status: %v (%d)", err, code)
	}
	out := res.(agentproto.FirewallStatusResult)
	if !out.Active || !strings.Contains(out.Ruleset, "table inet hosbah") {
		t.Fatalf("unexpected result: %+v", out)
	}
}
