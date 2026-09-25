package agentproto

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// MH-580 J1: a per-server firewall, rendered by the agent into its own nftables table (`table inet
// hosbah`) and applied with `nft -f` — never touching any other table on the box. Two ops:
// firewall.apply (renders and loads the whole ruleset; idempotent) and firewall.status (reads back
// what is actually loaded). Validation here runs on both sides of the wire, so a rule the control
// plane refuses to store can never reach the agent, and an agent running an older/newer protocol
// version still rejects anything it does not understand.

const (
	// KindFirewallApply renders the team's rules (plus the agent's own safety net) into
	// `table inet hosbah` and loads it with `nft -f`.
	KindFirewallApply = "firewall.apply"
	// KindFirewallStatus reads back the active ruleset of `table inet hosbah` (nft list table).
	KindFirewallStatus = "firewall.status"
)

// FirewallAction is what a rule does to matching traffic.
const (
	FirewallActionAllow = "allow"
	FirewallActionDeny  = "deny"
)

// FirewallProto is the protocol a rule matches, or "any" for both tcp and udp.
const (
	FirewallProtoTCP = "tcp"
	FirewallProtoUDP = "udp"
	FirewallProtoAny = "any"
)

// FirewallPolicy is the input chain's default verdict for traffic no rule matches.
const (
	FirewallPolicyAccept = "accept"
	FirewallPolicyDrop   = "drop"
)

// FirewallRuleSpec is one firewall rule, as stored and as sent to the agent.
type FirewallRuleSpec struct {
	ID string `json:"id,omitempty"`
	// Action: allow or deny.
	Action string `json:"action"`
	// Proto: tcp, udp or any.
	Proto string `json:"proto"`
	// PortStart/PortEnd: an inclusive port range; a single port has PortStart == PortEnd.
	PortStart int `json:"port_start"`
	PortEnd   int `json:"port_end"`
	// SourceCIDR: "any", a bare IP (normalized to a /32 or /128) or a CIDR.
	SourceCIDR string `json:"source_cidr"`
	Comment    string `json:"comment,omitempty"`
}

// FirewallApplyParams: firewall.apply. Rules must already be in position order (lowest first);
// the agent renders them in the order given. KeepWebPorts, when true (the default the API sends
// unless a caller explicitly overrides it), always keeps 80/tcp and 443/tcp open regardless of the
// team's rules, because Caddy needs them. SSHPort of 0 asks the agent to detect it from
// /etc/ssh/sshd_config (falling back to 22); either way the detected/given port is always allowed
// and reported back in the result, so the caller can never be locked out silently.
type FirewallApplyParams struct {
	DefaultPolicy string             `json:"default_policy"`
	SSHPort       int                `json:"ssh_port,omitempty"`
	KeepWebPorts  bool               `json:"keep_web_ports"`
	Rules         []FirewallRuleSpec `json:"rules"`
}

// FirewallApplyResult: firewall.apply.
type FirewallApplyResult struct {
	Ruleset       string `json:"ruleset"`
	RulesApplied  int    `json:"rules_applied"`
	SSHPort       int    `json:"ssh_port"`
	DefaultPolicy string `json:"default_policy"`
}

// FirewallStatusResult: firewall.status.
type FirewallStatusResult struct {
	Active  bool   `json:"active"` // whether `table inet hosbah` exists at all
	Ruleset string `json:"ruleset"`
}

// ValidFirewallAction reports whether s is a known rule action.
func ValidFirewallAction(s string) bool { return s == FirewallActionAllow || s == FirewallActionDeny }

// ValidFirewallProto reports whether s is a known rule protocol.
func ValidFirewallProto(s string) bool {
	return s == FirewallProtoTCP || s == FirewallProtoUDP || s == FirewallProtoAny
}

// ValidFirewallPolicy reports whether s is a known default policy.
func ValidFirewallPolicy(s string) bool { return s == FirewallPolicyAccept || s == FirewallPolicyDrop }

// NormalizeSourceCIDR turns "", "any", a bare IP or a CIDR into a canonical CIDR string ("any" for
// no restriction). An error means s is none of those.
func NormalizeSourceCIDR(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "any") {
		return "any", nil
	}
	if ip := net.ParseIP(s); ip != nil {
		if ip.To4() != nil {
			return s + "/32", nil
		}
		return s + "/128", nil
	}
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return "", fmt.Errorf("source must be \"any\", an IP address or a CIDR: %w", err)
	}
	return ipnet.String(), nil
}

// Validate checks one firewall rule, on both sides of the wire.
func (r FirewallRuleSpec) Validate() error {
	if !ValidFirewallAction(r.Action) {
		return errors.New("action must be allow or deny")
	}
	if !ValidFirewallProto(r.Proto) {
		return errors.New("proto must be tcp, udp or any")
	}
	if r.PortStart < 1 || r.PortStart > 65535 || r.PortEnd < r.PortStart || r.PortEnd > 65535 {
		return errors.New("ports must be 1-65535, and the end of a range must not be before its start")
	}
	if len(r.Comment) > 200 {
		return errors.New("comment is too long")
	}
	if _, err := NormalizeSourceCIDR(r.SourceCIDR); err != nil {
		return err
	}
	return nil
}

// Validate checks firewall.apply's params, on both sides of the wire.
func (p FirewallApplyParams) Validate() error {
	if !ValidFirewallPolicy(p.DefaultPolicy) {
		return errors.New("default_policy must be accept or drop")
	}
	if p.SSHPort != 0 && (p.SSHPort < 1 || p.SSHPort > 65535) {
		return errors.New("ssh_port must be 1-65535")
	}
	if len(p.Rules) > 500 {
		return errors.New("too many rules")
	}
	for _, r := range p.Rules {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// FirewallPreset is a named template of common rules, expanded server-side (internal/api) and over
// MCP so both surfaces offer the same three presets with the same ports.
type FirewallPreset struct {
	Name        string
	Description string
}

// FirewallPresets: web (80/443 tcp from anywhere), ssh (22 tcp from anywhere — redundant with the
// agent's own safety net, but explicit in the team's rule list), postgres-from-cidr (5432 tcp,
// source CIDR required).
var FirewallPresets = []FirewallPreset{
	{Name: "web", Description: "Allow HTTP and HTTPS (80, 443/tcp) from anywhere"},
	{Name: "ssh", Description: "Allow SSH (22/tcp) from anywhere"},
	{Name: "postgres-from-cidr", Description: "Allow Postgres (5432/tcp) from one CIDR"},
}

// BuildFirewallPreset expands a preset name (plus a source CIDR for the ones that need one) into
// the rule(s) it stands for. Shared by internal/api and internal/mcp so "presets" mean the same
// thing everywhere.
func BuildFirewallPreset(name, sourceCIDR string) ([]FirewallRuleSpec, error) {
	switch name {
	case "web":
		return []FirewallRuleSpec{
			{Action: FirewallActionAllow, Proto: FirewallProtoTCP, PortStart: 80, PortEnd: 80, SourceCIDR: "any", Comment: "web (preset)"},
			{Action: FirewallActionAllow, Proto: FirewallProtoTCP, PortStart: 443, PortEnd: 443, SourceCIDR: "any", Comment: "web (preset)"},
		}, nil
	case "ssh":
		return []FirewallRuleSpec{
			{Action: FirewallActionAllow, Proto: FirewallProtoTCP, PortStart: 22, PortEnd: 22, SourceCIDR: "any", Comment: "ssh (preset)"},
		}, nil
	case "postgres-from-cidr":
		cidr, err := NormalizeSourceCIDR(sourceCIDR)
		if err != nil {
			return nil, err
		}
		if cidr == "any" {
			return nil, errors.New("postgres-from-cidr needs a source CIDR")
		}
		return []FirewallRuleSpec{
			{Action: FirewallActionAllow, Proto: FirewallProtoTCP, PortStart: 5432, PortEnd: 5432, SourceCIDR: cidr, Comment: "postgres (preset)"},
		}, nil
	default:
		return nil, fmt.Errorf("unknown preset %q", name)
	}
}
