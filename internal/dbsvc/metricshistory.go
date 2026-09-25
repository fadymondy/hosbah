package dbsvc

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/metrics"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// DB metrics history (MH-575 F6): every running Postgres/MySQL service's KPIs, sampled roughly once a
// minutes the same way F6's on-demand Insights panel reads them — db.insights over the existing agent
// op queue, nothing new opened. One row per service per sample; retention mirrors internal/metrics
// (server_metrics): raw rows for 24h, then folded into 15-minute rows for 30 days.

const (
	// MetricsSampleEvery is how often the sampler wakes.
	MetricsSampleEvery = 5 * time.Minute
	// MetricsRetentionEvery mirrors internal/ops.MetricsRetentionEvery so both history tables are
	// compacted/pruned on the same cadence.
	MetricsRetentionEvery = 5 * time.Minute
	// MetricsOperationRetention is how long a finished sampling operation (db.insights) is kept.
	MetricsOperationRetention = 24 * time.Hour
	// MetricsMinuteRetention is how long minute rows are kept before folding into 15-minute rows.
	MetricsMinuteRetention = 24 * time.Hour
	// MetricsRetention is how long anything is kept.
	MetricsRetention = 30 * 24 * time.Hour
)

// StartMetricsSampler launches the periodic sampling and retention loops. Called once from Boot
// alongside StartScheduler; both share the same StopScheduler cancellation.
func (s *Service) StartMetricsSampler() {
	ctx, cancel := context.WithCancel(context.Background())
	s.metricsCancel = cancel
	go s.metricsSampleLoop(ctx)
	go s.metricsRetentionLoop(ctx)
}

// StopMetricsSampler ends both loops.
func (s *Service) StopMetricsSampler() {
	if s.metricsCancel != nil {
		s.metricsCancel()
	}
}

func (s *Service) metricsSampleLoop(ctx context.Context) {
	t := time.NewTicker(MetricsSampleEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sampleAll(ctx)
		}
	}
}

func (s *Service) metricsRetentionLoop(ctx context.Context) {
	t := time.NewTicker(MetricsRetentionEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _, _ = CompactMetricsHistory(ctx, s.a.SQLDB, time.Now().UTC())
		}
	}
}

// sampleAll takes one reading of every running Postgres/MySQL service. Each service's Insights call
// is independent and a slow/failed one (agent offline, timeout) never blocks the others or the next
// tick — it is just skipped this minute.
func (s *Service) sampleAll(ctx context.Context) {
	var services []db.DataService
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		services, err = q.ServicesForMetricsSampling(ctx)
		return err
	})
	if err != nil || len(services) == 0 {
		return
	}
	teams := map[string]bool{}
	for _, svc := range services {
		s.sampleOne(ctx, svc)
		teams[svc.TeamID] = true
	}
	// Each sample is a db.insights operation; keep the queue table from growing by a row per
	// service every tick by dropping finished ones after a day.
	cutoff := time.Now().Add(-MetricsOperationRetention)
	for teamID := range teams {
		_ = s.sys(ctx, teamID, func(q *db.Queries) error {
			_, err := q.PruneFinishedOperationsOfKind(ctx, db.PruneFinishedOperationsOfKindParams{TeamID: teamID, Kind: agentproto.KindDBInsights, FinishedAt: &cutoff})
			return err
		})
	}
}

func (s *Service) sampleOne(ctx context.Context, svc db.DataService) {
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var result agentproto.DBInsightsResult
	err := s.sys(sctx, svc.TeamID, func(q *db.Queries) error {
		r, err := s.Insights(sctx, q, svc.TeamID, svc)
		result = r
		return err
	})
	if err != nil {
		s.log.Debug("db metrics sample skipped", "service_id", svc.ID, "error", err)
		return
	}

	var sizeBytes int64
	for _, d := range result.Databases {
		sizeBytes += d.SizeBytes
	}

	var txCounter int64
	switch svc.Kind {
	case "postgres":
		txCounter = result.XactCommit + result.XactRollback
	case "mysql":
		txCounter = result.Questions
	}

	at := time.Now().UTC().Truncate(time.Minute)
	var ratePerSec float64
	_ = s.sys(ctx, svc.TeamID, func(q *db.Queries) error {
		last, err := q.LastDataServiceMetric(ctx, svc.ID)
		if err == nil {
			elapsed := at.Sub(last.At).Seconds()
			if elapsed > 0 && txCounter >= last.TxCounter {
				ratePerSec = math.Round(float64(txCounter-last.TxCounter)/elapsed*100) / 100
			}
		}
		return q.RecordDataServiceMetric(ctx, db.RecordDataServiceMetricParams{
			ServiceID: svc.ID, TeamID: svc.TeamID, At: at,
			Connections: int64(result.Connections), MaxConnections: int64(result.MaxConnections),
			CacheHitRatio: result.CacheHitRatio, SizeBytes: sizeBytes,
			SlowQueryCount: int64(len(result.LongRunning)), TxCounter: txCounter, RatePerSec: ratePerSec,
		})
	})
}

// CompactMetricsHistory folds minute rows older than MetricsMinuteRetention into 15-minute rows and
// drops rows older than MetricsRetention. Idempotent and safe to run concurrently with sampling.
func CompactMetricsHistory(ctx context.Context, sqlDB *sql.DB, now time.Time) (compacted, pruned int64, err error) {
	err = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var e error
		if compacted, e = q.CompactDataServiceMetrics(ctx, now.Add(-MetricsMinuteRetention)); e != nil {
			return e
		}
		if pruned, e = q.PruneDataServiceMetrics(ctx, now.Add(-MetricsRetention)); e != nil {
			return e
		}
		return nil
	})
	return compacted, pruned, err
}

// ---- chart bucketing -----------------------------------------------------------------------------

// MetricPoint is one bucket of a data service's chart.
type MetricPoint struct {
	At             time.Time `json:"at" doc:"Start of the bucket."`
	Connections    float64   `json:"connections"`
	MaxConnections int64     `json:"max_connections"`
	CacheHitRatio  float64   `json:"cache_hit_ratio" doc:"Percent."`
	SizeBytes      int64     `json:"size_bytes"`
	SlowQueryCount float64   `json:"slow_query_count"`
	RatePerSec     float64   `json:"rate_per_sec" doc:"Transactions/queries per second, averaged over the bucket."`
}

// MetricSeries is GET …/data-services/{service}/metrics.
type MetricSeries struct {
	Range       string        `json:"range" enum:"1h,6h,24h,7d,30d"`
	StepSeconds int           `json:"step_seconds"`
	Points      []MetricPoint `json:"points"`
}

type metricAcc struct {
	at                                    time.Time
	n                                     float64
	connections, cacheHit, slow, rate     float64
	maxConnections, sizeBytes             int64
}

// bucketMetrics groups rows into buckets of step starting at since. Gauges are averaged; empty
// buckets are left out (a gap in the chart, not a zero), matching internal/metrics.Bucket.
func bucketMetrics(rows []db.DataServiceMetricsSinceRow, since time.Time, step time.Duration) []MetricPoint {
	s := int64(step / time.Second)
	byKey := map[int64]*metricAcc{}
	for _, r := range rows {
		if r.At.Before(since) {
			continue
		}
		k := r.At.Unix() / s * s
		a := byKey[k]
		if a == nil {
			a = &metricAcc{at: time.Unix(k, 0).UTC()}
			byKey[k] = a
		}
		a.n++
		a.connections += float64(r.Connections)
		a.cacheHit += r.CacheHitRatio
		a.slow += float64(r.SlowQueryCount)
		a.rate += r.RatePerSec
		if r.MaxConnections > a.maxConnections {
			a.maxConnections = r.MaxConnections
		}
		if r.SizeBytes > a.sizeBytes {
			a.sizeBytes = r.SizeBytes
		}
	}
	keys := make([]int64, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]MetricPoint, 0, len(keys))
	for _, k := range keys {
		a := byKey[k]
		out = append(out, MetricPoint{
			At: a.at, Connections: round2(a.connections / a.n), MaxConnections: a.maxConnections,
			CacheHitRatio: round2(a.cacheHit / a.n), SizeBytes: a.sizeBytes,
			SlowQueryCount: round2(a.slow / a.n), RatePerSec: round2(a.rate / a.n),
		})
	}
	return out
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// metricsWindow returns where a series of span, drawn in buckets of step, starts — aligned to step
// and ending with the current (partial) bucket. Mirrors internal/metrics's unexported window().
func metricsWindow(now time.Time, span, step time.Duration) time.Time {
	sec := int64(step / time.Second)
	end := now.Unix() / sec * sec
	return time.Unix(end-int64(span/time.Second)+sec, 0).UTC()
}

// MetricsSeries reads one service's chart for a range (team scope; the caller resolved the service).
// r is one of internal/metrics.Ranges, the same window/bucket table server metrics charts use.
func (s *Service) MetricsSeries(ctx context.Context, q *db.Queries, teamID, serviceID string, r metrics.Range, now time.Time) (MetricSeries, error) {
	since := metricsWindow(now, r.Span, r.Step)
	rows, err := q.DataServiceMetricsSince(ctx, db.DataServiceMetricsSinceParams{ServiceID: serviceID, TeamID: teamID, Since: since})
	if err != nil {
		return MetricSeries{}, err
	}
	return MetricSeries{Range: r.Key, StepSeconds: int(r.Step / time.Second), Points: bucketMetrics(rows, since, r.Step)}, nil
}
