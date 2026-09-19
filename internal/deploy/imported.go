package deploy

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Imported workloads: an app with source "imported" is something Hosbah found running on a server
// (a container, a systemd unit, a Proxmox guest). Hosbah controls it like any other service — power,
// stats, logs, snapshots, resize, backup, recreate, destroy, domains (workloads.go) — but it has no
// build, so the git-only actions (deploy from git, rollback to a release, restart as a deployment,
// env vars, build settings, the deploy hook, the GitHub link) refuse it with ImportedCode.

// ImportedCode is the machine-readable code of the git-only refusal (409).
const ImportedCode = "imported_workload"

// SourceImported marks an imported app or data service.
const SourceImported = "imported"

// ImportedMessage is the git-only refusal's text for a named workload.
func ImportedMessage(name string) string {
	return name + " is imported; deploy from git applies to Hosbah-built apps"
}

// ImportedError is the git-only refusal. It is an ActionError (409) carrying ImportedCode.
func ImportedError(name string) error {
	return &ActionError{Status: http.StatusConflict, Msg: ImportedMessage(name), Code: ImportedCode}
}

// IsImported reports whether the app was imported rather than created from git.
func IsImported(a db.App) bool { return a.Source == SourceImported }

// refuseImported guards a git-only action.
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

// forgetImportedApp deletes only Hosbah's record of an imported app — the workload keeps running —
// and leaves a tombstone so the next discovery does not bring it back. The routes Hosbah made for
// its domains are taken down (they point at the workload through Hosbah's Caddy).
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
	hosts, err := s.dropImportedRoutes(ctx, q, teamID, actor, a)
	if err != nil {
		return err
	}
	if n, err := q.DeleteAppInTeam(ctx, db.DeleteAppInTeamParams{ID: a.ID, TeamID: teamID}); err != nil || n == 0 {
		return notFound("app")
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "app.import_forgotten", "app", a.ID, actor.audit(map[string]any{
		"name": a.Name, "origin_key": key, "note": "record only; the workload on the server was not changed"})); err != nil {
		return err
	}
	for _, h := range hosts {
		s.unwireTeamDNS(ctx, q, teamID, h)
		s.unpublishHost(ctx, q, teamID, h)
	}
	return nil
}

// dropImportedRoutes queues the removal of every proxy route Hosbah made for an imported app's
// domains and returns their hostnames (for the DNS clean-up after the rows are gone).
func (s *Service) dropImportedRoutes(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App) ([]string, error) {
	domains, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	var hosts []string
	for _, d := range domains {
		hosts = append(hosts, d.Hostname)
		if d.Upstream == nil {
			continue
		}
		raw, _ := json.Marshal(agentproto.ProxyParams{ProxyID: d.ID, Remove: true})
		if _, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: a.ServerID, Kind: agentproto.KindProxySet,
			Params: raw, CreatedBy: actorID(actor)}); err != nil {
			return nil, err
		}
	}
	return hosts, nil
}

func actorID(a Actor) string {
	if a.UserID != "" {
		return a.UserID
	}
	return "via:" + a.Via
}
