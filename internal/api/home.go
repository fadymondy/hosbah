package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/metrics"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/provisioning"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// The team home page: server metrics history (charts and sparklines), the team-wide deployment
// feed, and each server's tunnel summary on the server JSON.

// TunnelSummary is a server's way in from outside, as the server list shows it.
type TunnelSummary struct {
	Kind    string  `json:"kind" enum:"frp,tailscale"`
	Status  string  `json:"status" enum:"pending,active,failed,removing"`
	Address *string `json:"address"`
}

// pickTunnel prefers an active frp tunnel, then any active one, then frp, then anything.
func pickTunnel(ts []db.Tunnel) *TunnelSummary {
	best, score := -1, -1
	for i, t := range ts {
		s := 0
		if t.Status == "active" {
			s += 2
		}
		if t.Kind == "frp" {
			s++
		}
		if s > score {
			best, score = i, s
		}
	}
	if best < 0 {
		return nil
	}
	t := ts[best]
	return &TunnelSummary{Kind: t.Kind, Status: t.Status, Address: t.Address}
}

// attachTunnels fills Tunnel on each server from one query over the team's tunnels.
func attachTunnels(ctx context.Context, q *db.Queries, teamID string, servers []ServerOut) {
	rows, err := q.ListTunnelsOfTeam(ctx, teamID)
	if err != nil {
		return // the list still renders; the summary is a convenience
	}
	by := map[string][]db.Tunnel{}
	for _, r := range rows {
		by[r.ServerID] = append(by[r.ServerID], db.Tunnel{ID: r.ID, ServerID: r.ServerID, Kind: r.Kind, Status: r.Status, Address: r.Address})
	}
	for i := range servers {
		servers[i].Tunnel = pickTunnel(by[servers[i].ID])
	}
}

// serverTunnel is the detail view's summary.
func serverTunnel(ctx context.Context, q *db.Queries, teamID, serverID string) *TunnelSummary {
	ts, err := q.TunnelsOfServer(ctx, db.TunnelsOfServerParams{ServerID: serverID, TeamID: teamID})
	if err != nil {
		return nil
	}
	return pickTunnel(ts)
}

func rangeOf(key string) (metrics.Range, error) {
	if key == "" {
		key = "1h"
	}
	r, ok := metrics.Ranges[key]
	if !ok {
		return metrics.Range{}, huma.Error422UnprocessableEntity("range must be one of 1h, 6h, 24h, 7d, 30d")
	}
	return r, nil
}

func (d *deps) registerHome(api huma.API) {
	type sparksIn struct {
		TeamPath
		Range string `query:"range" enum:"1h,6h,24h,7d,30d" default:"1h"`
	}
	type sparksOut struct{ Body metrics.Sparks }
	huma.Register(api, huma.Operation{
		OperationID: "list-server-sparklines", Method: http.MethodGet, Path: "/api/teams/{team}/servers/metrics",
		Summary: "Every server's CPU, memory and GPU sparklines (at most 60 points each), in one call", Tags: []string{"Servers"},
	}, func(ctx context.Context, in *sparksIn) (*sparksOut, error) {
		r, err := rangeOf(in.Range)
		if err != nil {
			return nil, err
		}
		out := &sparksOut{}
		err = d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			servers, err := q.ListServersOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list servers")
			}
			ids := make([]string, len(servers))
			for i, s := range servers {
				ids[i] = s.ID
			}
			if out.Body, err = metrics.TeamSparks(ctx, q, m.TeamID, ids, r, time.Now()); err != nil {
				return huma.Error500InternalServerError("could not read metrics")
			}
			return nil
		})
		return out, err
	})

	type appLoadOut struct {
		Body struct {
			Apps map[string][]ops.AppLoadPoint `json:"apps" doc:"The last hour of each running app's container load, oldest first, keyed by app id."`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-app-load", Method: http.MethodGet, Path: "/api/teams/{team}/apps/metrics",
		Summary: "Every app's live container load (CPU, memory) for the last hour, from its server's heartbeats", Tags: []string{"Apps"},
	}, func(ctx context.Context, in *TeamPath) (*appLoadOut, error) {
		out := &appLoadOut{}
		out.Body.Apps = map[string][]ops.AppLoadPoint{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			apps, err := q.ListAppsOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list apps")
			}
			for _, a := range apps {
				if pts := d.ops.AppLoad(a.ServerID, a.ID); len(pts) > 0 {
					out.Body.Apps[a.ID] = pts
				}
			}
			return nil
		})
		return out, err
	})

	type seriesIn struct {
		ServerPath
		Range string `query:"range" enum:"1h,6h,24h,7d,30d" default:"1h"`
	}
	type seriesOut struct{ Body metrics.Series }
	huma.Register(api, huma.Operation{
		OperationID: "get-server-metrics", Method: http.MethodGet, Path: "/api/teams/{team}/servers/{server}/metrics",
		Summary: "A server's metrics history for a range, bucketed server-side (at most ~300 points)", Tags: []string{"Servers"},
	}, func(ctx context.Context, in *seriesIn) (*seriesOut, error) {
		r, err := rangeOf(in.Range)
		if err != nil {
			return nil, err
		}
		out := &seriesOut{}
		err = d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if out.Body, err = metrics.ServerSeries(ctx, q, m.TeamID, s.ID, r, time.Now()); err != nil {
				return huma.Error500InternalServerError("could not read metrics")
			}
			return nil
		})
		return out, err
	})

	type feedIn struct {
		TeamPath
		Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
		Before string `query:"before" maxLength:"200" doc:"next_cursor of the previous page."`
	}
	type feedOut struct{ Body deploy.Feed }
	huma.Register(api, huma.Operation{
		OperationID: "list-team-deployments", Method: http.MethodGet, Path: "/api/teams/{team}/deployments",
		Summary: "The team's latest deployments across every app, newest first", Tags: []string{"Apps"},
	}, func(ctx context.Context, in *feedIn) (*feedOut, error) {
		out := &feedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			f, err := deploy.TeamFeed(ctx, q, m.TeamID, in.Limit, in.Before)
			if errors.Is(err, deploy.ErrBadCursor) {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			if err != nil {
				return huma.Error500InternalServerError("could not list deployments")
			}
			out.Body = f
			return nil
		})
		return out, err
	})
}

// ServerPatchIn renames or restyles a server. Omit a field to leave it; "" clears color, icon
// and logo_url.
type ServerPatchIn struct {
	Name    *string `json:"name,omitempty" maxLength:"63" doc:"Letters, digits, dot, dash, underscore; unique in the team."`
	Color   *string `json:"color,omitempty" maxLength:"7" doc:"#RRGGBB, or \"\" to clear."`
	Icon    *string `json:"icon,omitempty" maxLength:"32" doc:"One of server, database, globe, cloud, cpu, box, rocket, shield, code, terminal, layers, zap, flame, leaf, star, heart, briefcase, home, store, graduation-cap, building, gamepad, music, camera; or \"\" to clear."`
	LogoURL *string `json:"logo_url,omitempty" maxLength:"500" doc:"An https:// image URL shown instead of the icon, or \"\" to clear."`
}

func (d *deps) registerServerAppearance(api huma.API) {
	type patchIn struct {
		ServerPath
		Body ServerPatchIn
	}
	type serverOut struct{ Body ServerOut }
	huma.Register(api, huma.Operation{
		OperationID: "update-server", Method: http.MethodPatch, Path: "/api/teams/{team}/servers/{server}",
		Summary: "Rename or restyle a server: name, color, icon, logo (owner or admin). Audited as server.updated.", Tags: []string{"Servers"},
	}, func(ctx context.Context, in *patchIn) (*serverOut, error) {
		out := &serverOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if !tenancy.Can(ctx, m, tenancy.AbilityManageServers) {
				return errForbidden
			}
			row, err := provisioning.UpdateServer(ctx, q, m.TeamID, id.ID, s, provisioning.ServerPatch{
				Name: in.Body.Name, Color: in.Body.Color, Icon: in.Body.Icon, LogoURL: in.Body.LogoURL})
			if err != nil {
				return provisioningErr(err)
			}
			out.Body = d.serverDetail(ctx, q, m, row)
			return nil
		})
		return out, err
	})
}
