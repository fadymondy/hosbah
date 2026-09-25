// Package costs is the arithmetic and shared row-formatting behind MH-575 H10 (cost and usage):
// splitting a server's monthly price across the apps that run on it, and the simple right-sizing
// rules that flag a server or an app as worth a second look. This file holds the pure arithmetic
// (no database access); overview.go formats rows internal/api/costs.go and internal/mcp/costs.go
// each fetch under tenancy.Tx into one shared Overview, the same split internal/rum uses between
// its service package and each caller's own queries.
package costs

import "sort"

// ServerHint is a right-sizing flag for one server over a window.
type ServerHint string

const (
	ServerHintNone            ServerHint = ""
	ServerHintOverProvisioned ServerHint = "over_provisioned" // p95 CPU < 10% and avg memory < 30%
	ServerHintUnderPressure   ServerHint = "under_pressure"   // p95 CPU > 85% or avg memory > 85%
)

// Thresholds the UI explains next to the badge (MH-575 H10 spec): simple, not tunable per team.
const (
	OverProvisionedCPUPercent = 10.0
	OverProvisionedMemPercent = 30.0
	UnderPressureCPUPercent   = 85.0
	UnderPressureMemPercent   = 85.0
)

// Usage is one server's usage summary over a window (7d or 30d), already averaged/percentiled by
// the SQL query (internal/db/queries/server_metric.sql: TeamServerUsageSummary/ServerUsageSummary).
type Usage struct {
	Readings      int64   // 0 means no metrics were recorded in the window at all
	AvgCPUPercent float64
	P95CPUPercent float64
	AvgMemUsed    float64 // bytes
	P95MemUsed    float64 // bytes
	MemTotal      int64   // bytes, 0 if never reported
	DiskUsed      int64   // bytes, latest reading
	DiskTotal     int64   // bytes, latest reading
}

// MemPercent is average memory used as a percentage of total, or 0 if total is unknown.
func (u Usage) MemPercent() float64 {
	if u.MemTotal <= 0 {
		return 0
	}
	return u.AvgMemUsed / float64(u.MemTotal) * 100
}

// DiskPercent is the latest disk-used percentage, or 0 if total is unknown.
func (u Usage) DiskPercent() float64 {
	if u.DiskTotal <= 0 {
		return 0
	}
	return float64(u.DiskUsed) / float64(u.DiskTotal) * 100
}

// Hint applies the right-sizing rule. Hints only fire with at least one reading in the window and a
// known memory total — a server that never reported metrics gets no opinion either way.
func (u Usage) Hint() ServerHint {
	if u.Readings == 0 || u.MemTotal <= 0 {
		return ServerHintNone
	}
	mem := u.MemPercent()
	switch {
	case u.P95CPUPercent > UnderPressureCPUPercent || mem > UnderPressureMemPercent:
		return ServerHintUnderPressure
	case u.P95CPUPercent < OverProvisionedCPUPercent && mem < OverProvisionedMemPercent:
		return ServerHintOverProvisioned
	default:
		return ServerHintNone
	}
}

// AppHint is a right-sizing flag for one app.
type AppHint string

const (
	AppHintNone     AppHint = ""
	AppHintOverCap  AppHint = "limit_far_exceeds_usage" // configured memory limit dwarfs what the server actually uses
	OverCapMultiple         = 4.0                       // limit > 4x the server's own average memory use
)

// AppMemoryHint flags an app whose configured memory limit is out of proportion with what its
// server is actually using. Per-app usage history is not collected (H10 scope: no new heavy
// collector), so this compares against the server's own average memory use — a coarse but
// explainable proxy, called out as such in the UI. limitMB 0 means "no limit set": no opinion.
func AppMemoryHint(limitMB int, serverAvgMemUsedBytes float64) AppHint {
	if limitMB <= 0 || serverAvgMemUsedBytes <= 0 {
		return AppHintNone
	}
	limitBytes := float64(limitMB) * 1024 * 1024
	if limitBytes > serverAvgMemUsedBytes*OverCapMultiple {
		return AppHintOverCap
	}
	return AppHintNone
}

// AppShare is one app (or data service) sharing a server's cost.
type AppShare struct {
	ID            string
	MemoryLimitMB int // 0: no configured limit
}

// Allocation is one app's slice of its server's monthly cost.
type Allocation struct {
	ID    string
	Cents int64
}

// Allocate splits a server's monthly cost (cents) across its apps, proportional to each app's
// configured memory limit as a share of the server's total memory (MH-575 H10: "cost allocation
// ... proportionally to memory share, fallback equal split"). serverMemTotalMB is the server's
// reported total memory in MB (0 if unknown). With no app limits set at all, the cost is split
// evenly and there is no idle remainder (no better signal to say any of the cost is unallocated).
// With limits set, whatever share of memory is not claimed by any app's limit — including an
// unknown total, which falls back to the sum of the limits themselves — becomes idleCents: the
// unallocated remainder the UI shows as "idle/overhead".
func Allocate(serverCents int64, apps []AppShare, serverMemTotalMB int) (allocations []Allocation, idleCents int64) {
	if serverCents <= 0 || len(apps) == 0 {
		return nil, serverCents
	}
	sumLimits := 0
	for _, a := range apps {
		if a.MemoryLimitMB > 0 {
			sumLimits += a.MemoryLimitMB
		}
	}
	out := make([]Allocation, len(apps))
	if sumLimits == 0 {
		// Equal split, remainder to the first (deterministic, sorted) app so cents always add up.
		share := serverCents / int64(len(apps))
		remainder := serverCents - share*int64(len(apps))
		for i, a := range apps {
			c := share
			if i == 0 {
				c += remainder
			}
			out[i] = Allocation{ID: a.ID, Cents: c}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return out, 0
	}
	// The denominator is at least sumLimits: an unknown total, or one overcommitted below the sum
	// of configured limits, allocates the whole cost with nothing left over for idle.
	denom := serverMemTotalMB
	if denom < sumLimits {
		denom = sumLimits
	}
	var allocated int64
	for i, a := range apps {
		c := int64(0)
		if a.MemoryLimitMB > 0 {
			c = serverCents * int64(a.MemoryLimitMB) / int64(denom)
		}
		out[i] = Allocation{ID: a.ID, Cents: c}
		allocated += c
	}
	idleCents = serverCents - allocated
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, idleCents
}
