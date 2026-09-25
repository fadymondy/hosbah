// Package apm is application performance monitoring for apps deployed on Hosbah (MH-575 H7): a
// per-app toggle and a rotatable, secret ingest key (hbt_...); public OTLP/HTTP JSON ingest
// endpoints (POST /api/otlp/v1/traces, POST /api/otlp/v1/logs); a reduced, scrubbed store of the
// interesting spans (root/server, error, or slow) and grouped exceptions (issues); and the
// team-scoped summaries served under /api/teams/{team}/apps/{app}/apm (internal/api).
//
// Unlike internal/rum's ingest key (public, travels in visitor page source, never stored beyond
// its hash), the APM key is a genuine bearer secret sent by the app's own server-side OTel SDK, so
// app_apm_settings keeps it sealed with the team DEK (KeySealed) as well as hashed (KeyHash) —
// deploys need the plaintext back to re-seal it into the target agent's env box
// (internal/deploy's envBox calls apm.Service.InjectEnv).
package apm

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
	// SweepEvery is how often the retention sweep looks for old rows to drop.
	SweepEvery = time.Hour
	// SpanRetention: how long a stored span is kept.
	SpanRetention = 7 * 24 * time.Hour
	// ExceptionRetention: how long an exception event (the raw occurrence, not the aggregate
	// apm_issues row) is kept.
	ExceptionRetention = 30 * 24 * time.Hour

	// MaxBodyBytes bounds one OTLP ingest POST body (after gzip decompression).
	MaxBodyBytes = 1 << 20 // 1 MiB
)

// Service is APM: the retention sweep, and the public OTLP ingest routes (ingest.go).
// internal/api/apm.go is the team-scoped settings/endpoints/traces/issues REST API, sharing this
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

// New builds the service. svc is used only for its PublicURL() (the OTEL_EXPORTER_OTLP_ENDPOINT
// shown in the settings snippet and injected into deploys), exactly like rum.Service.
func New(a *app.App, svc *ops.Service) *Service {
	log := a.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: a.SQLDB, ops: svc, log: log.With("component", "apm"), now: time.Now, lim: newRateLimiter()}
}

// Now is the sweep's clock (tests).
func (s *Service) Now() time.Time { s.mu.Lock(); defer s.mu.Unlock(); return s.now() }

// SetClock replaces the sweep's clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.mu.Lock(); s.now = now; s.mu.Unlock() }

// PublicURL is the base URL /api/otlp is served from.
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

// Sweep runs one retention pass now (exported for tests). System scope: every team's old rows.
func (s *Service) Sweep(ctx context.Context) (spans, events, issues int64, err error) {
	spansBefore := s.Now().Add(-SpanRetention)
	eventsBefore := s.Now().Add(-ExceptionRetention)
	err = tenancy.Tx(ctx, s.db, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var e error
		if spans, e = q.DeleteOldApmSpansSystem(ctx, spansBefore); e != nil {
			return e
		}
		if events, e = q.DeleteOldApmExceptionEventsSystem(ctx, eventsBefore); e != nil {
			return e
		}
		if issues, e = q.DeleteOldApmIssuesSystem(ctx, eventsBefore); e != nil {
			return e
		}
		return nil
	})
	return spans, events, issues, err
}

func (s *Service) sweep(ctx context.Context) {
	spans, events, issues, err := s.Sweep(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("apm retention sweep failed", "err", err)
		}
		return
	}
	if spans > 0 || events > 0 || issues > 0 {
		s.log.Info("apm retention sweep", "spans", spans, "events", events, "issues", issues)
	}
}
