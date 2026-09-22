package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
)

/*
Realtime in the pipeline. An app's app_realtime row names one worker of its hosbah.toml, the port
that worker listens on inside its container, and a path. A deploy, restart or rollback:

 1. starts that worker with StartParams.Realtime, so the agent publishes the port on an ephemeral
    127.0.0.1 port (as it does for web) and reports it back;
 2. routes <path>* of every hostname of the app to it — in the same route.set that switches web
    (deploy), or as the last routing step of app.rollback (restart / rollback);
 3. records what it did in releases.containers.realtime. That record — not the configuration — is
    what says whether the live release is routed.
*/

// ReleaseRealtime is releases.containers.realtime: the realtime worker of a release as it runs now.
type ReleaseRealtime struct {
	Process   string `json:"process"` // bare name: "reverb" is the release's "worker:reverb"
	Container string `json:"container"`
	Port      int    `json:"port"`      // inside the container
	HostPort  int    `json:"host_port"` // published on 127.0.0.1
	Path      string `json:"path"`      // what was routed when the release started
}

// RealtimeOfRelease is what the release recorded about its realtime worker, or nil (nil-safe).
func RealtimeOfRelease(rel *db.Release) *ReleaseRealtime {
	if rel == nil || rel.Containers == nil {
		return nil
	}
	var c struct {
		Realtime *ReleaseRealtime `json:"realtime"`
	}
	if json.Unmarshal([]byte(*rel.Containers), &c) != nil || c.Realtime == nil || c.Realtime.HostPort == 0 {
		return nil
	}
	return c.Realtime
}

// RealtimeWholeSite: "/" is the whole site, which only kind custom may ask for.
func RealtimeWholeSite(cfg db.AppRealtime) bool { return cfg.Path == "/" && cfg.Kind == "custom" }

// RealtimePublishOf is the typed start params of the realtime worker for a configuration.
func RealtimePublishOf(cfg db.AppRealtime) *agentproto.RealtimePublish {
	return &agentproto.RealtimePublish{Port: int(cfg.Port), Path: cfg.Path, WholeSite: RealtimeWholeSite(cfg)}
}

// RealtimeRouteOf is the realtime route an app should have NOW: there is one when realtime is
// enabled and the live release runs the configured process with the configured port published.
// The path comes from the configuration (a changed path needs no restart — the published port is
// the same); a changed process or port needs a restart, and until then nothing is routed.
func RealtimeRouteOf(cfg *db.AppRealtime, live *db.Release) *agentproto.RealtimeRoute {
	rec := RealtimeOfRelease(live)
	if cfg == nil || rec == nil || !cfg.Enabled || cfg.Process != rec.Process || int(cfg.Port) != rec.Port {
		return nil
	}
	rt := &agentproto.RealtimeRoute{Path: cfg.Path, WholeSite: RealtimeWholeSite(*cfg), Upstream: fmt.Sprintf("127.0.0.1:%d", rec.HostPort)}
	if rt.Validate() != nil {
		return nil
	}
	return rt
}

// realtimeConfig reads the app's configuration ((nil, nil): none).
func realtimeConfig(ctx context.Context, q *db.Queries, appID, teamID string) (*db.AppRealtime, error) {
	row, err := q.RealtimeOfApp(ctx, db.RealtimeOfAppParams{AppID: appID, TeamID: teamID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// realtimeOnce reads the app's realtime configuration once per deployment, just before its first
// container starts, and decides whether this release has a realtime worker: enabled, and the
// process is one the release declares. A configured process the release does not declare is said
// in the deployment's log and nothing is routed — it never fails a deploy.
func (r *runner) realtimeOnce(ctx context.Context, eff Effective) (*db.AppRealtime, error) {
	if r.realtimeLoaded {
		return r.realtime, nil
	}
	var cfg *db.AppRealtime
	if err := r.tx(ctx, func(q *db.Queries) error {
		var err error
		cfg, err = realtimeConfig(ctx, q, r.app.ID, r.dep.TeamID)
		return err
	}); err != nil {
		return nil, err
	}
	r.realtimeLoaded = true
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}
	if _, ok := eff.Processes[agentproto.RealtimeWorker(cfg.Process)]; !ok {
		r.logf("error", `realtime: this release declares no process "worker:%s" in hosbah.toml (or cloudy.toml) — nothing is routed to %s`, cfg.Process, cfg.Path)
		return nil, nil
	}
	if err := RealtimePublishOf(*cfg).Validate(); err != nil || !agentproto.ValidRealtimeProcess(cfg.Process) {
		r.logf("error", "realtime: the configuration is not valid — nothing is routed")
		return nil, nil
	}
	r.realtime = cfg
	r.logf("info", "realtime: worker:%s publishes port %d; %s* of the app's hostnames goes to it", cfg.Process, cfg.Port, cfg.Path)
	return cfg, nil
}

// realtimeStarted turns the realtime worker's start result into what the release records and what
// Caddy routes. (nil, nil) when this release has no realtime worker.
func (r *runner) realtimeStarted(res agentproto.StartResult) (*ReleaseRealtime, *agentproto.RealtimeRoute) {
	cfg := r.realtime
	if cfg == nil || res.HostPort == 0 {
		return nil, nil
	}
	rec := &ReleaseRealtime{Process: cfg.Process, Container: res.Container, Port: int(cfg.Port), HostPort: res.HostPort, Path: cfg.Path}
	return rec, &agentproto.RealtimeRoute{Path: cfg.Path, WholeSite: RealtimeWholeSite(*cfg), Upstream: fmt.Sprintf("127.0.0.1:%d", res.HostPort)}
}

// liveRealtimeRoute is the realtime route of the release that is live now (a failed switch points
// everything back at it).
func (r *runner) liveRealtimeRoute(ctx context.Context) *agentproto.RealtimeRoute {
	var cfg *db.AppRealtime
	_ = r.tx(ctx, func(q *db.Queries) error {
		var err error
		cfg, err = realtimeConfig(ctx, q, r.app.ID, r.dep.TeamID)
		return err
	})
	return RealtimeRouteOf(cfg, r.live)
}
