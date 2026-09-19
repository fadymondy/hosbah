package dbsvc

import (
	"context"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// SourceImported marks a data service Hosbah found running on a server (not one it provisioned).
// Hosbah knows no credential for it and never backs it up, restores it, creates databases or users
// in it, reveals or rotates its credentials, or removes it from the server.
const SourceImported = deploy.SourceImported

// refuseImported is the 409 imported_workload refusal (a deploy.ActionError carrying the code).
func refuseImported(svc db.DataService) error {
	if svc.Source == SourceImported {
		return deploy.ImportedError(svc.Name)
	}
	return nil
}

// forgetImported deletes only Hosbah's record of an imported service — no operation reaches the
// server — and leaves a tombstone so the next discovery does not bring it back.
func (s *Service) forgetImported(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService) error {
	key := ""
	if svc.OriginKey != nil {
		key = *svc.OriginKey
	}
	createdBy := actor.UserID
	if createdBy == "" {
		createdBy = "via:" + actor.Via
	}
	if key != "" {
		if err := q.IgnoreImported(ctx, db.IgnoreImportedParams{TeamID: teamID, ServerID: svc.ServerID, Kind: "data_service",
			OriginKey: key, Name: svc.Name, CreatedBy: createdBy}); err != nil {
			return err
		}
	}
	if n, err := q.DeleteDataServiceInTeam(ctx, db.DeleteDataServiceInTeamParams{ID: svc.ID, TeamID: teamID}); err != nil || n == 0 {
		return notFound("data service")
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "dataservice.import_forgotten", "data_service", svc.ID, auditExtra(actor, map[string]any{
		"name": svc.Name, "origin_key": key, "note": "record only; nothing on the server was changed"}))
}
