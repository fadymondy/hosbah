package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// DatabaseTools are the P3 managed-data-service tools. Like the app tools they call the same dbsvc
// actions as REST, so validation, roles, audit and "a secret never leaves in a list" are one
// implementation. Scopes: read — look; write — create a service, back up; secrets:read —
// reveal_db_url (audited); destructive — restore_backup (confirm = the service's name).
func DatabaseTools() []Tool {
	dbArg := str("Data service name or id")
	return []Tool{
		{
			Name: "list_databases", Scope: scopes.Read,
			Description: "The team's data services: managed ones (Postgres/MySQL/Redis/MinIO) and imported ones found running on its servers (marked imported; Hosbah never changes those): kind, version, status and endpoint.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				rows, err := env.Q.ListDataServicesOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list data services")
				}
				if len(rows) == 0 {
					return "No data services yet. create_database provisions one.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d data service(s):\n", len(rows))
				for _, r := range rows {
					endpoint := "—"
					if r.Host != nil && r.Port != nil {
						endpoint = fmt.Sprintf("%s:%d", *r.Host, *r.Port)
					}
					if r.Source == importer.SourceImported {
						endpoint += "  imported (" + orDash(r.ImportedState) + ")"
					}
					fmt.Fprintf(&b, "  %-20s %-9s %-8s %-10s endpoint %-22s (id %s)\n", r.Name, r.Kind, r.Version, r.Status, endpoint, r.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "get_database", Scope: scopes.Read,
			Description: "One data service: status, endpoint, its databases, users, latest backups and schedule.",
			InputSchema: schema([]string{"database"}, map[string]any{"database": dbArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				svc, err := findService(ctx, env, args)
				if err != nil {
					return "", err
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%s  (id %s)\nkind: %s %s\nstatus: %s\n", svc.Name, svc.ID, svc.Kind, svc.Version, svc.Status)
				if svc.Source == importer.SourceImported {
					fmt.Fprintf(&b, "imported: Hosbah found it running and holds no credential; it never backs it up or changes it\n%s\nlast seen: %s\n",
						importedLine(svc.Name, svc.OriginKind, svc.Origin, svc.ImportedState, svc.Kind+" "+svc.Version), lastSeen(svc.LastSeenAt))
				}
				if svc.Host != nil && svc.Port != nil {
					fmt.Fprintf(&b, "endpoint: %s:%d (private)\n", *svc.Host, *svc.Port)
				}
				if svc.Error != nil && *svc.Error != "" {
					fmt.Fprintf(&b, "error: %s\n", *svc.Error)
				}
				if dbs, err := env.Q.ListDatabasesOfService(ctx, db.ListDatabasesOfServiceParams{DataServiceID: svc.ID, TeamID: env.Caller.TeamID}); err == nil {
					for _, d := range dbs {
						fmt.Fprintf(&b, "database: %s (%s)\n", d.Name, d.Status)
					}
				}
				if us, err := env.Q.ListDatabaseUsersOfService(ctx, db.ListDatabaseUsersOfServiceParams{DataServiceID: svc.ID, TeamID: env.Caller.TeamID}); err == nil {
					for _, u := range us {
						fmt.Fprintf(&b, "user: %s (%s, %s)\n", u.Name, u.Grants, u.Status)
					}
				}
				if bks, err := env.Q.ListBackupsOfService(ctx, db.ListBackupsOfServiceParams{DataServiceID: svc.ID, TeamID: env.Caller.TeamID, Limit: 5}); err == nil {
					for _, bk := range bks {
						b.WriteString("backup: " + backupLine(bk) + "\n")
					}
				}
				return b.String(), nil
			},
		},
		{
			Name: "list_backups", Scope: scopes.Read,
			Description: "A data service's backups, newest first: status, size, snapshot and restore-test result.",
			InputSchema: schema([]string{"database"}, map[string]any{"database": dbArg, "limit": map[string]any{"type": "integer", "default": 20, "maximum": 100}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				svc, err := findService(ctx, env, args)
				if err != nil {
					return "", err
				}
				rows, err := env.Q.ListBackupsOfService(ctx, db.ListBackupsOfServiceParams{DataServiceID: svc.ID, TeamID: env.Caller.TeamID, Limit: int32(args.Int("limit", 20, 1, 100))})
				if err != nil {
					return "", errors.New("could not list backups")
				}
				if len(rows) == 0 {
					return svc.Name + " has no backups. backup_now takes one.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					b.WriteString("  " + backupLine(r) + "\n")
				}
				return b.String(), nil
			},
		},

		// ---- write ----
		{
			Name: "create_database", Scope: scopes.Write,
			Description: "Provision a managed data service (postgres, mysql, redis, or minio for S3-compatible buckets) on one of the team's servers. create_db adds databases and users inside it.",
			InputSchema: schema([]string{"name", "server", "kind"}, map[string]any{
				"name": str("Lowercase letters, digits, dashes"), "server": str("Server name or id"),
				"kind": map[string]any{"type": "string", "enum": []string{"postgres", "mysql", "redis", "minio"}, "description": "minio = S3-compatible object storage (buckets)"},
				"version": str("Image tag (optional; a current version by default)")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				svc, err := s.CreateService(ctx, env.Q, env.Caller.TeamID, mcpActor(env), dbsvc.CreateInput{
					Name: args.Str("name"), Server: args.Str("server"), Kind: args.Str("kind"), Version: args.Str("version")})
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Provisioning %s (%s %s) on server %s (id %s). It becomes 'running' once its container is up — poll get_database.",
					svc.Name, svc.Kind, svc.Version, args.Str("server"), svc.ID), nil
			},
		},
		{
			Name: "backup_now", Scope: scopes.Write,
			Description: "Back up a data service now (pg_dump -Fc / mysqldump / Redis RDB → restic). Returns when it is queued; poll list_backups.",
			InputSchema: schema([]string{"database"}, map[string]any{"database": dbArg}),
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
				bk, err := s.Backup(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, "manual")
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Backup of %s queued (id %s). Poll list_backups for its snapshot and size.", svc.Name, bk.ID), nil
			},
		},

		// ---- read: standalone database users (MH-574 F1) ----
		{
			Name: "db_users_list", Scope: scopes.Read,
			Description: "A data service's standalone users: which database each was created against, its grant preset and status. The service's own root/superuser credential is never a row here.",
			InputSchema: schema([]string{"database"}, map[string]any{"database": dbArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				svc, err := findService(ctx, env, args)
				if err != nil {
					return "", err
				}
				us, err := env.Q.ListDatabaseUsersOfService(ctx, db.ListDatabaseUsersOfServiceParams{DataServiceID: svc.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("could not list database users")
				}
				if len(us) == 0 {
					return "No standalone users yet. db_user_create adds one.", nil
				}
				dbs, _ := env.Q.ListDatabasesOfService(ctx, db.ListDatabasesOfServiceParams{DataServiceID: svc.ID, TeamID: env.Caller.TeamID})
				names := map[string]string{}
				for _, d := range dbs {
					names[d.ID] = d.Name
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d user(s) on %s:\n", len(us), svc.Name)
				for _, u := range us {
					dbName := "—"
					if u.DatabaseID != nil {
						if n, ok := names[*u.DatabaseID]; ok {
							dbName = n
						}
					}
					fmt.Fprintf(&b, "  %-20s db %-16s grants %-10s status %-9s (id %s)\n", u.Name, dbName, u.Grants, u.Status, u.ID)
				}
				return b.String(), nil
			},
		},

		// ---- write: standalone database users (MH-574 F1) ----
		{
			Name: "db_user_grant", Scope: scopes.Write,
			Description: "Grant a preset (all, readwrite or readonly) to a database user on one database. Re-applies from scratch, so it also changes an existing user's level.",
			InputSchema: schema([]string{"database", "user", "grants"}, map[string]any{"database": dbArg, "user": str("Database user name or id"),
				"target_database": str("The database/bucket to grant on (optional; defaults to the one the user was created against)"),
				"grants":          map[string]any{"type": "string", "enum": []string{"all", "readwrite", "readonly"}}}),
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
				u, err := s.GrantUser(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, args.Str("user"), args.Str("target_database"), args.Str("grants"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%s now has %s on this service.", u.Name, u.Grants), nil
			},
		},
		{
			Name: "db_user_revoke", Scope: scopes.Write,
			Description: "Revoke a database user's privileges on one database, leaving the login/role itself in place. db_user_delete removes the login entirely.",
			InputSchema: schema([]string{"database", "user"}, map[string]any{"database": dbArg, "user": str("Database user name or id"),
				"target_database": str("The database/bucket to revoke on (optional; defaults to the one the user was created against)")}),
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
				u, err := s.RevokeUser(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, args.Str("user"), args.Str("target_database"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%s's privileges on this service were revoked (its login still exists).", u.Name), nil
			},
		},

		// ---- secrets:read: standalone database users (their password travels through this scope, like reveal_db_url) ----
		{
			Name: "db_user_create", Scope: scopes.SecretsRead,
			Description: "Add a user to an existing database/bucket, with a grant preset. Returns its password once — Hosbah stores only the sealed form.",
			InputSchema: schema([]string{"database", "target_database", "user"}, map[string]any{"database": dbArg,
				"target_database": str("Database or bucket name or id"), "user": str("Lower snake_case"),
				"grants": map[string]any{"type": "string", "enum": []string{"all", "readwrite", "readonly"}, "description": "Default readwrite"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityRevealSecrets); err != nil {
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
				u, pw, err := s.CreateUser(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, args.Str("target_database"), args.Str("user"), args.Str("grants"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Created %s (%s) on %s (id %s). Password (shown once): %s", u.Name, u.Grants, svc.Name, u.ID, pw), nil
			},
		},
		{
			Name: "db_user_rotate_password", Scope: scopes.SecretsRead,
			Description: "Rotate a database user's password. Returns the new password once — Hosbah stores only the sealed form.",
			InputSchema: schema([]string{"database", "user"}, map[string]any{"database": dbArg, "user": str("Database user name or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityRevealSecrets); err != nil {
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
				u, pw, err := s.RotateUserPassword(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, args.Str("user"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Rotated %s's password (shown once): %s", u.Name, pw), nil
			},
		},

		// ---- secrets:read ----
		{
			Name: "reveal_db_url", Scope: scopes.SecretsRead,
			Description: "Reveal a data service's connection string (DATABASE_URL / REDIS_URL). Audited with this token's id.",
			InputSchema: schema([]string{"database"}, map[string]any{"database": dbArg, "user": str("Database user name or id (optional)")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityRevealSecrets); err != nil {
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
				url, err := s.RevealURL(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, args.Str("user"))
				if err != nil {
					return "", err
				}
				return url, nil
			},
		},

		// ---- destructive ----
		{
			Name: "restore_backup", Scope: scopes.Destructive, Destructive: true, ResourceType: "data_service",
			Description: "Restore a backup into a throwaway container and verify it (restore-test). confirm must equal the data service's name.",
			InputSchema: schema([]string{"database", "confirm"}, map[string]any{"database": dbArg,
				"backup": str("Backup id (optional; the newest succeeded backup by default)"),
				"confirm": str("The data service's name, exactly")}),
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
				bk, err := pickBackup(ctx, env, svc, args.Str("backup"))
				if err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				if err := s.RestoreTest(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, bk); err != nil {
					return "", err
				}
				return fmt.Sprintf("Restore-test of backup %s of %s started (a throwaway container; the live service is untouched). Poll list_backups for pass/fail.", bk.ID, svc.Name), nil
			},
		},
		{
			Name: "db_user_delete", Scope: scopes.Destructive, Destructive: true, ResourceType: "data_service",
			Description: "Delete a database user (its login/role/access-key). confirm must equal the user's name. Never the service's own root credential — that is not a user row.",
			InputSchema: schema([]string{"database", "user", "confirm"}, map[string]any{"database": dbArg, "user": str("Database user name or id"), "confirm": str("The user's name, exactly")}),
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
				if err := s.DeleteUser(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, args.Str("user"), args.Str("confirm")); err != nil {
					return "", err
				}
				return "Deleted database user " + args.Str("confirm") + " on " + svc.Name + ".", nil
			},
		},
	}
}

// ---- helpers -----------------------------------------------------------------------------------

func dbsOf(env *Env) (*dbsvc.Service, error) {
	if env.App == nil {
		return nil, errors.New("the data-service service is not running")
	}
	s, ok := env.App.DBSvc.(*dbsvc.Service)
	if !ok || s == nil {
		return nil, errors.New("the data-service service is not running")
	}
	return s, nil
}

func findService(ctx context.Context, env *Env, args Args) (db.DataService, error) {
	ref := args.Str("database")
	if ref == "" {
		return db.DataService{}, errors.New("database is required (a name or an id)")
	}
	return dbsvc.ServiceByRef(ctx, env.Q, env.Caller.TeamID, ref)
}

func pickBackup(ctx context.Context, env *Env, svc db.DataService, ref string) (db.Backup, error) {
	if ref != "" {
		bk, err := env.Q.BackupInTeam(ctx, db.BackupInTeamParams{ID: ref, TeamID: env.Caller.TeamID})
		if err != nil || bk.DataServiceID != svc.ID {
			return db.Backup{}, errors.New("backup not found for this service")
		}
		return bk, nil
	}
	rows, err := env.Q.ListBackupsOfService(ctx, db.ListBackupsOfServiceParams{DataServiceID: svc.ID, TeamID: env.Caller.TeamID, Limit: 20})
	if err != nil {
		return db.Backup{}, errors.New("could not list backups")
	}
	for _, r := range rows {
		if r.Status == "succeeded" {
			return r, nil
		}
	}
	return db.Backup{}, errors.New("no completed backup to restore — take one with backup_now first")
}

func backupLine(b db.Backup) string {
	size := "—"
	if b.SizeBytes != nil {
		size = fmt.Sprintf("%d bytes", *b.SizeBytes)
	}
	restore := "untested"
	if b.RestoreStatus != nil {
		restore = *b.RestoreStatus
	}
	line := fmt.Sprintf("%-8s %-9s %-10s snapshot %-10s restore %-8s (id %s", b.Kind, b.Status, size, orDash(b.SnapshotID), restore, b.ID)
	if b.CreatedAt != (time.Time{}) {
		line += ", " + b.CreatedAt.UTC().Format(time.RFC3339)
	}
	return line + ")"
}
