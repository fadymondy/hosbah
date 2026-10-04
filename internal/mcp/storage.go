package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/storage"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// StorageTools: the "Storage" section of an app — persistent volumes and S3 buckets on a MinIO
// data service. Same actions as the REST API (internal/storage, internal/dbsvc/buckets.go).
func StorageTools() []Tool {
	appArg := str("App name or id")
	flag := func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }

	return []Tool{
		{
			Name: "list_team_storage", Scope: scopes.Read,
			Description: "Every persistent volume and S3 bucket across the team's apps: app, volume name and mount path; bucket, " +
				"MinIO service and status. From the database only, so no sizes (get_app_storage asks the server). Never a secret.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				vols, err := env.Q.TeamVolumes(ctx, env.Caller.TeamID)
				if err != nil {
					return "", err
				}
				buckets, err := env.Q.TeamBuckets(ctx, env.Caller.TeamID)
				if err != nil {
					return "", err
				}
				var b strings.Builder
				if len(vols) == 0 {
					b.WriteString("No volumes.\n")
				}
				for _, v := range vols {
					fmt.Fprintf(&b, "volume %s  app %s  %s\n", v.Name, v.AppName, v.MountPath)
				}
				if len(buckets) == 0 {
					b.WriteString("No buckets.\n")
				}
				for _, k := range buckets {
					fmt.Fprintf(&b, "bucket %s on %s  app %s  %s", k.Bucket, k.ServiceName, k.AppName, k.Status)
					if k.Error != nil {
						fmt.Fprintf(&b, "  error: %s", *k.Error)
					}
					b.WriteString("\n")
				}
				return b.String(), nil
			},
		},
		{
			Name: "get_app_storage", Scope: scopes.Read,
			Description: "An app's persistent volumes (name, mount path, Docker volume, whether the live release mounts it, bytes when the " +
				"server reports them) and its S3 buckets (service, bucket, access key id, the variables it set, status). Never a secret.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				vols, err := storage.List(ctx, env.Q, env.Caller.TeamID, a)
				if err != nil {
					return "", err
				}
				buckets, err := dbsvc.ListBuckets(ctx, env.Q, env.Caller.TeamID, a)
				if err != nil {
					return "", err
				}
				text := func() string {
					var b strings.Builder
					if len(vols) == 0 {
						b.WriteString("No volumes. create_app_volume adds one.\n")
					}
					for _, v := range vols {
						size, state := "size unknown", "applies on the next deploy or restart"
						if v.Bytes != nil {
							size = fmt.Sprintf("%d bytes", *v.Bytes)
						}
						if v.Mounted {
							state = "mounted"
						}
						fmt.Fprintf(&b, "volume %s  %s  (%s)  %s, %s\n", v.Name, v.MountPath, v.Volume, state, size)
					}
					if len(buckets) == 0 {
						b.WriteString("No buckets. create_app_bucket adds one.\n")
					}
					for _, k := range buckets {
						fmt.Fprintf(&b, "bucket %s on %s  key %s  %s  sets %s", k.Bucket, k.Service, k.AccessKey, k.Status, strings.Join(k.Env, ", "))
						if k.Error != nil {
							fmt.Fprintf(&b, "  error: %s", *k.Error)
						}
						b.WriteString("\n")
					}
					return b.String()
				}
				o, oerr := opsOf(env)
				s, serr := deploys(env)
				if oerr != nil || serr != nil {
					return text(), nil
				}
				op := storage.QueueUsage(ctx, env.Q, o, env.Caller.TeamID, mcpActor(env), a, vols)
				if op.ID == "" {
					return text(), nil
				}
				env.Then = func(ctx context.Context) (string, error) { // after the commit; best effort
					var res agentproto.VolumeUsageResult
					if _, err := s.AwaitResult(ctx, op, storage.UsageWait, &res); err == nil {
						storage.WithUsage(vols, res)
					}
					return text(), nil
				}
				return "", nil
			},
		},
		{
			Name: "create_app_volume", Scope: scopes.Write,
			Description: "Add a persistent volume to an app: mounted at mount_path in its web and every worker container from the next " +
				"deploy or restart. The Docker volume's name is derived (hosbah-vol-<app id>-<name>; cloudy-vol-… for a volume made before the rename). System paths (/proc, /sys, /dev, /run, " +
				"/etc, /bin, /sbin, /usr, /lib, /lib64, /boot, /var/run) and paths inside another volume are refused. Owner or admin.",
			InputSchema: schema([]string{"app", "name", "mount_path"}, map[string]any{"app": appArg,
				"name":       str("Lowercase letter, then up to 30 lowercase letters, digits and dashes; unique in the app"),
				"mount_path": str("Absolute path inside the containers, e.g. /data")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				v, err := storage.Create(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, args.Str("name"), args.Str("mount_path"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Volume %s (%s) will be mounted at %s from %s's next deploy or restart.", v.Name, v.Volume, v.MountPath, a.Name), nil
			},
		},
		{
			Name: "remove_app_volume", Scope: scopes.Write,
			Description: "Remove a volume from an app: the next deploy or restart no longer mounts it; its data stays on the server unless " +
				"delete_data — refused while the live release still mounts it. confirm must equal the volume's name. Owner or admin.",
			InputSchema: schema([]string{"app", "volume", "confirm"}, map[string]any{"app": appArg,
				"volume":      str("Volume name or id"),
				"confirm":     str("The volume's name, exactly"),
				"delete_data": flag("Also remove the Docker volume and everything in it")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				v, err := storage.Find(ctx, env.Q, env.Caller.TeamID, a, args.Str("volume"))
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, v.Name); err != nil {
					return "", err
				}
				o, err := opsOf(env)
				if err != nil {
					return "", err
				}
				deleteData, _ := args["delete_data"].(bool)
				res, err := storage.Remove(ctx, env.Q, o, env.Caller.TeamID, mcpActor(env), a, v, v.Name, deleteData)
				if err != nil {
					return "", err
				}
				out := "Removed volume " + v.Name + " — not mounted from the next deploy or restart."
				if res.Operation != nil {
					out += " Its data is being removed on the server (operation " + *res.Operation + ")."
				} else {
					out += " Its data stays on the server."
				}
				return out, nil
			},
		},
		{
			Name: "create_app_bucket", Scope: scopes.Write,
			Description: "Create an S3 bucket for an app on one of the team's MinIO data services, with an access key limited to that bucket, " +
				"and set <PREFIX>_ENDPOINT, _BUCKET, _ACCESS_KEY_ID, _SECRET_ACCESS_KEY, _REGION and _USE_PATH_STYLE on the app as sealed " +
				"variables (next deploy or restart). Refused when one of those names is already set. The secret is never shown. Owner or admin.",
			InputSchema: schema([]string{"app", "service", "bucket"}, map[string]any{"app": appArg,
				"service":    str("The MinIO data service: name or id"),
				"bucket":     str("3 to 63 lowercase letters, digits and dashes; unique on the service"),
				"env_prefix": str("Prefix of the variables (default S3): an upper-case letter, then upper-case letters, digits and _")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
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
				b, op, err := s.CreateBucket(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, args.Str("service"), args.Str("bucket"), args.Str("env_prefix"))
				if err != nil {
					return "", err
				}
				teamID := env.Caller.TeamID
				env.Then = func(context.Context) (string, error) { // after the commit
					s.FollowBucket(teamID, b.ID, op)
					return "", nil
				}
				return fmt.Sprintf("Creating bucket %s on %s (operation %s). %s gets %s on its next deploy or restart; get_app_storage shows when it is ready.",
					b.Bucket, b.Service, op.ID, a.Name, strings.Join(b.Env, ", ")), nil
			},
		},
		{
			Name: "remove_app_bucket", Scope: scopes.Write,
			Description: "Remove a bucket from an app: its access key is revoked and the variables it set are unset. The bucket and its objects " +
				"stay unless delete_data — then only if the bucket is empty, unless force. confirm must equal the bucket's name. Owner or admin.",
			InputSchema: schema([]string{"app", "bucket", "confirm"}, map[string]any{"app": appArg,
				"bucket":      str("Bucket name or id"),
				"confirm":     str("The bucket's name, exactly"),
				"delete_data": flag("Also remove the bucket (only if it is empty, unless force)"),
				"force":       flag("With delete_data: remove the bucket with everything in it")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				b, err := dbsvc.FindBucket(ctx, env.Q, env.Caller.TeamID, a, args.Str("bucket"))
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, b.Bucket); err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				deleteData, _ := args["delete_data"].(bool)
				force, _ := args["force"].(bool)
				actor, teamID := mcpActor(env), env.Caller.TeamID
				rm, err := s.BeginBucketRemoval(ctx, env.Q, teamID, actor, a, b, b.Bucket, deleteData, force)
				if err != nil {
					return "", err
				}
				env.Then = func(ctx context.Context) (string, error) { // after the commit
					failure := s.AwaitBucketRemoval(ctx, rm)
					fin := context.WithoutCancel(ctx)
					if err := tenancy.Tx(fin, env.App.SQLDB, env.Scope, func(q *db.Queries, _ *sql.Tx) error {
						return s.FinishBucketRemoval(fin, q, teamID, actor, a, rm, failure)
					}); err != nil {
						return "", err
					}
					if failure != "" {
						return "", dbsvc.BucketRemovalRefused(failure)
					}
					out := "Removed bucket " + b.Bucket + " from " + a.Name + ": its key is revoked and " +
						strings.Join(dbsvc.BucketEnvNames(b.EnvPrefix), ", ") + " are unset (next deploy or restart)."
					if deleteData {
						out += " The bucket itself was removed."
					}
					return out, nil
				}
				return "", nil
			},
		},
	}
}

