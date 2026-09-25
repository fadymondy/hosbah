// Package app is the application container (Laravel-style): shared services
// wired once and handed to controllers, resolvers, actions, and seeders.
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	"github.com/togo-framework/togo"

	// SQLite is the kernel's built-in default driver (no plugin). Postgres/MySQL/
	// MongoDB drivers ship as PLUGINS — `togo new --db <stack>` blank-imports the
	// matching db-* plugin into internal/plugins, so the raw driver never lives in
	// this app's go.mod or app.go (microkernel: drivers are plugins).
	_ "modernc.org/sqlite"

	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// App holds the shared services handed to controllers, resolvers, actions, seeders.
// Service fields are nil unless the matching feature/provider plugin is installed.
type App struct {
	Kernel   *togo.Kernel
	DB       *db.Queries  // sqlc typed queries (for complex/typed SQL)
	SQLDB    *sql.DB      // raw handle used by the ORM
	Dialect  togo.Dialect // driver-specific SQL dialect (from DB_DRIVER)
	Hooks    *togo.Hooks
	Log      *slog.Logger
	Cache    togo.Cache
	Queue    togo.Queue
	Storage  togo.Storage
	Realtime togo.Broker

	// Keys is the envelope-encryption keyring (HOSBAH_KEK). Set by server.Boot,
	// which refuses to start without it.
	Keys *crypto.Keyring

	// Stop ends Hosbah's own background workers (the agent hub, the operations dispatcher and
	// sweeper). The togo kernel has no shutdown hooks, so server.Boot sets this and whoever
	// owns the process calls it before Kernel.Close.
	Stop func()
	// Ops is the *ops.Service (typed any: package ops imports app).
	Ops any
	// Deploy is the *deploy.Service (typed any for the same reason).
	Deploy any
	// DBSvc is the *dbsvc.Service (typed any for the same reason): P3 data services and backups.
	DBSvc any
	// Monitors is the *monitors.Service (typed any for the same reason): a site's alerts and monitors.
	Monitors any
	// Schedules is the *schedules.Service (typed any for the same reason): a site's cron jobs.
	Schedules any
	// Notifications is the *notifications.Service (typed any for the same reason): a team's alert
	// channels (email, Slack, Discord, webhook) and status pages.
	Notifications any
	// RUM is the *rum.Service (typed any for the same reason): H5 real-user monitoring (Core Web
	// Vitals) for apps, its retention sweep, and the public beacon/ingest routes.
	RUM any
	// Apm is the *apm.Service (typed any for the same reason): H7 traces and exceptions for apps,
	// its retention sweep, and the public OTLP/HTTP JSON ingest routes.
	Apm any
	// Scripts is the *scripts.Service (typed any for the same reason): MH-576 D4/D5 the team script
	// library, the built-in one-click installs, and running either on one or many servers.
	Scripts any
}

// New builds the container from the kernel, opening the DB if configured.
func New(ctx context.Context, k *togo.Kernel) *App {
	a := &App{
		Kernel:   k,
		Hooks:    k.Hooks,
		Log:      k.Log,
		Dialect:  k.Dialect(),
		Cache:    k.Cache,
		Queue:    k.Queue,
		Storage:  k.Storage,
		Realtime: k.Realtime,
	}
	if sqlDB, err := k.SQL(ctx); err == nil {
		a.SQLDB = sqlDB
		a.DB = db.New(sqlDB)
	} else {
		k.Log.Warn("database unavailable", "err", err)
	}
	return a
}

// T translates a key in the configured locale (backend trans()).
func (a *App) T(key string) string { return a.Kernel.T(key) }

// Emit dispatches an event: it fires kernel hooks (for in-process listeners /
// actions) AND broadcasts over SSE so frontend subscribers receive it. This is
// the event-driven bridge between backend and frontend.
func (a *App) Emit(ctx context.Context, event string, payload any) {
	_ = a.Hooks.Fire(ctx, event, payload)
	if a.Realtime == nil {
		return // realtime feature not enabled
	}
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte("{}")
	}
	a.Realtime.Publish(event, string(data))
}
