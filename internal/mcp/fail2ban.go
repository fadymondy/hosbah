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

// Fail2banTools are MH-580 J2: one sshd jail per server, installed and configured through
// fail2ban.install, with fail2ban.banned/fail2ban.unban to manage the current ban list.
func Fail2banTools() []Tool {
	return []Tool{
		{
			Name: "server_fail2ban_settings", Scope: scopes.Write,
			Description: "Install fail2ban if needed and (re)configure its sshd jail (enabled, bantime/findtime/maxretry in seconds " +
				"except maxretry which is a count). enabled=false stops and disables the jail without uninstalling the package.",
			InputSchema: schema([]string{"server"}, map[string]any{
				"server":   str("Server id or name"),
				"enabled":  map[string]any{"type": "boolean", "description": "Defaults to true"},
				"bantime":  map[string]any{"type": "integer", "description": "Seconds, 30-31536000, defaults to 600"},
				"findtime": map[string]any{"type": "integer", "description": "Seconds, 10-31536000, defaults to 600"},
				"maxretry": map[string]any{"type": "integer", "description": "1-100, defaults to 5"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				enabled := true
				if v, ok := args["enabled"].(bool); ok {
					enabled = v
				}
				p := agentproto.Fail2banInstallParams{Enabled: enabled, Bantime: args.Int("bantime", 600, 0, 31536000),
					Findtime: args.Int("findtime", 600, 0, 31536000), Maxretry: args.Int("maxretry", 5, 0, 100)}
				if err := p.Validate(); err != nil {
					return "", err
				}
				var res agentproto.Fail2banInstallResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindFail2banInstall, p, &res); err != nil {
					return "", err
				}
				if _, err := env.Q.UpsertFail2banSetting(ctx, db.UpsertFail2banSettingParams{TeamID: env.Caller.TeamID, ServerID: srv.ID,
					Enabled: p.Enabled, Bantime: int32(p.Bantime), Findtime: int32(p.Findtime), Maxretry: int32(p.Maxretry),
					UpdatedBy: env.Caller.UserID}); err != nil {
					return "", errors.New("could not save the fail2ban settings")
				}
				status := "installed"
				if !res.Installed {
					status = "already installed"
				}
				active := "inactive"
				if res.Active {
					active = "active"
				}
				return fmt.Sprintf("fail2ban %s, sshd jail %s (enabled=%v, bantime=%ds, findtime=%ds, maxretry=%d).", status, active,
					p.Enabled, p.Bantime, p.Findtime, p.Maxretry), nil
			},
		},
		{
			Name: "server_fail2ban_banned", Scope: scopes.Read,
			Description: "IPs currently banned in a server's sshd jail, read live from the server.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": str("Server id or name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				var res agentproto.Fail2banBannedResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindFail2banBanned, struct{}{}, &res); err != nil {
					return "", err
				}
				if !res.Active {
					return "fail2ban is not running on this server.", nil
				}
				if len(res.Banned) == 0 {
					return "No banned IPs in the " + res.Jail + " jail.", nil
				}
				return fmt.Sprintf("%d banned in the %s jail: %s", len(res.Banned), res.Jail, strings.Join(res.Banned, ", ")), nil
			},
		},
		{
			Name: "server_fail2ban_unban", Scope: scopes.Write,
			Description: "Clear one IP from a server's sshd jail.",
			InputSchema: schema([]string{"server", "ip"}, map[string]any{"server": str("Server id or name"), "ip": str("IP address to unban")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				p := agentproto.Fail2banUnbanParams{IP: args.Str("ip")}
				if err := p.Validate(); err != nil {
					return "", err
				}
				var res agentproto.Fail2banUnbanResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindFail2banUnban, p, &res); err != nil {
					return "", err
				}
				if !res.Unbanned {
					return p.IP + " was not banned.", nil
				}
				return "Unbanned " + p.IP + ".", nil
			},
		},
	}
}
