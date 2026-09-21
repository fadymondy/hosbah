package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/realtime"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The "Realtime" section of a site (internal/realtime): its websocket server runs as one worker of the
release, and Caddy sends a path of the site's hostnames to it.

Read: any member. Write: whoever may manage the app (manage_apps). An imported app has no release of
Hosbah's: 422 on every route. A team the caller is not in: 404.
*/

// RealtimeIn is PUT …/realtime.
type RealtimeIn struct {
	Enabled   *bool  `json:"enabled,omitempty" doc:"Default true. false keeps the configuration and removes the route at once."`
	Kind      string `json:"kind" enum:"reverb,socketio,ws,go,custom" doc:"What the process is. It only chooses the default path."`
	Process   string `json:"process" minLength:"1" maxLength:"31" doc:"^[a-z][a-z0-9_-]{0,30}$ — the bare name of a worker of cloudy.toml (reverb for \"worker:reverb\"). Not web."`
	Port      int    `json:"port" minimum:"1" maximum:"65535" doc:"The port the process listens on inside its container."`
	Path      string `json:"path,omitempty" maxLength:"100" doc:"Starts with /; letters, digits, . _ - and / only; no . or .. segments. Default: /app (reverb), /socket.io (socketio), /ws (others)."`
	WholeSite bool   `json:"whole_site,omitempty" doc:"Required, with kind custom, for the path / — the whole site goes to the process."`
}

func (d *deps) registerRealtime(api huma.API) {
	tags := []string{"Realtime"}
	path := "/api/teams/{team}/apps/{app}/realtime"

	type statusOut struct{ Body realtime.RealtimeStatus }
	huma.Register(api, huma.Operation{OperationID: "get-app-realtime-status", Method: http.MethodGet, Path: path, Tags: tags,
		Summary: "The site's realtime (websocket) configuration, a suggestion detected from its processes and the names of its environment " +
			"variables (confidence and evidence; never saved by itself), whether the live release is routed, and the URLs clients connect to. " +
			"connections is null: no kind exposes a dependable count. 422: an imported app. Any member.",
	}, func(ctx context.Context, in *AppPath) (*statusOut, error) {
		out := &statusOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body, err = realtime.Get(ctx, q, m.TeamID, a)
			return scheduleErr(err, "could not read the realtime configuration")
		})
		return out, err
	})

	type savedOut struct{ Body realtime.RealtimeSaved }
	huma.Register(api, huma.Operation{OperationID: "set-app-realtime", Method: http.MethodPut, Path: path, Tags: tags,
		Summary: "Save the site's realtime configuration (upsert): which worker of cloudy.toml is the websocket server, the port it listens on " +
			"inside its container, and the path of the site's hostnames that goes to it. From the next deploy or restart the agent publishes " +
			"that port on loopback and Caddy routes path* to it, ahead of the site's own route (applies: next_deploy). When the live release " +
			"already publishes that process's port — a changed path, or enabling again — the route changes at once (applies: now, operation). " +
			"422: a bad kind, process, port or path; the path / without kind custom and whole_site; a process the live release does not " +
			"declare (the message lists the declared workers); an imported app. Owner or admin. Audited (realtime.configured).",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body RealtimeIn
	}) (*savedOut, error) {
		out := &savedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body, err = realtime.Set(ctx, q, d.ops, m.TeamID, webActor(id), a, realtime.Input{Enabled: in.Body.Enabled, Kind: in.Body.Kind,
				Process: in.Body.Process, Port: in.Body.Port, Path: in.Body.Path, WholeSite: in.Body.WholeSite})
			return scheduleErr(err, "could not save the realtime configuration")
		})
		if err != nil {
			return nil, err
		}
		d.ops.Wake() // after the commit: a realtime.route may be waiting
		return out, nil
	})

	type removedOut struct{ Body realtime.RealtimeRemoved }
	huma.Register(api, huma.Operation{OperationID: "remove-app-realtime", Method: http.MethodDelete, Path: path, Tags: tags,
		Summary: "Remove the site's realtime configuration. A route the live release has is removed at once (operation: realtime.route); the " +
			"process keeps running, reachable on loopback only, until the next deploy or restart. 404: there is no configuration. " +
			"422: an imported app. Owner or admin. Audited (realtime.removed).",
	}, func(ctx context.Context, in *AppPath) (*removedOut, error) {
		out := &removedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body, err = realtime.Remove(ctx, q, d.ops, m.TeamID, webActor(id), a)
			return scheduleErr(err, "could not remove the realtime configuration")
		})
		if err != nil {
			return nil, err
		}
		d.ops.Wake()
		return out, nil
	})
}
