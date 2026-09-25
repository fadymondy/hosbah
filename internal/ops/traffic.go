package ops

import (
	"context"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/traffic"
)

// TrafficRetentionEvery is how often the retention job folds and prunes app_traffic_minutes.
const TrafficRetentionEvery = 5 * time.Minute

// trafficFrame handles a "traffic" frame: one or more apps' finished minute of Caddy access-log
// aggregates. Each app's bucket is written in the agent's own scope, so the foreign key to apps
// backstops the app id belonging to the agent's team even though the connection is scoped by
// server, not by app.
func (s *Service) trafficFrame(ctx context.Context, ac *agentConn, f agentproto.Frame) {
	for _, m := range f.Traffic {
		if m.AppID == "" {
			continue
		}
		mm := m
		err := s.agentTx(ctx, ac, func(q *db.Queries) error {
			return traffic.Record(ctx, q, mm.AppID, ac.serverID, ac.teamID, mm)
		})
		if err != nil {
			s.log.Warn("traffic minute not stored", "server", ac.serverID, "app", mm.AppID, "err", err)
		}
	}
}

// trafficRetentionLoop keeps app_traffic_minutes bounded, the same shape as the server metrics
// retention loop: it runs on every API instance and is idempotent.
func (s *Service) trafficRetentionLoop(ctx context.Context) {
	defer s.wg.Done()
	t := time.NewTicker(TrafficRetentionEvery)
	defer t.Stop()
	for {
		if _, _, err := traffic.Compact(ctx, s.a.SQLDB, s.now()); err != nil && ctx.Err() == nil {
			s.log.Warn("traffic retention failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
