package api

/*
The approval queue: the human half of the P4 destructive gate.

A team with require_destructive_approval on turns every destructive MCP call into a row here
instead of an action. These three routes are how somebody clears that queue — list what is
waiting, approve one, reject one — and they are deliberately thin: the gate itself lives in
internal/mcp/approval.go, and nothing in this file can authorise anything on its own.

Approving is AbilityManageTeam (owner or admin). It has to be at least as strong as the thing
being approved, and a deployer who could approve their own token's deletions would make the whole
setting decorative.
*/

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

// ApprovalOut is one pending or decided destructive call.
type ApprovalOut struct {
	ID           string     `json:"id"`
	Tool         string     `json:"tool"`
	ResourceType string     `json:"resource_type"`
	ResourceName string     `json:"resource_name"`
	Status       string     `json:"status" doc:"pending, approved, used, rejected or expired."`
	TokenID      string     `json:"token_id" doc:"The MCP token that asked."`
	TokenName    string     `json:"token_name"`
	UserID       string     `json:"user_id"`
	ArgsSummary  string     `json:"args_summary" doc:"Redacted JSON: the tool, the resource and the ARGUMENT NAMES only — never a value."`
	DecidedBy    *string    `json:"decided_by"`
	DecidedAt    *time.Time `json:"decided_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

func toApprovalOut(r db.ApprovalRequest, tokenName string) ApprovalOut {
	return ApprovalOut{
		ID: r.ID, Tool: r.Tool, ResourceType: r.ResourceType, ResourceName: r.ResourceName,
		Status: r.Status, TokenID: r.TokenID, TokenName: tokenName, UserID: r.UserID,
		ArgsSummary: r.ArgsSummary, DecidedBy: r.DecidedBy, DecidedAt: r.DecidedAt,
		ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt,
	}
}

func (d *deps) registerApprovals(api huma.API) {
	tags := []string{"Approvals"}

	type approvalsOut struct {
		Body struct {
			Requests []ApprovalOut `json:"requests"`
			// Enabled mirrors the team setting, so the page can explain an empty queue: nothing
			// waiting because nothing is gated is a different state from nothing waiting yet.
			Enabled bool `json:"enabled"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-approvals", Method: http.MethodGet, Path: "/api/teams/{team}/approvals",
		Summary: "Destructive MCP calls waiting on a human, and recently decided ones", Tags: tags,
	}, func(ctx context.Context, in *struct {
		TeamPath
		Status string `query:"status" enum:"pending,approved,used,rejected,expired" doc:"Only this status; omit for all."`
		Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	}) (*approvalsOut, error) {
		out := &approvalsOut{}
		out.Body.Requests = []ApprovalOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			t, err := q.GetTeam(ctx, m.TeamID)
			if err != nil {
				return errNotFound
			}
			out.Body.Enabled = t.RequireDestructiveApproval

			limit := int32(in.Limit)
			if limit == 0 {
				limit = 50
			}
			rows, err := q.ListApprovalRequestsOfTeam(ctx, db.ListApprovalRequestsOfTeamParams{
				TeamID: m.TeamID, Column2: in.Status, Limit: limit,
			})
			if err != nil {
				return huma.Error500InternalServerError("could not list approval requests")
			}
			names := tokenNames(ctx, q, m.TeamID)
			for _, r := range rows {
				out.Body.Requests = append(out.Body.Requests, toApprovalOut(r, names[r.TokenID]))
			}
			return nil
		})
		return out, err
	})

	decide := func(status, action string) func(context.Context, *struct {
		TeamPath
		Approval string `path:"approval" maxLength:"64"`
	}) (*struct{ Body ApprovalOut }, error) {
		return func(ctx context.Context, in *struct {
			TeamPath
			Approval string `path:"approval" maxLength:"64"`
		}) (*struct{ Body ApprovalOut }, error) {
			out := &struct{ Body ApprovalOut }{}
			err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
				r, err := q.DecideApprovalRequest(ctx, db.DecideApprovalRequestParams{
					ID: in.Approval, TeamID: m.TeamID, Status: status, DecidedBy: &id.ID,
				})
				if err != nil {
					// The statement only matches a pending, unexpired row of this team, so one
					// answer covers "no such request", "already decided" and "expired" — and the
					// caller cannot tell a stranger's id from a stale one, which is the point.
					return huma.Error404NotFound("no pending approval request with that id")
				}
				if err := tenancy.Record(ctx, q, m.TeamID, id.ID, action, r.ResourceType, r.ResourceName,
					map[string]any{"tool": r.Tool, "approval_request": r.ID, "mcp_token_id": r.TokenID}); err != nil {
					return huma.Error500InternalServerError("audit failed")
				}
				out.Body = toApprovalOut(r, tokenNames(ctx, q, m.TeamID)[r.TokenID])
				return nil
			})
			return out, err
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "approve-approval", Method: http.MethodPost, Path: "/api/teams/{team}/approvals/{approval}/approve",
		Summary: "Approve a held destructive call (owner or admin). The caller must retry it; approving does not run it.", Tags: tags,
	}, decide("approved", "approval.approved"))

	huma.Register(api, huma.Operation{
		OperationID: "reject-approval", Method: http.MethodPost, Path: "/api/teams/{team}/approvals/{approval}/reject",
		Summary: "Reject a held destructive call (owner or admin)", Tags: tags,
	}, decide("rejected", "approval.rejected"))
}

// tokenNames maps token id → name so the queue can say which credential asked, not just its id.
// A token revoked or deleted since the request was filed simply has no name; the id in the row is
// the durable record, and it is what the audit log carries too.
func tokenNames(ctx context.Context, q *db.Queries, teamID string) map[string]string {
	out := map[string]string{}
	rows, err := q.ListMcpTokensOfTeam(ctx, teamID)
	if err != nil {
		return out
	}
	for _, t := range rows {
		out[t.ID] = t.Name
	}
	return out
}
