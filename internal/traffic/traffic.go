// Package traffic is an app's traffic history: per-minute buckets the agent aggregates from
// Caddy's JSON access log and ships to the control plane, folded here the same way
// internal/metrics folds server heartbeats — minute rows for 24 h, 15-minute rows for 30 days.
//
// The agent does the heavy lifting (parsing log lines, hashing visitors, ranking paths and
// referrers) so nothing here ever sees a raw IP address or user agent string, only counts.
package traffic

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

const (
	// MinuteRetention is how long minute rows are kept before they are folded into 15 minutes.
	MinuteRetention = 24 * time.Hour
	// Retention is how long anything is kept.
	Retention = 30 * 24 * time.Hour
	// TopN bounds the top-paths/top-referrers lists kept per bucket.
	TopN = 10
)

// Range is one chart window and the bucket it is drawn with.
type Range struct {
	Key  string
	Span time.Duration
	Step time.Duration
}

// Ranges are the windows the API accepts.
var Ranges = map[string]Range{
	"1h":  {"1h", time.Hour, time.Minute},
	"24h": {"24h", 24 * time.Hour, 15 * time.Minute},
	"7d":  {"7d", 7 * 24 * time.Hour, time.Hour},
	"30d": {"30d", 30 * 24 * time.Hour, 3 * time.Hour},
}

// RangeKeys is the enum, in order.
var RangeKeys = []string{"1h", "24h", "7d", "30d"}

func window(now time.Time, span, step time.Duration) time.Time {
	s := int64(step / time.Second)
	end := now.Unix() / s * s
	return time.Unix(end-int64(span/time.Second)+s, 0).UTC()
}

// ---- recording ------------------------------------------------------------------------------

// Record writes one app's already-aggregated minute (agent scope). A bucket with no requests is
// still written (it says "the app served nothing this minute", useful for the empty state vs. no
// data at all) unless it is entirely empty (no requests and never flushed), which the agent does
// not send.
func Record(ctx context.Context, q *db.Queries, appID, serverID, teamID string, m agentproto.TrafficMinute) error {
	paths, _ := json.Marshal(topOrEmpty(m.TopPaths))
	refs, _ := json.Marshal(topOrEmpty(m.TopReferrers))
	return q.RecordAppTrafficMinute(ctx, db.RecordAppTrafficMinuteParams{
		AppID: appID, ServerID: serverID, TeamID: teamID, At: m.At.UTC().Truncate(time.Minute),
		Requests: m.Requests, Status2xx: m.Status2xx, Status3xx: m.Status3xx, Status4xx: m.Status4xx, Status5xx: m.Status5xx,
		BytesOut: m.BytesOut, DurationP50Ms: m.DurationP50Ms, DurationP95Ms: m.DurationP95Ms, UniqueVisitors: m.UniqueVisitors,
		TopPaths: string(paths), TopReferrers: string(refs),
	})
}

func topOrEmpty(kc []agentproto.KeyCount) []agentproto.KeyCount {
	if kc == nil {
		return []agentproto.KeyCount{}
	}
	return kc
}

// ---- retention --------------------------------------------------------------------------------

// Compact folds minute rows older than MinuteRetention into 15-minute rows (merging and
// re-ranking top paths/referrers in Go) and drops rows older than Retention. Idempotent and safe
// alongside agent writes.
func Compact(ctx context.Context, sqlDB *sql.DB, now time.Time) (compacted, pruned int64, err error) {
	err = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		rows, e := q.CompactAppTrafficMinutes(ctx, now.Add(-MinuteRetention))
		if e != nil {
			return fmt.Errorf("compact: %w", e)
		}
		type key struct{ app, bucket string }
		type acc struct {
			appID, serverID, teamID         string
			bucket                          time.Time
			requests, s2, s3, s4, s5, bytes int64
			uniqueVisitors                  int64
			p50w, p95w                      float64 // request-weighted sum, divided by requests at the end
			paths, refs                     map[string]int64
		}
		byKey := map[key]*acc{}
		for _, r := range rows {
			b := r.At.Truncate(15 * time.Minute)
			k := key{r.AppID, b.Format(time.RFC3339)}
			a := byKey[k]
			if a == nil {
				a = &acc{appID: r.AppID, serverID: r.ServerID, teamID: r.TeamID, bucket: b, paths: map[string]int64{}, refs: map[string]int64{}}
				byKey[k] = a
			}
			a.requests += r.Requests
			a.s2 += r.Status2xx
			a.s3 += r.Status3xx
			a.s4 += r.Status4xx
			a.s5 += r.Status5xx
			a.bytes += r.BytesOut
			a.uniqueVisitors += r.UniqueVisitors
			a.p50w += r.DurationP50Ms * float64(r.Requests)
			a.p95w += r.DurationP95Ms * float64(r.Requests)
			mergeCounts(a.paths, r.TopPaths)
			mergeCounts(a.refs, r.TopReferrers)
			a.serverID = r.ServerID // the last minute's server wins if the app moved servers mid-bucket
		}
		compacted = int64(len(rows))
		for _, a := range byKey {
			p50, p95 := 0.0, 0.0
			if a.requests > 0 {
				p50, p95 = a.p50w/float64(a.requests), a.p95w/float64(a.requests)
			}
			paths, _ := json.Marshal(rankTop(a.paths))
			refs, _ := json.Marshal(rankTop(a.refs))
			if e := q.UpsertAppTrafficBucket(ctx, db.UpsertAppTrafficBucketParams{
				AppID: a.appID, ServerID: a.serverID, TeamID: a.teamID, At: a.bucket, Step: 900,
				Requests: a.requests, Status2xx: a.s2, Status3xx: a.s3, Status4xx: a.s4, Status5xx: a.s5,
				BytesOut: a.bytes, DurationP50Ms: round1(p50), DurationP95Ms: round1(p95), UniqueVisitors: a.uniqueVisitors,
				TopPaths: string(paths), TopReferrers: string(refs),
			}); e != nil {
				return fmt.Errorf("upsert bucket: %w", e)
			}
		}
		if pruned, e = q.PruneAppTrafficMinutes(ctx, now.Add(-Retention)); e != nil {
			return fmt.Errorf("prune: %w", e)
		}
		return nil
	})
	return compacted, pruned, err
}

func mergeCounts(into map[string]int64, topJSON string) {
	var kc []agentproto.KeyCount
	if topJSON == "" || json.Unmarshal([]byte(topJSON), &kc) != nil {
		return
	}
	for _, e := range kc {
		into[e.Key] += e.Count
	}
}

func rankTop(counts map[string]int64) []agentproto.KeyCount {
	out := make([]agentproto.KeyCount, 0, len(counts))
	for k, c := range counts {
		out = append(out, agentproto.KeyCount{Key: k, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > TopN {
		out = out[:TopN]
	}
	return out
}

func round1(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10) / 10
}

// ---- queries ------------------------------------------------------------------------------------

// AnalyticsPoint is one bucket of the analytics chart.
type AnalyticsPoint struct {
	At        time.Time `json:"at" doc:"Start of the bucket."`
	Requests  int64     `json:"requests"`
	Status2xx int64     `json:"status_2xx"`
	Status3xx int64     `json:"status_3xx"`
	Status4xx int64     `json:"status_4xx"`
	Status5xx int64     `json:"status_5xx"`
	BytesOut  int64     `json:"bytes_out"`
	P50Ms     float64   `json:"p50_ms"`
	P95Ms     float64   `json:"p95_ms"`
	Visitors  int64     `json:"visitors" doc:"Approximate unique visitors (summed from the agent's per-minute counts; not deduplicated across buckets)."`
}

// AnalyticsSeries is GET …/apps/{app}/analytics.
type AnalyticsSeries struct {
	Range        string                `json:"range" enum:"1h,24h,7d,30d"`
	StepSeconds  int                   `json:"step_seconds"`
	Points       []AnalyticsPoint      `json:"points"`
	Totals       Totals                `json:"totals"`
	TopPaths     []agentproto.KeyCount `json:"top_paths"`
	TopReferrers []agentproto.KeyCount `json:"top_referrers"`
}

// Totals summarise the whole range: the headline tiles.
type Totals struct {
	Requests  int64   `json:"requests"`
	Visitors  int64   `json:"visitors"`
	ErrorRate float64 `json:"error_rate" doc:"Share of requests with a 4xx or 5xx status, 0-1."`
	P95Ms     float64 `json:"p95_ms" doc:"Request-weighted average of the buckets' p95."`
}

type sample struct {
	at                            time.Time
	requests, s2, s3, s4, s5, byt int64
	visitors                      int64
	p50, p95                      float64
	paths, refs                   map[string]int64
}

// AppSeries reads one app's chart for a range (team scope; the caller resolved the app).
func AppSeries(ctx context.Context, q *db.Queries, teamID, appID string, r Range, now time.Time) (AnalyticsSeries, error) {
	since := window(now, r.Span, r.Step)
	rows, err := q.AppTrafficSince(ctx, db.AppTrafficSinceParams{AppID: appID, TeamID: teamID, Since: since})
	if err != nil {
		return AnalyticsSeries{}, err
	}
	s := int64(r.Step / time.Second)
	byKey := map[int64]*sample{}
	allPaths, allRefs := map[string]int64{}, map[string]int64{}
	var out AnalyticsSeries
	out.Range, out.StepSeconds = r.Key, int(r.Step/time.Second)
	for _, row := range rows {
		if row.At.Before(since) {
			continue
		}
		k := row.At.Unix() / s * s
		a := byKey[k]
		if a == nil {
			a = &sample{at: time.Unix(k, 0).UTC(), paths: map[string]int64{}, refs: map[string]int64{}}
			byKey[k] = a
		}
		a.requests += row.Requests
		a.s2 += row.Status2xx
		a.s3 += row.Status3xx
		a.s4 += row.Status4xx
		a.s5 += row.Status5xx
		a.byt += row.BytesOut
		a.visitors += row.UniqueVisitors
		a.p50 += row.DurationP50Ms * float64(row.Requests)
		a.p95 += row.DurationP95Ms * float64(row.Requests)
		mergeCounts(a.paths, row.TopPaths)
		mergeCounts(a.refs, row.TopReferrers)
		mergeCounts(allPaths, row.TopPaths)
		mergeCounts(allRefs, row.TopReferrers)
		out.Totals.Requests += row.Requests
		out.Totals.Visitors += row.UniqueVisitors
		out.Totals.P95Ms += row.DurationP95Ms * float64(row.Requests)
	}
	keys := make([]int64, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out.Points = make([]AnalyticsPoint, 0, len(keys))
	for _, k := range keys {
		a := byKey[k]
		p50, p95 := 0.0, 0.0
		if a.requests > 0 {
			p50, p95 = a.p50/float64(a.requests), a.p95/float64(a.requests)
		}
		out.Points = append(out.Points, AnalyticsPoint{At: a.at, Requests: a.requests, Status2xx: a.s2, Status3xx: a.s3, Status4xx: a.s4, Status5xx: a.s5,
			BytesOut: a.byt, P50Ms: round1(p50), P95Ms: round1(p95), Visitors: a.visitors})
	}
	if out.Totals.Requests > 0 {
		out.Totals.ErrorRate = round2(float64(sumErrors(rows)) / float64(out.Totals.Requests))
		out.Totals.P95Ms = round1(out.Totals.P95Ms / float64(out.Totals.Requests))
	}
	out.TopPaths = rankTop(allPaths)
	out.TopReferrers = rankTop(allRefs)
	if out.TopPaths == nil {
		out.TopPaths = []agentproto.KeyCount{}
	}
	if out.TopReferrers == nil {
		out.TopReferrers = []agentproto.KeyCount{}
	}
	return out, nil
}

func sumErrors(rows []db.AppTrafficSinceRow) int64 {
	var n int64
	for _, r := range rows {
		n += r.Status4xx + r.Status5xx
	}
	return n
}

func round2(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}
