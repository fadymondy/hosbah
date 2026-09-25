package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/costs"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Cost and usage (MH-575 H10): what a team's infrastructure costs and how each server and app uses it.
Pricing is a per-server monthly amount the team sets (internal/db/schema/server_cost.sql — no row
means "not priced", the common case for a BYO or Proxmox guest server), plus free-form extra monthly
line items (cost_line_items: "Cloudflare", "backups storage", ...). Usage comes from server_metrics
(the same table H1's charts read); a server's cost is split across its apps proportionally to each
app's configured memory limit (internal/deploy.Resources), the only per-app signal that is already
stored — true per-app usage history is not collected (no new heavy collector), so the UI is told this
plainly. internal/costs holds the arithmetic and the shared row-to-output formatting (BuildOverview);
this file only reads under tenancy.Tx and calls it, so REST and MCP (internal/mcp/costs.go) compute
the same numbers. Reading is any member; setting prices and line items is owner/admin
(tenancy.AbilityManageTeam), matching internal/tenancy/roles.go's owner/admin-only abilities.
*/

// LineItemPath is a team's cost line item, addressed by id.
type LineItemPath struct {
	TeamPath
	Item string `path:"item" maxLength:"64"`
}

func (d *deps) registerCosts(api huma.API) {
	tags := []string{"Costs"}
	base := "/api/teams/{team}/costs"

	huma.Register(api, huma.Operation{OperationID: "costs-overview", Method: http.MethodGet, Path: base + "/overview", Tags: tags,
		Summary: "What the team's infrastructure costs and how it is used: server prices, usage and right-sizing hints over the " +
			"window, cost split across each server's apps, and the extra monthly line items. Any member.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Window string `query:"window" enum:"7d,30d" default:"30d"`
	}) (*struct{ Body costs.CostsOverview }, error) {
		out := &struct{ Body costs.CostsOverview }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			body, err := loadCostsOverview(ctx, q, m.TeamID, in.Window)
			if err != nil {
				return huma.Error500InternalServerError("could not build the cost overview")
			}
			out.Body = body
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "get-server-usage", Method: http.MethodGet, Path: "/api/teams/{team}/servers/{server}/usage", Tags: []string{"Servers"},
		Summary: "One server's usage over a window: avg/p95 CPU, average memory, latest disk, and the same over-provisioned/under-pressure " +
			"hint the cost overview shows. Any member.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Window string `query:"window" enum:"7d,30d" default:"30d"`
	}) (*struct {
		Body struct {
			Window string         `json:"window"`
			Usage  costs.UsageOut `json:"usage"`
			Hint   string         `json:"hint,omitempty" enum:",over_provisioned,under_pressure"`
		}
	}, error) {
		out := &struct {
			Body struct {
				Window string         `json:"window"`
				Usage  costs.UsageOut `json:"usage"`
				Hint   string         `json:"hint,omitempty" enum:",over_provisioned,under_pressure"`
			}
		}{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			window := costs.NormalizeWindow(in.Window)
			since := costs.WindowSince(time.Now(), window)
			row, err := q.ServerUsageSummary(ctx, db.ServerUsageSummaryParams{TeamID: m.TeamID, ServerID: s.ID, Since: since})
			if err != nil {
				return huma.Error500InternalServerError("could not read usage")
			}
			u := costs.UsageFromServerRow(row)
			out.Body.Window = window
			out.Body.Usage = costs.ToUsageOut(u)
			out.Body.Hint = string(u.Hint())
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "put-server-cost", Method: http.MethodPut, Path: base + "/servers/{server}", Tags: tags,
		Summary: "Set (or replace) a server's monthly price. Servers with no price set (the common case for BYO or Proxmox guests) show " +
			"as unpriced in the overview. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			Amount   float64 `json:"amount" minimum:"0" maximum:"1000000"`
			Currency string  `json:"currency,omitempty" minLength:"3" maxLength:"3" default:"USD"`
		}
	}) (*struct{ Body costs.Money }, error) {
		out := &struct{ Body costs.Money }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			currency := in.Body.Currency
			if currency == "" {
				currency = "USD"
			}
			row, err := q.UpsertServerCost(ctx, db.UpsertServerCostParams{
				TeamID: m.TeamID, ServerID: s.ID, AmountCents: costs.DollarsToCents(in.Body.Amount), Currency: currency, CreatedBy: id.ID,
			})
			if err != nil {
				return huma.Error500InternalServerError("could not save the server's price")
			}
			out.Body = costs.Money{Amount: float64(row.AmountCents) / 100, Currency: row.Currency}
			return tenancy.Record(ctx, q, m.TeamID, webActor(id).UserID, "server.cost_set", "server", s.ID,
				map[string]any{"amount_cents": row.AmountCents, "currency": row.Currency})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-server-cost", Method: http.MethodDelete, Path: base + "/servers/{server}", Tags: tags,
		DefaultStatus: http.StatusNoContent, Summary: "Clear a server's price (back to unpriced). Owner or admin. Audited.",
	}, func(ctx context.Context, in *ServerPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if err := q.DeleteServerCostOfServer(ctx, db.DeleteServerCostOfServerParams{TeamID: m.TeamID, ServerID: s.ID}); err != nil {
				return huma.Error500InternalServerError("could not clear the server's price")
			}
			return tenancy.Record(ctx, q, m.TeamID, webActor(id).UserID, "server.cost_cleared", "server", s.ID, nil)
		})
		return &struct{}{}, err
	})

	huma.Register(api, huma.Operation{OperationID: "list-cost-line-items", Method: http.MethodGet, Path: base + "/line-items", Tags: tags,
		Summary: "The team's extra monthly line items (e.g. \"Cloudflare\", \"backups storage\"). Any member.",
	}, func(ctx context.Context, in *TeamPath) (*struct{ Body []costs.LineItemOut }, error) {
		out := &struct{ Body []costs.LineItemOut }{Body: []costs.LineItemOut{}}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.TeamCostLineItems(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list line items")
			}
			for _, r := range rows {
				out.Body = append(out.Body, costs.ToLineItemOut(r))
			}
			return nil
		})
		return out, err
	})

	type lineItemIn struct {
		Name     string  `json:"name" minLength:"1" maxLength:"120"`
		Amount   float64 `json:"amount" minimum:"0" maximum:"1000000"`
		Currency string  `json:"currency,omitempty" minLength:"3" maxLength:"3" default:"USD"`
	}

	huma.Register(api, huma.Operation{OperationID: "create-cost-line-item", Method: http.MethodPost, Path: base + "/line-items", Tags: tags,
		DefaultStatus: http.StatusCreated, Summary: "Add an extra monthly line item. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body lineItemIn
	}) (*struct{ Body costs.LineItemOut }, error) {
		out := &struct{ Body costs.LineItemOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			currency := in.Body.Currency
			if currency == "" {
				currency = "USD"
			}
			row, err := q.CreateCostLineItem(ctx, db.CreateCostLineItemParams{
				TeamID: m.TeamID, Name: in.Body.Name, AmountCents: costs.DollarsToCents(in.Body.Amount), Currency: currency, CreatedBy: id.ID,
			})
			if err != nil {
				return huma.Error500InternalServerError("could not create the line item")
			}
			out.Body = costs.ToLineItemOut(row)
			return tenancy.Record(ctx, q, m.TeamID, webActor(id).UserID, "cost_line_item.created", "cost_line_item", row.ID,
				map[string]any{"name": row.Name, "amount_cents": row.AmountCents, "currency": row.Currency})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-cost-line-item", Method: http.MethodPut, Path: base + "/line-items/{item}", Tags: tags,
		Summary: "Rename or reprice a line item. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		LineItemPath
		Body lineItemIn
	}) (*struct{ Body costs.LineItemOut }, error) {
		out := &struct{ Body costs.LineItemOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			currency := in.Body.Currency
			if currency == "" {
				currency = "USD"
			}
			row, err := q.UpdateCostLineItem(ctx, db.UpdateCostLineItemParams{
				ID: in.Item, TeamID: m.TeamID, Name: in.Body.Name, AmountCents: costs.DollarsToCents(in.Body.Amount), Currency: currency,
			})
			if isNoRows(err) {
				return errNotFound
			}
			if err != nil {
				return huma.Error500InternalServerError("could not update the line item")
			}
			out.Body = costs.ToLineItemOut(row)
			return tenancy.Record(ctx, q, m.TeamID, webActor(id).UserID, "cost_line_item.updated", "cost_line_item", row.ID,
				map[string]any{"name": row.Name, "amount_cents": row.AmountCents, "currency": row.Currency})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-cost-line-item", Method: http.MethodDelete, Path: base + "/line-items/{item}", Tags: tags,
		DefaultStatus: http.StatusNoContent, Summary: "Remove a line item. Owner or admin. Audited.",
	}, func(ctx context.Context, in *LineItemPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			n, err := q.DeleteCostLineItemOfTeam(ctx, db.DeleteCostLineItemOfTeamParams{ID: in.Item, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not delete the line item")
			}
			if n == 0 {
				return errNotFound
			}
			return tenancy.Record(ctx, q, m.TeamID, webActor(id).UserID, "cost_line_item.deleted", "cost_line_item", in.Item, nil)
		})
		return &struct{}{}, err
	})
}

// loadCostsOverview fetches every row costs.BuildOverview needs, under the caller's tenancy.Tx.
func loadCostsOverview(ctx context.Context, q *db.Queries, teamID, window string) (costs.CostsOverview, error) {
	servers, err := q.ListServersOfTeam(ctx, teamID)
	if err != nil {
		return costs.CostsOverview{}, err
	}
	priceRows, err := q.TeamServerCosts(ctx, teamID)
	if err != nil {
		return costs.CostsOverview{}, err
	}
	since := costs.WindowSince(time.Now(), window)
	usageRows, err := q.TeamServerUsageSummary(ctx, db.TeamServerUsageSummaryParams{TeamID: teamID, Since: since})
	if err != nil {
		return costs.CostsOverview{}, err
	}
	appRows, err := q.ListAppsOfTeam(ctx, teamID)
	if err != nil {
		return costs.CostsOverview{}, err
	}
	lineItemRows, err := q.TeamCostLineItems(ctx, teamID)
	if err != nil {
		return costs.CostsOverview{}, err
	}
	return costs.BuildOverview(window, servers, priceRows, usageRows, appRows, lineItemRows), nil
}
