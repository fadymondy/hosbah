package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Systemd services of one server (MH-576 D3): a read-only list (any member) and start/stop/restart/
// reload of one unit (owner or admin, audited, confirm required over MCP — see internal/mcp). The
// agent's own unit is refused on both sides (agentproto.ProtectedUnit): there is no way to restart
// the thing carrying the restart command without a human at the console.

// ServiceListIn is the optional filter on a server's units.
type ServiceListIn struct {
	ServerPath
	Query string `query:"query" maxLength:"200" doc:"Case-insensitive substring match on the unit name."`
}

// ServiceActionIn starts, stops, restarts or reloads one unit.
type ServiceActionIn struct {
	Unit   string `json:"unit" minLength:"1" maxLength:"255" doc:"A systemd .service unit name."`
	Action string `json:"action" enum:"start,stop,restart,reload"`
}

func (d *deps) registerServices(api huma.API) {
	tags := []string{"Services"}
	base := "/api/teams/{team}/servers/{server}/services"

	type listOut struct{ Body agentproto.ServiceListResult }
	huma.Register(api, huma.Operation{OperationID: "list-server-services", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The server's systemd .service units (running through systemctl, not a shell), with their load/active/sub state. " +
			"Any member.",
	}, func(ctx context.Context, in *ServiceListIn) (*listOut, error) {
		out := &listOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			p := agentproto.ServiceListParams{Query: in.Query}
			if err := p.Validate(); err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			return d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindServiceList, p, &out.Body)
		})
		return out, err
	})

	type actionOut struct {
		Body agentproto.ServiceActionResult
	}
	huma.Register(api, huma.Operation{OperationID: "action-server-service", Method: http.MethodPost, Path: base + "/action", Tags: tags,
		Summary: "Start, stop, restart or reload one systemd unit. Refuses hosbah-agent.service and the legacy cloudy-agent.service on " +
			"both sides: the agent cannot be asked to stop or restart itself through its own operation queue. " +
			"422: not a valid unit name. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body ServiceActionIn
	}) (*actionOut, error) {
		out := &actionOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			p := agentproto.ServiceActionParams{Unit: in.Body.Unit, Action: in.Body.Action}
			if err := p.Validate(); err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			if err := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindServiceAction, p, &out.Body); err != nil {
				return err
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server_service."+in.Body.Action, "server", srv.ID,
				map[string]any{"unit": in.Body.Unit, "via": "web"})
		})
		return out, err
	})
}

// runServiceOp enqueues a service.list/service.action operation and waits for it (both have their
// own short timeout — see internal/ops/queue.go's Timeout), unmarshalling its result into out.
func (d *deps) runServiceOp(ctx context.Context, teamID, serverID, kind string, params any, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return huma.Error500InternalServerError("could not encode the operation")
	}
	var opID string
	err = d.tx(ctx, tenancy.Scope{System: true, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		op, _, e := d.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: serverID, Kind: kind, Params: raw, CreatedBy: "hosbah"})
		if e != nil {
			return e
		}
		opID = op.ID
		return nil
	})
	if err != nil {
		return actionErr(err, "could not run the operation")
	}
	d.ops.Wake()
	done, err := d.ops.Await(ctx, opID, serverID, nil)
	if err != nil {
		return actionErr(err, "the operation did not finish")
	}
	if done.Status != "ok" {
		if done.Error != nil {
			return huma.Error409Conflict(*done.Error)
		}
		return huma.Error409Conflict("the operation did not succeed: " + done.Status)
	}
	if out != nil && done.Result != nil {
		if e := json.Unmarshal([]byte(*done.Result), out); e != nil {
			return huma.Error500InternalServerError("could not read the operation's result")
		}
	}
	return nil
}
