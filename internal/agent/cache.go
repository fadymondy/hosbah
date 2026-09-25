package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// The edge cache (MH-576 D8, cache.set / cache.purge): one shared Varnish container per server
// ("hosbah-cache"), sitting in front of every cache-enabled app's real upstream.
//
// Why Varnish, not Caddy's own cache handler: this agent installs Caddy from the official release
// tarball (tools.go's installFromTarball) — a plain build, never xcaddy — so there is no cache
// module to turn on; the only options that work with what is actually installed are a Varnish (or
// similar) container in the path, or a CDN's cache rules. A CDN needs the domain to be proxied
// through it (only true for teams with Cloudflare credentials and HOSBAH_APPS_PROXIED — see
// internal/cloudflare), so it cannot be the only answer; Varnish works for any app on any server.
//
// The hard part of "a container in front of the upstream" is that an app's Upstream is an
// ephemeral 127.0.0.1:<port> that changes on every deploy (apps.go's release/start/drain cycle).
// Reconfiguring Varnish's own backend on every deploy would race the cache toggle against the
// deploy pipeline. Instead Varnish's backend is fixed forever, at a loopback-only Caddy server
// (hosbah_origin, 127.0.0.1:8180 — see caddy.go) that mirrors every app's current upstream by the
// same hostnames Caddy already tracks; that mirror is re-rendered by the same route.set that
// already keeps the public route current, so nothing new has to stay in sync. Turning caching on
// or off for an app only flips which address the PUBLIC route dials: the app's own upstream
// directly, or Varnish's fixed address. Varnish itself is only ever touched to load new VCL
// (per-host TTL/bypass rules) or to ban a hostname on purge — never to learn a new backend.
const (
	cacheContainer = "hosbah-cache"
	// cacheImage: the official Varnish image (BSD-2-Clause), pinned to the current stable line.
	cacheImage = "varnish:7.5"
	// cacheHTTPPort is where the container's varnishd listens, published to the host's loopback
	// only — nothing but this agent's Caddy config (cacheVarnishAddr) ever dials it.
	cacheHTTPPort = 6081
	// cacheVarnishAddr is the fixed address a cache-enabled app's public Caddy route dials.
	cacheVarnishAddr = "127.0.0.1:6081"
	// cacheOriginAddr is the fixed address Varnish's own backend always dials (see caddy.go).
	cacheOriginAddr = "127.0.0.1:8180"
	// cacheVCLContainerPath is where the VCL is mounted inside the container, read-only: the file
	// on the host (cacheVCLPath) is rewritten in place, so a bind mount serves the new bytes
	// without touching the container; reloadVarnish then tells varnishd to (re)compile it.
	cacheVCLContainerPath = "/etc/varnish/default.vcl"
)

var cacheDir = filepath.Join(workRoot, "cache")

// cacheVCLPath is the host-side file bind-mounted into the container. Overridable in tests.
var cacheVCLPath = func() string { return filepath.Join(cacheDir, "default.vcl") }

// cacheConfig is an app's edge-cache settings, stored on its routeEntry (Cache) so route.set never
// has to be told about it and cache.set never has to be told the app's hosts/upstream again.
type cacheConfig struct {
	Enabled       bool     `json:"enabled"`
	TTLSeconds    int64    `json:"ttl_seconds"`
	BypassPaths   []string `json:"bypass_paths,omitempty"`
	BypassCookies []string `json:"bypass_cookies,omitempty"`
}

// cacheSet turns an app's edge cache on or off, or changes its settings while it is on.
//
// Enabling is ordered deliberately: the shared Varnish container and its VCL (already knowing this
// app, with the right TTL/bypass rules) must be fully ready BEFORE the public route is ever pointed
// at Varnish's fixed address — otherwise there is a window, between the route flip and the container
// actually answering, where every request to the app 502s. So the container is started (or confirmed
// running), the VCL is written and loaded, and only then does the one applyRoutes call that flips the
// dial target run; if anything before that point fails, the route table is never touched at all. And
// applyRoutes itself only ever persists a route change Caddy has already accepted — a rejected config
// load returns an error here with the previous route table (both on disk and inside Caddy) untouched,
// so a bad cache.set can never leave a server half-migrated onto a cache that isn't there.
//
// Disabling never needs any of this: it only ever removes the "dial Varnish" flag from an app already
// known to route table, which is always safe to apply immediately.
func cacheSet(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.CacheParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	t, err := loadRoutes()
	if err != nil {
		return nil, 1, err
	}
	if _, ok := t.Apps[p.AppID]; !ok {
		return nil, 2, errors.New("this app has no route on this server yet — deploy it first")
	}

	if !p.Enabled {
		if err := applyRoutes(oc, func(t *routeTable) {
			e := t.Apps[p.AppID]
			e.Cache = nil
			t.Apps[p.AppID] = e
		}); err != nil {
			return nil, 1, err
		}
		if t2, lerr := loadRoutes(); lerr == nil {
			anyEnabled := false
			for _, e := range t2.Apps {
				if e.Cache != nil && e.Cache.Enabled {
					anyEnabled = true
					break
				}
			}
			if !anyEnabled {
				if _, rerr := execOutput(oc, "docker", "rm", "-f", cacheContainer); rerr == nil {
					oc.Info("no app on this server caches anymore; removed the Varnish container")
				}
			}
		}
		return finishCacheSet(oc, false, 0)
	}

	// Fixed loopback ports: refuse to touch either the container or a route unless both are free, or
	// already ours (our own container; our own Caddy origin server, already up for another app).
	if err := checkCachePorts(oc, t); err != nil {
		return nil, 2, err
	}

	cfg := &cacheConfig{Enabled: true, TTLSeconds: p.TTLSeconds, BypassPaths: p.BypassPaths, BypassCookies: p.BypassCookies}
	candidate := cloneRouteTable(t)
	ce := candidate.Apps[p.AppID]
	ce.Cache = cfg
	candidate.Apps[p.AppID] = ce

	if err := ensureCacheContainer(oc); err != nil {
		return nil, 1, err
	}
	if err := writeVCL(candidate); err != nil {
		return nil, 1, fmt.Errorf("could not write the cache configuration: %w", err)
	}
	if err := reloadVarnish(oc); err != nil {
		return nil, 1, err
	}

	if err := applyRoutes(oc, func(t *routeTable) {
		e := t.Apps[p.AppID]
		e.Cache = cfg
		t.Apps[p.AppID] = e
	}); err != nil {
		return nil, 1, err
	}

	// Re-render once more from the table actually now live: applyRoutes re-reads routes.json itself
	// (under its own lock), so if anything else about the table changed between the reads above and
	// this flip landing, Varnish's VCL must never lag what Caddy is really dialing.
	if final, ferr := loadRoutes(); ferr == nil {
		if werr := writeVCL(final); werr == nil {
			_ = reloadVarnish(oc)
		}
	}

	return finishCacheSet(oc, true, p.TTLSeconds)
}

func cloneRouteTable(t routeTable) routeTable {
	out := routeTable{AskURL: t.AskURL, Apps: make(map[string]routeEntry, len(t.Apps)), Proxies: t.Proxies}
	for id, e := range t.Apps {
		out.Apps[id] = e
	}
	return out
}

func finishCacheSet(oc *OpContext, enabled bool, ttl int64) (any, int, error) {
	stats, _ := readCacheStats(oc)
	state := "disabled"
	if enabled {
		state = fmt.Sprintf("enabled (ttl %ds)", ttl)
	}
	oc.Info("edge cache %s for this app", state)
	return map[string]any{"enabled": enabled, "hits": stats.Hits, "misses": stats.Misses}, 0, nil
}

// cachePortInUse reports whether something is already listening at addr. Overridable in tests.
var cachePortInUse = func(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// cacheContainerIsRunning wraps containerRunning(oc, cacheContainer). Overridable in tests.
var cacheContainerIsRunning = func(oc *OpContext) bool { return containerRunning(oc, cacheContainer) }

// checkCachePorts refuses to start the cache container or switch any route onto Varnish unless port
// 6081 (Varnish itself) and port 8180 (Caddy's loopback-only origin mirror, see caddy.go) are either
// free or already held by our own pieces. Without this, whatever already has one of these fixed
// loopback ports bound would otherwise be silently shadowed (or, for 8180, could make Caddy's very
// next config load fail outright, taking every route on the server down with it) — cache.set instead
// fails clearly, up front, before anything is touched.
func checkCachePorts(oc *OpContext, t routeTable) error {
	if !cacheContainerIsRunning(oc) && cachePortInUse(cacheVarnishAddr) {
		return fmt.Errorf("port %d (%s) is already in use by something other than the edge cache container; free it and try again", cacheHTTPPort, cacheVarnishAddr)
	}
	originUp := false
	for _, e := range t.Apps {
		if e.Cache != nil && e.Cache.Enabled {
			originUp = true
			break
		}
	}
	if !originUp && cachePortInUse(cacheOriginAddr) {
		return fmt.Errorf("port 8180 (%s) is already in use by something other than Caddy's cache origin server; free it and try again", cacheOriginAddr)
	}
	return nil
}

// cachePurge bans every one of the app's hostnames from Varnish's cache.
func cachePurge(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.CachePurgeParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	t, err := loadRoutes()
	if err != nil {
		return nil, 1, err
	}
	e, ok := t.Apps[p.AppID]
	if !ok || e.Cache == nil || !e.Cache.Enabled {
		return nil, 2, errors.New("the edge cache is not enabled for this app")
	}
	if !containerRunning(oc, cacheContainer) {
		return nil, 1, errors.New("the cache container is not running")
	}
	for _, h := range e.Hosts {
		if _, err := execOutput(oc, "docker", "exec", cacheContainer, "varnishadm", "ban", "req.http.host == \""+h.Hostname+"\""); err != nil {
			return nil, 1, fmt.Errorf("could not purge %s: %w", h.Hostname, err)
		}
	}
	stats, _ := readCacheStats(oc)
	oc.Info("purged the edge cache for %d hostname(s)", len(e.Hosts))
	return map[string]any{"purged": len(e.Hosts), "hits": stats.Hits, "misses": stats.Misses}, 0, nil
}

// ensureCacheContainer starts the shared Varnish container if it is not already running. The VCL
// file is created (possibly empty/pass-through) before the container starts, since varnishd
// refuses to start with a missing config file.
func ensureCacheContainer(oc *OpContext) error {
	if containerRunning(oc, cacheContainer) {
		return nil
	}
	_, _ = execOutput(oc, "docker", "rm", "-f", cacheContainer)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(cacheVCLPath()); os.IsNotExist(err) {
		if err := os.WriteFile(cacheVCLPath(), []byte(passthroughVCL()), 0o600); err != nil { //#nosec G306 -- read by Varnish's own container user
			return err
		}
	}
	if _, err := oc.Run("docker", cacheContainerArgs()...); err != nil {
		return fmt.Errorf("could not start the cache container: %w", err)
	}
	return waitVarnishReady(oc)
}

// cacheContainerArgs is the whole `docker run` argv (minus "docker" itself) for the shared Varnish
// container. It runs with --network host, not a published port: on the default bridge network a
// container's own 127.0.0.1 is its own network namespace's loopback, never the host's — so the
// backend this container's Varnish dials (cacheOriginAddr, Caddy's loopback-only origin mirror on
// the HOST) would be unreachable and every cached request would 502. --network host puts the
// container directly on the host's network namespace, so both varnishd's own listen address
// (cacheVarnishAddr) and the backend it dials are the real, single 127.0.0.1.
func cacheContainerArgs() []string {
	args := []string{"run", "-d", "--restart", "unless-stopped", "--name", cacheContainer,
		"--network", "host",
		"--label", agentproto.NamePrefix + "cache=1",
		"--log-opt", "max-size=10m", "--log-opt", "max-file=3",
	}
	args = append(args, "-v", cacheVCLPath()+":"+cacheVCLContainerPath+":ro")
	args = append(args, cacheImage, "varnishd", "-F", "-f", cacheVCLContainerPath, "-a", cacheVarnishAddr, "-s", "malloc,256m")
	return args
}

func waitVarnishReady(oc *OpContext) error {
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := execOutput(oc, "docker", "exec", cacheContainer, "varnishadm", "vcl.list"); err == nil {
			return nil
		}
		if oc.Ctx.Err() != nil {
			return oc.Ctx.Err()
		}
		if time.Now().After(deadline) {
			return errors.New("Varnish did not become ready within 20s")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// reloadVarnish loads the (already rewritten) VCL file under a fresh label and switches to it —
// Varnish keeps serving the previous VCL's cached objects, nothing drops mid-request.
func reloadVarnish(oc *OpContext) error {
	label := "hosbah-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := execOutput(oc, "docker", "exec", cacheContainer, "varnishadm", "vcl.load", label, cacheVCLContainerPath); err != nil {
		return fmt.Errorf("Varnish refused the new configuration: %w", err)
	}
	if _, err := execOutput(oc, "docker", "exec", cacheContainer, "varnishadm", "vcl.use", label); err != nil {
		return fmt.Errorf("could not switch Varnish to the new configuration: %w", err)
	}
	return nil
}

// readCacheStats is a cheap, opportunistic reading off varnishstat — never a live counter.
func readCacheStats(oc *OpContext) (agentproto.CacheStats, error) {
	out, err := execOutput(oc, "docker", "exec", cacheContainer, "varnishstat", "-1", "-j")
	if err != nil {
		return agentproto.CacheStats{}, err
	}
	return parseVarnishStats(out), nil
}

func parseVarnishStats(out string) agentproto.CacheStats {
	var parsed map[string]json.RawMessage
	if json.Unmarshal([]byte(out), &parsed) != nil {
		return agentproto.CacheStats{}
	}
	get := func(k string) int64 {
		raw, ok := parsed[k]
		if !ok {
			return 0
		}
		var f struct {
			Value int64 `json:"value"`
		}
		_ = json.Unmarshal(raw, &f)
		return f.Value
	}
	return agentproto.CacheStats{Hits: get("MAIN.cache_hit"), Misses: get("MAIN.cache_miss")}
}

// writeVCL atomically rewrites the host-side VCL file so the bind-mounted container sees the new
// bytes on its next `varnishadm vcl.load` (reloadVarnish).
func writeVCL(t routeTable) error {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return err
	}
	tmp := cacheVCLPath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(renderVCL(t)), 0o600); err != nil { //#nosec G306 -- read by Varnish's own container user
		return err
	}
	return os.Rename(tmp, cacheVCLPath())
}

// passthroughVCL is the file ensureCacheContainer creates before Varnish has ever been told about
// a cached app, so varnishd (which refuses to start without a valid VCL) has something to load.
func passthroughVCL() string {
	return "vcl 4.1;\n\nbackend default {\n    .host = \"127.0.0.1\";\n    .port = \"8180\";\n}\n\nsub vcl_recv {\n    return (pass);\n}\n"
}

// vclRegexEscape escapes every PCRE/regex metacharacter so a bypass path's literal characters are
// matched literally once interpolated into a VCL regex (Varnish's req.url ~ "..."). Combined with
// ValidCacheBypassPath's narrow charset (agentproto/cache.go — letters, digits, / _ . -, nothing
// that could ever close the quoted string or start a VCL comment/statement), the only metacharacter
// this can see today is '.', but every one is escaped so a future charset relaxation can never
// accidentally turn a bypass path into something other than the literal path it names.
func vclRegexEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\.+*?()|[]{}^$`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// renderVCL is the whole Varnish configuration for this server: one backend (the origin mirror,
// always at cacheOriginAddr — see caddy.go) and, per cache-enabled app, an if-branch matching its
// hostnames that sets the TTL and bypasses on its configured paths and cookies. Any request whose
// Host does not match a known, cache-enabled hostname is passed straight through uncached — Caddy
// only ever sends such a request to Varnish for an app whose cache is on, but a stale VCL between
// two loads must never accidentally cache the wrong thing.
//
// Three rules apply to every request, before any per-app branch: only GET/HEAD is ever a candidate
// for caching (anything else — POST, PUT, DELETE… — always passes straight through); a request that
// carries Authorization always passes through (a cached response must never leak between two
// different bearer identities); and hostnames/bypass paths/bypass cookies are re-checked here with
// the same validators CacheParams.Validate uses on both sides of the wire, so a corrupt or
// hand-edited routes.json can never turn into a VCL injection even if something upstream of this
// function forgot to validate. Client identity (X-Forwarded-For/-Proto) is threaded through
// explicitly for the same reason the Caddy origin server sets trusted_proxies (see caddy.go): the
// origin mirror behind Varnish must see the real client, not Varnish's own loopback connection.
func renderVCL(t routeTable) string {
	var ids []string
	for id, e := range t.Apps {
		if e.Cache != nil && e.Cache.Enabled && e.Upstream != "" && len(e.Hosts) > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	var b strings.Builder
	b.WriteString("vcl 4.1;\nimport std;\n\nbackend default {\n    .host = \"127.0.0.1\";\n    .port = \"8180\";\n}\n\n")
	b.WriteString("sub vcl_recv {\n    unset req.http.X-Hosbah-TTL;\n")
	b.WriteString("    if (req.http.X-Forwarded-For) {\n        set req.http.X-Forwarded-For = req.http.X-Forwarded-For + \", \" + client.ip;\n    } else {\n        set req.http.X-Forwarded-For = client.ip;\n    }\n")
	b.WriteString("    if (!req.http.X-Forwarded-Proto) {\n        set req.http.X-Forwarded-Proto = \"http\";\n    }\n")
	b.WriteString("    if (req.method != \"GET\" && req.method != \"HEAD\") {\n        return (pass);\n    }\n")
	b.WriteString("    if (req.http.Authorization) {\n        return (pass);\n    }\n")
	hasApps := false
	for _, id := range ids {
		e := t.Apps[id]
		hosts := make([]string, 0, len(e.Hosts))
		for _, h := range e.Hosts {
			if !agentproto.ValidHostname(h.Hostname) {
				continue // defense in depth: never trust a hand-edited/corrupt routes.json
			}
			hosts = append(hosts, h.Hostname)
		}
		if len(hosts) == 0 {
			continue
		}
		sort.Strings(hosts)
		conds := make([]string, len(hosts))
		for j, h := range hosts {
			conds[j] = fmt.Sprintf("req.http.host == %q", h)
		}
		kw := "if"
		if hasApps {
			kw = "elsif"
		}
		hasApps = true
		fmt.Fprintf(&b, "    %s (%s) {\n", kw, strings.Join(conds, " || "))
		fmt.Fprintf(&b, "        set req.http.X-Hosbah-TTL = %q;\n", strconv.FormatInt(e.Cache.TTLSeconds, 10))
		for _, path := range e.Cache.BypassPaths {
			if !agentproto.ValidCacheBypassPath(path) {
				continue
			}
			fmt.Fprintf(&b, "        if (req.url ~ \"^%s\") {\n            return (pass);\n        }\n", vclRegexEscape(path))
		}
		for _, c := range e.Cache.BypassCookies {
			if !agentproto.ValidCacheBypassCookie(c) {
				continue
			}
			fmt.Fprintf(&b, "        if (req.http.Cookie ~ \"%s\") {\n            return (pass);\n        }\n", vclRegexEscape(c))
		}
		b.WriteString("    }\n")
	}
	if hasApps {
		b.WriteString("    else {\n        return (pass);\n    }\n")
	} else {
		b.WriteString("    return (pass);\n")
	}
	b.WriteString("}\n\nsub vcl_backend_response {\n")
	b.WriteString("    if (bereq.http.X-Hosbah-TTL) {\n        set beresp.ttl = std.duration(bereq.http.X-Hosbah-TTL + \"s\", 60s);\n    }\n")
	// A response that sets a cookie must never be shared across visitors, whatever TTL was just set
	// above and whatever the app's own Cache-Control says.
	b.WriteString("    if (beresp.http.Set-Cookie) {\n        set beresp.ttl = 0s;\n        set beresp.uncacheable = true;\n        return (deliver);\n    }\n")
	b.WriteString("}\n")
	return b.String()
}
