package costs

// Overview assembly (MH-575 H10): both the team REST API (internal/api/costs.go) and the MCP tools
// (internal/mcp/costs.go) fetch the same rows under tenancy.Tx and hand them to BuildOverview, so the
// totals, allocation and hints are computed in exactly one place — the same split internal/rum uses
// between its service package (shared formatting) and each caller (its own queries).

import (
	"sort"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// Window is one of the two windows the cost/usage endpoints understand. 30d is also what the
// right-sizing hints are judged over, per the H10 spec ("p95 CPU < 10% and memory < 30% over 30d").
func WindowSince(now time.Time, window string) time.Time {
	if window == "7d" {
		return now.Add(-7 * 24 * time.Hour)
	}
	return now.Add(-30 * 24 * time.Hour)
}

func NormalizeWindow(window string) string {
	if window == "7d" {
		return "7d"
	}
	return "30d"
}

// Money is a monthly amount, in the currency's ordinary decimal units (e.g. 12.50, not cents).
type Money struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

func centsToMoney(cents int64, currency string) Money {
	return Money{Amount: float64(cents) / 100, Currency: currency}
}

// DollarsToCents rounds to the nearest cent rather than truncating, so 9.995 does not become 9.99.
func DollarsToCents(amount float64) int64 {
	if amount < 0 {
		amount = 0
	}
	return int64(amount*100 + 0.5)
}

// UsageOut is one server's usage summary over the window, rounded for display.
type UsageOut struct {
	Readings      int64   `json:"readings" doc:"Metric rows in the window; 0 means no data to judge usage from."`
	AvgCPUPercent float64 `json:"avg_cpu_percent"`
	P95CPUPercent float64 `json:"p95_cpu_percent"`
	MemPercent    float64 `json:"mem_percent" doc:"Average memory used, as a percentage of total."`
	DiskPercent   float64 `json:"disk_percent" doc:"Latest disk-used percentage."`
}

func toUsageOut(u Usage) UsageOut {
	return UsageOut{Readings: u.Readings, AvgCPUPercent: round1(u.AvgCPUPercent), P95CPUPercent: round1(u.P95CPUPercent),
		MemPercent: round1(u.MemPercent()), DiskPercent: round1(u.DiskPercent())}
}

// ToUsageOut is toUsageOut, exported for the single-server usage endpoint (REST and MCP), which does
// not go through BuildOverview.
func ToUsageOut(u Usage) UsageOut { return toUsageOut(u) }

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// AppCostOut is one app's slice of its server's monthly cost.
type AppCostOut struct {
	AppID         string  `json:"app_id"`
	AppName       string  `json:"app_name"`
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency,omitempty"`
	MemoryLimitMB int     `json:"memory_limit_mb,omitempty" doc:"The app's configured memory limit, the basis for its cost share. 0: none set."`
	Hint          string  `json:"hint,omitempty" enum:",limit_far_exceeds_usage" doc:"limit_far_exceeds_usage: the app's memory limit is far above what its server actually uses on average — a coarse proxy, since per-app usage history is not collected."`
}

// ServerCostOut is one server: its price (if set), its usage over the window, a right-sizing hint,
// and how its cost splits across its apps.
type ServerCostOut struct {
	ServerID   string       `json:"server_id"`
	ServerName string       `json:"server_name"`
	Priced     bool         `json:"priced"`
	Amount     float64      `json:"amount,omitempty"`
	Currency   string       `json:"currency,omitempty"`
	Usage      *UsageOut    `json:"usage,omitempty"`
	Hint       string       `json:"hint,omitempty" enum:",over_provisioned,under_pressure" doc:"over_provisioned: p95 CPU < 10% and memory < 30% over the window. under_pressure: p95 CPU > 85% or memory > 85%."`
	Apps       []AppCostOut `json:"apps"`
	IdleAmount float64      `json:"idle_amount,omitempty" doc:"The part of this server's price not allocated to any app (unused capacity, or the whole price with no apps)."`
}

// LineItemOut is one extra monthly cost outside per-server pricing.
type LineItemOut struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Amount    float64   `json:"amount"`
	Currency  string    `json:"currency"`
	CreatedAt time.Time `json:"created_at"`
}

func ToLineItemOut(r db.CostLineItem) LineItemOut {
	return LineItemOut{ID: r.ID, Name: r.Name, Amount: float64(r.AmountCents) / 100, Currency: r.Currency, CreatedAt: r.CreatedAt}
}

// CurrencyAmount is a total in one currency; mixed currencies are never converted or combined.
type CurrencyAmount struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

func addCents(totals map[string]int64, currency string, cents int64) {
	if currency == "" {
		currency = "USD"
	}
	totals[currency] += cents
}

func sortedTotals(totals map[string]int64) []CurrencyAmount {
	out := make([]CurrencyAmount, 0, len(totals))
	for cur, cents := range totals {
		out = append(out, CurrencyAmount{Currency: cur, Amount: float64(cents) / 100})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Currency < out[j].Currency })
	return out
}

// CostsOverview is GET .../costs/overview's payload, and what cost_overview (MCP) formats as text.
type CostsOverview struct {
	Window               string           `json:"window"`
	Totals               []CurrencyAmount `json:"totals" doc:"Monthly total (server prices + line items), per currency."`
	AllocatedTotals      []CurrencyAmount `json:"allocated_totals" doc:"Sum of every app's allocated share, per currency."`
	IdleTotals           []CurrencyAmount `json:"idle_totals" doc:"Sum of unallocated server cost (idle/overhead), per currency."`
	OverProvisionedCount int              `json:"over_provisioned_count"`
	UnderPressureCount   int              `json:"under_pressure_count"`
	Servers              []ServerCostOut  `json:"servers"`
	LineItems            []LineItemOut    `json:"line_items"`
}

func usageFromRow(readings int64, avgCPU, p95CPU, avgMem, p95Mem float64, memTotal, diskUsed, diskTotal int64) Usage {
	return Usage{Readings: readings, AvgCPUPercent: avgCPU, P95CPUPercent: p95CPU, AvgMemUsed: avgMem, P95MemUsed: p95Mem,
		MemTotal: memTotal, DiskUsed: diskUsed, DiskTotal: diskTotal}
}

// UsageFromServerRow adapts db.ServerUsageSummaryRow (one server) without this package depending on
// sqlc's generated field order — callers pass fields, not the generated row type, to keep this
// package's only cross-package dependency (db/gen) confined to the row/model types below.
func UsageFromServerRow(row db.ServerUsageSummaryRow) Usage {
	return usageFromRow(row.Readings, row.AvgCpuPercent, row.P95CpuPercent, row.AvgMemUsed, row.P95MemUsed,
		row.MemTotal, row.LatestDiskUsed, row.LatestDiskTotal)
}

func usageFromTeamRow(row db.TeamServerUsageSummaryRow) Usage {
	return usageFromRow(row.Readings, row.AvgCpuPercent, row.P95CpuPercent, row.AvgMemUsed, row.P95MemUsed,
		row.MemTotal, row.LatestDiskUsed, row.LatestDiskTotal)
}

// shareApp pairs an app row with the memory limit Allocate needs.
type shareApp struct {
	ID            string
	Name          string
	MemoryLimitMB int
}

func appOf(a db.ListAppsOfTeamRow) shareApp {
	mb := 0
	if a.ResourcesMemoryMb != nil {
		mb = int(*a.ResourcesMemoryMb)
	}
	return shareApp{ID: a.ID, Name: a.Name, MemoryLimitMB: mb}
}

// BuildOverview assembles the whole cost/usage overview from rows already fetched under tenancy.Tx —
// the one place REST and MCP share (internal/api/costs.go and internal/mcp/costs.go each issue their
// own queries, matching internal/rum's split between per-caller queries and shared formatting).
func BuildOverview(window string, servers []db.Server, priceRows []db.ServerCost, usageRows []db.TeamServerUsageSummaryRow,
	appRows []db.ListAppsOfTeamRow, lineItemRows []db.CostLineItem) CostsOverview {
	window = NormalizeWindow(window)

	prices := make(map[string]db.ServerCost, len(priceRows))
	for _, r := range priceRows {
		prices[r.ServerID] = r
	}
	usageByServer := make(map[string]Usage, len(usageRows))
	for _, r := range usageRows {
		usageByServer[r.ServerID] = usageFromTeamRow(r)
	}
	appsByServer := make(map[string][]shareApp, len(appRows))
	for _, a := range appRows {
		appsByServer[a.ServerID] = append(appsByServer[a.ServerID], appOf(a))
	}

	out := CostsOverview{Window: window, Servers: []ServerCostOut{}, LineItems: []LineItemOut{}}
	totals := map[string]int64{}
	allocated := map[string]int64{}
	idleTotals := map[string]int64{}

	for _, s := range servers {
		usage := usageByServer[s.ID]
		hint := usage.Hint()
		switch hint {
		case ServerHintOverProvisioned:
			out.OverProvisionedCount++
		case ServerHintUnderPressure:
			out.UnderPressureCount++
		}

		so := ServerCostOut{ServerID: s.ID, ServerName: s.Name, Apps: []AppCostOut{}}
		if usage.Readings > 0 {
			u := toUsageOut(usage)
			so.Usage = &u
			so.Hint = string(hint)
		}

		apps := appsByServer[s.ID]
		if price, ok := prices[s.ID]; ok {
			so.Priced = true
			so.Amount = float64(price.AmountCents) / 100
			so.Currency = price.Currency
			addCents(totals, price.Currency, price.AmountCents)

			shares := make([]AppShare, len(apps))
			for i, a := range apps {
				shares[i] = AppShare{ID: a.ID, MemoryLimitMB: a.MemoryLimitMB}
			}
			memTotalMB := int(usage.MemTotal / (1024 * 1024))
			allocs, idleCents := Allocate(price.AmountCents, shares, memTotalMB)
			byID := map[string]int64{}
			for _, al := range allocs {
				byID[al.ID] = al.Cents
			}
			for _, a := range apps {
				cents := byID[a.ID]
				addCents(allocated, price.Currency, cents)
				so.Apps = append(so.Apps, AppCostOut{
					AppID: a.ID, AppName: a.Name, Amount: float64(cents) / 100, Currency: price.Currency,
					MemoryLimitMB: a.MemoryLimitMB, Hint: string(AppMemoryHint(a.MemoryLimitMB, usage.AvgMemUsed)),
				})
			}
			addCents(idleTotals, price.Currency, idleCents)
			so.IdleAmount = float64(idleCents) / 100
		} else {
			// Not priced: still show the apps (0 cost each) so the UI lists what runs here.
			for _, a := range apps {
				so.Apps = append(so.Apps, AppCostOut{AppID: a.ID, AppName: a.Name, MemoryLimitMB: a.MemoryLimitMB,
					Hint: string(AppMemoryHint(a.MemoryLimitMB, usage.AvgMemUsed))})
			}
		}
		out.Servers = append(out.Servers, so)
	}
	sort.Slice(out.Servers, func(i, j int) bool { return out.Servers[i].ServerName < out.Servers[j].ServerName })

	for _, li := range lineItemRows {
		out.LineItems = append(out.LineItems, ToLineItemOut(li))
		addCents(totals, li.Currency, li.AmountCents)
	}

	out.Totals = sortedTotals(totals)
	out.AllocatedTotals = sortedTotals(allocated)
	out.IdleTotals = sortedTotals(idleTotals)
	return out
}
