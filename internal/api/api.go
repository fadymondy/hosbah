// Package api is Hosbah's hand-written, team-scoped REST surface (Huma → OpenAPI).
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
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/platform"
	"github.com/fadymondy/hosbah/internal/saasify"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

type deps struct {
	a   *app.App
	ops *ops.Service
}

// Register mounts every Hosbah route. a and svc may be nil (OpenAPI export only).
func Register(api huma.API, a *app.App, svc *ops.Service) {
	d := &deps{a: a, ops: svc}
	d.registerHome(api) // before registerServers: /servers/metrics is a literal, not a {server}
	d.registerServers(api)
	d.registerServerAppearance(api)
	d.registerServerLogo(api)
	d.registerAppLogo(api)
	d.registerCanvas(api)
	d.registerDeployConfig(api)
	d.registerSharedServices(api)
	d.registerAppServiceLinks(api)
	d.registerImport(api)
	d.registerOperations(api)
	d.registerApps(api)
	if svc != nil && a != nil && a.SQLDB != nil {
		saasify.WebhookDEK = func(ctx context.Context, teamID string) ([]byte, error) {
			return svc.TeamDEK(ctx, db.New(a.SQLDB), teamID)
		}
	}
	d.registerProducts(api)
	d.registerProductKeysPublic(api)
	d.registerWorkloads(api)
	d.registerDatabases(api)
	d.registerResources(api)
	d.registerProxyHosts(api)
	d.registerFirewall(api)
	d.registerFail2ban(api)
	d.registerTunnels(api)
	d.registerGitHub(api)
	d.registerGitHubApp(api)
	d.registerFiles(api)
	d.registerSFTPUsers(api)
	d.registerRepository(api)
	d.registerDNS(api)
	d.registerMonitors(api)
	d.registerNotificationChannels(api)
	d.registerStatusPages(api)
	d.registerLogDrains(api)
	d.registerRum(api)
	d.registerApm(api)
	d.registerEdgeCache(api)
	d.registerHTTPRules(api)
	d.registerCosts(api)
	d.registerSchedules(api)
	d.registerScripts(api)
	d.registerSiteTemplates(api)
	d.registerServices(api)
	d.registerStorage(api)
	d.registerBrowsers(api)
	d.registerInstalls(api)
	d.registerRealtime(api)
	d.registerQueues(api)
	d.registerProcesses(api)
	d.registerCredentials(api)
	d.registerMe(api)
	d.registerTeams(api)
	d.registerMembers(api)
	d.registerInvitations(api)
	d.registerTokens(api)
	d.registerAudit(api)
	d.registerApprovals(api)
	d.registerFeedback(api)
	d.registerAdmin(api)
	d.registerPlatformAdmin(api)
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
		if row.SuspendedAt != nil && !d.suspendedReadAllowed(ctx, id.ID) {
			return huma.NewError(platform.ErrTeamSuspended.Status, platform.ErrTeamSuspended.Msg)
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

// suspendedReadAllowed: in a suspended team only a platform admin's reads (GET/HEAD) go through.
// The role is re-read from the database, not trusted from the session.
func (d *deps) suspendedReadAllowed(ctx context.Context, userID string) bool {
	switch tenancy.MetaFrom(ctx).Method {
	case http.MethodGet, http.MethodHead:
	default:
		return false
	}
	u, err := db.New(d.a.SQLDB).UserByID(ctx, userID)
	return err == nil && u.DisabledAt == nil && platform.IsAdminRoles(u.Roles)
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
