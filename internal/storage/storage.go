// Package storage is the "Storage" section of a git-deployed site (app): its persistent volumes.
// (Its S3 buckets live with the data services, internal/dbsvc/buckets.go.)
//
// A volume is a name and a mount path. The Docker volume behind it is DERIVED —
// hosbah-vol-<app id>-<name> (cloudy-vol-… when made before the rename; the agent keeps that name) — and the deploy pipeline (internal/deploy, startParams) hands the
// app's volumes to the agent as typed mounts, which the agent validates again and adds to the web
// and every worker container of a release. So a volume applies from the next deploy, restart or
// rollback; whether the live release mounts it is read from what that release recorded
// (releases.containers.volumes), not guessed from timestamps.
//
// Like internal/schedules' actions, every function runs in the CALLER's team-scoped transaction
// after the caller checked the member's role, and writes its audit entry on that transaction.
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// MaxVolumesPerApp bounds an app's volumes.
const MaxVolumesPerApp = agentproto.MaxAppVolumes

// AppliesNextDeploy is what a change answers with: nothing running is touched.
const AppliesNextDeploy = "next_deploy"

func invalid(msg string) error  { return &deploy.ActionError{Status: 422, Msg: msg} }
func conflict(msg string) error { return &deploy.ActionError{Status: 409, Msg: msg} }
func notFound(what string) error {
	return &deploy.ActionError{Status: 404, Msg: what + " not found"}
}

// RefuseImported: storage belongs to a release Hosbah deploys. An imported app has none: 422.
func RefuseImported(a db.App) error {
	if deploy.IsImported(a) {
		return invalid("storage belongs to a site Hosbah deploys — " + a.Name + " is an imported app")
	}
	return nil
}

// Volume is a volume as the API shows it.
type Volume struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MountPath string    `json:"mount_path"`
	Volume    string    `json:"volume" doc:"The Docker volume on the app's server: hosbah-vol-<app id>-<name> (the agent keeps using cloudy-vol-… for a volume made before the rename). Derived, never chosen."`
	Mounted   bool      `json:"mounted" doc:"true when the live release's containers were started with this volume at this path. false: it applies from the next deploy or restart."`
	Bytes     *int64    `json:"bytes" doc:"What Docker reports the volume holds (rounded); null when the server could not be asked, Docker reports none, or the volume does not exist yet."`
	CreatedAt time.Time `json:"created_at"`
}

func view(a db.App, v db.AppVolume, live *db.Release) Volume {
	out := Volume{ID: v.ID, Name: v.Name, MountPath: v.MountPath, Volume: agentproto.VolumeName(a.ID, v.Name), CreatedAt: v.CreatedAt}
	for _, m := range deploy.MountedVolumes(live) {
		if m.Volume == out.Volume && m.MountPath == v.MountPath {
			out.Mounted = true
		}
	}
	return out
}

func liveRelease(ctx context.Context, q *db.Queries, a db.App) *db.Release {
	if live, err := q.LiveReleaseOfApp(ctx, a.ID); err == nil && live.AppID == a.ID {
		return &live
	}
	return nil
}

// List is the app's volumes, by name. bytes is filled in by WithUsage.
func List(ctx context.Context, q *db.Queries, teamID string, a db.App) ([]Volume, error) {
	rows, err := q.ListVolumesOfApp(ctx, db.ListVolumesOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	live := liveRelease(ctx, q, a)
	out := make([]Volume, 0, len(rows))
	for _, v := range rows {
		out = append(out, view(a, v, live))
	}
	return out, nil
}

// Find resolves one volume of an app by id or name.
func Find(ctx context.Context, q *db.Queries, teamID string, a db.App, ref string) (db.AppVolume, error) {
	v, err := q.VolumeOfApp(ctx, db.VolumeOfAppParams{AppID: a.ID, TeamID: teamID, Ref: strings.TrimSpace(ref)})
	if err != nil {
		return db.AppVolume{}, notFound("volume")
	}
	return v, nil
}

// Create adds a volume. 422: a bad name or mount path, or an imported app. 409: the name or the
// path is taken, the path is inside (or around) another volume's, or the app has MaxVolumesPerApp.
func Create(ctx context.Context, q *db.Queries, teamID string, actor deploy.Actor, a db.App, name, mountPath string) (Volume, error) {
	if err := RefuseImported(a); err != nil {
		return Volume{}, err
	}
	name = strings.TrimSpace(name)
	if !agentproto.ValidVolumeSlug(name) {
		return Volume{}, invalid("a volume name is a lowercase letter, then up to 30 lowercase letters, digits and dashes")
	}
	clean, err := agentproto.CleanMountPath(strings.TrimSpace(mountPath))
	if err != nil {
		return Volume{}, invalid(err.Error())
	}
	rows, err := q.ListVolumesOfApp(ctx, db.ListVolumesOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return Volume{}, err
	}
	if len(rows) >= MaxVolumesPerApp {
		return Volume{}, conflict(fmt.Sprintf("an app has at most %d volumes", MaxVolumesPerApp))
	}
	for _, o := range rows {
		switch {
		case o.Name == name:
			return Volume{}, conflict("a volume named " + name + " already exists")
		case o.MountPath == clean:
			return Volume{}, conflict("volume " + o.Name + " is already mounted at " + clean)
		case agentproto.PathWithin(clean, o.MountPath):
			return Volume{}, conflict(clean + " is inside volume " + o.Name + "'s mount path " + o.MountPath)
		case agentproto.PathWithin(o.MountPath, clean):
			return Volume{}, conflict(clean + " contains volume " + o.Name + "'s mount path " + o.MountPath)
		}
	}
	row, err := q.InsertAppVolume(ctx, db.InsertAppVolumeParams{TeamID: teamID, AppID: a.ID, Name: name, MountPath: clean})
	if err != nil {
		return Volume{}, conflict("that volume name or mount path is already in use")
	}
	return view(a, row, liveRelease(ctx, q, a)), tenancy.Record(ctx, q, teamID, actor.UserID, "storage.volume_created", "app", a.ID,
		auditOf(actor, map[string]any{"volume": row.ID, "name": row.Name, "mount_path": row.MountPath}))
}

// Removed is what removing a volume answers with.
type Removed struct {
	Removed    bool    `json:"removed"`
	Applies    string  `json:"applies" enum:"next_deploy" doc:"The record is gone; running containers keep the mount until the next deploy or restart."`
	DeleteData bool    `json:"delete_data"`
	Operation  *string `json:"operation" doc:"With delete_data: the volume.remove operation on the app's server (docker volume rm). It fails, removing nothing, while any container still uses the volume."`
}

// Remove deletes the record — the volume is no longer mounted from the next deploy. With deleteData
// it also queues volume.remove for the Docker volume; that is refused (409) while the live release
// still mounts it: the caller deploys or restarts first, then removes the data.
func Remove(ctx context.Context, q *db.Queries, o *ops.Service, teamID string, actor deploy.Actor, a db.App, v db.AppVolume, confirm string, deleteData bool) (Removed, error) {
	out := Removed{Applies: AppliesNextDeploy, DeleteData: deleteData}
	if err := RefuseImported(a); err != nil {
		return out, err
	}
	if confirm != v.Name {
		return out, invalid("confirm must be the volume's name: " + v.Name)
	}
	docker := agentproto.VolumeName(a.ID, v.Name)
	if deleteData {
		for _, m := range deploy.MountedVolumes(liveRelease(ctx, q, a)) {
			if m.Volume == docker {
				return out, conflict("the live release still mounts " + v.Name + " — remove it without delete_data and deploy or restart the app first; to delete the data afterwards, add a volume named " + v.Name + " again and remove it with delete_data before the next deploy")
			}
		}
	}
	if n, err := q.DeleteAppVolumeInTeam(ctx, db.DeleteAppVolumeInTeamParams{ID: v.ID, TeamID: teamID}); err != nil || n == 0 {
		if err == nil {
			err = notFound("volume")
		}
		return out, err
	}
	out.Removed = true
	extra := map[string]any{"volume": v.ID, "name": v.Name, "mount_path": v.MountPath, "delete_data": deleteData}
	if deleteData {
		p := agentproto.VolumeRemoveParams{AppID: a.ID, Volume: docker}
		if err := p.Validate(); err != nil {
			return out, invalid(err.Error())
		}
		raw, _ := json.Marshal(p)
		op, _, err := o.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: a.ServerID, Kind: agentproto.KindVolumeRemove, Params: raw, CreatedBy: createdBy(actor)})
		if err != nil {
			return out, err
		}
		out.Operation, extra["operation"] = &op.ID, op.ID
	}
	return out, tenancy.Record(ctx, q, teamID, actor.UserID, "storage.volume_removed", "app", a.ID, auditOf(actor, extra))
}

// UsageWait is how long a read waits for the server's answer about the volumes.
const UsageWait = 8 * time.Second

// QueueUsage queues volume.usage for the app's volumes when its server can be asked. A zero
// operation (no error) means there is nothing to ask or nobody to ask: usage is best effort.
func QueueUsage(ctx context.Context, q *db.Queries, o *ops.Service, teamID string, actor deploy.Actor, a db.App, vols []Volume) db.Operation {
	if len(vols) == 0 || deploy.IsImported(a) {
		return db.Operation{}
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil || srv.AgentPublicKey == nil || !o.Connected(srv.ID) {
		return db.Operation{}
	}
	p := agentproto.VolumeUsageParams{AppID: a.ID}
	for _, v := range vols {
		p.Volumes = append(p.Volumes, v.Volume)
	}
	if p.Validate() != nil {
		return db.Operation{}
	}
	raw, _ := json.Marshal(p)
	op, _, err := o.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: agentproto.KindVolumeUsage, Params: raw,
		CreatedBy: createdBy(actor), QueueTTL: time.Minute})
	if err != nil {
		return db.Operation{}
	}
	return op
}

// WithUsage copies what the server answered into the volumes. A volume Docker has not created yet
// (never deployed with) keeps bytes null.
func WithUsage(vols []Volume, res agentproto.VolumeUsageResult) {
	for i := range vols {
		for _, u := range res.Volumes {
			if u.Volume == vols[i].Volume && u.Exists {
				vols[i].Bytes = u.Bytes
			}
		}
	}
}

func createdBy(a deploy.Actor) string {
	if a.UserID != "" {
		return a.UserID
	}
	return "via:" + a.Via
}

func auditOf(a deploy.Actor, extra map[string]any) map[string]any {
	if a.Via != "" && a.Via != "web" {
		extra["via"] = a.Via
	}
	if a.TokenID != "" {
		extra["mcp_token_id"] = a.TokenID
	}
	return extra
}
