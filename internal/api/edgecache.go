package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Edge cache (MH-576 D8): a per-app Varnish toggle sitting in front of the app's own upstream, on
// its own server. See internal/deploy/edgecache.go for the settings/agent-op logic and
// internal/agent/cache.go for the agent side. Reading is any member; turning the cache on/off,
// changing its settings and purging are owner/admin (AbilityManageApps), like every other
// app-configuration change.

// EdgeCacheOut is an app's edge cache settings and last known status.
type EdgeCacheOut struct {
	Enabled       bool     `json:"enabled"`
	TTLSeconds    int64    `json:"ttl_seconds"`
	BypassPaths   []string `json:"bypass_paths"`
	BypassCookies []string `json:"bypass_cookies"`
	Status        string   `json:"status" doc:"off | pending | running | error"`
	Error         *string  `json:"error,omitempty"`
	Hits          int64    `json:"hits"`
	Misses        int64    `json:"misses"`
	LastPurgedAt  *string  `json:"last_purged_at,omitempty"`
}

type edgeCacheIn struct {
	AppPath
	Body struct {
		Enabled       bool     `json:"enabled"`
		TTLSeconds    int64    `json:"ttl_seconds" default:"60" minimum:"1" maximum:"86400"`
		BypassPaths   []string `json:"bypass_paths,omitempty" doc:"Paths whose requests always skip the cache, e.g. /admin, /api."`
		BypassCookies []string `json:"bypass_cookies,omitempty" doc:"Cookie names whose presence always skips the cache, e.g. session."`
	}
}

func edgeCacheOut(row db.AppEdgeCache) EdgeCacheOut {
	out := EdgeCacheOut{
		Enabled: row.Enabled, TTLSeconds: row.TtlSeconds,
		BypassPaths: decodeBypassPathsOut(row.BypassPaths), BypassCookies: decodeBypassCookiesOut(row.BypassCookies),
		Status: row.Status, Error: row.Error, Hits: row.Hits, Misses: row.Misses,
	}
	if row.LastPurgedAt != nil {
		s := row.LastPurgedAt.Format("2006-01-02T15:04:05Z07:00")
		out.LastPurgedAt = &s
	}
	return out
}

// decodeBypassPathsOut reads back the jsonb-encoded bypass path list stored by deploy.SetEdgeCache.
func decodeBypassPathsOut(raw string) []string {
	var paths []string
	if raw == "" {
		return []string{}
	}
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return []string{}
	}
	return paths
}

func decodeBypassCookiesOut(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	return strings.Split(raw, ",")
}

func (d *deps) registerEdgeCache(api huma.API) {
	tags := []string{"Edge cache"}
	base := "/api/teams/{team}/apps/{app}/cache"

	huma.Register(api, huma.Operation{OperationID: "get-app-edge-cache", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "An app's edge cache (Varnish) settings and last known status. Any member.",
	}, func(ctx context.Context, in *AppPath) (*struct{ Body EdgeCacheOut }, error) {
		out := &struct{ Body EdgeCacheOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			row, err := deploy.EdgeCacheOfApp(ctx, q, m.TeamID, a.ID)
			if err != nil {
				return huma.Error500InternalServerError("could not read the edge cache settings")
			}
			out.Body = edgeCacheOut(row)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-app-edge-cache", Method: http.MethodPut, Path: base, Tags: tags,
		Summary: "Turn the edge cache on or off, or change its TTL and bypass rules while it is on. Applies to the app's server " +
			"at once (an agent-managed Varnish container). Owner or admin. Audited.",
	}, func(ctx context.Context, in *edgeCacheIn) (*struct{ Body EdgeCacheOut }, error) {
		out := &struct{ Body EdgeCacheOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			s := d.deploys()
			if s == nil {
				return huma.Error500InternalServerError("deploy service unavailable")
			}
			row, err := s.SetEdgeCache(ctx, q, m.TeamID, webActor(id), a, deploy.EdgeCacheInput{
				Enabled: in.Body.Enabled, TTLSeconds: in.Body.TTLSeconds, BypassPaths: in.Body.BypassPaths, BypassCookies: in.Body.BypassCookies,
			})
			if err != nil {
				return actionErr(err, "could not update the edge cache settings")
			}
			out.Body = edgeCacheOut(row)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "purge-app-edge-cache", Method: http.MethodPost, Path: base + "/purge", Tags: tags,
		Summary: "Ban every one of the app's hostnames from the Varnish cache right now. Only meaningful while the cache is on. " +
			"Owner or admin. Audited.",
	}, func(ctx context.Context, in *AppPath) (*struct{ Body EdgeCacheOut }, error) {
		out := &struct{ Body EdgeCacheOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			s := d.deploys()
			if s == nil {
				return huma.Error500InternalServerError("deploy service unavailable")
			}
			row, err := s.PurgeEdgeCache(ctx, q, m.TeamID, webActor(id), a)
			if err != nil {
				return actionErr(err, "could not purge the edge cache")
			}
			out.Body = edgeCacheOut(row)
			return nil
		})
		return out, err
	})
}
