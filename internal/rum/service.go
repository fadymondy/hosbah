package rum

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

const (
	// SweepEvery is how often the retention sweep looks for samples to drop.
	SweepEvery = time.Hour
	// Retention is how long a raw sample is kept. Storage: raw samples with percentile_cont computed
	// directly over them at read time (see internal/db/queries/rum_sample.sql) rather than an hourly
	// rollup table — self-hosted RUM volume is small (one app's own visitors, not a public SaaS
	// analytics product), 14 days of raw rows at a few hundred bytes each is a trivial table size, and
	// percentile_cont over a two-week window with the (team_id, app_id, created_at) index stays fast
	// without the extra write path, storage and staleness a rollup job would add.
	Retention = 14 * 24 * time.Hour

	// MaxBodyBytes bounds one POST /api/rum/{key} body.
	MaxBodyBytes = 8 << 10
)

// Service is real-user monitoring: the retention sweep, and the public ingest/beacon routes
// (public.go). internal/api/rum.go is the team-scoped settings/summary REST API, sharing this
// Service's *sql.DB through tenancy.Tx like every other feature.
type Service struct {
	db  *sql.DB
	ops *ops.Service
	log *slog.Logger

	mu   sync.Mutex
	now  func() time.Time
	lim  *rateLimiter
	stop context.CancelFunc
	wg   sync.WaitGroup
}

// New builds the service. svc is used only for its PublicURL() (the /rum.js and /api/rum/{key}
// URLs shown in the settings snippet), exactly like deploy.Service.HookURL.
func New(a *app.App, svc *ops.Service) *Service {
	log := a.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: a.SQLDB, ops: svc, log: log.With("component", "rum"), now: time.Now, lim: newRateLimiter()}
}

// Now is the sweep's clock (tests).
func (s *Service) Now() time.Time { s.mu.Lock(); defer s.mu.Unlock(); return s.now() }

// SetClock replaces the sweep's clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.mu.Lock(); s.now = now; s.mu.Unlock() }

// PublicURL is the base URL the beacon snippet and the ingest endpoint are served from.
func (s *Service) PublicURL() string {
	if s.ops == nil {
		return ""
	}
	return s.ops.PublicURL()
}

// Start launches the retention sweep; Stop ends it.
func (s *Service) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(SweepEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sweep(ctx)
			}
		}
	}()
}

// Stop is the counterpart of Start. Safe to call twice.
func (s *Service) Stop() {
	if s.stop != nil {
		s.stop()
		s.stop = nil
	}
	s.wg.Wait()
}

// Sweep runs one retention pass now (exported for tests). System scope: every team's old samples.
func (s *Service) Sweep(ctx context.Context) (int64, error) {
	before := s.Now().Add(-Retention)
	var n int64
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var e error
		n, e = q.DeleteOldRumSamplesSystem(ctx, before)
		return e
	})
	return n, err
}

func (s *Service) sweep(ctx context.Context) {
	n, err := s.Sweep(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("rum retention sweep failed", "err", err)
		}
		return
	}
	if n > 0 {
		s.log.Info("rum retention sweep", "deleted", n)
	}
}
