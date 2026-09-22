package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fadymondy/hosbah/internal/brand"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

const invitationTTL = 7 * 24 * time.Hour

// acceptedOut is POST /api/invitations/{token}/accept's answer: {"team": {...}}.
type acceptedOut struct {
	Body struct {
		Team TeamSummary `json:"team"`
	}
}

type InvitationOut struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func toInvitationOut(i db.Invitation) InvitationOut {
	status := i.Status
	if status == "pending" && time.Now().After(i.ExpiresAt) {
		status = "expired"
	}
	return InvitationOut{ID: i.ID, Email: i.Email, Role: i.Role, Status: status, ExpiresAt: i.ExpiresAt, CreatedAt: i.CreatedAt}
}

// acceptURL is the one-time link. There is no mail delivery in local dev, so the
// inviter is shown it once and passes it on. The web app negotiates the locale.
func acceptURL(token string) string {
	origin := strings.TrimRight(brand.Getenv("WEB_ORIGIN"), "/")
	if origin == "" {
		origin = "http://localhost:3710"
	}
	return origin + "/invite/" + token
}

func (d *deps) registerInvitations(api huma.API) {
	type invitationsListOut struct {
		Body struct {
			Invitations []InvitationOut `json:"invitations"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-invitations", Method: http.MethodGet, Path: "/api/teams/{team}/invitations",
		Summary: "Invitations of a team (owner or admin)", Tags: []string{"Invitations"},
	}, func(ctx context.Context, in *TeamPath) (*invitationsListOut, error) {
		out := &invitationsListOut{}
		out.Body.Invitations = []InvitationOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMembers, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListInvitationsOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list invitations")
			}
			for _, r := range rows {
				out.Body.Invitations = append(out.Body.Invitations, toInvitationOut(r))
			}
			return nil
		})
		return out, err
	})

	type invitationCreatedOut struct {
		Body struct {
			Invitation InvitationOut `json:"invitation"`
			AcceptURL  string        `json:"accept_url" doc:"Contains the one-time token. Shown once; only its hash is stored."`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-invitation", Method: http.MethodPost, Path: "/api/teams/{team}/invitations",
		DefaultStatus: http.StatusCreated, Summary: "Invite someone by email (owner or admin)", Tags: []string{"Invitations"},
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body struct {
			Email string `json:"email" format:"email" maxLength:"254"`
			Role  string `json:"role" enum:"admin,deployer,viewer"`
		}
	}) (*invitationCreatedOut, error) {
		out := &invitationCreatedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMembers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			raw := make([]byte, 32)
			_, _ = rand.Read(raw)
			token := hex.EncodeToString(raw)
			inv, err := q.InsertInvitation(ctx, db.InsertInvitationParams{
				TeamID: m.TeamID, Email: strings.ToLower(strings.TrimSpace(in.Body.Email)), Role: in.Body.Role,
				TokenHash: tokens.Hash(token), InvitedBy: id.ID, ExpiresAt: time.Now().Add(invitationTTL).UTC(),
			})
			if err != nil {
				return huma.Error500InternalServerError("could not create the invitation")
			}
			if err := tenancy.Record(ctx, q, m.TeamID, id.ID, "invitation.created", "invitation", inv.ID,
				map[string]any{"email": inv.Email, "role": inv.Role}); err != nil {
				return huma.Error500InternalServerError("audit failed")
			}
			out.Body.Invitation = toInvitationOut(inv)
			out.Body.AcceptURL = acceptURL(token)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "revoke-invitation", Method: http.MethodDelete, Path: "/api/teams/{team}/invitations/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Revoke a pending invitation (owner or admin)", Tags: []string{"Invitations"},
	}, func(ctx context.Context, in *struct {
		TeamPath
		ID string `path:"id" maxLength:"64"`
	}) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageMembers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			n, err := q.RevokeInvitation(ctx, db.RevokeInvitationParams{ID: in.ID, TeamID: m.TeamID})
			if err != nil || n == 0 {
				return errNotFound
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "invitation.revoked", "invitation", in.ID, nil)
		})
		return nil, err
	})

	type tokenPath struct {
		Token string `path:"token" maxLength:"128"`
	}
	type viewOut struct {
		Body struct {
			TeamName string    `json:"team_name"`
			Email    string    `json:"email"`
			Role     string    `json:"role"`
			Status   string    `json:"status"`
			Expires  time.Time `json:"expires_at"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "view-invitation", Method: http.MethodGet, Path: "/api/invitations/{token}",
		Summary: "Look at an invitation you hold the link to", Tags: []string{"Invitations"},
	}, func(ctx context.Context, in *tokenPath) (*viewOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		out := &viewOut{}
		hash := tokens.Hash(in.Token)
		err = d.tx(ctx, tenancy.Scope{UserID: id.ID, TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
			inv, err := q.InvitationByTokenHash(ctx, hash)
			if err != nil {
				return errNotFound
			}
			// The token proves the invitation; reading the team's name needs its context.
			if err := tenancy.SetTeam(ctx, tx, inv.TeamID); err != nil {
				return err
			}
			t, err := q.GetTeam(ctx, inv.TeamID)
			if err != nil {
				return errNotFound
			}
			v := toInvitationOut(inv)
			out.Body.TeamName, out.Body.Email, out.Body.Role, out.Body.Status, out.Body.Expires = t.Name, inv.Email, inv.Role, v.Status, inv.ExpiresAt
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "accept-invitation", Method: http.MethodPost, Path: "/api/invitations/{token}/accept",
		Summary: "Join the team an invitation is for", Tags: []string{"Invitations"},
	}, func(ctx context.Context, in *tokenPath) (*acceptedOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		out := &acceptedOut{}
		hash := tokens.Hash(in.Token)
		err = d.tx(ctx, tenancy.Scope{UserID: id.ID, TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
			inv, err := q.InvitationByTokenHash(ctx, hash)
			if err != nil {
				return errNotFound
			}
			if inv.Status != "pending" {
				return huma.Error410Gone("this invitation has already been " + inv.Status)
			}
			if time.Now().After(inv.ExpiresAt) {
				return huma.Error410Gone("this invitation has expired")
			}
			u, err := q.UserByID(ctx, id.ID)
			if err != nil {
				return huma.Error401Unauthorized("unauthorized")
			}
			// An invitation is for a person, not for whoever holds the link.
			if !strings.EqualFold(strings.TrimSpace(u.Email), inv.Email) {
				return huma.Error403Forbidden("this invitation was sent to a different email address")
			}
			if err := tenancy.SetTeam(ctx, tx, inv.TeamID); err != nil {
				return err
			}
			if _, err := q.MembershipFor(ctx, db.MembershipForParams{TeamID: inv.TeamID, UserID: id.ID}); err == nil {
				return huma.Error409Conflict("you are already in this team")
			}
			if _, err := q.InsertTeamMember(ctx, db.InsertTeamMemberParams{TeamID: inv.TeamID, UserID: id.ID, Role: inv.Role}); err != nil {
				return huma.Error500InternalServerError("could not join the team")
			}
			if n, err := q.AcceptInvitation(ctx, db.AcceptInvitationParams{ID: inv.ID, AcceptedBy: ptr(id.ID)}); err != nil || n == 0 {
				return huma.Error409Conflict("this invitation is no longer pending")
			}
			if err := tenancy.Record(ctx, q, inv.TeamID, id.ID, "invitation.accepted", "invitation", inv.ID,
				map[string]any{"email": inv.Email, "role": inv.Role}); err != nil {
				return huma.Error500InternalServerError("audit failed")
			}
			t, err := q.GetTeam(ctx, inv.TeamID)
			if err != nil {
				return errNotFound
			}
			out.Body.Team = TeamSummary{ID: t.ID, Name: t.Name, Slug: t.Slug, Role: inv.Role, CreatedAt: t.CreatedAt}
			return nil
		})
		return out, err
	})
}
