// Package api is Cloudy's hand-written, team-scoped REST surface (Huma → OpenAPI).
//
// Generated CRUD (internal/rest, from make:resource) is deliberately not mounted:
// it lists and edits every row of a table with no notion of a team. Everything
// here instead:
//
//   - takes identity only from the session (auth.IdentityFrom), never from the body;
//   - addresses team data by path, /api/teams/{team}/..., and answers 404 — the
//     same answer as for a team that does not exist — when the caller is not a
//     member, so ids cannot be probed;
//   - names team_id in every statement AND runs inside tenancy.Tx, so the
//     row-level-security policies back that filter up;
//   - checks the caller's team role through togo authz (tenancy.Can) and writes
//     an audit entry on the same transaction as every change.
package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/cloudy/internal/app"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

type deps struct{ a *app.App }

// Register mounts every Cloudy route. a may be nil (OpenAPI export only).
func Register(api huma.API, a *app.App) {
	d := &deps{a: a}
	d.registerMe(api)
	d.registerTeams(api)
	d.registerMembers(api)
	d.registerInvitations(api)
	d.registerTokens(api)
	d.registerAudit(api)
	d.registerFeedback(api)
	d.registerAdmin(api)
}

var (
	errNotFound  = huma.Error404NotFound("not found")
	errForbidden = huma.Error403Forbidden("your role in this team does not allow that")
)

// current is the signed-in user. The authorization middleware has already refused
// anonymous requests; this is the handler's own check, not the only one.
func current(ctx context.Context) (*auth.Identity, error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok || id == nil || id.ID == "" {
		return nil, huma.Error401Unauthorized("unauthorized")
	}
	return id, nil
}

func (d *deps) tx(ctx context.Context, s tenancy.Scope, fn func(q *db.Queries, tx *sql.Tx) error) error {
	return tenancy.Tx(ctx, d.a.SQLDB, s, fn)
}

// inTeam runs fn inside teamID's context after checking the caller is a member
// holding ability. Not a member → 404 (indistinguishable from "no such team").
func (d *deps) inTeam(ctx context.Context, teamID, ability string,
	fn func(q *db.Queries, tx *sql.Tx, id *auth.Identity, m *tenancy.Membership) error) error {
	id, err := current(ctx)
	if err != nil {
		return err
	}
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return errNotFound
	}
	return d.tx(ctx, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, tx *sql.Tx) error {
		// {team} is an id or a slug, resolved only among the caller's own memberships —
		// so another team's id and a made-up one are the same 404.
		row, err := q.MembershipForRef(ctx, db.MembershipForRefParams{UserID: id.ID, Ref: teamID})
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return huma.Error500InternalServerError("membership lookup failed")
		}
		if err := tenancy.SetTeam(ctx, tx, row.TeamID); err != nil {
			return err
		}
		m := &tenancy.Membership{TeamID: row.TeamID, UserID: id.ID, Role: row.Role}
		if ability != "" && !tenancy.Can(ctx, m, ability) {
			return errForbidden
		}
		return fn(q, tx, id, m)
	})
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func ptr[T any](v T) *T { return &v }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func optional(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}
