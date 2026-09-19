package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

type TeamOut struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Role        string `json:"role"`
	MemberCount int64  `json:"member_count"`
	// RequireDestructiveApproval: a destructive MCP call files an approval request instead of acting.
	RequireDestructiveApproval bool      `json:"require_destructive_approval"`
	CreatedAt                  time.Time `json:"created_at"`
}

type teamOut struct{ Body TeamOut }

type TeamPath struct {
	Team string `path:"team" maxLength:"64"`
}

var slugJunk = regexp.MustCompile(`[^a-z0-9]+`)

// slugFor makes a URL-safe slug with a random suffix. The suffix is what makes it
// unique: under row-level security a new team cannot see other teams' slugs to
// check for a clash, and the unique index is the arbiter.
func slugFor(name string) string {
	base := strings.Trim(slugJunk.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(base) > 40 {
		base = strings.Trim(base[:40], "-")
	}
	if base == "" {
		base = "team"
	}
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return base + "-" + hex.EncodeToString(b)
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (d *deps) registerTeams(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-team", Method: http.MethodPost, Path: "/api/teams",
		DefaultStatus: http.StatusCreated, Summary: "Create a team (you become its owner)", Tags: []string{"Teams"},
	}, func(ctx context.Context, in *struct {
		Body struct {
			Name string `json:"name" minLength:"1" maxLength:"80"`
		}
	}) (*teamOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("a team needs a name")
		}

		// The id is chosen here, not by the database: the RLS insert policy needs it in
		// context before the row exists (teams_insert: id = app.team_id AND owner = you).
		teamID := newUUID()
		_, wrapped, err := d.a.Keys.NewTeamKey(teamID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not create the team key")
		}

		out := &teamOut{}
		err = d.tx(ctx, tenancy.Scope{UserID: id.ID, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
			t, err := q.InsertTeam(ctx, db.InsertTeamParams{ID: teamID, Name: name, Slug: slugFor(name), OwnerID: id.ID, DekWrapped: wrapped})
			if err != nil {
				return huma.Error500InternalServerError("could not create the team")
			}
			if _, err := q.InsertTeamMember(ctx, db.InsertTeamMemberParams{TeamID: teamID, UserID: id.ID, Role: tenancy.RoleOwner}); err != nil {
				return huma.Error500InternalServerError("could not add you to the team")
			}
			if err := tenancy.Record(ctx, q, teamID, id.ID, "team.created", "team", teamID, map[string]any{"name": name}); err != nil {
				return huma.Error500InternalServerError("audit failed")
			}
			out.Body = TeamOut{ID: t.ID, Name: t.Name, Slug: t.Slug, Role: tenancy.RoleOwner, MemberCount: 1, CreatedAt: t.CreatedAt}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-team", Method: http.MethodGet, Path: "/api/teams/{team}",
		Summary: "A team you belong to", Tags: []string{"Teams"},
	}, func(ctx context.Context, in *TeamPath) (*teamOut, error) {
		out := &teamOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			t, err := q.GetTeam(ctx, m.TeamID)
			if err != nil {
				return errNotFound
			}
			n, _ := q.CountMembers(ctx, m.TeamID)
			out.Body = TeamOut{ID: t.ID, Name: t.Name, Slug: t.Slug, Role: m.Role, MemberCount: n, RequireDestructiveApproval: t.RequireDestructiveApproval, CreatedAt: t.CreatedAt}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-team", Method: http.MethodPatch, Path: "/api/teams/{team}",
		Summary: "Rename a team or change its settings (owner or admin)", Tags: []string{"Teams"},
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body struct {
			Name string `json:"name,omitempty" maxLength:"80"`
			// RequireDestructiveApproval is a pointer so that omitting it leaves the setting alone —
			// a rename must not silently turn the approval gate off.
			RequireDestructiveApproval *bool `json:"require_destructive_approval,omitempty" doc:"When on, a destructive MCP call files an approval request instead of acting."`
		}
	}) (*teamOut, error) {
		out := &teamOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			before, err := q.GetTeam(ctx, m.TeamID)
			if err != nil {
				return errNotFound
			}
			t := before
			if in.Body.RequireDestructiveApproval != nil && *in.Body.RequireDestructiveApproval != before.RequireDestructiveApproval {
				if t, err = q.SetTeamDestructiveApproval(ctx, db.SetTeamDestructiveApprovalParams{
					ID: m.TeamID, RequireDestructiveApproval: *in.Body.RequireDestructiveApproval,
				}); err != nil {
					return huma.Error500InternalServerError("could not change the approval setting")
				}
				if err := tenancy.Record(ctx, q, m.TeamID, id.ID, "team.destructive_approval_changed", "team", m.TeamID,
					map[string]any{"require_destructive_approval": map[string]bool{"old": before.RequireDestructiveApproval, "new": *in.Body.RequireDestructiveApproval}}); err != nil {
					return huma.Error500InternalServerError("audit failed")
				}
			}
			if name := strings.TrimSpace(in.Body.Name); name != "" && name != before.Name {
				if t, err = q.RenameTeam(ctx, db.RenameTeamParams{ID: m.TeamID, Name: name}); err != nil {
					return huma.Error500InternalServerError("could not rename")
				}
				if err := tenancy.Record(ctx, q, m.TeamID, id.ID, "team.renamed", "team", m.TeamID,
					map[string]any{"name": map[string]string{"old": before.Name, "new": name}}); err != nil {
					return huma.Error500InternalServerError("audit failed")
				}
			}
			n, _ := q.CountMembers(ctx, m.TeamID)
			out.Body = TeamOut{ID: t.ID, Name: t.Name, Slug: t.Slug, Role: m.Role, MemberCount: n, RequireDestructiveApproval: t.RequireDestructiveApproval, CreatedAt: t.CreatedAt}
			return nil
		})
		return out, err
	})
}
