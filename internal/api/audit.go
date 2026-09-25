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

// AuditListQuery is the team and platform audit routes' shared filter set: an actor (id or a
// substring of their email), an action prefix, a subject/target type and id, a created_at range,
// and a free-text search over action, subject, details and the actor's email.
type AuditListQuery struct {
	Limit       int    `query:"limit" minimum:"1" maximum:"500" default:"100"`
	Offset      int    `query:"offset" minimum:"0" default:"0"`
	Actor       string `query:"actor" maxLength:"320" doc:"A causer id, or a substring of their email."`
	Action      string `query:"action" maxLength:"100" doc:"Prefix match, e.g. \"server.\"."`
	SubjectType string `query:"subject_type" maxLength:"100"`
	SubjectID   string `query:"subject_id" maxLength:"200"`
	Since       string `query:"since" doc:"ISO-8601; entries at or after this time."`
	Until       string `query:"until" doc:"ISO-8601; entries at or before this time."`
	Q           string `query:"q" maxLength:"200" doc:"Free-text search across action, subject, changes and actor email."`
}

func (d *deps) registerAudit(api huma.API) {
	type auditListOut struct {
		Body struct {
			Entries []AuditEntryOut `json:"entries"`
			Total   int64           `json:"total"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-team-audit", Method: http.MethodGet, Path: "/api/teams/{team}/audit",
		Summary: "The team's audit log (owner or admin); filter by actor, action prefix, subject, date range or free text", Tags: []string{"Audit"},
	}, func(ctx context.Context, in *struct {
		TeamPath
		AuditListQuery
	}) (*auditListOut, error) {
		out := &auditListOut{}
		out.Body.Entries = []AuditEntryOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityViewAudit, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			limit := in.Limit
			if limit <= 0 {
				limit = 100
			}
			params := db.ListTeamActivityParams{
				TeamID: m.TeamID, Actor: in.Actor, Action: in.Action, SubjectType: in.SubjectType, SubjectID: in.SubjectID,
				Since: in.Since, Until: in.Until, Q: in.Q, Lim: int32(limit), Off: int32(in.Offset),
			}
			rows, err := q.ListTeamActivity(ctx, params)
			if err != nil {
				return huma.Error500InternalServerError("could not read the audit log")
			}
			total, err := q.CountTeamActivity(ctx, db.CountTeamActivityParams{
				TeamID: m.TeamID, Actor: in.Actor, Action: in.Action, SubjectType: in.SubjectType, SubjectID: in.SubjectID,
				Since: in.Since, Until: in.Until, Q: in.Q,
			})
			if err != nil {
				return huma.Error500InternalServerError("could not read the audit log")
			}
			out.Body.Total = total
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
