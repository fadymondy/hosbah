package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ManageTools close the gap between the dashboard and MCP, so an SDK, the CLI or an agent can do
// everything a team member can: databases inside a service, attaching, dropping, backup schedules,
// server provisioning steps, app settings, releases, deploy hooks, member roles, team settings and
// credentials. Each calls the same service function as the REST handler.
func ManageTools() []Tool {
	dbArg := str("Data service name or id")
	appArg := str("App name or id")
	return []Tool{
		// ---- databases inside a service ----
		{
			Name: "create_db", Scope: scopes.Write,
			Description: "Create a database (a bucket on MinIO) and a user with grants inside a running Postgres, MySQL or MinIO service. The password is generated and sealed; attach_database hands it to an app.",
			InputSchema: schema([]string{"database", "name"}, map[string]any{"database": dbArg,
				"name":   str("Database name, lower snake_case (MinIO buckets use dashes for underscores)"),
				"user":   str("User name (default: the database name)"),
				"grants": map[string]any{"type": "string", "enum": []string{"all", "readwrite", "readonly"}, "default": "all"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				svc, err := findService(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				d, u, err := s.CreateDatabaseAndUser(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, args.Str("name"), args.Str("user"), args.Str("grants"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Creating database %s and user %s (%s) on %s. Poll get_database for ready.\n", d.Name, u.Name, u.Grants, svc.Name), nil
			},
		},
		{
			Name: "drop_database", Scope: scopes.Destructive, Destructive: true, ResourceType: "database",
			Description: "Drop one database (a bucket on MinIO) of a service, with its users. Its data is gone; backups are kept. confirm must equal the database name.",
			InputSchema: schema([]string{"database", "name", "confirm"}, map[string]any{"database": dbArg,
				"name": str("Database name or id"), "confirm": str("The database name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				svc, err := findService(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, args.Str("name")); err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				d, err := s.DropDatabase(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, args.Str("name"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Dropping %s on %s. It disappears from get_database once the server confirms; if the drop fails it shows as failed and can be retried. Backups are kept.\n", d.Name, svc.Name), nil
			},
		},
		{
			Name: "attach_database", Scope: scopes.Write,
			Description: "Give an app a data service's connection as environment variables (DATABASE_URL, REDIS_URL, or S3_URL + AWS_* for MinIO). Applied on the next deploy.",
			InputSchema: schema([]string{"database", "app"}, map[string]any{"database": dbArg, "app": appArg,
				"user": str("Which database user (default: the first)")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				svc, err := findService(ctx, env, args)
				if err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				v, err := s.Attach(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, a, args.Str("user"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Attached %s to %s as %s. Deploy the app to use it.\n", svc.Name, a.Name, v), nil
			},
		},
		{
			Name: "delete_data_service", Scope: scopes.Destructive, Destructive: true, ResourceType: "data_service",
			Description: "Delete a data service: its container and volume are removed from the server with every database in it. confirm must equal the service name.",
			InputSchema: schema([]string{"database", "confirm"}, map[string]any{"database": dbArg, "confirm": str("The data service's name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				svc, err := findService(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, svc.Name); err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				if err := s.DeleteService(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc); err != nil {
					return "", err
				}
				return "Deleted " + svc.Name + "; its container and volume are being removed.\n", nil
			},
		},
		{
			Name: "set_backup_schedule", Scope: scopes.Write,
			Description: "Create or change a data service's backup schedule (cron, retention, target, automatic restore-test, paused).",
			InputSchema: schema([]string{"database", "cron"}, map[string]any{"database": dbArg,
				"cron":         str("Cron expression, e.g. 0 3 * * *"),
				"retention":    map[string]any{"type": "integer", "description": "Backups to keep (default 7)"},
				"target":       map[string]any{"type": "string", "enum": []string{"local", "minio", "s3"}, "default": "local"},
				"restore_test": map[string]any{"type": "boolean", "description": "Restore-test each backup"},
				"paused":       map[string]any{"type": "boolean"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				svc, err := findService(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				target := args.Str("target")
				if target == "" {
					target = "local"
				}
				sch, err := s.SetSchedule(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, dbsvc.ScheduleInput{Cron: args.Str("cron"),
					Retention: args.Int("retention", 7, 1, 365), Target: target, RestoreTest: args.Bool("restore_test"), Paused: args.Bool("paused")})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Backups of %s: %s, keep %d, target %s, paused %v.\n", svc.Name, sch.Cron, sch.Retention, sch.Target, sch.Paused), nil
			},
		},

		// ---- servers ----
		{
			Name: "run_server_step", Scope: scopes.Write,
			Description: "Queue an operation on a server: system.info (refresh facts and discovered services), docker.check, a provisioning step (packages.base, docker.install, swap.ensure, restic.install) or agent.update.",
			InputSchema: schema([]string{"server", "kind"}, map[string]any{"server": str("Server name or id"),
				"kind": map[string]any{"type": "string", "enum": []string{"system.info", "docker.check", "provision.step", "agent.update"}},
				"step": map[string]any{"type": "string", "enum": []string{"packages.base", "docker.install", "swap.ensure", "restic.install"}}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				svc, err := opsOf(env)
				if err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				if srv.Status != "enrolled" {
					return "", errors.New("that server's agent has not enrolled yet")
				}
				kind := args.Str("kind")
				params := json.RawMessage(`{}`)
				switch kind {
				case agentproto.KindSystemInfo, agentproto.KindDockerCheck:
				case agentproto.KindProvisionStep:
					step := args.Str("step")
					switch step {
					case agentproto.StepBasePackages, agentproto.StepDockerInstall, agentproto.StepSwap, agentproto.StepRestic:
					default:
						return "", errors.New("step must be packages.base, docker.install, swap.ensure or restic.install")
					}
					params, _ = json.Marshal(map[string]any{"step": step})
				case agentproto.KindAgentUpdate:
					arch := "amd64"
					if srv.Arch != nil && *srv.Arch != "" {
						arch = *srv.Arch
					}
					if params, _, err = svc.UpdateParams(arch); err != nil {
						return "", err
					}
				default:
					return "", errors.New("kind must be system.info, docker.check, provision.step or agent.update")
				}
				op, _, err := svc.Enqueue(ctx, env.Q, ops.EnqueueSpec{TeamID: env.Caller.TeamID, ServerID: srv.ID, Kind: kind, Params: params, CreatedBy: actor(env)})
				if err != nil {
					return "", errors.New("could not queue the operation")
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "operation.queued", "operation", op.ID,
					auditChanges(env, map[string]any{"server": srv.Name, "kind": kind, "params": json.RawMessage(op.Params)})); err != nil {
					return "", err
				}
				return fmt.Sprintf("Queued %s on %s (operation %s). Follow it with list_operations.\n", kind, srv.Name, op.ID), nil
			},
		},

		// ---- apps ----
		{
			Name: "update_app", Scope: scopes.Write,
			Description: "Change an app's branch, port, health-check path/timeout or auto-deploy pause (git apps; takes effect on the next deploy), " +
				"and its appearance (any app, imported too): title (display name, at most 80 characters), description (at most 280), color (#RRGGBB), icon (" +
				strings.Join(deploy.AppIcons(), ", ") + "). Omit a field to leave it; an empty string clears title, description, color or icon. The name cannot be changed.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg, "branch": str("Branch"),
				"port":                map[string]any{"type": "integer"},
				"healthcheck_path":    str("Health-check path"),
				"healthcheck_timeout": map[string]any{"type": "integer", "description": "Seconds, 5–600"},
				"autodeploy_paused":   map[string]any{"type": "boolean"},
				"title":               str("Display name (at most 80 characters), or empty to clear"),
				"description":         str("A short note (at most 280 characters), or empty to clear"),
				"color":               str("#RRGGBB, or empty to clear"),
				"icon":                map[string]any{"type": "string", "enum": append([]string{""}, deploy.AppIcons()...), "description": "Icon key, or empty to clear"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				branch, path, paused := deploy.BranchOf(a), a.HealthcheckPath, a.AutodeployPaused
				if v := args.Str("branch"); v != "" {
					branch = v
				}
				if v := args.Str("healthcheck_path"); v != "" {
					path = v
				}
				if v, ok := args["autodeploy_paused"].(bool); ok {
					paused = v
				}
				port := args.Int("port", int(a.Port), 1, 65535)
				timeout := args.Int("healthcheck_timeout", int(a.HealthcheckTimeout), 5, 600)
				opt := func(k string) *string {
					if _, ok := args[k]; !ok {
						return nil
					}
					v := args.Str(k)
					return &v
				}
				look := deploy.AppearancePatch{Title: opt("title"), Description: opt("description"), Color: opt("color"), Icon: opt("icon")}
				settings := false
				for _, k := range []string{"branch", "port", "healthcheck_path", "healthcheck_timeout", "autodeploy_paused"} {
					_, ok := args[k]
					settings = settings || ok
				}
				u, out := a, ""
				// Settings are a git app's (an imported one is refused); appearance is any app's.
				if settings || look.Empty() {
					if u, err = s.UpdateSettings(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, branch, port, timeout, path, paused); err != nil {
						return "", err
					}
					out = fmt.Sprintf("%s: branch %s, port %d, health %s (%ds), auto-deploy paused %v.\n", u.Name, deploy.BranchOf(u), u.Port, u.HealthcheckPath, u.HealthcheckTimeout, u.AutodeployPaused)
				}
				if !look.Empty() {
					if u, err = s.UpdateAppearance(ctx, env.Q, env.Caller.TeamID, mcpActor(env), u, look); err != nil {
						return "", err
					}
					out += fmt.Sprintf("%s: title %s · description %s · color %s · icon %s\n", u.Name, orDash(u.Title), orDash(u.Description), orDash(u.Color), orDash(u.Icon))
				}
				return out, nil
			},
		},
		{
			Name: "list_releases", Scope: scopes.Read,
			Description: "An app's releases (images kept for instant rollback): version, status, commit, when activated.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				rows, err := env.Q.ListReleasesOfApp(ctx, db.ListReleasesOfAppParams{AppID: a.ID, TeamID: env.Caller.TeamID, Limit: 50})
				if err != nil {
					return "", errors.New("could not list releases")
				}
				if len(rows) == 0 {
					return a.Name + " has no releases yet.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					when := "—"
					if r.ActivatedAt != nil {
						when = r.ActivatedAt.UTC().Format(time.RFC3339)
					}
					fmt.Fprintf(&b, "  v%-4d %-8s %s  activated %s  (id %s)\n", r.Version, r.Status, orDash(r.CommitSha), when, r.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "rotate_deploy_hook", Scope: scopes.SecretsRead,
			Description: "Replace an app's deploy-hook URL; the old one stops working at once. Returns the new URL once.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				u, err := s.RotateHook(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a)
				if err != nil {
					return "", err
				}
				return "New deploy hook for " + a.Name + " (shown once): " + u + "\n", nil
			},
		},

		// ---- team ----
		{
			Name: "update_member_role", Scope: scopes.Write,
			Description: "Change a member's role between admin, deployer and viewer. Ownership changes are dashboard-only.",
			InputSchema: schema([]string{"member", "role"}, map[string]any{"member": str("Member id or email"),
				"role": map[string]any{"type": "string", "enum": []string{"admin", "deployer", "viewer"}}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageMembers); err != nil {
					return "", err
				}
				role := args.Str("role")
				if role != tenancy.RoleAdmin && role != tenancy.RoleDeployer && role != tenancy.RoleViewer {
					return "", errors.New("role must be admin, deployer or viewer")
				}
				members, err := env.Q.ListMembersOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list members")
				}
				ref := strings.ToLower(args.Str("member"))
				for _, m := range members {
					if m.ID != ref && strings.ToLower(m.Email) != ref {
						continue
					}
					if m.Role == tenancy.RoleOwner {
						return "", errors.New("ownership changes are dashboard-only")
					}
					row, err := env.Q.UpdateMemberRole(ctx, db.UpdateMemberRoleParams{ID: m.ID, TeamID: env.Caller.TeamID, Role: role})
					if err != nil {
						return "", errors.New("could not update the role")
					}
					if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "member.role_changed", "team_member", m.ID,
						auditChanges(env, map[string]any{"user_id": m.UserID, "role": map[string]string{"old": m.Role, "new": row.Role}})); err != nil {
						return "", err
					}
					return fmt.Sprintf("%s is now %s.\n", m.Email, row.Role), nil
				}
				return "", errors.New("member not found")
			},
		},
		{
			Name: "delete_credential", Scope: scopes.Write,
			Description: "Delete a provider credential (Proxmox, Cloudflare). Servers created with it keep running but can no longer be managed through the provider. confirm must equal the credential name.",
			InputSchema: schema([]string{"credential", "confirm"}, map[string]any{"credential": str("Credential id or name"), "confirm": str("The credential name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				c, err := findCredential(ctx, env, args.Str("credential"))
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, c.Name); err != nil {
					return "", err
				}
				if n, err := env.Q.DeleteCredentialInTeam(ctx, db.DeleteCredentialInTeamParams{ID: c.ID, TeamID: env.Caller.TeamID}); err != nil || n == 0 {
					return "", errors.New("credential not found")
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "credential.deleted", "credential", c.ID, auditChanges(env, map[string]any{"name": c.Name})); err != nil {
					return "", err
				}
				return "Deleted credential " + c.Name + ".\n", nil
			},
		},
	}
}
