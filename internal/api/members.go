package api

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

type MemberOut struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type MemberPath struct {
	Team   string `path:"team" maxLength:"64"`
	Member string `path:"member" maxLength:"64"`
}

func (d *deps) registerMembers(api huma.API) {
	type membersListOut struct {
		Body struct {
			Members []MemberOut `json:"members"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-members", Method: http.MethodGet, Path: "/api/teams/{team}/members",
		Summary: "Members of a team", Tags: []string{"Members"},
	}, func(ctx context.Context, in *TeamPath) (*membersListOut, error) {
		out := &membersListOut{}
		out.Body.Members = []MemberOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListMembersOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list members")
			}
			for _, r := range rows {
				out.Body.Members = append(out.Body.Members, MemberOut{ID: r.ID, UserID: r.UserID, Email: r.Email, Role: r.Role, CreatedAt: r.CreatedAt})
			}
			return nil
		})
		return out, err
	})

	type memberOut struct{ Body MemberOut }
	huma.Register(api, huma.Operation{
		OperationID: "update-member-role", Method: http.MethodPatch, Path: "/api/teams/{team}/members/{member}",
		Summary: "Change a member's role (owner or admin)", Tags: []string{"Members"},
	}, func(ctx context.Context, in *struct {
		MemberPath
		Body struct {
			Role string `json:"role" enum:"owner,admin,deployer,viewer"`
		}
	}) (*memberOut, error) {
		out := &memberOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMembers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			target, err := q.MemberInTeam(ctx, db.MemberInTeamParams{ID: in.Member, TeamID: m.TeamID})
			if isNoRows(err) {
				return errNotFound
			}
			if err != nil {
				return huma.Error500InternalServerError("lookup failed")
			}
			// Only an owner makes or unmakes an owner.
			if (target.Role == tenancy.RoleOwner || in.Body.Role == tenancy.RoleOwner) && m.Role != tenancy.RoleOwner {
				return huma.Error403Forbidden("only an owner can change who owns the team")
			}
			if target.Role == tenancy.RoleOwner && in.Body.Role != tenancy.RoleOwner {
				if n, _ := q.CountOwners(ctx, m.TeamID); n <= 1 {
					return huma.Error409Conflict("a team needs at least one owner")
				}
			}
			row, err := q.UpdateMemberRole(ctx, db.UpdateMemberRoleParams{ID: target.ID, TeamID: m.TeamID, Role: in.Body.Role})
			if err != nil {
				return huma.Error500InternalServerError("could not update")
			}
			if err := tenancy.Record(ctx, q, m.TeamID, id.ID, "member.role_changed", "team_member", target.ID,
				map[string]any{"user_id": target.UserID, "role": map[string]string{"old": target.Role, "new": row.Role}}); err != nil {
				return huma.Error500InternalServerError("audit failed")
			}
			out.Body = MemberOut{ID: row.ID, UserID: row.UserID, Role: row.Role, CreatedAt: row.CreatedAt}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "remove-member", Method: http.MethodDelete, Path: "/api/teams/{team}/members/{member}",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Remove a member (owner or admin), or leave the team yourself", Tags: []string{"Members"},
	}, func(ctx context.Context, in *MemberPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			target, err := q.MemberInTeam(ctx, db.MemberInTeamParams{ID: in.Member, TeamID: m.TeamID})
			if isNoRows(err) {
				return errNotFound
			}
			if err != nil {
				return huma.Error500InternalServerError("lookup failed")
			}
			leaving := target.UserID == id.ID
			if !leaving {
				if !tenancy.Can(ctx, m, tenancy.AbilityManageMembers) {
					return errForbidden
				}
				if target.Role == tenancy.RoleOwner && m.Role != tenancy.RoleOwner {
					return huma.Error403Forbidden("only an owner can remove an owner")
				}
			}
			if target.Role == tenancy.RoleOwner {
				if n, _ := q.CountOwners(ctx, m.TeamID); n <= 1 {
					return huma.Error409Conflict("a team needs at least one owner")
				}
			}
			n, err := q.DeleteMemberInTeam(ctx, db.DeleteMemberInTeamParams{ID: target.ID, TeamID: m.TeamID})
			if err != nil || n == 0 {
				return errNotFound
			}
			action := "member.removed"
			if leaving {
				action = "member.left"
			}
			if err := tenancy.Record(ctx, q, m.TeamID, id.ID, action, "team_member", target.ID,
				map[string]any{"user_id": target.UserID, "role": target.Role}); err != nil {
				return huma.Error500InternalServerError("audit failed")
			}
			return nil
		})
		return nil, err
	})
}
