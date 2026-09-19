package deploy

import (
	"context"
	"net/http"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Imported workloads: an app with source "imported" is something Hosbah found running on a server
// (a container, a systemd unit, a Proxmox guest). Hosbah shows it and never deploys, restarts,
// stops, reconfigures or removes it. Every mutating action refuses one with ImportedCode.

// ImportedCode is the machine-readable code of the refusal (409).
const ImportedCode = "imported_workload"

// SourceImported marks an imported app or data service.
const SourceImported = "imported"

// ImportedMessage is the refusal's text for a named workload.
func ImportedMessage(name string) string {
	return name + " is imported: Hosbah shows it but does not deploy or change it"
}

// ImportedError is the refusal. It is an ActionError (409) carrying ImportedCode.
func ImportedError(name string) error {
	return &ActionError{Status: http.StatusConflict, Msg: ImportedMessage(name), Code: ImportedCode}
}

// IsImported reports whether the app was imported rather than created from git.
func IsImported(a db.App) bool { return a.Source == SourceImported }

func refuseImported(a db.App) error {
	if IsImported(a) {
		return ImportedError(a.Name)
	}
	return nil
}

// BranchOf is a git app's branch ("" for an imported app).
func BranchOf(a db.App) string {
	if a.Branch == nil {
		return ""
	}
	return *a.Branch
}

// RepoOf is a git app's repository URL ("" for an imported app).
func RepoOf(a db.App) string {
	if a.RepoURL == nil {
		return ""
	}
	return *a.RepoURL
}

// forgetImportedApp deletes only Hosbah's record of an imported app — no operation reaches the
// server — and leaves a tombstone so the next discovery does not bring it back.
func (s *Service) forgetImportedApp(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App) error {
	key := ""
	if a.OriginKey != nil {
		key = *a.OriginKey
	}
	if key != "" {
		if err := q.IgnoreImported(ctx, db.IgnoreImportedParams{TeamID: teamID, ServerID: a.ServerID, Kind: "app",
			OriginKey: key, Name: a.Name, CreatedBy: actorID(actor)}); err != nil {
			return err
		}
	}
	if n, err := q.DeleteAppInTeam(ctx, db.DeleteAppInTeamParams{ID: a.ID, TeamID: teamID}); err != nil || n == 0 {
		return notFound("app")
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "app.import_forgotten", "app", a.ID, actor.audit(map[string]any{
		"name": a.Name, "origin_key": key, "note": "record only; nothing on the server was changed"}))
}

func actorID(a Actor) string {
	if a.UserID != "" {
		return a.UserID
	}
	return "via:" + a.Via
}
