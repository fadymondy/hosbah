package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// AppResources is an app's stored memory/CPU limits: a base and, keyed by process name ("web" or
// "worker:<name>"), overrides — the same shape hosbah.toml's [resources] merges into.
type AppResources struct {
	Base      agentproto.ResourceLimits            `json:"base"`
	Processes map[string]agentproto.ResourceLimits `json:"processes,omitempty"`
}

// appResources reads an app row's resource columns into Defaults for Resolve — the base the app's
// own settings hold, before hosbah.toml overrides it (see config.go's Resolve).
func appResources(a db.App) (agentproto.ResourceLimits, map[string]agentproto.ResourceLimits) {
	r := agentproto.ResourceLimits{}
	if a.ResourcesMemoryMb != nil {
		r.MemoryMB = int(*a.ResourcesMemoryMb)
	}
	if a.ResourcesCpus.Valid {
		r.CPUs = a.ResourcesCpus.Float64
	}
	var procs map[string]agentproto.ResourceLimits
	if a.ResourcesProcesses != "" && a.ResourcesProcesses != "{}" {
		_ = json.Unmarshal([]byte(a.ResourcesProcesses), &procs)
	}
	return r, procs
}

// Resources returns the app's currently stored limits.
func Resources(a db.App) AppResources {
	base, procs := appResources(a)
	return AppResources{Base: base, Processes: procs}
}

// SetResources validates and stores an app's memory/CPU limits (base + optional per-process
// overrides), then live-applies them to the live release's containers with app.limits (a docker
// update, no restart) so saving in the UI takes effect without a redeploy. A future deploy or
// restart picks the stored limits up through hosbah.toml's Resolve merge either way.
func (s *Service) SetResources(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, res AppResources) (db.App, error) {
	if err := refuseImported(a); err != nil {
		return db.App{}, err
	}
	if err := res.Base.Validate(); err != nil {
		return db.App{}, invalid(err.Error())
	}
	for name, r := range res.Processes {
		if !agentproto.ValidProcess(name) {
			return db.App{}, invalid("not a valid process name: " + name)
		}
		if err := r.Validate(); err != nil {
			return db.App{}, invalid(name + ": " + err.Error())
		}
	}
	procsJSON := "{}"
	if len(res.Processes) > 0 {
		raw, _ := json.Marshal(res.Processes)
		procsJSON = string(raw)
	}
	arg := db.UpdateAppResourcesParams{ID: a.ID, TeamID: teamID, ResourcesProcesses: procsJSON}
	if res.Base.MemoryMB > 0 {
		mb := int64(res.Base.MemoryMB)
		arg.ResourcesMemoryMb = &mb
	}
	if res.Base.CPUs > 0 {
		arg.ResourcesCpus = sql.NullFloat64{Float64: res.Base.CPUs, Valid: true}
	}
	updated, err := q.UpdateAppResources(ctx, arg)
	if err != nil {
		return db.App{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "app.resources", "app", a.ID,
		actor.audit(map[string]any{"memory_mb": res.Base.MemoryMB, "cpus": res.Base.CPUs})); err != nil {
		return db.App{}, err
	}
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.applyResources(bg, teamID, updated)
	}()
	return updated, nil
}

// applyResources live-updates the currently live release's containers. Best-effort: a transient
// failure here isn't fatal, since the next deploy or restart also picks up the stored limits.
func (s *Service) applyResources(ctx context.Context, teamID string, a db.App) {
	_ = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		live, err := q.LiveReleaseOfApp(ctx, a.ID)
		if err != nil || live.TeamID != teamID {
			return nil // nothing live yet
		}
		names := liveContainerNames(&live)
		if len(names) == 0 {
			return nil
		}
		base, procs := appResources(a)
		byProcess := map[string]agentproto.ResourceLimits{}
		for name, container := range names {
			r := base
			if o, ok := procs[name]; ok {
				r = o
			}
			byProcess[container] = r
		}
		// Group containers that share the same limits into one op each, since a redeploy carries a
		// single set for "web" and one per worker anyway; one op per distinct set keeps this simple.
		byLimits := map[agentproto.ResourceLimits][]string{}
		for container, r := range byProcess {
			byLimits[r] = append(byLimits[r], container)
		}
		for r, containers := range byLimits {
			p := agentproto.AppLimitsParams{AppID: a.ID, Containers: containers, Resources: r}
			raw, _ := json.Marshal(p)
			if _, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: a.ServerID, Kind: agentproto.KindAppLimits,
				Params: raw, CreatedBy: "hosbah"}); err != nil {
				return err
			}
		}
		return nil
	})
}

// liveContainerNames maps process name ("web", "worker:<name>") to its container name for a release,
// read back from releases.containers (see releaseContainers in volumes.go) and releases.config (the
// Effective that named the workers, in the same sorted order they were started in — Effective.Workers()).
func liveContainerNames(rel *db.Release) map[string]string {
	if rel == nil || rel.Containers == nil {
		return nil
	}
	var c struct {
		Web     string   `json:"web"`
		Workers []string `json:"workers"`
	}
	if err := json.Unmarshal([]byte(*rel.Containers), &c); err != nil {
		return nil
	}
	out := map[string]string{}
	if c.Web != "" {
		out["web"] = c.Web
	}
	var eff Effective
	if err := json.Unmarshal([]byte(rel.Config), &eff); err == nil {
		names := eff.Workers()
		for i, container := range c.Workers {
			if i < len(names) {
				out[names[i]] = container
			}
		}
	}
	return out
}
