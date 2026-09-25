// Package quota is per-team quotas (MH-574 E13): a soft guardrail on top of the platform-wide
// max_servers_per_team (internal/platform), set per team by a platform admin, defaulting to
// unlimited. Enforced at the three creation points that grow a team's footprint: adding a server
// (internal/provisioning.AddServer), creating an app, and creating a data service.
package quota

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// Error carries an HTTP status the REST layer maps directly (403: quota reached; the MCP layer
// just shows Msg).
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func refuse(what string, limit, have int64) error {
	return &Error{Status: http.StatusForbidden, Msg: fmt.Sprintf(
		"this team's quota allows at most %d %s (it has %d) — ask a platform admin to raise it", limit, what, have)}
}

// Quota is a team's limits. A nil field is unlimited.
type Quota struct {
	MaxServers      *int64 `json:"max_servers"`
	MaxApps         *int64 `json:"max_apps"`
	MaxDataServices *int64 `json:"max_data_services"`
}

// Usage is a team's current counts against Quota, for GET .../quotas.
type Usage struct {
	Quota
	Servers      int64 `json:"servers"`
	Apps         int64 `json:"apps"`
	DataServices int64 `json:"data_services"`
}

// Get reads a team's quota (q in team scope, or platform-admin scope — the SELECT policy allows
// both). A team with no row is unlimited on every field.
func Get(ctx context.Context, q *db.Queries, teamID string) (Quota, error) {
	row, err := q.GetTeamQuota(ctx, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return Quota{}, nil
	}
	if err != nil {
		return Quota{}, fmt.Errorf("could not read the team's quota: %w", err)
	}
	return Quota{MaxServers: row.MaxServers, MaxApps: row.MaxApps, MaxDataServices: row.MaxDataServices}, nil
}

// GetUsage is Get plus the team's current counts (q in team scope: it counts through the team's
// own servers/apps/data_services queries, which RLS already scopes to the team).
func GetUsage(ctx context.Context, q *db.Queries, teamID string) (Usage, error) {
	quo, err := Get(ctx, q, teamID)
	if err != nil {
		return Usage{}, err
	}
	servers, err := q.CountServersOfTeam(ctx, teamID)
	if err != nil {
		return Usage{}, fmt.Errorf("could not count the team's servers: %w", err)
	}
	apps, err := q.CountAppsOfTeam(ctx, teamID)
	if err != nil {
		return Usage{}, fmt.Errorf("could not count the team's apps: %w", err)
	}
	dbs, err := q.CountDataServicesOfTeam(ctx, teamID)
	if err != nil {
		return Usage{}, fmt.Errorf("could not count the team's data services: %w", err)
	}
	return Usage{Quota: quo, Servers: servers, Apps: apps, DataServices: dbs}, nil
}

// Set is a platform admin's write (q in {PlatformAdmin: true} scope; teamID is explicit — the
// scope itself carries no team). A nil pointer clears that field back to unlimited.
func Set(ctx context.Context, q *db.Queries, teamID, actorID string, quo Quota) (Quota, error) {
	row, err := q.UpsertTeamQuota(ctx, db.UpsertTeamQuotaParams{TeamID: teamID, MaxServers: quo.MaxServers,
		MaxApps: quo.MaxApps, MaxDataServices: quo.MaxDataServices, UpdatedBy: &actorID})
	if err != nil {
		return Quota{}, fmt.Errorf("could not save the team's quota: %w", err)
	}
	return Quota{MaxServers: row.MaxServers, MaxApps: row.MaxApps, MaxDataServices: row.MaxDataServices}, nil
}

// CheckServers refuses adding a server when the team's max_servers is already met.
func CheckServers(ctx context.Context, q *db.Queries, teamID string) error {
	quo, err := Get(ctx, q, teamID)
	if err != nil || quo.MaxServers == nil {
		return err
	}
	n, err := q.CountServersOfTeam(ctx, teamID)
	if err != nil {
		return fmt.Errorf("could not count the team's servers: %w", err)
	}
	if n >= *quo.MaxServers {
		return refuse("server(s)", *quo.MaxServers, n)
	}
	return nil
}

// CheckApps refuses creating an app when the team's max_apps is already met.
func CheckApps(ctx context.Context, q *db.Queries, teamID string) error {
	quo, err := Get(ctx, q, teamID)
	if err != nil || quo.MaxApps == nil {
		return err
	}
	n, err := q.CountAppsOfTeam(ctx, teamID)
	if err != nil {
		return fmt.Errorf("could not count the team's apps: %w", err)
	}
	if n >= *quo.MaxApps {
		return refuse("app(s)", *quo.MaxApps, n)
	}
	return nil
}

// CheckDataServices refuses creating a data service when the team's max_data_services is already met.
func CheckDataServices(ctx context.Context, q *db.Queries, teamID string) error {
	quo, err := Get(ctx, q, teamID)
	if err != nil || quo.MaxDataServices == nil {
		return err
	}
	n, err := q.CountDataServicesOfTeam(ctx, teamID)
	if err != nil {
		return fmt.Errorf("could not count the team's data services: %w", err)
	}
	if n >= *quo.MaxDataServices {
		return refuse("data service(s)", *quo.MaxDataServices, n)
	}
	return nil
}
