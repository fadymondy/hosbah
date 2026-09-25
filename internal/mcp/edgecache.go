package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// EdgeCacheTools: MH-576 D8 — a per-app Varnish edge cache toggle sitting in front of the app's own
// upstream, on its own server. See internal/deploy/edgecache.go for the settings/agent-op logic and
// internal/agent/cache.go for the agent side.
func EdgeCacheTools() []Tool {
	deploys := func(env *Env) *deploy.Service {
		s, _ := env.App.Deploy.(*deploy.Service)
		return s
	}

	return []Tool{
		{
			Name: "app_cache_settings", Scope: scopes.Write,
			Description: "Read or change an app's edge cache (Varnish) settings: on/off, TTL in seconds, and bypass rules " +
				"(paths like /admin or /api, cookie names like session). Pass only app to read the current settings; pass any of " +
				"enabled/ttl_seconds/bypass_paths/bypass_cookies to change them. Applies to the app's server at once. Owner or admin.",
			InputSchema: schema([]string{"app"}, map[string]any{
				"app":            str("App id or name"),
				"enabled":        map[string]any{"type": "boolean", "description": "Turn the cache on or off"},
				"ttl_seconds":    map[string]any{"type": "integer", "description": "Cache TTL in seconds (1-86400), default 60"},
				"bypass_paths":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Paths that always skip the cache, e.g. /admin"},
				"bypass_cookies": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Cookie names that always skip the cache, e.g. session"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				_, hasEnabled := args["enabled"]
				_, hasTTL := args["ttl_seconds"]
				_, hasPaths := args["bypass_paths"]
				_, hasCookies := args["bypass_cookies"]
				if !hasEnabled && !hasTTL && !hasPaths && !hasCookies {
					row, err := deploy.EdgeCacheOfApp(ctx, env.Q, env.Caller.TeamID, a.ID)
					if err != nil {
						return "", errors.New("could not read the edge cache settings")
					}
					return cacheSettingsSummary(row), nil
				}
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				current, cerr := deploy.EdgeCacheOfApp(ctx, env.Q, env.Caller.TeamID, a.ID)
				if cerr != nil {
					return "", errors.New("could not read the edge cache settings")
				}
				in := deploy.EdgeCacheInput{Enabled: current.Enabled, TTLSeconds: current.TtlSeconds,
					BypassPaths: cacheStrSlice(args["bypass_paths"]), BypassCookies: cacheStrSlice(args["bypass_cookies"])}
				if hasEnabled {
					in.Enabled = args.Bool("enabled")
				}
				if hasTTL {
					in.TTLSeconds = int64(args.Int("ttl_seconds", 60, 1, 86400))
				} else if in.TTLSeconds == 0 {
					in.TTLSeconds = 60
				}
				if !hasPaths {
					in.BypassPaths = decodeBypassPaths(current.BypassPaths)
				}
				if !hasCookies {
					in.BypassCookies = decodeBypassCookies(current.BypassCookies)
				}
				s := deploys(env)
				if s == nil {
					return "", errors.New("the deploy service is not running")
				}
				row, err := s.SetEdgeCache(ctx, env.Q, env.Caller.TeamID, deploy.Actor{UserID: actor(env), Via: "mcp", TokenID: env.Caller.TokenID}, a, in)
				if err != nil {
					return "", err
				}
				return cacheSettingsSummary(row), nil
			},
		},
		{
			Name: "app_cache_purge", Scope: scopes.Write,
			Description: "Ban every one of the app's hostnames from the Varnish edge cache right now. Only meaningful while the " +
				"cache is on. Owner or admin.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": str("App id or name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s := deploys(env)
				if s == nil {
					return "", errors.New("the deploy service is not running")
				}
				row, err := s.PurgeEdgeCache(ctx, env.Q, env.Caller.TeamID, deploy.Actor{UserID: actor(env), Via: "mcp", TokenID: env.Caller.TokenID}, a)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Purged the edge cache for %s. %s", a.Name, cacheSettingsSummary(row)), nil
			},
		},
	}
}

// decodeBypassPaths/decodeBypassCookies mirror the unexported helpers in internal/deploy — kept
// here too since the MCP tool needs to read back the currently stored values (deploy.EdgeCacheInput
// only ever describes a full replacement, not a partial one).
func decodeBypassPaths(raw string) []string {
	if raw == "" {
		return nil
	}
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return nil
	}
	return paths
}

func decodeBypassCookies(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func cacheStrSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func cacheSettingsSummary(row db.AppEdgeCache) string {
	state := "off"
	if row.Enabled {
		state = "on"
	}
	msg := fmt.Sprintf("Edge cache is %s (status: %s, ttl: %ds, hits: %d, misses: %d).", state, row.Status, row.TtlSeconds, row.Hits, row.Misses)
	if row.Error != nil && *row.Error != "" {
		msg += " Error: " + *row.Error
	}
	if paths := decodeBypassPaths(row.BypassPaths); len(paths) > 0 {
		msg += " Bypass paths: " + strings.Join(paths, ", ") + "."
	}
	if cookies := decodeBypassCookies(row.BypassCookies); len(cookies) > 0 {
		msg += " Bypass cookies: " + strings.Join(cookies, ", ") + "."
	}
	return msg
}
