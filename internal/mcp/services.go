package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ServiceTools are MH-576 D3: systemd units of one server. list_services is read-only; service_action
// is destructive (a wrong restart can take a site down) so it needs confirm like other destructive
// tools, and refuses the agent's own unit on both sides.
func ServiceTools() []Tool {
	return []Tool{
		{
			Name: "list_services", Scope: scopes.Read,
			Description: "The server's systemd .service units and their state.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": str("Server id or name"),
				"query": str("Case-insensitive substring match on the unit name (optional)")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				var res agentproto.ServiceListResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindServiceList, agentproto.ServiceListParams{Query: args.Str("query")}, &res); err != nil {
					return "", err
				}
				if len(res.Units) == 0 {
					return "No service units found.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d unit(s):\n", len(res.Units))
				for _, u := range res.Units {
					enabled := "disabled"
					if u.Enabled {
						enabled = "enabled"
					}
					fmt.Fprintf(&b, "  %-32s %-10s %-10s %s\n", u.Name, u.ActiveState, u.SubState, enabled)
				}
				return b.String(), nil
			},
		},
		{
			Name: "service_action", Scope: scopes.Destructive, Destructive: true, ResourceType: "service",
			Description: "Start, stop, restart or reload one systemd unit. Refuses hosbah-agent.service / cloudy-agent.service. confirm must equal the unit name.",
			InputSchema: schema([]string{"server", "unit", "action", "confirm"}, map[string]any{
				"server":  str("Server id or name"),
				"unit":    str("Unit name, e.g. caddy.service"),
				"action":  map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "reload"}},
				"confirm": str("The unit name, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				unit := args.Str("unit")
				if err := RequireConfirm(args, unit); err != nil {
					return "", err
				}
				p := agentproto.ServiceActionParams{Unit: unit, Action: args.Str("action")}
				if err := p.Validate(); err != nil {
					return "", err
				}
				var res agentproto.ServiceActionResult
				if err := runServiceOp(ctx, env, srv.ID, agentproto.KindServiceAction, p, &res); err != nil {
					return "", err
				}
				return fmt.Sprintf("%s %s: now %s (%s).", p.Action, unit, res.ActiveState, res.SubState), nil
			},
		},
	}
}

// runServiceOp enqueues a service.list/service.action operation for server and waits for it, both
// short-lived (internal/ops/queue.go's Timeout), unmarshalling its result into out.
func runServiceOp(ctx context.Context, env *Env, serverID, kind string, params any, out any) error {
	svc, err := opsOf(env)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return errors.New("could not encode the operation")
	}
	var opID string
	err = tenancy.Tx(ctx, env.App.SQLDB, tenancy.Scope{System: true, TeamID: env.Caller.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		op, _, e := svc.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: env.Caller.TeamID, ServerID: serverID, Kind: kind, Params: raw, CreatedBy: env.Caller.UserID})
		if e != nil {
			return e
		}
		opID = op.ID
		return nil
	})
	if err != nil {
		return err
	}
	svc.Wake()
	done, err := svc.Await(ctx, opID, serverID, nil)
	if err != nil {
		return err
	}
	if done.Status != "ok" {
		if done.Error != nil {
			return errors.New(*done.Error)
		}
		return errors.New("the operation did not succeed: " + done.Status)
	}
	if out != nil && done.Result != nil {
		return json.Unmarshal([]byte(*done.Result), out)
	}
	return nil
}
