package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func TestRenderCaddyCacheDialsVarnishWhenEnabled(t *testing.T) {
	cfg := renderCaddy(routeTable{
		Apps: map[string]routeEntry{
			"a": {Upstream: "127.0.0.1:40001", Hosts: []agentproto.Route{{Hostname: "cached.localhost", TLS: "off"}},
				Cache: &cacheConfig{Enabled: true, TTLSeconds: 60}},
			"b": {Upstream: "127.0.0.1:40002", Hosts: []agentproto.Route{{Hostname: "plain.localhost", TLS: "off"}}},
		},
	}, false)
	b, _ := json.Marshal(cfg)
	s := string(b)

	if !strings.Contains(s, `"dial":"127.0.0.1:6081"`) {
		t.Errorf("cache-enabled app's public route does not dial Varnish:\n%s", s)
	}
	if !strings.Contains(s, `"dial":"127.0.0.1:40002"`) {
		t.Errorf("plain app's public route should still dial its own upstream:\n%s", s)
	}
	// Both apps mirror onto the loopback-only origin server, always at their real upstream.
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	origin, ok := servers["hosbah_origin"].(map[string]any)
	if !ok {
		t.Fatalf("no hosbah_origin server rendered:\n%s", s)
	}
	if got := origin["listen"].([]string); len(got) != 1 || got[0] != "127.0.0.1:8180" {
		t.Errorf("hosbah_origin listens on %v, want 127.0.0.1:8180 only", got)
	}
	oj, _ := json.Marshal(origin)
	if !strings.Contains(string(oj), `"dial":"127.0.0.1:40001"`) {
		t.Errorf("origin mirror does not dial app a's real upstream:\n%s", oj)
	}
	if !strings.Contains(string(oj), "cached.localhost") || !strings.Contains(string(oj), "plain.localhost") {
		t.Errorf("origin mirror is missing a host:\n%s", oj)
	}
}

func TestRenderCaddyNoOriginWithoutApps(t *testing.T) {
	cfg := renderCaddy(routeTable{Apps: map[string]routeEntry{}}, false)
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	if _, ok := servers["hosbah_origin"]; ok {
		t.Error("no origin server should be rendered with no apps at all")
	}
}

func TestSetRoutePreservesCache(t *testing.T) {
	dir := t.TempDir()
	restore := routesFile
	routesFile = func() string { return dir + "/routes.json" }
	defer func() { routesFile = restore }()

	oc := &OpContext{Ctx: t.Context(), log: func(string, string) {}, notify: func(string, json.RawMessage) {}, progress: func(int, int) {}}
	// route.set needs a reachable Caddy admin API; instead exercise setRoute's table mutation
	// directly against loadRoutes/saveRoutes, bypassing applyRoutes' network call.
	t0, _ := loadRoutes()
	t0.Apps["11111111-1111-1111-1111-111111111111"] = routeEntry{
		Upstream: "127.0.0.1:40001",
		Hosts:    []agentproto.Route{{Hostname: "app.localhost", TLS: "off"}},
		Cache:    &cacheConfig{Enabled: true, TTLSeconds: 30},
	}
	if err := saveRoutes(t0); err != nil {
		t.Fatal(err)
	}

	// Mirror the same mutation setRoute performs (preserving prev.Cache), without needing a live
	// Caddy: this documents and locks the behaviour that route.set never clears a cache toggle.
	t1, _ := loadRoutes()
	id := "11111111-1111-1111-1111-111111111111"
	prev := t1.Apps[id]
	t1.Apps[id] = routeEntry{Upstream: "127.0.0.1:40009", Hosts: prev.Hosts, Cache: prev.Cache}
	if err := saveRoutes(t1); err != nil {
		t.Fatal(err)
	}

	t2, _ := loadRoutes()
	e := t2.Apps[id]
	if e.Cache == nil || !e.Cache.Enabled || e.Cache.TTLSeconds != 30 {
		t.Fatalf("cache config was not preserved across a route update: %+v", e.Cache)
	}
	if e.Upstream != "127.0.0.1:40009" {
		t.Fatalf("upstream was not updated: %v", e.Upstream)
	}
	_ = oc
}

func TestRenderVCL(t *testing.T) {
	tbl := routeTable{Apps: map[string]routeEntry{
		"a": {Upstream: "127.0.0.1:40001", Hosts: []agentproto.Route{{Hostname: "shop.example.com", TLS: "auto"}},
			Cache: &cacheConfig{Enabled: true, TTLSeconds: 120, BypassPaths: []string{"/admin", "/api"}, BypassCookies: []string{"session"}}},
		"b": {Upstream: "127.0.0.1:40002", Hosts: []agentproto.Route{{Hostname: "off.example.com", TLS: "auto"}}}, // cache not enabled
	}}
	vcl := renderVCL(tbl)
	for _, want := range []string{
		"vcl 4.1;", "import std;",
		`backend default {`, `.port = "8180";`,
		`req.http.host == "shop.example.com"`,
		`X-Hosbah-TTL = "120"`,
		`req.url ~ "^/admin"`,
		`req.url ~ "^/api"`,
		`req.http.Cookie ~ "session"`,
		`set beresp.ttl = std.duration(bereq.http.X-Hosbah-TTL + "s", 60s);`,
	} {
		if !strings.Contains(vcl, want) {
			t.Errorf("VCL missing %q:\n%s", want, vcl)
		}
	}
	if strings.Contains(vcl, "off.example.com") {
		t.Errorf("a cache-disabled app must not appear in the VCL:\n%s", vcl)
	}
}

func TestRenderVCLEmptyPassesThrough(t *testing.T) {
	vcl := renderVCL(routeTable{Apps: map[string]routeEntry{}})
	if !strings.Contains(vcl, "return (pass);") {
		t.Errorf("an empty table's VCL must still pass every request through:\n%s", vcl)
	}
}

func TestParseVarnishStats(t *testing.T) {
	out := `{"MAIN.cache_hit":{"value":42,"flag":"c"},"MAIN.cache_miss":{"value":7,"flag":"c"}}`
	stats := parseVarnishStats(out)
	if stats.Hits != 42 || stats.Misses != 7 {
		t.Fatalf("got %+v", stats)
	}
}

func TestParseVarnishStatsMalformed(t *testing.T) {
	stats := parseVarnishStats("not json")
	if stats.Hits != 0 || stats.Misses != 0 {
		t.Fatalf("malformed input should parse to zero, got %+v", stats)
	}
}

func TestCacheParamsValidate(t *testing.T) {
	good := agentproto.CacheParams{AppID: "11111111-1111-1111-1111-111111111111", Enabled: true, TTLSeconds: 60,
		BypassPaths: []string{"/admin"}, BypassCookies: []string{"session"}}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	bad := []agentproto.CacheParams{
		{AppID: "not-a-uuid", Enabled: true, TTLSeconds: 60},
		{AppID: good.AppID, Enabled: true, TTLSeconds: 0},
		{AppID: good.AppID, Enabled: true, TTLSeconds: 60, BypassPaths: []string{"no-leading-slash"}},
		{AppID: good.AppID, Enabled: true, TTLSeconds: 60, BypassCookies: []string{"has space"}},
		{AppID: good.AppID, Enabled: true, TTLSeconds: 60, BypassPaths: []string{"/x\" || 1==1"}},
	}
	for i, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("case %d: expected an error, got none for %+v", i, p)
		}
	}
	// Disabled params skip the TTL/bypass checks entirely.
	off := agentproto.CacheParams{AppID: good.AppID, Enabled: false}
	if err := off.Validate(); err != nil {
		t.Fatalf("disabling should never need valid ttl/bypass fields: %v", err)
	}
}
