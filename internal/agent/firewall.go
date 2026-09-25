package agent

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
MH-580 J1: a per-server firewall, entirely inside `table inet hosbah`. Every apply renders the
WHOLE table from the team's rules (plus this agent's own safety net) and loads it with `nft -f`,
the same "always render the whole document" approach internal/agent/caddy.go uses for routes — no
drift to reconcile, nothing partially applied. The table is torn down and rebuilt every time
(`add table` then `delete table` then a fresh `table { ... }` block): idempotent, and it never
touches any other nft table (chains, NAT rules, Docker's own tables, anything) already on the box.

Safety net, always rendered first in the input chain, ahead of anything the team asked for:
  - loopback is always accepted
  - established/related connections are always accepted (so replies to outbound traffic — the
    agent's own websocket included — are never dropped)
  - the SSH port (detected from sshd_config, or given explicitly) is always accepted
  - 80/tcp and 443/tcp (Caddy) stay open unless KeepWebPorts is explicitly false

A default policy of "drop" only ever affects the input chain; forward and output are left alone,
so the agent's outbound connection to the control plane (an OUTPUT, not INPUT, decision) can never
be cut by this feature.
*/

const nftTable = "inet hosbah"

// sshdConfigPath is where sshd's own port is read from; overridable in tests.
var sshdConfigPath = "/etc/ssh/sshd_config"

// detectSSHPort reads the Port directive from sshd_config (the last one wins, as sshd itself does)
// and falls back to 22 when the file is missing or has none.
func detectSSHPort() int {
	f, err := os.Open(sshdConfigPath) //#nosec G304 -- fixed, well-known path
	if err != nil {
		return 22
	}
	defer f.Close()
	port := 22
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.EqualFold(fields[0], "Port") {
			if n, err := strconv.Atoi(fields[1]); err == nil && n >= 1 && n <= 65535 {
				port = n
			}
		}
	}
	return port
}

// sanitizeComment makes a rule's free-text comment safe to embed in a double-quoted nft string
// literal: no quote, backslash or control character can end the string early or start a new
// statement. This is the "injection rejected" guarantee — agentproto's Validate() already bounds
// length, this bounds content.
func sanitizeComment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '"' || r == '\\' || r == '\n' || r == '\r' || r == ';' || r == '{' || r == '}':
			b.WriteRune('\'')
		case r < 0x20:
			// drop other control characters entirely
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func portSpec(start, end int) string {
	if start == end {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d-%d", start, end)
}

// renderFirewallRule renders one team rule as zero or more nft statements (one per protocol, when
// Proto is "any"): the pure, side-effect-free part unit-tested with golden strings.
func renderFirewallRule(r agentproto.FirewallRuleSpec) []string {
	verdict := "accept"
	if r.Action == agentproto.FirewallActionDeny {
		verdict = "drop"
	}
	cidr, err := agentproto.NormalizeSourceCIDR(r.SourceCIDR)
	if err != nil {
		cidr = "any" // Validate() already refused this on both sides; never reached in practice
	}
	var saddr string
	if cidr != "any" {
		fam := "ip"
		if strings.Contains(cidr, ":") {
			fam = "ip6"
		}
		saddr = fmt.Sprintf("%s saddr %s ", fam, cidr)
	}
	comment := sanitizeComment(r.Comment)
	suffix := ""
	if comment != "" {
		suffix = fmt.Sprintf(` comment "%s"`, comment)
	}
	protos := []string{r.Proto}
	if r.Proto == agentproto.FirewallProtoAny {
		protos = []string{agentproto.FirewallProtoTCP, agentproto.FirewallProtoUDP}
	}
	lines := make([]string, 0, len(protos))
	for _, p := range protos {
		lines = append(lines, fmt.Sprintf("        %s%s dport %s %s%s", saddr, p, portSpec(r.PortStart, r.PortEnd), verdict, suffix))
	}
	return lines
}

// renderFirewallNFT renders the whole `table inet hosbah` document: the safety net first, then
// every enabled rule in order, then the default policy. sshPort is what the caller resolved
// (detectSSHPort(), or an explicit override) — always passed in so the render stays pure.
func renderFirewallNFT(p agentproto.FirewallApplyParams, sshPort int) string {
	var b strings.Builder
	b.WriteString("add table " + nftTable + "\n")
	b.WriteString("delete table " + nftTable + "\n")
	b.WriteString("table " + nftTable + " {\n")
	b.WriteString("    chain input {\n")
	b.WriteString(fmt.Sprintf("        type filter hook input priority filter; policy %s;\n", p.DefaultPolicy))
	b.WriteString("        ct state established,related accept\n")
	b.WriteString(`        iif "lo" accept` + "\n")
	b.WriteString(fmt.Sprintf("        tcp dport %d accept comment \"hosbah: ssh (always allowed)\"\n", sshPort))
	if p.KeepWebPorts {
		b.WriteString(`        tcp dport {80, 443} accept comment "hosbah: web (caddy)"` + "\n")
	}
	// FirewallRuleSpec carries no Enabled flag of its own: the API only ever sends enabled rules,
	// already in position order, to firewall.apply (see internal/api/firewall.go).
	for _, r := range p.Rules {
		for _, line := range renderFirewallRule(r) {
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("    }\n")
	b.WriteString("}\n")
	return b.String()
}

// firewallHost applies and reports the nftables table. run is swapped for a fake in tests.
type firewallHost struct{ run runFunc }

func newFirewallHost() *firewallHost { return &firewallHost{run: execRun} }

func (h *firewallHost) apply(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.FirewallApplyParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	sshPort := p.SSHPort
	if sshPort == 0 {
		sshPort = detectSSHPort()
	}
	ruleset := renderFirewallNFT(p, sshPort)
	if err := h.applyViaFile(oc, ruleset); err != nil {
		return nil, 1, err
	}
	oc.Info("firewall applied: default policy %s, ssh port %d, %d rule(s)", p.DefaultPolicy, sshPort, len(p.Rules))
	return agentproto.FirewallApplyResult{Ruleset: ruleset, RulesApplied: len(p.Rules), SSHPort: sshPort, DefaultPolicy: p.DefaultPolicy}, 0, nil
}

// applyViaFile writes the ruleset to a private temp file, checks it with `nft -c -f`, and only
// then loads it with `nft -f`, so a syntax error never touches the live table.
func (h *firewallHost) applyViaFile(oc *OpContext, ruleset string) error {
	path := agentproto.WorkDir + "/firewall.nft"
	if err := os.MkdirAll(agentproto.WorkDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(ruleset), 0o600); err != nil {
		return err
	}
	if _, err := h.run(oc.Ctx, nil, "nft", "-c", "-f", path); err != nil {
		return fmt.Errorf("the ruleset did not pass nft's own check, nothing was applied: %w", err)
	}
	if _, err := h.run(oc.Ctx, nil, "nft", "-f", path); err != nil {
		return err
	}
	return nil
}

func (h *firewallHost) status(oc *OpContext) (any, int, error) {
	out, err := h.run(oc.Ctx, nil, "nft", "list", "table", nftTable)
	if err != nil {
		// nft exits non-zero when the table does not exist yet — not applied is a valid state.
		return agentproto.FirewallStatusResult{Active: false}, 0, nil
	}
	return agentproto.FirewallStatusResult{Active: true, Ruleset: string(out)}, 0, nil
}
