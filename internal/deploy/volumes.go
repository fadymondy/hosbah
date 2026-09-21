package deploy

import (
	"context"
	"encoding/json"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// MountsOf turns an app's volumes into what the agent mounts. The Docker volume's name is derived
// here (cloudy-vol-<app id>-<name>) — nothing a user typed names a volume on the host.
func MountsOf(appID string, vols []db.AppVolume) []agentproto.Mount {
	out := make([]agentproto.Mount, 0, len(vols))
	for _, v := range vols {
		out = append(out, agentproto.Mount{Volume: agentproto.VolumeName(appID, v.Name), MountPath: v.MountPath})
	}
	return out
}

// mountsOnce reads the app's volumes once per deployment, just before its first container starts:
// web and every worker of one release get the same mounts, and the release records them.
func (r *runner) mountsOnce(ctx context.Context) ([]agentproto.Mount, error) {
	if r.mountsLoaded {
		return r.mounts, nil
	}
	err := r.tx(ctx, func(q *db.Queries) error {
		vols, err := q.ListVolumesOfApp(ctx, db.ListVolumesOfAppParams{AppID: r.app.ID, TeamID: r.dep.TeamID})
		if err != nil {
			return err
		}
		r.mounts = MountsOf(r.app.ID, vols)
		return agentproto.ValidateMounts(r.app.ID, r.mounts)
	})
	if err != nil {
		return nil, err
	}
	r.mountsLoaded = true
	return r.mounts, nil
}

// releaseContainers is releases.containers: the containers of the release as they run now, and the
// volumes they were started with ("volumes" is how storage knows whether a volume is mounted).
// "realtime" is the realtime worker and the port it publishes (realtime.go), when there is one.
func releaseContainers(web string, workers []string, mounts []agentproto.Mount, realtime *ReleaseRealtime) string {
	if mounts == nil {
		mounts = []agentproto.Mount{}
	}
	c := map[string]any{"web": web, "workers": workers, "volumes": mounts}
	if realtime != nil {
		c["realtime"] = realtime
	}
	raw, _ := json.Marshal(c)
	return string(raw)
}

// MountedVolumes is the mounts a release's containers were started with (nil-safe).
func MountedVolumes(rel *db.Release) []agentproto.Mount {
	if rel == nil || rel.Containers == nil {
		return nil
	}
	var c struct {
		Volumes []agentproto.Mount `json:"volumes"`
	}
	_ = json.Unmarshal([]byte(*rel.Containers), &c)
	return c.Volumes
}
