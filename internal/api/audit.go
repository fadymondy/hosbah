package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

type AuditEntryOut struct {
	ID          string          `json:"id"`
	Action      string          `json:"action"`
	SubjectType string          `json:"subject_type"`
	SubjectID   string          `json:"subject_id"`
	CauserEmail string          `json:"causer_email"`
	IP          string          `json:"ip"`
	CreatedAt   string          `json:"created_at"`
	Changes     json.RawMessage `json:"changes"`
}

func (d *deps) registerAudit(api huma.API) {
	type auditListOut struct {
		Body struct {
			Entries []AuditEntryOut `json:"entries"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-team-audit", Method: http.MethodGet, Path: "/api/teams/{team}/audit",
		Summary: "The team's audit log (owner or admin)", Tags: []string{"Audit"},
	}, func(ctx context.Context, in *struct {
		TeamPath
		Limit int `query:"limit" minimum:"1" maximum:"500" default:"100"`
	}) (*auditListOut, error) {
		out := &auditListOut{}
		out.Body.Entries = []AuditEntryOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityViewAudit, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			limit := in.Limit
			if limit <= 0 {
				limit = 100
			}
			rows, err := q.ListTeamActivity(ctx, db.ListTeamActivityParams{TeamID: ptr(m.TeamID), Limit: int32(limit)})
			if err != nil {
				return huma.Error500InternalServerError("could not read the audit log")
			}
			for _, r := range rows {
				changes := json.RawMessage(r.Changes)
				if !json.Valid(changes) {
					changes = json.RawMessage(`{}`)
				}
				out.Body.Entries = append(out.Body.Entries, AuditEntryOut{
					ID: r.ID, Action: r.Action, SubjectType: r.SubjectType, SubjectID: r.SubjectID,
					CauserEmail: r.CauserEmail, IP: r.Ip, CreatedAt: r.CreatedAt, Changes: changes,
				})
			}
			return nil
		})
		return out, err
	})
}
