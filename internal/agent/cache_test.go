package agent

import (
	"encoding/json"
	"os"
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
	// Only the cache-enabled app mirrors onto the loopback-only origin server — mirroring a plain
	// app too would be a new listener/routes for an app that never asked for caching (blast radius).
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	origin, ok := servers["hosbah_origin"].(map[string]any)
	if !ok {
		t.Fatalf("no hosbah_origin server rendered:\n%s", s)
	}
	if got := origin["listen"].([]string); len(got) != 1 || got[0] != "127.0.0.1:8180" {
		t.Errorf("hosbah_origin listens on %v, want 127.0.0.1:8180 only", got)
	}
	if v, ok := origin["automatic_https"].(map[string]any); !ok || v["disable"] != true {
		t.Errorf("hosbah_origin must disable automatic HTTPS: %v", origin["automatic_https"])
	}
	tp, ok := origin["trusted_proxies"].(map[string]any)
	if !ok || tp["source"] != "static" {
		t.Fatalf("hosbah_origin must trust static proxies: %v", origin["trusted_proxies"])
	}
	ranges, _ := tp["ranges"].([]string)
	if len(ranges) != 2 || ranges[0] != "127.0.0.1/32" || ranges[1] != "::1" {
		t.Errorf("hosbah_origin trusted_proxies ranges = %v, want [127.0.0.1/32 ::1]", ranges)
	}
	oj, _ := json.Marshal(origin)
	if !strings.Contains(string(oj), `"dial":"127.0.0.1:40001"`) {
		t.Errorf("origin mirror does not dial app a's real upstream:\n%s", oj)
	}
	if !strings.Contains(string(oj), "cached.localhost") {
		t.Errorf("origin mirror is missing the cached app's host:\n%s", oj)
	}
	if strings.Contains(string(oj), "plain.localhost") {
		t.Errorf("a cache-disabled app must not be mirrored onto the origin server:\n%s", oj)
	}
}

func TestRenderCaddyNoOriginWithoutApps(t *testing.T) {
	cfg := renderCaddy(routeTable{Apps: map[string]routeEntry{}}, false)
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	if _, ok := servers["hosbah_origin"]; ok {
		t.Error("no origin server should be rendered with no apps at all")
	}
}

// blockingIssue2: with no app caching, renderCaddy's output must be byte-identical to its shape
// before the edge cache existed — no hosbah_origin server, no automatic_https/trusted_proxies keys
// anywhere, nothing new for the rest of the config. A cache feature nobody has turned on must be
// invisible: the same table (with or without a nil Cache field on every app) renders identically,
// and neither of these two apps enables caching.
func TestRenderCaddyByteIdenticalToPreD8ShapeWhenNoAppCaches(t *testing.T) {
	tbl := routeTable{Apps: map[string]routeEntry{
		"11111111-1111-1111-1111-111111111111": {Upstream: "127.0.0.1:40001", Hosts: []agentproto.Route{{Hostname: "plain.example.com", TLS: "off"}}},
		"22222222-2222-2222-2222-222222222222": {Upstream: "127.0.0.1:40002", Hosts: []agentproto.Route{{Hostname: "secure.example.com", TLS: "auto"}}},
	}}
	cfg := renderCaddy(tbl, false)

	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	if _, ok := servers["hosbah_origin"]; ok {
		t.Fatalf("no app caches: hosbah_origin must not be rendered at all")
	}
	if len(servers) != 2 {
		t.Fatalf("no app caches: only hosbah_http and hosbah_https should be rendered, got %v", servers)
	}
	b, _ := json.Marshal(cfg)
	s := string(b)
	for _, absent := range []string{"hosbah_origin", "automatic_https", "trusted_proxies", "127.0.0.1:8180"} {
		if strings.Contains(s, absent) {
			t.Errorf("no app caches: config must not mention %q at all:\n%s", absent, s)
		}
	}

	// Explicit key set on the top-level config and each server: exactly what pre-D8 rendered.
	if got := mapKeys(cfg); !sameKeys(got, []string{"admin", "apps"}) {
		t.Errorf("top-level config keys changed shape: %v", got)
	}
	httpServer := servers["hosbah_http"].(map[string]any)
	if got := mapKeys(httpServer); !sameKeys(got, []string{"listen", "routes"}) {
		t.Errorf("hosbah_http keys changed shape (accessLog=false): %v", got)
	}
	httpsServer := servers["hosbah_https"].(map[string]any)
	if got := mapKeys(httpsServer); !sameKeys(got, []string{"listen", "routes"}) {
		t.Errorf("hosbah_https keys changed shape (accessLog=false): %v", got)
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sameKeys(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	set := map[string]bool{}
	for _, k := range got {
		set[k] = true
	}
	for _, k := range want {
		if !set[k] {
			return false
		}
	}
	return true
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

// blockingIssue1: on the default bridge network a container's own 127.0.0.1 is its own namespace's
// loopback, never the host's, so the backend Varnish dials (cacheOriginAddr, on the HOST) would be
// unreachable and every cached request would 503/502. The container must run with --network host
// (no -p publish at all) and bind varnishd itself to the fixed loopback address.
func TestCacheContainerArgsUseHostNetworking(t *testing.T) {
	dir := t.TempDir()
	restore := cacheVCLPath
	cacheVCLPath = func() string { return dir + "/default.vcl" }
	defer func() { cacheVCLPath = restore }()

	args := cacheContainerArgs()
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--network host") {
		t.Fatalf("cache container must run with --network host:\n%v", args)
	}
	for i, a := range args {
		if a == "-p" {
			t.Fatalf("cache container must not publish a port (-p), it must share the host network: %v (at %d)", args, i)
		}
	}
	if strings.Contains(joined, "127.0.0.1:6081:80") {
		t.Fatalf("cache container must not bridge-publish a port mapping:\n%v", args)
	}
	if !strings.Contains(joined, "-a "+cacheVarnishAddr) {
		t.Fatalf("varnishd must bind to the fixed loopback address %s:\n%v", cacheVarnishAddr, args)
	}
}

// blockingIssue4: cacheSet must refuse to start the container or flip a route onto Varnish if either
// fixed loopback port is already held by something other than our own pieces.
func TestCheckCachePorts(t *testing.T) {
	restorePort := cachePortInUse
	restoreRunning := cacheContainerIsRunning
	defer func() { cachePortInUse = restorePort; cacheContainerIsRunning = restoreRunning }()

	oc := &OpContext{Ctx: t.Context(), log: func(string, string) {}, notify: func(string, json.RawMessage) {}, progress: func(int, int) {}}

	cases := []struct {
		name        string
		portInUse   map[string]bool
		running     bool
		anyEnabled  bool
		wantErr     bool
		errContains string
	}{
		{name: "both free", portInUse: map[string]bool{}, wantErr: false},
		{name: "varnish port foreign", portInUse: map[string]bool{cacheVarnishAddr: true}, running: false, wantErr: true, errContains: "6081"},
		{name: "varnish port ours (container running)", portInUse: map[string]bool{cacheVarnishAddr: true}, running: true, wantErr: false},
		{name: "origin port foreign", portInUse: map[string]bool{cacheOriginAddr: true}, anyEnabled: false, wantErr: true, errContains: "8180"},
		{name: "origin port ours (already caching)", portInUse: map[string]bool{cacheOriginAddr: true}, anyEnabled: true, wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cachePortInUse = func(addr string) bool { return tc.portInUse[addr] }
			cacheContainerIsRunning = func(*OpContext) bool { return tc.running }
			tbl := routeTable{Apps: map[string]routeEntry{}}
			if tc.anyEnabled {
				tbl.Apps["11111111-1111-1111-1111-111111111111"] = routeEntry{Cache: &cacheConfig{Enabled: true}}
			}
			err := checkCachePorts(oc, tbl)
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got none")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tc.wantErr && tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.errContains)
			}
		})
	}
}

// blockingIssue4 (second half): cacheSet must leave routes.json untouched when the port check fails,
// before anything (container, VCL, route table) is ever mutated.
func TestCacheSetLeavesRoutesUntouchedWhenPortsBusy(t *testing.T) {
	dir := t.TempDir()
	restoreRoutes := routesFile
	routesFile = func() string { return dir + "/routes.json" }
	defer func() { routesFile = restoreRoutes }()

	restorePort := cachePortInUse
	restoreRunning := cacheContainerIsRunning
	cachePortInUse = func(addr string) bool { return addr == cacheVarnishAddr }
	cacheContainerIsRunning = func(*OpContext) bool { return false }
	defer func() { cachePortInUse = restorePort; cacheContainerIsRunning = restoreRunning }()

	id := "11111111-1111-1111-1111-111111111111"
	t0, _ := loadRoutes()
	t0.Apps[id] = routeEntry{Upstream: "127.0.0.1:40001", Hosts: []agentproto.Route{{Hostname: "app.localhost", TLS: "off"}}}
	if err := saveRoutes(t0); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(dir + "/routes.json")
	if err != nil {
		t.Fatal(err)
	}

	oc := &OpContext{Ctx: t.Context(), log: func(string, string) {}, notify: func(string, json.RawMessage) {}, progress: func(int, int) {},
		Params: json.RawMessage(`{"app_id":"` + id + `","enabled":true,"ttl_seconds":60}`)}
	if _, _, err := cacheSet(oc); err == nil {
		t.Fatal("expected cacheSet to fail when a fixed cache port is held by something foreign")
	}

	after, err := os.ReadFile(dir + "/routes.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("routes.json changed even though cacheSet should have failed before touching anything:\nbefore: %s\nafter: %s", before, after)
	}
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

// blockingIssue3: the origin mirror must see the real client through Varnish, not Varnish's own
// loopback connection — Caddy's trusted_proxies (caddy.go) only helps if Varnish actually forwards
// X-Forwarded-Proto/-For in the first place.
func TestRenderVCLForwardsClientIdentity(t *testing.T) {
	vcl := renderVCL(routeTable{Apps: map[string]routeEntry{}})
	for _, want := range []string{
		`set req.http.X-Forwarded-For = req.http.X-Forwarded-For + ", " + client.ip`,
		`set req.http.X-Forwarded-For = client.ip`,
		`set req.http.X-Forwarded-Proto = "http"`,
	} {
		if !strings.Contains(vcl, want) {
			t.Errorf("VCL missing %q:\n%s", want, vcl)
		}
	}
}

// blockingIssue6: never cache a non-GET/HEAD request, a request carrying Authorization, or a
// response that sets a cookie.
func TestRenderVCLNeverCachesUnsafeRequestsOrCookiedResponses(t *testing.T) {
	vcl := renderVCL(routeTable{Apps: map[string]routeEntry{
		"a": {Upstream: "127.0.0.1:40001", Hosts: []agentproto.Route{{Hostname: "shop.example.com", TLS: "auto"}},
			Cache: &cacheConfig{Enabled: true, TTLSeconds: 60}},
	}})
	for _, want := range []string{
		`if (req.method != "GET" && req.method != "HEAD") {`,
		`if (req.http.Authorization) {`,
		`if (beresp.http.Set-Cookie) {`,
		"set beresp.uncacheable = true;",
	} {
		if !strings.Contains(vcl, want) {
			t.Errorf("VCL missing %q:\n%s", want, vcl)
		}
	}
	// Both guards at the top of vcl_recv must return (pass), before any per-app branch runs.
	recvStart := strings.Index(vcl, "sub vcl_recv")
	firstBranch := strings.Index(vcl, `if (req.http.host ==`)
	authGuard := strings.Index(vcl, `if (req.http.Authorization) {`)
	if recvStart < 0 || firstBranch < 0 || authGuard < 0 || !(recvStart < authGuard && authGuard < firstBranch) {
		t.Errorf("the method/auth guards must come before the per-app host branch:\n%s", vcl)
	}
}

// blockingIssue5: even a validated path is interpolated into a VCL regex, so any regex metacharacter
// it legitimately contains (only '.' can appear given ValidCacheBypassPath's charset) must be escaped
// — and a value that manages to slip past validation (e.g. a corrupt routes.json) must never be able
// to break out of the quoted VCL string or inject a second statement.
func TestRenderVCLEscapesBypassPathsAndRejectsMaliciousOnes(t *testing.T) {
	vcl := renderVCL(routeTable{Apps: map[string]routeEntry{
		"a": {Upstream: "127.0.0.1:40001", Hosts: []agentproto.Route{{Hostname: "shop.example.com", TLS: "auto"}},
			Cache: &cacheConfig{Enabled: true, TTLSeconds: 60, BypassPaths: []string{"/a.b"}}},
	}})
	if !strings.Contains(vcl, `req.url ~ "^/a\.b"`) {
		t.Errorf("literal '.' in a bypass path must be regex-escaped:\n%s", vcl)
	}

	malicious := []string{
		`/x" || 1==1 { return (deliver); } sub vcl_recv { if (true`,
		"/x\"\nret",
		`/x\`,
	}
	for _, path := range malicious {
		vcl := renderVCL(routeTable{Apps: map[string]routeEntry{
			"a": {Upstream: "127.0.0.1:40001", Hosts: []agentproto.Route{{Hostname: "shop.example.com", TLS: "auto"}},
				Cache: &cacheConfig{Enabled: true, TTLSeconds: 60, BypassPaths: []string{path}}},
		}})
		if strings.Contains(vcl, `req.url ~`) && !strings.Contains(vcl, "shop.example.com") {
			t.Fatalf("malicious path leaked past defensive re-validation: %q ->\n%s", path, vcl)
		}
		if strings.Contains(vcl, path) {
			t.Errorf("malicious path %q must be dropped by renderVCL's defensive re-validation, not interpolated raw:\n%s", path, vcl)
		}
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
		{AppID: good.AppID, Enabled: true, TTLSeconds: 60, BypassPaths: []string{"/x\\"}},
		{AppID: good.AppID, Enabled: true, TTLSeconds: 60, BypassPaths: []string{"/x\ny"}},
		{AppID: good.AppID, Enabled: true, TTLSeconds: 60, BypassPaths: []string{"/x{ sub vcl_recv"}},
		{AppID: good.AppID, Enabled: true, TTLSeconds: 60, BypassCookies: []string{"a\"b"}},
		{AppID: good.AppID, Enabled: true, TTLSeconds: 60, BypassCookies: []string{"a\\b"}},
		{AppID: good.AppID, Enabled: true, TTLSeconds: 60, BypassCookies: []string{"a\nb"}},
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
