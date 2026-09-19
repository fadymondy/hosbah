package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/scopes"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

// TunnelTools reach servers without a public address: frp (published at the team's edge) and
// tailscale (joined to the team's tailnet).
func TunnelTools() []Tool {
	return []Tool{
		{
			Name: "list_tunnels", Scope: scopes.Read,
			Description: "The team's tunnels: server, kind (frp/tailscale), status, address (frps host:port or tailnet IP).",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				rows, err := env.Q.ListTunnelsOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list tunnels")
				}
				if len(rows) == 0 {
					return "No tunnels.", nil
				}
				var b strings.Builder
				for _, t := range rows {
					fmt.Fprintf(&b, "  %-20s %-10s %-8s %-26s %s (id %s)\n", t.ServerName, t.Kind, t.Status, orDash(t.Address), orDash(t.Error), t.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "enable_tunnel", Scope: scopes.Write,
			Description: "Open a tunnel for a server without a public address, with one of the team's frp or tailscale credentials. " +
				"frp: the server's Caddy is exposed on a port of the team's frps, and every domain on the server is published through " +
				"the team's Cloudflare (proxied record at the frps public IP) and NPM (proxy host to the tunnel) credentials. " +
				"tailscale: the server joins the team's tailnet. Follow it with list_tunnels.",
			InputSchema: schema([]string{"server", "credential"}, map[string]any{"server": str("Server name or id"), "credential": str("frp or tailscale credential name or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				t, err := s.EnableTunnel(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, args.Str("credential"))
				if err != nil {
					return "", err
				}
				port := ""
				if t.RemotePort.Valid {
					port = fmt.Sprintf(" on remote port %d", t.RemotePort.Int32)
				}
				return fmt.Sprintf("Opening a %s tunnel for %s%s (id %s). Watch list_tunnels until it is active.\n", t.Kind, srv.Name, port, t.ID), nil
			},
		},
		{
			Name: "disable_tunnel", Scope: scopes.Write,
			Description: "Close a server's frp or tailscale tunnel.",
			InputSchema: schema([]string{"server", "kind"}, map[string]any{"server": str("Server name or id"),
				"kind": map[string]any{"type": "string", "enum": []string{"frp", "tailscale"}}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				ts, err := env.Q.TunnelsOfServer(ctx, db.TunnelsOfServerParams{ServerID: srv.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("lookup failed")
				}
				for _, t := range ts {
					if t.Kind == args.Str("kind") {
						if err := s.DisableTunnel(ctx, env.Q, env.Caller.TeamID, mcpActor(env), t); err != nil {
							return "", err
						}
						return "Closed the " + t.Kind + " tunnel of " + srv.Name + ".\n", nil
					}
				}
				return "", errors.New("tunnel not found")
			},
		},
	}
}
