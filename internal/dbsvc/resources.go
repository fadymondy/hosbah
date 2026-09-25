package dbsvc

import (
	"context"
	"database/sql"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// serviceResources reads a data service's stored memory/CPU limits into the typed agent protocol
// shape. Both columns are nullable; unset means no limit (agentproto.ResourceLimits{}, its zero
// value, which DockerArgs() and the argv builders treat as "no --memory/--cpus flags").
func serviceResources(svc db.DataService) agentproto.ResourceLimits {
	r := agentproto.ResourceLimits{}
	if svc.ResourcesMemoryMb != nil {
		r.MemoryMB = int(*svc.ResourcesMemoryMb)
	}
	if svc.ResourcesCpus.Valid {
		r.CPUs = svc.ResourcesCpus.Float64
	}
	return r
}

// Resources returns the service's current limits.
func (s *Service) Resources(svc db.DataService) agentproto.ResourceLimits {
	return serviceResources(svc)
}

// SetResources validates and stores new memory/CPU limits for a Hosbah-managed data service, then
// live-applies them to the running container with db.limits (a docker update, no restart) so saving
// in the UI takes effect without a redeploy. Imported services aren't Hosbah's to constrain.
func (s *Service) SetResources(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, r agentproto.ResourceLimits) (db.DataService, error) {
	if isImported(svc) {
		return db.DataService{}, conflict("this service was imported; Hosbah does not manage its resources")
	}
	if err := r.Validate(); err != nil {
		return db.DataService{}, invalid(err.Error())
	}
	arg := db.UpdateDataServiceResourcesParams{ID: svc.ID, TeamID: teamID}
	if r.MemoryMB > 0 {
		mb := int64(r.MemoryMB)
		arg.ResourcesMemoryMb = &mb
	}
	if r.CPUs > 0 {
		arg.ResourcesCpus = sql.NullFloat64{Float64: r.CPUs, Valid: true}
	}
	updated, err := q.UpdateDataServiceResources(ctx, arg)
	if err != nil {
		return db.DataService{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "dataservice.resources", "data_service", svc.ID,
		auditExtra(actor, map[string]any{"memory_mb": r.MemoryMB, "cpus": r.CPUs})); err != nil {
		return db.DataService{}, err
	}
	if svc.Status == "ok" || svc.Status == "running" {
		s.spawn(func(bg context.Context) { s.applyResources(bg, teamID, updated) })
	}
	return updated, nil
}

// applyResources fires the live docker update; best-effort — the next provision/restart also picks
// up the stored limits, so a transient failure here isn't fatal.
func (s *Service) applyResources(ctx context.Context, teamID string, svc db.DataService) {
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		p := agentproto.DBLimitsParams{ServiceID: svc.ID, Container: contOf(svc), Resources: serviceResources(svc)}
		raw := mustJSON(p)
		_, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: svc.ServerID, Kind: agentproto.KindDBLimits, Params: raw, CreatedBy: "hosbah"})
		return err
	})
}
