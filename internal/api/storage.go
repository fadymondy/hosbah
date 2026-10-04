package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/storage"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The "Storage" section of a site: persistent volumes (internal/storage) and S3 buckets on a MinIO
data service (internal/dbsvc/buckets.go).

Read: any member. Write: whoever may manage the app (manage_apps). Imported apps have no release to
mount into and no env Hosbah injects: every write is 422. Everything here applies from the app's
next deploy or restart — nothing running is touched. A bucket's secret is never in a response.
*/

// VolumePath addresses one volume of an app (id or name).
type VolumePath struct {
	AppPath
	Volume string `path:"volume" maxLength:"64" doc:"Volume id or name."`
}

// BucketPath addresses one bucket of an app (id or bucket name).
type BucketPath struct {
	AppPath
	Bucket string `path:"bucket" maxLength:"64" doc:"Bucket id or name."`
}

// StorageOut is a site's storage.
type StorageOut struct {
	Volumes        []storage.Volume `json:"volumes"`
	Buckets        []dbsvc.Bucket   `json:"buckets"`
	UsageOperation *string          `json:"usage_operation" doc:"The volume.usage operation that was asked for the volumes' bytes; null when the app has no volumes or its server is not connected."`
}

func (d *deps) registerStorage(api huma.API) {
	tags := []string{"Storage"}
	base := "/api/teams/{team}/apps/{app}/storage"

	type teamStorageOut struct {
		Body struct {
			Volumes []db.TeamVolumesRow `json:"volumes" doc:"Every app volume of the team, with its app and server. No bytes: an app's storage asks its server."`
			Buckets []db.TeamBucketsRow `json:"buckets" doc:"Every app bucket of the team, with its app and MinIO service. Never a secret."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-team-storage", Method: http.MethodGet, Path: "/api/teams/{team}/storage", Tags: tags,
		Summary: "Every persistent volume and S3 bucket of the team's apps, from the database only (no server is asked). Any member.",
	}, func(ctx context.Context, in *TeamPath) (*teamStorageOut, error) {
		out := &teamStorageOut{}
		out.Body.Volumes, out.Body.Buckets = []db.TeamVolumesRow{}, []db.TeamBucketsRow{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			vols, err := q.TeamVolumes(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list the volumes")
			}
			buckets, err := q.TeamBuckets(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list the buckets")
			}
			out.Body.Volumes = append(out.Body.Volumes, vols...)
			out.Body.Buckets = append(out.Body.Buckets, buckets...)
			return nil
		})
		if err != nil {
			return nil, err
		}
		return out, nil
	})

	type storageOut struct{ Body StorageOut }
	huma.Register(api, huma.Operation{OperationID: "get-app-storage", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The app's persistent volumes and S3 buckets. A volume's mounted says whether the live release runs with it; bytes is " +
			"best effort — the server is asked (up to 8 s) what Docker reports, and an offline or silent server leaves bytes null, " +
			"still 200. A bucket's secret is never returned. Any member.",
	}, func(ctx context.Context, in *AppPath) (*storageOut, error) {
		out := &storageOut{Body: StorageOut{Volumes: []storage.Volume{}, Buckets: []dbsvc.Bucket{}}}
		var op db.Operation
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if out.Body.Volumes, err = storage.List(ctx, q, m.TeamID, a); err != nil {
				return huma.Error500InternalServerError("could not list the volumes")
			}
			if out.Body.Buckets, err = dbsvc.ListBuckets(ctx, q, m.TeamID, a); err != nil {
				return huma.Error500InternalServerError("could not list the buckets")
			}
			op = storage.QueueUsage(ctx, q, d.ops, m.TeamID, webActor(id), a, out.Body.Volumes)
			return nil
		})
		if err != nil {
			return nil, err
		}
		if op.ID != "" { // after the commit: the operation exists for the dispatcher
			out.Body.UsageOperation = &op.ID
			var res agentproto.VolumeUsageResult
			if _, err := d.deploys().AwaitResult(ctx, op, storage.UsageWait, &res); err == nil {
				storage.WithUsage(out.Body.Volumes, res)
			}
		}
		return out, nil
	})

	type volumeCreatedOut struct {
		Body struct {
			storage.Volume
			Applies string `json:"applies" enum:"next_deploy" doc:"The volume is mounted from the app's next deploy or restart."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "create-app-volume", Method: http.MethodPost, Path: base + "/volumes",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a persistent volume: a Docker volume named hosbah-vol-<app id>-<name> (derived; cloudy-vol-… for one made before the rename), mounted at mount_path in the web and " +
			"every worker container from the next deploy or restart. 422: a bad name; a mount path that is not absolute, is /, or is at or " +
			"under /proc, /sys, /dev, /run, /etc, /bin, /sbin, /usr, /lib, /lib64, /boot or /var/run (after normalising, so /data/../etc " +
			"is /etc); an imported app. 409: the name or path is taken, the path is inside or around another volume's, or the app has 10. " +
			"Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Name      string `json:"name" minLength:"1" maxLength:"31" doc:"^[a-z][a-z0-9-]{0,30}$, unique in the app."`
			MountPath string `json:"mount_path" minLength:"2" maxLength:"200" doc:"Absolute path inside the containers."`
		}
	}) (*volumeCreatedOut, error) {
		out := &volumeCreatedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body.Volume, err = storage.Create(ctx, q, m.TeamID, webActor(id), a, in.Body.Name, in.Body.MountPath)
			out.Body.Applies = storage.AppliesNextDeploy
			return scheduleErr(err, "could not create the volume")
		})
		return out, err
	})

	type volumeRemovedOut struct{ Body storage.Removed }
	huma.Register(api, huma.Operation{OperationID: "remove-app-volume", Method: http.MethodDelete, Path: base + "/volumes/{volume}", Tags: tags,
		Summary: "Remove a volume (confirm = its name): the record goes and the next deploy or restart no longer mounts it; the data stays on " +
			"the server. With delete_data the Docker volume is removed too (operation: a volume.remove on the app's server) — 409 while the " +
			"live release still mounts it (remove the record and deploy or restart first; a volume added again under the same name is the " +
			"same Docker volume, so its data can then be removed with delete_data before the next deploy). The operation itself fails, " +
			"removing nothing, while any container uses the volume. 422: confirm is not the name; an imported app. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		VolumePath
		Body struct {
			Confirm    string `json:"confirm" minLength:"1" maxLength:"64" doc:"The volume's name, exactly."`
			DeleteData bool   `json:"delete_data,omitempty" doc:"Also remove the Docker volume and everything in it."`
		}
	}) (*volumeRemovedOut, error) {
		out := &volumeRemovedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			v, err := storage.Find(ctx, q, m.TeamID, a, in.Volume)
			if err != nil {
				return errNotFound
			}
			out.Body, err = storage.Remove(ctx, q, d.ops, m.TeamID, webActor(id), a, v, in.Body.Confirm, in.Body.DeleteData)
			return scheduleErr(err, "could not remove the volume")
		})
		return out, err
	})

	type bucketCreatedOut struct {
		Body struct {
			dbsvc.Bucket
			Applies   string `json:"applies" enum:"next_deploy" doc:"The variables reach the app on its next deploy or restart."`
			Operation string `json:"operation" doc:"The bucket.create operation; the bucket's status follows it (creating, then ready or failed)."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "create-app-bucket", Method: http.MethodPost, Path: base + "/buckets",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Create an S3 bucket for the app on one of the team's MinIO data services, with an access key whose policy reaches that " +
			"bucket only, and set <PREFIX>_ENDPOINT, _BUCKET, _ACCESS_KEY_ID, _SECRET_ACCESS_KEY, _REGION (us-east-1) and _USE_PATH_STYLE " +
			"(true) on the app as sealed variables (env lists the names; the secret is never returned). 201 with status creating — it " +
			"turns ready or failed when the server has answered. 422: a bad bucket name or env_prefix, a service that is not a managed " +
			"MinIO, an imported app. 409: the bucket exists on that service, the app has 5, the service is not running, or one of the " +
			"variable names is already set on the app (nothing is ever overwritten). Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Service   string `json:"service" minLength:"1" maxLength:"64" doc:"The MinIO data service: id or name."`
			Bucket    string `json:"bucket" minLength:"3" maxLength:"63" doc:"3 to 63 of a-z, 0-9 and -; unique on the service."`
			EnvPrefix string `json:"env_prefix,omitempty" maxLength:"21" doc:"^[A-Z][A-Z0-9_]{0,20}$ (default S3)."`
		}
	}) (*bucketCreatedOut, error) {
		out := &bucketCreatedOut{}
		var (
			op     db.Operation
			teamID string
		)
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			teamID = m.TeamID
			out.Body.Bucket, op, err = d.dbs().CreateBucket(ctx, q, m.TeamID, webActor(id), a, in.Body.Service, in.Body.Bucket, in.Body.EnvPrefix)
			return storageErr(err, "could not create the bucket")
		})
		if err != nil {
			return nil, err
		}
		d.dbs().FollowBucket(teamID, out.Body.ID, op) // after the commit
		out.Body.Applies, out.Body.Operation = storage.AppliesNextDeploy, op.ID
		return out, nil
	})

	type bucketRemovedOut struct {
		Body struct {
			Removed    bool     `json:"removed"`
			DeleteData bool     `json:"delete_data"`
			Unset      []string `json:"unset" doc:"The variables removed from the app."`
			Applies    string   `json:"applies" enum:"next_deploy" doc:"The app loses the variables on its next deploy or restart; the key is already revoked."`
			Operation  *string  `json:"operation" doc:"The bucket.remove operation; null when the bucket never came to exist."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "remove-app-bucket", Method: http.MethodDelete, Path: base + "/buckets/{bucket}", Tags: tags,
		Summary: "Remove a bucket from the app (confirm = the bucket's name): its access key is revoked on the MinIO service and the variables " +
			"it set are unset. The bucket and its objects stay unless delete_data — and then only if the bucket is empty, unless force. " +
			"Waits up to 90 s for the server; 409 (nothing changed): the bucket is not empty, the server is offline or did not answer, " +
			"or the bucket is still being created. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		BucketPath
		Body struct {
			Confirm    string `json:"confirm" minLength:"1" maxLength:"64" doc:"The bucket's name, exactly."`
			DeleteData bool   `json:"delete_data,omitempty" doc:"Also remove the bucket — only if it is empty, unless force."`
			Force      bool   `json:"force,omitempty" doc:"With delete_data: remove the bucket with everything in it."`
		}
	}) (*bucketRemovedOut, error) {
		out := &bucketRemovedOut{}
		var rm dbsvc.BucketRemoval
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			b, err := dbsvc.FindBucket(ctx, q, m.TeamID, a, in.Bucket)
			if err != nil {
				return errNotFound
			}
			rm, err = d.dbs().BeginBucketRemoval(ctx, q, m.TeamID, webActor(id), a, b, in.Body.Confirm, in.Body.DeleteData, in.Body.Force)
			return storageErr(err, "could not remove the bucket")
		})
		if err != nil {
			return nil, err
		}
		failure := d.dbs().AwaitBucketRemoval(ctx, rm) // after the commit
		fin := context.WithoutCancel(ctx) // a caller who hung up must not leave the bucket 'removing'
		err = d.inTeam(fin, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(fin, q, m, in.App)
			if err != nil {
				return err
			}
			return storageErr(d.dbs().FinishBucketRemoval(fin, q, m.TeamID, webActor(id), a, rm, failure), "could not remove the bucket")
		})
		if err != nil {
			return nil, err
		}
		if failure != "" {
			return nil, storageErr(dbsvc.BucketRemovalRefused(failure), "could not remove the bucket")
		}
		out.Body.Removed, out.Body.DeleteData, out.Body.Unset, out.Body.Applies = true, in.Body.DeleteData, dbsvc.BucketEnvNames(rm.Bucket.EnvPrefix), storage.AppliesNextDeploy
		if rm.Operation.ID != "" {
			out.Body.Operation = &rm.Operation.ID
		}
		return out, nil
	})
}

// storageErr maps a dbsvc error; an imported app is this section's 422.
func storageErr(err error, fallback string) error { return dbErr(err, fallback) }
