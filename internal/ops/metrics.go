package ops

import (
	"context"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/metrics"
)

// MetricsRetentionEvery is how often the retention job folds and prunes server_metrics.
const MetricsRetentionEvery = 5 * time.Minute

// LiveFresh is how old the latest reading may be and still be shown as live.
const LiveFresh = 30 * time.Second

// liveMetrics holds, per connected server, the latest reading (for the server stream's live
// "metrics" event) and the minute being accumulated for the history. Readings arrive every
// ~5 s; the history gets one row per server per minute.
//
// This is in memory on the instance holding the agent's connection, keyed by the server id the
// agent authenticated as. A stream only reads it for a server it has just resolved in its own
// team-scoped transaction, so it shows nothing row-level security would not.
type liveMetrics struct {
	mu     sync.Mutex
	latest map[string]liveReading
	minute map[string]*metrics.Minute
}

type liveReading struct {
	m  agentproto.Metrics
	at time.Time // when the control plane received it
}

// recordMetrics takes one reading (a heartbeat's or a live one): it becomes the server's live
// point, joins the current minute, and a finished minute is written in the agent's own scope.
func (s *Service) recordMetrics(ctx context.Context, ac *agentConn, m agentproto.Metrics) {
	now := s.now()
	s.live.mu.Lock()
	if s.live.latest == nil {
		s.live.latest, s.live.minute = map[string]liveReading{}, map[string]*metrics.Minute{}
	}
	s.live.latest[ac.serverID] = liveReading{m: m, at: now}
	mm := s.live.minute[ac.serverID]
	if mm == nil {
		mm = &metrics.Minute{}
		s.live.minute[ac.serverID] = mm
	}
	done := mm.Add(m, now)
	s.live.mu.Unlock()
	if done != nil {
		s.writeMinute(ctx, ac, *done)
	}
}

// flushMetrics writes the minute in progress when the agent disconnects.
func (s *Service) flushMetrics(ac *agentConn) {
	s.live.mu.Lock()
	var mm metrics.Minute
	if cur := s.live.minute[ac.serverID]; cur != nil {
		mm = *cur
		delete(s.live.minute, ac.serverID)
	}
	s.live.mu.Unlock()
	if mm.N > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.writeMinute(ctx, ac, mm)
	}
}

func (s *Service) writeMinute(ctx context.Context, ac *agentConn, mm metrics.Minute) {
	err := s.agentTx(ctx, ac, func(q *db.Queries) error {
		return metrics.Record(ctx, q, ac.serverID, ac.teamID, mm)
	})
	if err != nil {
		s.log.Warn("metrics minute not stored", "server", ac.serverID, "err", err)
	}
}

// liveMetricsFrame handles a light "metrics" frame: a reading, not a heartbeat (last_seen_at and
// servers.metrics are left to the heartbeats).
func (s *Service) liveMetricsFrame(ctx context.Context, ac *agentConn, f agentproto.Frame) {
	if f.Metrics == nil {
		return
	}
	s.recordMetrics(ctx, ac, *f.Metrics)
	s.broker.Publish(ServerTopic(ac.serverID))
}

// LivePoint is the server's latest reading as a chart point, if one arrived within LiveFresh.
// Callers must have resolved serverID within the caller's team first.
func (s *Service) LivePoint(serverID string) (metrics.Point, bool) {
	s.live.mu.Lock()
	r, ok := s.live.latest[serverID]
	s.live.mu.Unlock()
	if !ok || s.now().Sub(r.at) > LiveFresh {
		return metrics.Point{}, false
	}
	return metrics.LivePoint(r.m, r.at), true
}

// metricsRetentionLoop keeps server_metrics bounded (minute rows for 24 h, 15-minute rows for
// 30 days). Like the timeout sweeper it runs on every API instance; the job is idempotent.
func (s *Service) metricsRetentionLoop(ctx context.Context) {
	defer s.wg.Done()
	t := time.NewTicker(MetricsRetentionEvery)
	defer t.Stop()
	for {
		if _, _, err := metrics.Compact(ctx, s.a.SQLDB, s.now()); err != nil && ctx.Err() == nil {
			s.log.Warn("metrics retention failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
