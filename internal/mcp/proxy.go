package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/deploy"
	"github.com/fadymondy/cloudy/internal/scopes"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

// ProxyTools are the Caddy proxy manager: a hostname in front of any upstream a server can reach.
func ProxyTools() []Tool {
	proxyArg := str("Proxy host id or hostname")
	tlsArg := map[string]any{"type": "string", "enum": []string{"auto", "internal", "off"},
		"description": "auto: public certificate once verified; internal: Caddy's local CA; off: plain HTTP"}
	upstreamArg := str("host:port, http://host:port or https://host:port (e.g. 10.0.0.5:5000)")
	return []Tool{
		{
			Name: "list_proxy_hosts", Scope: scopes.Read,
			Description: "The team's proxy hosts: hostname → upstream, server, TLS mode, enabled, verified.",
			InputSchema: schema(nil, map[string]any{"server": str("Optional: only this server (id or name)")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				rows, err := env.Q.ListProxyHostsOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list proxy hosts")
				}
				only := args.Str("server")
				var b strings.Builder
				for _, p := range rows {
					if only != "" && only != p.ServerID && only != p.ServerName {
						continue
					}
					fmt.Fprintf(&b, "  %-36s → %-28s on %-16s tls %-8s %s %s  (id %s)\n", p.Hostname, p.Upstream, p.ServerName, p.Tls,
						onOff(p.Enabled, "enabled", "disabled"), onOff(p.VerifiedAt != nil, "verified", "unverified"), p.ID)
				}
				if b.Len() == 0 {
					return "No proxy hosts.", nil
				}
				return b.String(), nil
			},
		},
		{
			Name: "add_proxy_host", Scope: scopes.Write,
			Description: "Put an upstream behind a hostname on one of the team's servers (its Caddy terminates TLS and proxies, websockets included). " +
				"If the team's Cloudflare credential holds the zone, the DNS record is created and the hostname verified at once; otherwise " +
				"verify it with verify_proxy_host after adding the TXT record shown.",
			InputSchema: schema([]string{"server", "hostname", "upstream"}, map[string]any{
				"server": str("Server id or name"), "hostname": str("e.g. nas.example.com"), "upstream": upstreamArg, "tls": tlsArg,
				"skip_verify": map[string]any{"type": "boolean", "description": "https upstream with a self-signed certificate"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				p, err := s.CreateProxyHost(ctx, env.Q, env.Caller.TeamID, mcpActor(env), deploy.ProxyHostInput{Server: args.Str("server"),
					Hostname: args.Str("hostname"), Upstream: args.Str("upstream"), TLS: args.Str("tls"), SkipVerify: args.Bool("skip_verify")})
				if err != nil {
					return "", err
				}
				return proxySummary("Added", p), nil
			},
		},
		{
			Name: "update_proxy_host", Scope: scopes.Write,
			Description: "Change a proxy host's upstream, TLS mode or enabled flag; applied on its server at once.",
			InputSchema: schema([]string{"proxy"}, map[string]any{
				"proxy": proxyArg, "upstream": upstreamArg, "tls": tlsArg,
				"enabled":     map[string]any{"type": "boolean"},
				"skip_verify": map[string]any{"type": "boolean"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				p, err := findProxy(ctx, env, args)
				if err != nil {
					return "", err
				}
				in := deploy.ProxyHostInput{Upstream: args.Str("upstream"), TLS: args.Str("tls"), SkipVerify: args.Bool("skip_verify")}
				if v, ok := args["enabled"].(bool); ok {
					in.Enabled = &v
				}
				u, err := s.UpdateProxyHost(ctx, env.Q, env.Caller.TeamID, mcpActor(env), p, in)
				if err != nil {
					return "", err
				}
				return proxySummary("Updated", u), nil
			},
		},
		{
			Name: "verify_proxy_host", Scope: scopes.Write,
			Description: "Check a proxy host's _cloudy-verify TXT record. Verification gates its public certificate.",
			InputSchema: schema([]string{"proxy"}, map[string]any{"proxy": proxyArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				p, err := findProxy(ctx, env, args)
				if err != nil {
					return "", err
				}
				_, msg, err := s.VerifyProxyHost(ctx, env.Q, env.Caller.TeamID, mcpActor(env), p)
				return msg, err
			},
		},
		{
			Name: "remove_proxy_host", Scope: scopes.Write,
			Description: "Remove a proxy host from its server's Caddy and from Cloudy (and the DNS record Cloudy created for it, if any).",
			InputSchema: schema([]string{"proxy"}, map[string]any{"proxy": proxyArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				p, err := findProxy(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := s.DeleteProxyHost(ctx, env.Q, env.Caller.TeamID, mcpActor(env), p); err != nil {
					return "", err
				}
				return "Removed " + p.Hostname + ".", nil
			},
		},
	}
}

func findProxy(ctx context.Context, env *Env, args Args) (db.ProxyHost, error) {
	p, err := deploy.ProxyHostByRef(ctx, env.Q, env.Caller.TeamID, args.Str("proxy"))
	if err != nil {
		return db.ProxyHost{}, errors.New("proxy host not found")
	}
	return p, nil
}

func proxySummary(verb string, p db.ProxyHost) string {
	out := fmt.Sprintf("%s %s → %s (tls %s, %s, id %s).\n", verb, p.Hostname, p.Upstream, p.Tls, onOff(p.Enabled, "enabled", "disabled"), p.ID)
	if p.VerifiedAt == nil && p.Tls == "auto" {
		out += fmt.Sprintf("Not verified yet: add a TXT record _cloudy-verify.%s = %s, then call verify_proxy_host.\n", p.Hostname, p.VerifyToken)
	}
	return out
}

func onOff(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}
