package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/costs"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// CostTools: MH-575 H10 cost and usage — what the team's servers cost and how they (and their apps)
// use that capacity. cost_overview and server_usage read internal/costs.BuildOverview's numbers, the
// same ones the team REST API (internal/api/costs.go) shows; set_server_cost and cost_line_items
// write the prices/line items an owner or admin sets. No tool here is destructive: clearing a price
// or removing a line item only stops a number from being priced or listed, nothing is torn down.
func CostTools() []Tool {
	return []Tool{
		{
			Name: "cost_overview", Scope: scopes.Read,
			Description: "The team's monthly infrastructure cost and how it is used: totals by currency, each server's price, usage " +
				"and right-sizing hint (over_provisioned: p95 CPU < 10% and memory < 30% over the window; under_pressure: p95 CPU > 85% " +
				"or memory > 85%), its cost split across its apps by memory share, and the extra monthly line items.",
			InputSchema: schema(nil, map[string]any{
				"window": map[string]any{"type": "string", "enum": []string{"7d", "30d"}, "description": "Default 30d"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				window := costs.NormalizeWindow(args.Str("window"))
				ov, err := loadOverview(ctx, env, window)
				if err != nil {
					return "", err
				}
				return renderOverview(ov), nil
			},
		},
		{
			Name: "server_usage", Scope: scopes.Read,
			Description: "One server's usage over a window: avg/p95 CPU, average memory, latest disk, and the same right-sizing hint " +
				"the cost overview shows.",
			InputSchema: schema([]string{"server"}, map[string]any{
				"server": str("Server id or name"),
				"window": map[string]any{"type": "string", "enum": []string{"7d", "30d"}, "description": "Default 30d"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				s, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				window := costs.NormalizeWindow(args.Str("window"))
				since := costs.WindowSince(time.Now(), window)
				row, err := env.Q.ServerUsageSummary(ctx, db.ServerUsageSummaryParams{TeamID: env.Caller.TeamID, ServerID: s.ID, Since: since})
				if err != nil {
					return "", errors.New("could not read usage")
				}
				u := costs.UsageFromServerRow(row)
				if u.Readings == 0 {
					return fmt.Sprintf("%s: no metrics recorded in the last %s.", s.Name, window), nil
				}
				out := costs.ToUsageOut(u)
				var b strings.Builder
				fmt.Fprintf(&b, "%s over %s (%d reading(s)):\n", s.Name, window, out.Readings)
				fmt.Fprintf(&b, "  cpu   avg=%.1f%% p95=%.1f%%\n", out.AvgCPUPercent, out.P95CPUPercent)
				fmt.Fprintf(&b, "  mem   %.1f%% used\n", out.MemPercent)
				fmt.Fprintf(&b, "  disk  %.1f%% used\n", out.DiskPercent)
				if hint := u.Hint(); hint != costs.ServerHintNone {
					fmt.Fprintf(&b, "  hint: %s\n", hint)
				}
				return b.String(), nil
			},
		},
		{
			Name: "set_server_cost", Scope: scopes.Write,
			Description: "Set (or replace) a server's monthly price, used by cost_overview to split its cost across its apps. Pass " +
				"amount=0 (or omit) with clear=true to remove the price (the server shows as unpriced again). Owner or admin. Audited.",
			InputSchema: schema([]string{"server"}, map[string]any{
				"server":   str("Server id or name"),
				"amount":   map[string]any{"type": "number", "description": "Monthly price, in the currency's ordinary units (e.g. 12.50)"},
				"currency": str("3-letter currency code, default USD"),
				"clear":    map[string]any{"type": "boolean", "description": "Remove the price instead of setting it"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageTeam); err != nil {
					return "", err
				}
				s, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				if args.Bool("clear") {
					if err := env.Q.DeleteServerCostOfServer(ctx, db.DeleteServerCostOfServerParams{TeamID: env.Caller.TeamID, ServerID: s.ID}); err != nil {
						return "", errors.New("could not clear the server's price")
					}
					if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "server.cost_cleared", "server", s.ID, auditChanges(env, nil)); err != nil {
						return "", err
					}
					return s.Name + " is now unpriced.", nil
				}
				currency := strings.ToUpper(strings.TrimSpace(args.Str("currency")))
				if currency == "" {
					currency = "USD"
				}
				amount := args.Float64("amount", -1, 0, 1_000_000)
				if amount < 0 {
					return "", errors.New("amount is required (or pass clear=true to remove the price)")
				}
				row, err := env.Q.UpsertServerCost(ctx, db.UpsertServerCostParams{
					TeamID: env.Caller.TeamID, ServerID: s.ID, AmountCents: costs.DollarsToCents(amount), Currency: currency, CreatedBy: env.Caller.UserID,
				})
				if err != nil {
					return "", errors.New("could not save the server's price")
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "server.cost_set", "server", s.ID,
					auditChanges(env, map[string]any{"amount_cents": row.AmountCents, "currency": row.Currency})); err != nil {
					return "", err
				}
				return fmt.Sprintf("%s is now priced at %.2f %s/month.", s.Name, float64(row.AmountCents)/100, row.Currency), nil
			},
		},
		{
			Name: "cost_line_items", Scope: scopes.Write,
			Description: "List, add, rename/reprice or remove the team's extra monthly line items (costs outside per-server pricing, " +
				"e.g. \"Cloudflare\", \"backups storage\"). action=list needs no other argument and any member may call it; add/update/" +
				"remove need an owner or admin role and are audited.",
			InputSchema: schema([]string{"action"}, map[string]any{
				"action":   map[string]any{"type": "string", "enum": []string{"list", "add", "update", "remove"}},
				"id":       str("Line item id (from action=list); required for update/remove"),
				"name":     str("Required for add; optional for update (keeps the current name if omitted)"),
				"amount":   map[string]any{"type": "number", "description": "Monthly amount; required for add; optional for update"},
				"currency": str("3-letter currency code, default USD; optional for update"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				switch args.Str("action") {
				case "list", "":
					rows, err := env.Q.TeamCostLineItems(ctx, env.Caller.TeamID)
					if err != nil {
						return "", errors.New("could not list line items")
					}
					if len(rows) == 0 {
						return "No line items.", nil
					}
					var b strings.Builder
					for _, r := range rows {
						li := costs.ToLineItemOut(r)
						fmt.Fprintf(&b, "  %-24s %8.2f %s  (id %s)\n", li.Name, li.Amount, li.Currency, li.ID)
					}
					return b.String(), nil
				case "add":
					if err := needs(ctx, env, tenancy.AbilityManageTeam); err != nil {
						return "", err
					}
					name := strings.TrimSpace(args.Str("name"))
					if name == "" {
						return "", errors.New("name is required")
					}
					amount := args.Float64("amount", -1, 0, 1_000_000)
					if amount < 0 {
						return "", errors.New("amount is required")
					}
					currency := strings.ToUpper(strings.TrimSpace(args.Str("currency")))
					if currency == "" {
						currency = "USD"
					}
					row, err := env.Q.CreateCostLineItem(ctx, db.CreateCostLineItemParams{
						TeamID: env.Caller.TeamID, Name: name, AmountCents: costs.DollarsToCents(amount), Currency: currency, CreatedBy: env.Caller.UserID,
					})
					if err != nil {
						return "", errors.New("could not create the line item")
					}
					if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "cost_line_item.created", "cost_line_item", row.ID,
						auditChanges(env, map[string]any{"name": row.Name, "amount_cents": row.AmountCents, "currency": row.Currency})); err != nil {
						return "", err
					}
					return fmt.Sprintf("Added %s: %.2f %s/month (id %s).", row.Name, float64(row.AmountCents)/100, row.Currency, row.ID), nil
				case "update":
					if err := needs(ctx, env, tenancy.AbilityManageTeam); err != nil {
						return "", err
					}
					id := args.Str("id")
					if id == "" {
						return "", errors.New("id is required")
					}
					existing, err := env.Q.CostLineItemOfTeam(ctx, db.CostLineItemOfTeamParams{ID: id, TeamID: env.Caller.TeamID})
					if err != nil {
						return "", errors.New("line item not found")
					}
					name := strings.TrimSpace(args.Str("name"))
					if name == "" {
						name = existing.Name
					}
					currency := strings.ToUpper(strings.TrimSpace(args.Str("currency")))
					if currency == "" {
						currency = existing.Currency
					}
					amount := args.Float64("amount", -1, 0, 1_000_000)
					amountCents := existing.AmountCents
					if amount >= 0 {
						amountCents = costs.DollarsToCents(amount)
					}
					row, err := env.Q.UpdateCostLineItem(ctx, db.UpdateCostLineItemParams{
						ID: id, TeamID: env.Caller.TeamID, Name: name, AmountCents: amountCents, Currency: currency,
					})
					if err != nil {
						return "", errors.New("could not update the line item")
					}
					if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "cost_line_item.updated", "cost_line_item", row.ID,
						auditChanges(env, map[string]any{"name": row.Name, "amount_cents": row.AmountCents, "currency": row.Currency})); err != nil {
						return "", err
					}
					return fmt.Sprintf("Updated %s: %.2f %s/month.", row.Name, float64(row.AmountCents)/100, row.Currency), nil
				case "remove":
					if err := needs(ctx, env, tenancy.AbilityManageTeam); err != nil {
						return "", err
					}
					id := args.Str("id")
					if id == "" {
						return "", errors.New("id is required")
					}
					n, err := env.Q.DeleteCostLineItemOfTeam(ctx, db.DeleteCostLineItemOfTeamParams{ID: id, TeamID: env.Caller.TeamID})
					if err != nil || n == 0 {
						return "", errors.New("line item not found")
					}
					if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "cost_line_item.deleted", "cost_line_item", id, auditChanges(env, nil)); err != nil {
						return "", err
					}
					return "Removed the line item.", nil
				default:
					return "", errors.New("action must be one of: list, add, update, remove")
				}
			},
		},
	}
}

// loadOverview fetches every row costs.BuildOverview needs for the token's team, matching
// internal/api/costs.go's loadCostsOverview so REST and MCP compute the same numbers.
func loadOverview(ctx context.Context, env *Env, window string) (costs.CostsOverview, error) {
	servers, err := env.Q.ListServersOfTeam(ctx, env.Caller.TeamID)
	if err != nil {
		return costs.CostsOverview{}, errors.New("could not list servers")
	}
	priceRows, err := env.Q.TeamServerCosts(ctx, env.Caller.TeamID)
	if err != nil {
		return costs.CostsOverview{}, errors.New("could not read server prices")
	}
	since := costs.WindowSince(time.Now(), window)
	usageRows, err := env.Q.TeamServerUsageSummary(ctx, db.TeamServerUsageSummaryParams{TeamID: env.Caller.TeamID, Since: since})
	if err != nil {
		return costs.CostsOverview{}, errors.New("could not read usage")
	}
	appRows, err := env.Q.ListAppsOfTeam(ctx, env.Caller.TeamID)
	if err != nil {
		return costs.CostsOverview{}, errors.New("could not list apps")
	}
	lineItemRows, err := env.Q.TeamCostLineItems(ctx, env.Caller.TeamID)
	if err != nil {
		return costs.CostsOverview{}, errors.New("could not list line items")
	}
	return costs.BuildOverview(window, servers, priceRows, usageRows, appRows, lineItemRows), nil
}

func renderOverview(ov costs.CostsOverview) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Cost overview (%s window):\n", ov.Window)
	if len(ov.Totals) == 0 {
		b.WriteString("  no priced servers or line items yet\n")
	}
	for _, t := range ov.Totals {
		fmt.Fprintf(&b, "  total     %10.2f %s\n", t.Amount, t.Currency)
	}
	for _, t := range ov.AllocatedTotals {
		fmt.Fprintf(&b, "  allocated %10.2f %s\n", t.Amount, t.Currency)
	}
	for _, t := range ov.IdleTotals {
		fmt.Fprintf(&b, "  idle      %10.2f %s\n", t.Amount, t.Currency)
	}
	fmt.Fprintf(&b, "  over_provisioned=%d under_pressure=%d\n", ov.OverProvisionedCount, ov.UnderPressureCount)

	if len(ov.Servers) > 0 {
		b.WriteString("\nServers:\n")
		for _, s := range ov.Servers {
			price := "unpriced"
			if s.Priced {
				price = fmt.Sprintf("%.2f %s/mo", s.Amount, s.Currency)
			}
			hint := s.Hint
			if hint == "" {
				hint = "-"
			}
			fmt.Fprintf(&b, "  %-24s %-16s hint=%-18s", s.ServerName, price, hint)
			if s.Usage != nil {
				fmt.Fprintf(&b, "cpu_avg=%.1f%% mem=%.1f%%", s.Usage.AvgCPUPercent, s.Usage.MemPercent)
			} else {
				b.WriteString("no usage data")
			}
			b.WriteString("\n")
			for _, a := range s.Apps {
				ahint := ""
				if a.Hint != "" {
					ahint = "  hint=" + a.Hint
				}
				fmt.Fprintf(&b, "      app %-20s %.2f %s%s\n", a.AppName, a.Amount, a.Currency, ahint)
			}
			if s.IdleAmount > 0 {
				fmt.Fprintf(&b, "      idle %.2f %s\n", s.IdleAmount, s.Currency)
			}
		}
	}

	if len(ov.LineItems) > 0 {
		b.WriteString("\nLine items:\n")
		for _, li := range ov.LineItems {
			fmt.Fprintf(&b, "  %-24s %8.2f %s\n", li.Name, li.Amount, li.Currency)
		}
	}
	return b.String()
}
