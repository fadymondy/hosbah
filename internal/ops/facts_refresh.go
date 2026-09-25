package ops

import (
	"context"
	"database/sql"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// FactsRefreshEvery is how old a server's last system.info may get before the control plane asks
// again: discovered services and Proxmox guests (and the guest auto-link) follow without a click.
const FactsRefreshEvery = 6 * time.Hour

// factsRefreshLoop queues a system.info for every online server that has not had one within
// FactsRefreshEvery. Like the other loops it runs on every API instance; the NOT EXISTS check makes
// a second instance's pass a no-op once the first has queued.
func (s *Service) factsRefreshLoop(ctx context.Context) {
	defer s.wg.Done()
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		if err := s.refreshFacts(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("facts refresh failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) refreshFacts(ctx context.Context) error {
	var due []db.ServersDueFactsRefreshRow
	if err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		due, err = q.ServersDueFactsRefresh(ctx, int32(FactsRefreshEvery/time.Second))
		return err
	}); err != nil {
		return err
	}
	queued := false
	for _, sv := range due {
		// operations has no system INSERT policy: queue in the server's team scope.
		err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: sv.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
			_, _, err := s.Enqueue(ctx, q, EnqueueSpec{TeamID: sv.TeamID, ServerID: sv.ID, Kind: agentproto.KindSystemInfo,
				Params: []byte("{}"), CreatedBy: "hosbah"})
			return err
		})
		if err != nil {
			s.log.Warn("facts refresh: queue failed", "server", sv.ID, "err", err)
			continue
		}
		queued = true
	}
	if queued {
		s.Wake()
	}
	return nil
}
