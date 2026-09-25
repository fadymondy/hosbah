package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// FirewallTools are MH-580 J1: a per-server firewall. Rules are staged in firewall_rules and only
// take effect on the server when server_firewall_apply renders and loads them with nft (see
// internal/agent/firewall.go). SSH and 80/443 are always kept open unless the caller explicitly
// asks for a drop default policy, which needs confirm="drop".
func FirewallTools() []Tool {
	ruleArg := str("Firewall rule id")
	return []Tool{
		{
			Name: "list_firewall_rules", Scope: scopes.Read,
			Description: "A server's staged firewall rules, in the order they would be applied.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": str("Server id or name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				rows, err := env.Q.ListFirewallRulesOfServer(ctx, db.ListFirewallRulesOfServerParams{ServerID: srv.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("could not list firewall rules")
				}
				if len(rows) == 0 {
					return "No firewall rules staged. SSH and (unless overridden) 80/443 stay open regardless.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					fmt.Fprintf(&b, "  %-6s %-4s %s%s %-22s %s  (id %s%s)\n", r.Action, r.Proto, portRange(int(r.PortStart), int(r.PortEnd)),
						strings.Repeat(" ", max0(0, 12-len(portRange(int(r.PortStart), int(r.PortEnd))))), r.SourceCidr, r.Comment, r.ID,
						onOff(r.Enabled, "", ", disabled"))
				}
				return b.String(), nil
			},
		},
		{
			Name: "add_firewall_rule", Scope: scopes.Write,
			Description: "Stage a firewall rule for a server. Not applied to the server until server_firewall_apply.",
			InputSchema: schema([]string{"server", "action", "proto", "port_start", "port_end"}, map[string]any{
				"server": str("Server id or name"), "action": map[string]any{"type": "string", "enum": []string{"allow", "deny"}},
				"proto":       map[string]any{"type": "string", "enum": []string{"tcp", "udp", "any"}},
				"port_start":  map[string]any{"type": "integer", "description": "1-65535"},
				"port_end":    map[string]any{"type": "integer", "description": "1-65535, >= port_start"},
				"source_cidr": str(`"any", an IP, or a CIDR (default "any")`),
				"comment":     str("Optional, up to 200 characters"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				cidr := args.Str("source_cidr")
				if cidr == "" {
					cidr = "any"
				}
				spec := agentproto.FirewallRuleSpec{Action: args.Str("action"), Proto: args.Str("proto"), PortStart: args.Int("port_start", 0, 0, 65535),
					PortEnd: args.Int("port_end", 0, 0, 65535), SourceCIDR: cidr, Comment: args.Str("comment")}
				if err := spec.Validate(); err != nil {
					return "", err
				}
				rows, _ := env.Q.ListFirewallRulesOfServer(ctx, db.ListFirewallRulesOfServerParams{ServerID: srv.ID, TeamID: env.Caller.TeamID})
				r, err := env.Q.CreateFirewallRule(ctx, db.CreateFirewallRuleParams{TeamID: env.Caller.TeamID, ServerID: srv.ID,
					Action: spec.Action, Proto: spec.Proto, PortStart: int32(spec.PortStart), PortEnd: int32(spec.PortEnd),
					SourceCidr: spec.SourceCIDR, Comment: spec.Comment, Enabled: true, Position: int32(len(rows)), CreatedBy: env.Caller.UserID})
				if err != nil {
					return "", errors.New("could not add the firewall rule")
				}
				return fmt.Sprintf("Staged %s %s %s from %s (id %s). Call server_firewall_apply to load it.", r.Action, r.Proto,
					portRange(int(r.PortStart), int(r.PortEnd)), r.SourceCidr, r.ID), nil
			},
		},
		{
			Name: "remove_firewall_rule", Scope: scopes.Write,
			Description: "Remove a staged firewall rule. Not applied to the server until server_firewall_apply.",
			InputSchema: schema([]string{"rule"}, map[string]any{"rule": ruleArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				r, err := env.Q.FirewallRuleInTeam(ctx, db.FirewallRuleInTeamParams{TeamID: env.Caller.TeamID, ID: args.Str("rule")})
				if err != nil {
					return "", errors.New("firewall rule not found")
				}
				if err := env.Q.DeleteFirewallRuleOfTeam(ctx, db.DeleteFirewallRuleOfTeamParams{ID: r.ID, TeamID: env.Caller.TeamID}); err != nil {
					return "", errors.New("could not remove the firewall rule")
				}
				return "Removed. Call server_firewall_apply to load the change.", nil
			},
		},
		{
			Name: "server_firewall_apply", Scope: scopes.Destructive, Destructive: true, ResourceType: "server",
			Description: "Render the server's staged rules (SSH and, unless keep_web_ports is false, 80/443 always kept) into " +
				"`table inet hosbah` and load it with nft — a wrong rule set can cut the server off, so confirm must equal the " +
				"server's name. Switching default_policy to \"drop\" needs confirm to instead equal \"drop\".",
			InputSchema: schema([]string{"server", "confirm"}, map[string]any{
				"server":         str("Server id or name"),
				"default_policy": map[string]any{"type": "string", "enum": []string{"accept", "drop"}, "description": "Defaults to accept"},
				"ssh_port":       map[string]any{"type": "integer", "description": "Defaults to 22"},
				"keep_web_ports": map[string]any{"type": "boolean", "description": "Defaults to true: keep 80/443 open for Caddy"},
				"confirm":        str(`The server's name, exactly — or "drop" when default_policy is drop`),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				policy := args.Str("default_policy")
				if policy == "" {
					policy = agentproto.FirewallPolicyAccept
				}
				if !agentproto.ValidFirewallPolicy(policy) {
					return "", errors.New("default_policy must be accept or drop")
				}
				if policy == agentproto.FirewallPolicyDrop {
					if err := RequireConfirm(args, agentproto.FirewallPolicyDrop); err != nil {
						return "", err
					}
				} else if err := RequireConfirm(args, srv.Name); err != nil {
					return "", err
				}
				sshPort := args.Int("ssh_port", 22, 1, 65535)
				keepWeb := true
				if v, ok := args["keep_web_ports"].(bool); ok {
					keepWeb = v
				}
				rows, err := env.Q.ListFirewallRulesOfServer(ctx, db.ListFirewallRulesOfServerParams{ServerID: srv.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("could not list firewall rules")
				}
				p := agentproto.FirewallApplyParams{DefaultPolicy: policy, SSHPort: sshPort, KeepWebPorts: keepWeb}
				for _, r := range rows {
					if !r.Enabled {
						continue
					}
					p.Rules = append(p.Rules, agentproto.FirewallRuleSpec{Action: r.Action, Proto: r.Proto, PortStart: int(r.PortStart),
						PortEnd: int(r.PortEnd), SourceCIDR: r.SourceCidr, Comment: r.Comment})
				}
				if err := p.Validate(); err != nil {
					return "", err
				}
				if _, err := env.Q.UpsertFirewallSetting(ctx, db.UpsertFirewallSettingParams{TeamID: env.Caller.TeamID, ServerID: srv.ID,
					DefaultPolicy: policy, SshPort: int32(sshPort), UpdatedBy: env.Caller.UserID}); err != nil {
					return "", errors.New("could not save the firewall settings")
				}
				var res agentproto.FirewallApplyResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindFirewallApply, p, &res); err != nil {
					return "", err
				}
				return fmt.Sprintf("Applied: default %s, ssh port %d, %d rule(s).", policy, sshPort, len(p.Rules)), nil
			},
		},
		{
			Name: "server_firewall_status", Scope: scopes.Read,
			Description: "The active `table inet hosbah` ruleset, read live from the server.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": str("Server id or name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				var res agentproto.FirewallStatusResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindFirewallStatus, struct{}{}, &res); err != nil {
					return "", err
				}
				if !res.Active {
					return "No active hosbah firewall table on this server.", nil
				}
				return res.Ruleset, nil
			},
		},
	}
}

func portRange(start, end int) string {
	if start == end {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d-%d", start, end)
}

func max0(a, b int) int {
	if a > b {
		return a
	}
	return b
}
