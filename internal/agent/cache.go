package agent

import (
	"encoding/json"
	"errors"
	"fmt"
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

// cacheSet turns an app's edge cache on or off, or changes its settings while it is on. It always
// re-renders the whole route table into Caddy (dial target) and, when at least one app on this
// server caches, into Varnish's VCL — so a stale rule never outlives the app it belonged to.
func cacheSet(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.CacheParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	found := true
	if err := applyRoutes(oc, func(t *routeTable) {
		e, ok := t.Apps[p.AppID]
		if !ok {
			found = false
			return
		}
		if p.Enabled {
			e.Cache = &cacheConfig{Enabled: true, TTLSeconds: p.TTLSeconds, BypassPaths: p.BypassPaths, BypassCookies: p.BypassCookies}
		} else {
			e.Cache = nil
		}
		t.Apps[p.AppID] = e
	}); err != nil {
		return nil, 1, err
	}
	if !found {
		return nil, 2, errors.New("this app has no route on this server yet — deploy it first")
	}
	t, err := loadRoutes()
	if err != nil {
		return nil, 1, err
	}
	anyEnabled := false
	for _, e := range t.Apps {
		if e.Cache != nil && e.Cache.Enabled {
			anyEnabled = true
			break
		}
	}
	if !anyEnabled {
		if _, rerr := execOutput(oc, "docker", "rm", "-f", cacheContainer); rerr == nil {
			oc.Info("no app on this server caches anymore; removed the Varnish container")
		}
		return map[string]any{"enabled": false}, 0, nil
	}
	if err := ensureCacheContainer(oc); err != nil {
		return nil, 1, err
	}
	if err := writeVCL(t); err != nil {
		return nil, 1, fmt.Errorf("could not write the cache configuration: %w", err)
	}
	if err := reloadVarnish(oc); err != nil {
		return nil, 1, err
	}
	stats, _ := readCacheStats(oc)
	state := "disabled"
	if p.Enabled {
		state = fmt.Sprintf("enabled (ttl %ds)", p.TTLSeconds)
	}
	oc.Info("edge cache %s for this app", state)
	return map[string]any{"enabled": p.Enabled, "hits": stats.Hits, "misses": stats.Misses}, 0, nil
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
	args := []string{"run", "-d", "--restart", "unless-stopped", "--name", cacheContainer,
		"--label", agentproto.NamePrefix + "cache=1",
		"--log-opt", "max-size=10m", "--log-opt", "max-file=3",
	}
	args = append(args, "-p", "127.0.0.1:"+strconv.Itoa(cacheHTTPPort)+":80")
	args = append(args, "-v", cacheVCLPath()+":"+cacheVCLContainerPath+":ro")
	args = append(args, cacheImage, "varnishd", "-F", "-f", cacheVCLContainerPath, "-a", ":80", "-s", "malloc,256m")
	if _, err := oc.Run("docker", args...); err != nil {
		return fmt.Errorf("could not start the cache container: %w", err)
	}
	return waitVarnishReady(oc)
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

// renderVCL is the whole Varnish configuration for this server: one backend (the origin mirror,
// always at cacheOriginAddr — see caddy.go) and, per cache-enabled app, an if-branch matching its
// hostnames that sets the TTL and bypasses on its configured paths and cookies. Any request whose
// Host does not match a known, cache-enabled hostname is passed straight through uncached — Caddy
// only ever sends such a request to Varnish for an app whose cache is on, but a stale VCL between
// two loads must never accidentally cache the wrong thing.
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
	hasApps := false
	for i, id := range ids {
		e := t.Apps[id]
		hosts := make([]string, 0, len(e.Hosts))
		for _, h := range e.Hosts {
			hosts = append(hosts, h.Hostname)
		}
		sort.Strings(hosts)
		conds := make([]string, len(hosts))
		for j, h := range hosts {
			conds[j] = fmt.Sprintf("req.http.host == %q", h)
		}
		kw := "if"
		if i > 0 {
			kw = "elsif"
		}
		hasApps = true
		fmt.Fprintf(&b, "    %s (%s) {\n", kw, strings.Join(conds, " || "))
		fmt.Fprintf(&b, "        set req.http.X-Hosbah-TTL = %q;\n", strconv.FormatInt(e.Cache.TTLSeconds, 10))
		for _, path := range e.Cache.BypassPaths {
			fmt.Fprintf(&b, "        if (req.url ~ \"^%s\") {\n            return (pass);\n        }\n", path)
		}
		for _, c := range e.Cache.BypassCookies {
			fmt.Fprintf(&b, "        if (req.http.Cookie ~ \"%s\") {\n            return (pass);\n        }\n", c)
		}
		b.WriteString("    }\n")
	}
	if hasApps {
		b.WriteString("    else {\n        return (pass);\n    }\n")
	} else {
		b.WriteString("    return (pass);\n")
	}
	b.WriteString("}\n\nsub vcl_backend_response {\n    if (bereq.http.X-Hosbah-TTL) {\n        set beresp.ttl = std.duration(bereq.http.X-Hosbah-TTL + \"s\", 60s);\n    }\n}\n")
	return b.String()
}
