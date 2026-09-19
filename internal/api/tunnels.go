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

// TunnelOut is a server's tunnel (frp or tailscale).
type TunnelOut struct {
	ID           string    `json:"id"`
	ServerID     string    `json:"server_id"`
	ServerName   string    `json:"server_name,omitempty"`
	CredentialID string    `json:"credential_id"`
	Kind         string    `json:"kind" enum:"frp,tailscale"`
	RemotePort   *int32    `json:"remote_port"`
	Status       string    `json:"status" enum:"pending,active,failed,removing"`
	Address      *string   `json:"address" doc:"frp: frps host:port; tailscale: the server's tailnet IP"`
	Error        *string   `json:"error"`
	CreatedAt    time.Time `json:"created_at"`
}

func toTunnelOut(t db.Tunnel, serverName string) TunnelOut {
	o := TunnelOut{ID: t.ID, ServerID: t.ServerID, ServerName: serverName, CredentialID: t.CredentialID, Kind: t.Kind,
		Status: t.Status, Address: t.Address, Error: t.Error, CreatedAt: t.CreatedAt}
	if t.RemotePort.Valid {
		p := t.RemotePort.Int32
		o.RemotePort = &p
	}
	return o
}

func (d *deps) registerTunnels(api huma.API) {
	tags := []string{"Tunnels"}

	type listOut struct {
		Body struct {
			Tunnels []TunnelOut `json:"tunnels"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-tunnels", Method: http.MethodGet, Path: "/api/teams/{team}/tunnels",
		Tags: tags, Summary: "The team's tunnels (frp, tailscale): how servers without a public address are reached",
	}, func(ctx context.Context, in *TeamPath) (*listOut, error) {
		out := &listOut{}
		out.Body.Tunnels = []TunnelOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListTunnelsOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list tunnels")
			}
			for _, r := range rows {
				out.Body.Tunnels = append(out.Body.Tunnels, toTunnelOut(db.Tunnel{ID: r.ID, TeamID: r.TeamID, ServerID: r.ServerID,
					CredentialID: r.CredentialID, Kind: r.Kind, RemotePort: r.RemotePort, Status: r.Status, Address: r.Address,
					Error: r.Error, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}, r.ServerName))
			}
			return nil
		})
		return out, err
	})

	type oneOut struct{ Body TunnelOut }
	huma.Register(api, huma.Operation{OperationID: "enable-tunnel", Method: http.MethodPost, Path: "/api/teams/{team}/servers/{server}/tunnels",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Open a tunnel for a server with one of the team's frp or tailscale credentials (owner or admin). frp: domains on the server " +
			"are then published through the team's Cloudflare and NPM credentials.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			Credential string `json:"credential" minLength:"1" maxLength:"80" doc:"frp or tailscale credential id or name"`
		}
	}) (*oneOut, error) {
		out := &oneOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			t, err := d.deploys().EnableTunnel(ctx, q, m.TeamID, webActor(id), s, in.Body.Credential)
			if err != nil {
				return actionErr(err, "could not open the tunnel")
			}
			out.Body = toTunnelOut(t, s.Name)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "disable-tunnel", Method: http.MethodDelete, Path: "/api/teams/{team}/servers/{server}/tunnels/{kind}",
		DefaultStatus: http.StatusNoContent, Tags: tags, Summary: "Close a server's frp or tailscale tunnel (owner or admin)",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Kind string `path:"kind" enum:"frp,tailscale"`
	}) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			ts, err := q.TunnelsOfServer(ctx, db.TunnelsOfServerParams{ServerID: s.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("lookup failed")
			}
			for _, t := range ts {
				if t.Kind == in.Kind {
					return actionErr(d.deploys().DisableTunnel(ctx, q, m.TeamID, webActor(id), t), "could not close the tunnel")
				}
			}
			return errNotFound
		})
		return nil, err
	})
}
