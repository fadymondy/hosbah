package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// WorkloadTools control imported workloads — the sites (apps) and databases Hosbah found running on
// a server — with the same deploy.Service functions the REST API calls. Roles: power start, stats,
// logs and the snapshot list need deployer+; stop/shutdown/restart, resize, recreate, snapshot
// create/rollback and backups need deployer+ and are audited; deleting a snapshot and destroying a
// workload need owner/admin and are destructive (confirm, the approval gate, audit).
func WorkloadTools() []Tool {
	target := map[string]any{
		"app":      str("Imported app (site) name or id"),
		"database": str("Imported data service name or id (instead of app)"),
	}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range target {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	appOnly := func(extra map[string]any) map[string]any {
		out := map[string]any{"app": str("Imported app (a Proxmox guest) name or id")}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	return []Tool{
		{
			Name: "workload_power", Scope: scopes.Deploy,
			Description: "Start, stop (a Proxmox guest: hard stop), shutdown (graceful) or restart (a guest: reboot) an imported site or database — " +
				"a Proxmox guest or a Docker container Hosbah found on a server. Queued as a signed operation; audited.",
			InputSchema: schema([]string{"action"}, with(map[string]any{
				"action": map[string]any{"type": "string", "enum": []string{"start", "stop", "shutdown", "restart"}}})),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueWorkload(ctx, env, args, tenancy.AbilityDeploy, func(s *deploy.Service, w deploy.Workload) (db.Operation, error) {
					return s.Power(ctx, env.Q, env.Caller.TeamID, mcpActor(env), w, args.Str("action"))
				}, fmt.Sprintf("%s queued", args.Str("action")))
			},
		},
		{
			Name: "workload_stats", Scope: scopes.Read,
			Description: "Current CPU %, memory, disk and uptime of an imported site or database, read by the server's agent.",
			InputSchema: schema(nil, with(nil)),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return "", err
				}
				s, w, err := resolveWorkload(ctx, env, args)
				if err != nil {
					return "", err
				}
				op, err := s.Stats(ctx, env.Q, env.Caller.TeamID, mcpActor(env), w)
				if err != nil {
					return "", err
				}
				env.Then = func(ctx context.Context) (string, error) {
					var st agentproto.WorkloadStats
					if _, err := s.AwaitResult(ctx, op, 30*time.Second, &st); err != nil {
						if errors.Is(err, deploy.ErrStillRunning) {
							return fmt.Sprintf("The agent has not answered yet (operation %s).", op.ID), nil
						}
						return "", err
					}
					return fmt.Sprintf("%s: %s · CPU %.1f%% · memory %s / %s · disk %s / %s · up %s", w.Name, st.Status, st.CPUPercent,
						bytesOf(st.MemUsedBytes), bytesOf(st.MemTotalBytes), bytesOf(st.DiskUsedBytes), bytesOf(st.DiskTotalBytes),
						(time.Duration(st.UptimeSeconds) * time.Second).String()), nil
				}
				return "", nil
			},
		},
		{
			Name: "workload_logs", Scope: scopes.Read,
			Description: "Recent log lines of an imported container (docker logs), relayed as an operation; read them with get_operation_logs / the server's operation log.",
			InputSchema: schema(nil, with(map[string]any{
				"tail":           map[string]any{"type": "integer", "default": 200, "maximum": 2000},
				"since":          str("Only lines since: 15m, 2h, or an RFC 3339 time"),
				"follow_seconds": map[string]any{"type": "integer", "default": 0, "maximum": 300}})),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueWorkload(ctx, env, args, tenancy.AbilityDeploy, func(s *deploy.Service, w deploy.Workload) (db.Operation, error) {
					return s.WorkloadLogs(ctx, env.Q, env.Caller.TeamID, mcpActor(env), w, args.Int("tail", 200, 0, 2000), args.Str("since"),
						args.Int("follow_seconds", 0, 0, 300))
				}, "logs relay queued")
			},
		},
		{
			Name: "recreate_container", Scope: scopes.Deploy,
			Description: "Pull an imported container's image and recreate it with the same configuration. Refused if it cannot be reproduced exactly; " +
				"on any failure the old container is put back. Audited.",
			InputSchema: schema(nil, with(nil)),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueWorkload(ctx, env, args, tenancy.AbilityDeploy, func(s *deploy.Service, w deploy.Workload) (db.Operation, error) {
					return s.Recreate(ctx, env.Q, env.Caller.TeamID, mcpActor(env), w)
				}, "recreate queued")
			},
		},
		{
			Name: "resize_guest", Scope: scopes.Deploy,
			Description: "Change an imported Proxmox guest's cores and memory and grow its root disk (never shrinks). Audited.",
			InputSchema: schema([]string{"app"}, appOnly(map[string]any{
				"cores":        map[string]any{"type": "integer", "minimum": 0, "maximum": 512},
				"memory_mb":    map[string]any{"type": "integer", "minimum": 0},
				"disk_grow_gb": map[string]any{"type": "integer", "minimum": 0, "description": "Grow the root disk by this many GB"}})),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueWorkload(ctx, env, args, tenancy.AbilityDeploy, func(s *deploy.Service, w deploy.Workload) (db.Operation, error) {
					return s.Resize(ctx, env.Q, env.Caller.TeamID, mcpActor(env), w, args.Int("cores", 0, 0, agentproto.MaxResizeCores),
						args.Int("memory_mb", 0, 0, agentproto.MaxResizeMemoryMB), args.Int("disk_grow_gb", 0, 0, agentproto.MaxDiskGrowGB))
				}, "resize queued")
			},
		},
		{
			Name: "list_snapshots", Scope: scopes.Read,
			Description: "An imported Proxmox guest's snapshots, read by the agent.",
			InputSchema: schema([]string{"app"}, appOnly(nil)),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return "", err
				}
				s, w, err := resolveWorkload(ctx, env, args)
				if err != nil {
					return "", err
				}
				op, err := s.Snapshot(ctx, env.Q, env.Caller.TeamID, mcpActor(env), w, agentproto.SnapshotList, "", "")
				if err != nil {
					return "", err
				}
				env.Then = func(ctx context.Context) (string, error) {
					var res agentproto.SnapshotResult
					if _, err := s.AwaitResult(ctx, op, 30*time.Second, &res); err != nil {
						if errors.Is(err, deploy.ErrStillRunning) {
							return fmt.Sprintf("The agent has not answered yet (operation %s).", op.ID), nil
						}
						return "", err
					}
					if len(res.Snapshots) == 0 {
						return w.Name + " has no snapshots.", nil
					}
					var b strings.Builder
					for _, sn := range res.Snapshots {
						fmt.Fprintf(&b, "  %-24s %s  %s\n", sn.Name, time.Unix(sn.SnapTime, 0).UTC().Format(time.RFC3339), sn.Description)
					}
					return b.String(), nil
				}
				return "", nil
			},
		},
		{
			Name: "create_snapshot", Scope: scopes.Deploy,
			Description: "Take a snapshot of an imported Proxmox guest. Audited.",
			InputSchema: schema([]string{"app", "name"}, appOnly(map[string]any{"name": str("Snapshot name: letters, digits, - and _"), "description": str("Optional")})),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueWorkload(ctx, env, args, tenancy.AbilityDeploy, func(s *deploy.Service, w deploy.Workload) (db.Operation, error) {
					return s.Snapshot(ctx, env.Q, env.Caller.TeamID, mcpActor(env), w, agentproto.SnapshotCreate, args.Str("name"), args.Str("description"))
				}, "snapshot queued")
			},
		},
		{
			Name: "rollback_snapshot", Scope: scopes.Deploy,
			Description: "Roll an imported Proxmox guest back to one of its snapshots. Audited.",
			InputSchema: schema([]string{"app", "name"}, appOnly(map[string]any{"name": str("Snapshot name")})),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueWorkload(ctx, env, args, tenancy.AbilityDeploy, func(s *deploy.Service, w deploy.Workload) (db.Operation, error) {
					return s.Snapshot(ctx, env.Q, env.Caller.TeamID, mcpActor(env), w, agentproto.SnapshotRollback, args.Str("name"), "")
				}, "rollback queued")
			},
		},
		{
			Name: "delete_snapshot", Scope: scopes.Destructive, Destructive: true, ResourceType: "app",
			Description: "Delete a snapshot of an imported Proxmox guest. Owner or admin; confirm must equal the app's name.",
			InputSchema: schema([]string{"app", "name", "confirm"}, appOnly(map[string]any{"name": str("Snapshot name"), "confirm": str("The app's name, exactly")})),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueWorkload(ctx, env, args, tenancy.AbilityManageApps, func(s *deploy.Service, w deploy.Workload) (db.Operation, error) {
					if err := RequireConfirm(args, w.Name); err != nil {
						return db.Operation{}, err
					}
					return s.Snapshot(ctx, env.Q, env.Caller.TeamID, mcpActor(env), w, agentproto.SnapshotDelete, args.Str("name"), "")
				}, "snapshot deletion queued")
			},
		},
		{
			Name: "backup_guest", Scope: scopes.Deploy,
			Description: "Back up an imported Proxmox guest with vzdump (snapshot mode, zstd) to the node's backup storage or the one given. Audited.",
			InputSchema: schema([]string{"app"}, appOnly(map[string]any{"storage": str("Proxmox storage id (default: the node's first backup storage)")})),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueWorkload(ctx, env, args, tenancy.AbilityDeploy, func(s *deploy.Service, w deploy.Workload) (db.Operation, error) {
					return s.GuestBackup(ctx, env.Q, env.Caller.TeamID, mcpActor(env), w, args.Str("storage"))
				}, "backup queued")
			},
		},
		{
			Name: "permanently_destroy_app", Scope: scopes.Destructive, Destructive: true, ResourceType: "app",
			Description: "PERMANENTLY DELETES THE REAL WORKLOAD — cannot be undone. An imported site's Proxmox guest is stopped and destroyed, " +
				"or its container removed, then its record. To only stop showing it in Hosbah, use delete_app instead. " +
				"Owner or admin; confirm must equal the app's name. purge: pvesh --purge for a guest, a container's anonymous volumes. " +
				"Held for approval when the team requires it.",
			InputSchema: schema([]string{"app", "confirm"}, map[string]any{"app": str("Imported app name or id"), "confirm": str("The app's name, exactly"),
				"purge": map[string]any{"type": "boolean", "default": false}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueWorkload(ctx, env, Args{"app": args["app"], "confirm": args["confirm"], "purge": args["purge"]}, tenancy.AbilityManageApps,
					func(s *deploy.Service, w deploy.Workload) (db.Operation, error) {
						if err := RequireConfirm(args, w.Name); err != nil {
							return db.Operation{}, err
						}
						return s.Destroy(ctx, env.Q, env.Caller.TeamID, mcpActor(env), w, args.Bool("purge"))
					}, "destroy queued; the record goes once the server reports the workload gone")
			},
		},
		{
			Name: "permanently_destroy_data_service", Scope: scopes.Destructive, Destructive: true, ResourceType: "data_service",
			Description: "PERMANENTLY DELETES THE REAL DATABASE WORKLOAD — cannot be undone, its data is lost. An imported database's container " +
				"or Proxmox guest is destroyed, then its record. To only stop showing it in Hosbah, use delete_data_service instead. Owner or admin; " +
				"confirm must equal the service's name. Held for approval when the team requires it.",
			InputSchema: schema([]string{"database", "confirm"}, map[string]any{"database": str("Imported data service name or id"),
				"confirm": str("The service's name, exactly"), "purge": map[string]any{"type": "boolean", "default": false}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueWorkload(ctx, env, Args{"database": args["database"], "confirm": args["confirm"], "purge": args["purge"]}, tenancy.AbilityManageApps,
					func(s *deploy.Service, w deploy.Workload) (db.Operation, error) {
						if err := RequireConfirm(args, w.Name); err != nil {
							return db.Operation{}, err
						}
						return s.Destroy(ctx, env.Q, env.Caller.TeamID, mcpActor(env), w, args.Bool("purge"))
					}, "destroy queued; the record goes once the server reports the workload gone")
			},
		},
		{
			Name: "set_database_credentials", Scope: scopes.Write,
			Description: "Give Hosbah the credentials of an imported database. They are checked on the server first and, if they work, sealed with " +
				"the team key — databases, users and backups then work for it. Owner or admin; audited.",
			InputSchema: schema([]string{"database", "password"}, map[string]any{"database": str("Imported data service name or id"),
				"username": str("Required for Postgres and MySQL"), "password": str("The password"),
				"database_name": str("Postgres: the database to log in to (default postgres)")}),
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
				if _, err := deploys(env); err != nil {
					return "", err
				}
				creds := dbsvc.Credentials{User: args.Str("username"), Password: args.Str("password"), Database: args.Str("database_name")}
				actor, teamID := mcpActor(env), env.Caller.TeamID
				// The probe is awaited after this call's transaction commits (it has its own).
				env.Then = func(ctx context.Context) (string, error) {
					res, err := s.SetCredentials(ctx, teamID, actor, svc, creds)
					if err != nil {
						return "", err
					}
					if !res.Verified {
						return fmt.Sprintf("Stored the credentials for %s. %s", svc.Name, res.Detail), nil
					}
					return fmt.Sprintf("The credentials work (%s); %s now has databases, users and backups.", res.Version, svc.Name), nil
				}
				return "", nil
			},
		},
	}
}

// resolveWorkload finds the app or data service the call names.
func resolveWorkload(ctx context.Context, env *Env, args Args) (*deploy.Service, deploy.Workload, error) {
	s, err := deploys(env)
	if err != nil {
		return nil, deploy.Workload{}, err
	}
	if args.Str("app") != "" {
		a, err := findApp(ctx, env, args)
		if err != nil {
			return nil, deploy.Workload{}, err
		}
		return s, deploy.WorkloadOfApp(a), nil
	}
	if args.Str("database") != "" {
		svc, err := findService(ctx, env, args)
		if err != nil {
			return nil, deploy.Workload{}, err
		}
		return s, deploy.WorkloadOfService(svc), nil
	}
	return nil, deploy.Workload{}, errors.New("app or database is required (a name or an id)")
}

// queueWorkload checks the role, resolves the target and queues one operation.
func queueWorkload(ctx context.Context, env *Env, args Args, ability string, fn func(*deploy.Service, deploy.Workload) (db.Operation, error), what string) (string, error) {
	if err := needs(ctx, env, ability); err != nil {
		return "", err
	}
	s, w, err := resolveWorkload(ctx, env, args)
	if err != nil {
		return "", err
	}
	op, err := fn(s, w)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: %s on %s (operation %s). It shows in the server's operation log.", w.Name, what, w.OriginKind, op.ID), nil
}
