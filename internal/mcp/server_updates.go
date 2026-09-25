package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ServerUpdateTools are MH-578 C8: listing upgradable packages (apt/dnf/apk aware), running the
// upgrade, and toggling automatic security updates (unattended-upgrades on Debian/Ubuntu,
// dnf-automatic on RHEL). server_updates_run is destructive (it changes installed packages and may
// require a reboot), so it needs confirm; the read and the toggle do not.
func ServerUpdateTools() []Tool {
	return []Tool{
		{
			Name: "server_updates_list", Scope: scopes.Read,
			Description: "List a server's upgradable packages (apt/dnf/apk aware) and whether a reboot is already required.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": str("Server id or name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				var res agentproto.PkgListResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindPkgList, agentproto.PkgListParams{}, &res); err != nil {
					return "", err
				}
				packages, merr := json.Marshal(res.Packages)
				if merr != nil || len(packages) == 0 {
					packages = []byte("[]")
				}
				// Best effort: cache the reading for the server page even though the answer below
				// already has everything the caller asked for.
				_, _ = env.Q.UpsertServerUpdateStatusCheck(ctx, db.UpsertServerUpdateStatusCheckParams{
					TeamID: env.Caller.TeamID, ServerID: srv.ID, OsFamily: string(res.OSFamily), PackageManager: string(res.PackageManager),
					UpgradableCount: int64(res.UpgradableCount), Packages: string(packages), RebootRequired: res.RebootRequired,
				})
				if res.UpgradableCount == 0 {
					return fmt.Sprintf("%s/%s: no upgradable packages.", res.OSFamily, res.PackageManager), nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%s/%s: %d upgradable package(s)%s:\n", res.OSFamily, res.PackageManager, res.UpgradableCount,
					rebootNote(res.RebootRequired))
				for _, p := range res.Packages {
					fmt.Fprintf(&b, "  %-32s %s -> %s\n", p.Name, p.Current, p.Candidate)
				}
				return b.String(), nil
			},
		},
		{
			Name: "server_updates_run", Scope: scopes.Destructive, Destructive: true, ResourceType: "server",
			Description: "Run the package upgrade now. security_only asks dnf for --security (apt has no portable security-only flag " +
				"and ignores it). confirm must equal the server's id or name.",
			InputSchema: schema([]string{"server", "confirm"}, map[string]any{
				"server":        str("Server id or name"),
				"security_only": map[string]any{"type": "boolean", "description": "dnf only: security updates only"},
				"confirm":       str("The server id or name, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, args.Str("server")); err != nil {
					return "", err
				}
				p := agentproto.PkgUpgradeParams{SecurityOnly: args.Bool("security_only")}
				var res agentproto.PkgUpgradeResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindPkgUpgrade, p, &res); err != nil {
					return "", err
				}
				_, _ = env.Q.RecordServerUpgradeRun(ctx, db.RecordServerUpgradeRunParams{
					TeamID: env.Caller.TeamID, ServerID: srv.ID, RebootRequired: res.RebootRequired,
				})
				if res.Upgraded == 0 {
					return fmt.Sprintf("Nothing to upgrade.%s", rebootNote(res.RebootRequired)), nil
				}
				return fmt.Sprintf("Upgraded %d package(s).%s", res.Upgraded, rebootNote(res.RebootRequired)), nil
			},
		},
		{
			Name: "server_updates_auto_set", Scope: scopes.Write,
			Description: "Turn automatic security updates on or off for a server (unattended-upgrades on Debian/Ubuntu, dnf-automatic " +
				"on RHEL; reports unsupported otherwise).",
			InputSchema: schema([]string{"server", "enabled"}, map[string]any{
				"server":  str("Server id or name"),
				"enabled": map[string]any{"type": "boolean", "description": "Switch automatic security updates on or off"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				p := agentproto.PkgAutoupdateSetParams{Enabled: args.Bool("enabled")}
				var res agentproto.PkgAutoupdateSetResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindPkgAutoupdateSet, p, &res); err != nil {
					return "", err
				}
				_, _ = env.Q.SetServerAutoUpdates(ctx, db.SetServerAutoUpdatesParams{
					TeamID: env.Caller.TeamID, ServerID: srv.ID, AutoSecurityUpdates: res.Enabled, AutoSupported: res.Supported,
				})
				if !res.Supported {
					return fmt.Sprintf("%s: automatic security updates are not supported (%s).", res.OSFamily, res.Error), nil
				}
				state := "off"
				if res.Enabled {
					state = "on"
				}
				return fmt.Sprintf("%s: automatic security updates are now %s.", res.OSFamily, state), nil
			},
		},
	}
}

func rebootNote(rebootRequired bool) string {
	if rebootRequired {
		return " A reboot is required."
	}
	return ""
}
