package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Edge cache (MH-576 D8): a per-app Varnish toggle sitting in front of the app's own upstream, on
// its own server (internal/agentproto/cache.go, internal/agent/cache.go). Settings live in
// app_edge_caches, one row per app, upserted on every change; status/hits/misses/last_purged_at
// mirror the agent's most recent cache.set/cache.purge result — never a live counter, read
// opportunistically off varnishstat whenever one of those ops runs.
//
// bypass_paths is stored as a jsonb array (sqlc's go_type override makes it a plain Go string
// holding the encoded JSON); bypass_cookies is a comma-joined plain string — neither needs its own
// column type since cookie names and paths are already validated narrowly by CacheParams.Validate.

var edgeCacheTimeout = 30 * time.Second

// EdgeCacheInput turns an app's edge cache on or off, or changes its settings while it is on.
type EdgeCacheInput struct {
	Enabled       bool
	TTLSeconds    int64
	BypassPaths   []string
	BypassCookies []string
}

// EdgeCacheOfApp reads an app's cache settings, or a zero (never configured, effectively off) row
// when none exist yet — most apps never turn this on, so "not found" is not an error here.
func EdgeCacheOfApp(ctx context.Context, q *db.Queries, teamID, appID string) (db.AppEdgeCache, error) {
	row, err := q.AppEdgeCacheOfApp(ctx, db.AppEdgeCacheOfAppParams{TeamID: teamID, AppID: appID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.AppEdgeCache{AppID: appID, TeamID: teamID, Status: "off", BypassPaths: "[]"}, nil
	}
	return row, err
}

// decodeBypassPaths reads back the jsonb-encoded bypass path list; a blank/invalid value (never
// configured yet) is just an empty list, not an error.
func decodeBypassPaths(raw string) []string {
	var paths []string
	if raw == "" {
		return nil
	}
	_ = json.Unmarshal([]byte(raw), &paths)
	return paths
}

func decodeBypassCookies(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

// SetEdgeCache changes an app's cache settings, applies them on the app's server at once (agent
// cache.set), and records the agent's result (status, and hit/miss counters read off varnishstat)
// back onto the settings row. Owner/admin only — enforced by the caller (AbilityManageApps).
func (s *Service) SetEdgeCache(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, in EdgeCacheInput) (db.AppEdgeCache, error) {
	if in.Enabled && (in.TTLSeconds < 1 || in.TTLSeconds > 86400) {
		return db.AppEdgeCache{}, invalid("ttl_seconds must be between 1 and 86400")
	}
	params := agentproto.CacheParams{AppID: a.ID, Enabled: in.Enabled, TTLSeconds: in.TTLSeconds,
		BypassPaths: in.BypassPaths, BypassCookies: in.BypassCookies}
	if err := params.Validate(); err != nil {
		return db.AppEdgeCache{}, invalid(err.Error())
	}
	pathsJSON, err := json.Marshal(nonNilStrings(in.BypassPaths))
	if err != nil {
		return db.AppEdgeCache{}, invalid("bad bypass paths")
	}
	row, err := q.UpsertAppEdgeCache(ctx, db.UpsertAppEdgeCacheParams{
		AppID: a.ID, TeamID: teamID, Enabled: in.Enabled, TtlSeconds: in.TTLSeconds,
		BypassPaths: string(pathsJSON), BypassCookies: strings.Join(in.BypassCookies, ","), CreatedBy: actor.UserID,
	})
	if err != nil {
		return db.AppEdgeCache{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "app.edge_cache_updated", "app", a.ID,
		actor.audit(map[string]any{"enabled": in.Enabled, "ttl_seconds": in.TTLSeconds})); err != nil {
		return row, err
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil || srv.AgentPublicKey == nil {
		_, _ = q.SetAppEdgeCacheStatus(ctx, db.SetAppEdgeCacheStatusParams{Status: "error", Error: strPtr("that app's server has no agent enrolled yet"), TeamID: teamID, AppID: a.ID})
		return row, conflict("that app's server has no agent enrolled yet")
	}
	raw, _ := json.Marshal(params)
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: agentproto.KindCacheSet, Params: raw, CreatedBy: actor.UserID})
	if err != nil {
		return row, err
	}
	var result agentproto.CacheStats
	status, errMsg := "running", (*string)(nil)
	if !in.Enabled {
		status = "off"
	}
	if _, aerr := s.AwaitResult(ctx, op, edgeCacheTimeout, &result); aerr != nil {
		status = "error"
		msg := aerr.Error()
		errMsg = &msg
	}
	updated, serr := q.SetAppEdgeCacheStatus(ctx, db.SetAppEdgeCacheStatusParams{Status: status, Error: errMsg, TeamID: teamID, AppID: a.ID})
	if serr != nil {
		return row, serr
	}
	if errMsg == nil {
		if final, rerr := q.RecordAppEdgeCachePurge(ctx, db.RecordAppEdgeCachePurgeParams{
			Hits: result.Hits, Misses: result.Misses, LastPurgedAt: updated.LastPurgedAt, TeamID: teamID, AppID: a.ID,
		}); rerr == nil {
			updated = final
		}
	}
	return updated, nil
}

// PurgeEdgeCache bans every one of the app's hostnames from Varnish's cache right now.
func (s *Service) PurgeEdgeCache(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App) (db.AppEdgeCache, error) {
	row, err := q.AppEdgeCacheOfApp(ctx, db.AppEdgeCacheOfAppParams{TeamID: teamID, AppID: a.ID})
	if errors.Is(err, sql.ErrNoRows) || !row.Enabled {
		return db.AppEdgeCache{}, conflict("the edge cache is not enabled for this app")
	}
	if err != nil {
		return db.AppEdgeCache{}, err
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil || srv.AgentPublicKey == nil {
		return db.AppEdgeCache{}, conflict("that app's server has no agent enrolled yet")
	}
	raw, _ := json.Marshal(agentproto.CachePurgeParams{AppID: a.ID})
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: agentproto.KindCachePurge, Params: raw, CreatedBy: actor.UserID})
	if err != nil {
		return db.AppEdgeCache{}, err
	}
	var result struct {
		Purged int   `json:"purged"`
		Hits   int64 `json:"hits"`
		Misses int64 `json:"misses"`
	}
	if _, err := s.AwaitResult(ctx, op, edgeCacheTimeout, &result); err != nil {
		return db.AppEdgeCache{}, err
	}
	now := time.Now()
	updated, err := q.RecordAppEdgeCachePurge(ctx, db.RecordAppEdgeCachePurgeParams{
		Hits: result.Hits, Misses: result.Misses, LastPurgedAt: &now, TeamID: teamID, AppID: a.ID,
	})
	if err != nil {
		return db.AppEdgeCache{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "app.edge_cache_purged", "app", a.ID,
		actor.audit(map[string]any{"purged": result.Purged})); err != nil {
		return updated, err
	}
	return updated, nil
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func strPtr(s string) *string { return &s }
