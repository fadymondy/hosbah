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
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/metrics"
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
					cred := "Hosbah holds credentials for it"
					switch {
					case dbsvc.CredentialsRequired(svc):
						cred = "give it credentials first (set_database_credentials) before databases, users or backups work"
					case !dbsvc.HasCredentials(svc):
						cred = "a native Proxmox LXC guest — Hosbah reaches it as the matching OS user (peer auth / auth_socket), no stored credential needed"
					}
					fmt.Fprintf(&b, "imported: Hosbah found it running; it never backs it up on its own; %s\n%s\nlast seen: %s\n",
						cred, importedLine(svc.Name, svc.OriginKind, svc.Origin, svc.ImportedState, svc.Kind+" "+svc.Version), lastSeen(svc.LastSeenAt))
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
		{
			Name: "backups_overview", Scope: scopes.Read,
			Description: "Backups across every data service of the team at a glance: per service its newest backup (status, when, error), the newest successful one (when, size, restore test) and its schedule. Services with neither are listed as unprotected.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				svcs, err := env.Q.ListDataServicesOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list data services")
				}
				sums, err := env.Q.TeamBackupSummaries(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list backups")
				}
				if len(svcs) == 0 {
					return "No data services yet. create_database provisions one.", nil
				}
				by := make(map[string]db.TeamBackupSummariesRow, len(sums))
				for _, r := range sums {
					by[r.DataServiceID] = r
				}
				now := time.Now()
				var b, none strings.Builder
				for _, s := range svcs {
					r, ok := by[s.ID]
					if !ok {
						fmt.Fprintf(&none, "  %s (%s)\n", s.Name, s.Kind)
						continue
					}
					last := "never"
					if r.LastStatus != nil {
						last = *r.LastStatus + " " + ago(r.LastCreatedAt, now)
						if r.LastError != nil && *r.LastError != "" {
							last += " — " + *r.LastError
						}
					}
					good := "none"
					if r.LastOkAt != nil {
						good = ago(r.LastOkAt, now)
						if r.LastOkSize != nil {
							good += ", " + humanBytes(*r.LastOkSize)
						}
						good += ", restore " + orDash(r.LastRestoreStatus)
					}
					sched := "manual only"
					if r.ScheduleCron != nil {
						sched = *r.ScheduleCron + " to " + orDash(r.ScheduleTarget)
						if r.SchedulePaused.Valid && r.SchedulePaused.Bool {
							sched += " (paused)"
						} else if r.ScheduleNextRunAt != nil {
							sched += ", next " + r.ScheduleNextRunAt.UTC().Format(time.RFC3339)
						}
					}
					fmt.Fprintf(&b, "  %s (%s): %d backup(s); last %s; last good %s; schedule %s\n", s.Name, s.Kind, r.BackupCount, last, good, sched)
				}
				out := b.String()
				if none.Len() > 0 {
					out += "No backups and no schedule (backup_now or a schedule on the database page):\n" + none.String()
				}
				return out, nil
			},
		},

		// ---- write ----
		{
			Name: "create_database", Scope: scopes.Write,
			Description: "Provision a managed data service (postgres, mysql, redis, or minio for S3-compatible buckets) on one of the team's servers. create_db adds databases and users inside it.",
			InputSchema: schema([]string{"name", "server", "kind"}, map[string]any{
				"name": str("Lowercase letters, digits, dashes"), "server": str("Server name or id"),
				"kind":    map[string]any{"type": "string", "enum": []string{"postgres", "mysql", "redis", "minio"}, "description": "minio = S3-compatible object storage (buckets)"},
				"version": str("Image tag (optional; a current version by default). For postgres, pgvector-<tag> (e.g. pgvector-pg17, pgvector-0.8.0-pg17) provisions pgvector/pgvector:<tag> instead of the plain postgres image, so vector and other pgvector extensions are available.")}),
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
				"backup":  str("Backup id (optional; the newest succeeded backup by default)"),
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

		// ---- table browser and SQL console (MH-574 F2): postgres/mysql only. Owner/admin only
		// (AbilityRevealSecrets, checked inside Run) regardless of the token's scope — table contents
		// can hold secrets, so it is treated as reveal-grade like a user's password. db_query never
		// writes; db_execute is the one destructive tool that can (confirm = the service's name).
		{
			Name: "db_tables", Scope: scopes.Read,
			Description: "A database's tables: schema, name, estimated row count and size. Postgres and MySQL only.",
			InputSchema: schema([]string{"database", "target_database"}, map[string]any{"database": dbArg,
				"target_database": str("Database or bucket name or id")}),
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
				res, err := s.Query(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, dbsvc.QueryInput{Database: args.Str("target_database"), Mode: "tables"})
				if err != nil {
					return "", err
				}
				if len(res.Tables) == 0 {
					return "No tables.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d table(s):\n", len(res.Tables))
				for _, t := range res.Tables {
					name := t.Name
					if t.Schema != "" {
						name = t.Schema + "." + t.Name
					}
					fmt.Fprintf(&b, "  %-32s ~%d rows  %d bytes\n", name, t.RowsEstimate, t.SizeBytes)
				}
				return b.String(), nil
			},
		},
		{
			Name: "db_table_columns", Scope: scopes.Read,
			Description: "A table's columns: name and type.",
			InputSchema: schema([]string{"database", "target_database", "table"}, map[string]any{"database": dbArg,
				"target_database": str("Database or bucket name or id"), "table": str("Table name, or schema.table for Postgres")}),
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
				res, err := s.Query(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, dbsvc.QueryInput{Database: args.Str("target_database"), Mode: "columns", Table: args.Str("table")})
				if err != nil {
					return "", err
				}
				if len(res.Columns) == 0 {
					return "No columns.", nil
				}
				var b strings.Builder
				for _, c := range res.Columns {
					fmt.Fprintf(&b, "  %-32s %s\n", c.Name, c.Type)
				}
				return b.String(), nil
			},
		},
		{
			Name: "db_table_rows", Scope: scopes.Read,
			Description: "A page of a table's rows, read-only.",
			InputSchema: schema([]string{"database", "target_database", "table"}, map[string]any{"database": dbArg,
				"target_database": str("Database or bucket name or id"), "table": str("Table name, or schema.table for Postgres"),
				"limit": map[string]any{"type": "integer", "default": 100, "maximum": 500}, "offset": map[string]any{"type": "integer", "default": 0}}),
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
				res, err := s.Query(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, dbsvc.QueryInput{
					Database: args.Str("target_database"), Mode: "rows", Table: args.Str("table"),
					Limit: args.Int("limit", 100, 1, 500), Offset: args.Int("offset", 0, 0, 1<<30)})
				if err != nil {
					return "", err
				}
				return renderQueryResult(res), nil
			},
		},
		{
			Name: "db_query", Scope: scopes.Read,
			Description: "Run one read-only SQL statement (always rolled back). db_execute is the write version.",
			InputSchema: schema([]string{"database", "target_database", "sql"}, map[string]any{"database": dbArg,
				"target_database": str("Database or bucket name or id"), "sql": str("One SQL statement")}),
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
				res, err := s.Query(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, dbsvc.QueryInput{
					Database: args.Str("target_database"), Mode: "sql", SQL: args.Str("sql"), Write: false})
				if err != nil {
					return "", err
				}
				return renderQueryResult(res), nil
			},
		},

		// ---- DB metrics and slow queries (MH-575 F6): postgres/mysql only, owner/admin only
		// (AbilityRevealSecrets — the same tier the table browser and SQL console use). Every
		// statement db.insights runs is fixed and agent-owned; nothing here reaches user SQL.
		{
			Name: "db_insights", Scope: scopes.Read,
			Description: "DB metrics for a Postgres/MySQL data service: connections vs max, cache hit ratio, transactions/status counters, database sizes, long-running queries and locks waiting.",
			InputSchema: schema([]string{"database"}, map[string]any{"database": dbArg}),
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
				res, err := s.Insights(ctx, env.Q, env.Caller.TeamID, svc)
				if err != nil {
					return "", err
				}
				return renderInsights(res), nil
			},
		},
		{
			Name: "db_slow_queries", Scope: scopes.Read,
			Description: "Top statements by total time (pg_stat_statements / performance_schema), plus a hint how to enable it when it is not available.",
			InputSchema: schema([]string{"database"}, map[string]any{"database": dbArg}),
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
				res, err := s.Insights(ctx, env.Q, env.Caller.TeamID, svc)
				if err != nil {
					return "", err
				}
				if !res.StatStatementsAvailable {
					return res.StatStatementsHint, nil
				}
				if len(res.TopStatements) == 0 {
					return "No statements recorded yet.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d top statement(s) by total time:\n", len(res.TopStatements))
				for _, t := range res.TopStatements {
					fmt.Fprintf(&b, "  calls %-8d total %-10.1fms mean %-8.2fms rows %-8d %s\n", t.Calls, t.TotalTimeMS, t.MeanTimeMS, t.Rows, clipStr(t.Query, 200))
				}
				return b.String(), nil
			},
		},

		{
			Name: "db_metrics_history", Scope: scopes.Read,
			Description: "A Postgres/MySQL data service's KPI history: connections vs max, cache hit ratio, database size, slow-query count and transactions/queries per second, sampled about once a minute.",
			InputSchema: schema([]string{"database"}, map[string]any{"database": dbArg,
				"range": map[string]any{"type": "string", "enum": []string{"1h", "6h", "24h", "7d", "30d"}, "default": "1h"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				svc, err := findService(ctx, env, args)
				if err != nil {
					return "", err
				}
				r, ok := metrics.Ranges[args.Str("range")]
				if !ok {
					r = metrics.Ranges["1h"]
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				series, err := s.MetricsSeries(ctx, env.Q, env.Caller.TeamID, svc.ID, r, time.Now())
				if err != nil {
					return "", errors.New("could not read metrics history")
				}
				if len(series.Points) == 0 {
					return fmt.Sprintf("%s has no metrics history in the last %s yet.", svc.Name, r.Key), nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%s — last %s (%d point(s)):\n", svc.Name, r.Key, len(series.Points))
				for _, p := range series.Points {
					fmt.Fprintf(&b, "  %s  conn %.0f/%d  cache hit %.1f%%  size %s  slow %.0f  rate %.2f/s\n",
						p.At.Format(time.RFC3339), p.Connections, p.MaxConnections, p.CacheHitRatio, bytesOf(uint64(p.SizeBytes)), p.SlowQueryCount, p.RatePerSec)
				}
				return b.String(), nil
			},
		},

		// ---- destructive ----
		{
			Name: "db_cancel_query", Scope: scopes.Destructive, Destructive: true, ResourceType: "data_service",
			Description: "Cancel a running backend/query (pg_cancel_backend / KILL QUERY), using the pid/id from db_insights' long-running list. confirm must equal the data service's name. Audited.",
			InputSchema: schema([]string{"database", "id", "confirm"}, map[string]any{"database": dbArg,
				"id":      map[string]any{"type": "integer", "description": "The Postgres pid or MySQL processlist Id"},
				"confirm": str("The data service's name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityRevealSecrets); err != nil {
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
				res, err := s.CancelQuery(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, int64(args.Int("id", 0, 1, 1<<62)))
				if err != nil {
					return "", err
				}
				if !res.OK {
					return "Not cancelled: " + res.Detail, nil
				}
				return "Cancelled.", nil
			},
		},

		// ---- destructive: the one tool that can write ----
		{
			Name: "db_execute", Scope: scopes.Destructive, Destructive: true, ResourceType: "data_service",
			Description: "Run one SQL statement that writes (commits instead of rolling back). Audited with the statement text. confirm must equal the data service's name.",
			InputSchema: schema([]string{"database", "target_database", "sql", "confirm"}, map[string]any{"database": dbArg,
				"target_database": str("Database or bucket name or id"), "sql": str("One SQL statement"), "confirm": str("The data service's name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityRevealSecrets); err != nil {
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
				res, err := s.Query(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, dbsvc.QueryInput{
					Database: args.Str("target_database"), Mode: "sql", SQL: args.Str("sql"), Write: true})
				if err != nil {
					return "", err
				}
				return renderQueryResult(res), nil
			},
		},

		{
			Name: "db_import", Scope: scopes.Destructive, Destructive: true, ResourceType: "data_service",
			Description: "Import an existing Postgres database into one Hosbah already manages (pg_dump | pg_restore, run inside the managed service's own container — this is how a production database is migrated in). target_database must be a database Hosbah manages on that service. source_url is a postgres:// connection string to the database being imported from; it is a secret and never echoed back or logged. Without replace, refused if target_database already has tables; with replace, it is dropped and recreated first (same owner) so a final cutover sync can re-run the same import. Runs in the background; poll get_database for the resulting status (ready/failed). confirm must equal the data service's name.",
			InputSchema: schema([]string{"database", "target_database", "source_url", "confirm"}, map[string]any{"database": dbArg,
				"target_database": str("The database to import into — must already exist on this service (create_db makes it)"),
				"source_url":      str("postgres://user:pass@host:port/db — the database to import from"),
				"replace":         map[string]any{"type": "boolean", "default": false, "description": "Drop and recreate target_database first (same owner), instead of refusing when it already has tables"},
				"confirm":         str("The data service's name, exactly")}),
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
				dbRow, err := dbsvc.DatabaseInService(ctx, env.Q, env.Caller.TeamID, svc, args.Str("target_database"))
				if err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				if err := s.ImportDatabase(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, dbRow, dbsvc.ImportInput{
					SourceURL: args.Str("source_url"), Replace: args.Bool("replace"),
				}); err != nil {
					return "", err
				}
				return fmt.Sprintf("Importing into %s on %s. It runs pg_dump | pg_restore in the background — poll get_database for 'ready' or 'failed'.", dbRow.Name, svc.Name), nil
			},
		},
	}
}

// renderQueryResult formats a table/SQL result as a small text table, capped like the JSON API.
func renderQueryResult(res agentproto.DBQueryResult) string {
	var b strings.Builder
	if len(res.Columns) == 0 {
		fmt.Fprintf(&b, "%d row(s), %dms\n", res.RowCount, res.DurationMS)
		return b.String()
	}
	names := make([]string, len(res.Columns))
	for i, c := range res.Columns {
		names[i] = c.Name
	}
	fmt.Fprintln(&b, strings.Join(names, " | "))
	for _, row := range res.Rows {
		cells := make([]string, len(row))
		for i, c := range row {
			if c == nil {
				cells[i] = "NULL"
			} else {
				cells[i] = *c
			}
		}
		fmt.Fprintln(&b, strings.Join(cells, " | "))
	}
	fmt.Fprintf(&b, "(%d row(s), %dms%s)\n", res.RowCount, res.DurationMS, map[bool]string{true: ", truncated", false: ""}[res.Truncated])
	return b.String()
}

// renderInsights formats db.insights' answer as text.
func renderInsights(res agentproto.DBInsightsResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d/%d connections, cache hit %.1f%%, %d lock(s) waiting\n",
		res.Kind, res.Connections, res.MaxConnections, res.CacheHitRatio, res.LocksWaiting)
	if res.Kind == "postgres" {
		fmt.Fprintf(&b, "transactions: %d committed, %d rolled back; %d deadlock(s); %d temp byte(s)\n",
			res.XactCommit, res.XactRollback, res.Deadlocks, res.TempBytes)
	} else {
		fmt.Fprintf(&b, "threads: %d connected, %d running; %d question(s), %d slow quer(y/ies); up %ds; slow_query_log=%s\n",
			res.ThreadsConnected, res.ThreadsRunning, res.Questions, res.SlowQueries, res.UptimeSeconds, res.SlowQueryLog)
	}
	for _, d := range res.Databases {
		fmt.Fprintf(&b, "  database %-24s %d bytes\n", d.Name, d.SizeBytes)
	}
	if len(res.LongRunning) == 0 {
		b.WriteString("no long-running queries\n")
	} else {
		fmt.Fprintf(&b, "%d long-running quer(y/ies):\n", len(res.LongRunning))
		for _, q := range res.LongRunning {
			fmt.Fprintf(&b, "  pid %-8s %6.1fs  %-10s %s\n", q.PID, q.DurationSeconds, q.State, clipStr(q.Query, 200))
		}
	}
	if !res.StatStatementsAvailable {
		b.WriteString(res.StatStatementsHint + "\n")
	}
	return b.String()
}

func clipStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
