package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Import (MH-574 A4): what the last sync of a server's discovered workloads found, what it became
(an app, a database, a shared service, or was skipped/ignored/errored), and a member's sticky
decision for one item — import as app, import as database, mark as a shared service, or ignore.
See internal/importer.Sync for the sync itself; this file is only the read/decide/run-now surface.
Read: any member. Deciding and running now: owner, admin or deployer (AbilityOperate — the same
tier that may queue system.info today).
*/

// ImportRunOut is the outcome of the last import sync of one server.
type ImportRunOut struct {
	Status     string  `json:"status" enum:"ok,error"`
	Error      *string `json:"error"`
	Discovered int64   `json:"discovered"`
	Created    int64   `json:"created"`
	Updated    int64   `json:"updated"`
	Missing    int64   `json:"missing"`
	Removed    int64   `json:"removed"`
	Ignored    int64   `json:"ignored"`
	StartedAt  string  `json:"started_at"`
	FinishedAt *string `json:"finished_at"`
}

// ImportItemOut is one discovered workload and what became of it.
type ImportItemOut struct {
	OriginKind   string  `json:"origin_kind"`
	OriginKey    string  `json:"origin_key"`
	Name         string  `json:"name"`
	DetectedKind *string `json:"detected_kind"`
	Target       string  `json:"target" enum:"app,data_service"`
	Outcome      string  `json:"outcome" enum:"imported_app,imported_data_service,imported_shared_service,linked_guest,updated,missing,removed,skipped,ignored,error"`
	Reason       *string `json:"reason"`
	Error        *string `json:"error"`
	RecordID     *string `json:"record_id"`
	Decision     *string `json:"decision" enum:"app,database,shared_service,ignore" doc:"The member's sticky choice for this item, when one was made."`
}

func toImportRunOut(r db.ImportRun) ImportRunOut {
	out := ImportRunOut{Status: r.Status, Error: r.Error, Discovered: int64(r.Discovered), Created: int64(r.Created),
		Updated: int64(r.Updated), Missing: int64(r.Missing), Removed: int64(r.Removed), Ignored: int64(r.Ignored),
		StartedAt: r.StartedAt.Format(timeLayout)}
	if r.FinishedAt != nil {
		s := r.FinishedAt.Format(timeLayout)
		out.FinishedAt = &s
	}
	return out
}

const timeLayout = "2006-01-02T15:04:05.999999999Z07:00"

func toImportItemOut(it db.ImportItem, decisions map[string]string) ImportItemOut {
	out := ImportItemOut{OriginKind: it.OriginKind, OriginKey: it.OriginKey, Name: it.Name, DetectedKind: it.DetectedKind,
		Target: it.Target, Outcome: it.Outcome, Reason: it.Reason, Error: it.Error, RecordID: it.RecordID}
	if d, ok := decisions[it.OriginKey]; ok {
		out.Decision = &d
	}
	return out
}

func (d *deps) registerImport(api huma.API) {
	tags := []string{"Import"}
	basePath := "/api/teams/{team}/servers/{server}/import"

	type importOut struct {
		Body struct {
			Run   *ImportRunOut   `json:"run"`
			Items []ImportItemOut `json:"items"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "get-server-import", Method: http.MethodGet, Path: basePath, Tags: tags,
		Summary: "The last import sync of a server: when it ran, its counts, and every discovered workload with what became of it. Any member.",
	}, func(ctx context.Context, in *ServerPath) (*importOut, error) {
		out := &importOut{}
		out.Body.Items = []ImportItemOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			decisionRows, err := q.ImportDecisionsOfServer(ctx, db.ImportDecisionsOfServerParams{ServerID: s.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not list the server's import decisions")
			}
			decisions := map[string]string{}
			for _, r := range decisionRows {
				decisions[r.OriginKey] = r.Decision
			}
			if run, err := q.ImportRunOfServer(ctx, db.ImportRunOfServerParams{ServerID: s.ID, TeamID: m.TeamID}); err == nil {
				o := toImportRunOut(run)
				out.Body.Run = &o
			}
			items, err := q.ImportItemsOfServer(ctx, db.ImportItemsOfServerParams{ServerID: s.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not list the server's discovered workloads")
			}
			for _, it := range items {
				out.Body.Items = append(out.Body.Items, toImportItemOut(it, decisions))
			}
			return nil
		})
		return out, err
	})

	type importRunNowOut struct {
		Status int
		Body   OperationOut
	}
	huma.Register(api, huma.Operation{OperationID: "run-server-import", Method: http.MethodPost, Path: basePath + "/run", Tags: tags,
		DefaultStatus: http.StatusCreated,
		Summary:       "Run import now: queue a system.info refresh, which syncs discovered workloads on completion. Owner, admin or deployer.",
	}, func(ctx context.Context, in *ServerPath) (*importRunNowOut, error) {
		out := &importRunNowOut{Status: http.StatusCreated}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if err := need(ctx, m, tenancy.AbilityOperate); err != nil {
				return err
			}
			if s.Status != "enrolled" {
				return huma.Error409Conflict("this server's agent has not enrolled yet")
			}
			op, created, err := d.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: m.TeamID, ServerID: s.ID, Kind: agentproto.KindSystemInfo,
				Params: json.RawMessage(`{}`), CreatedBy: id.ID})
			if err != nil {
				return huma.Error500InternalServerError("could not queue the import")
			}
			out.Body = toOperationOut(op)
			if !created {
				out.Status = http.StatusOK
			}
			return nil
		})
		return out, err
	})

	// ImportItemPath addresses one discovered workload of a server by its origin key (a container
	// name, a systemd unit, or "guest:<vmid>").
	type ImportItemPath struct {
		ServerPath
		Item string `path:"item" maxLength:"128"`
	}
	type importDecideOut struct {
		Body struct {
			OriginKey string `json:"origin_key"`
			Decision  string `json:"decision"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "decide-server-import-item", Method: http.MethodPut, Path: basePath + "/items/{item}", Tags: tags,
		Summary: "Choose what a discovered workload becomes: import as app, import as database, mark as a shared service, or ignore. " +
			"Sticky — the next sync always honours it. Owner, admin or deployer.",
	}, func(ctx context.Context, in *struct {
		ImportItemPath
		Body struct {
			Decision string `json:"decision" enum:"app,database,shared_service,ignore"`
		}
	}) (*importDecideOut, error) {
		out := &importDecideOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if err := need(ctx, m, tenancy.AbilityOperate); err != nil {
				return err
			}
			// The item's kind and name come from the last sync's record of it, when there is one
			// (so a fresh decision on an item nobody has synced yet still records something sane).
			kind, name := "app", in.Item
			if it, err := q.ImportItemsOfServer(ctx, db.ImportItemsOfServerParams{ServerID: s.ID, TeamID: m.TeamID}); err == nil {
				for _, row := range it {
					if row.OriginKey == in.Item {
						kind, name = row.Target, row.Name
						break
					}
				}
			}
			if _, err := q.SetImportDecision(ctx, db.SetImportDecisionParams{TeamID: m.TeamID, ServerID: s.ID, Kind: kind,
				OriginKey: in.Item, Name: name, CreatedBy: id.ID, Decision: in.Body.Decision}); err != nil {
				return huma.Error500InternalServerError("could not save the decision")
			}
			out.Body.OriginKey, out.Body.Decision = in.Item, in.Body.Decision
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server.import_decided", "server", s.ID,
				map[string]any{"origin_key": in.Item, "decision": in.Body.Decision})
		})
		return out, err
	})
}
