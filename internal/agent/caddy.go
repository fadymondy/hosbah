package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
Routing. The agent owns Caddy's whole configuration: a route table (app → hostnames + upstream)
persisted in /var/lib/hosbah/routes.json, rendered into one JSON document and handed to Caddy's
admin API with POST /load. Caddy swaps configs gracefully — requests in flight finish on the old
upstream, new ones go to the new — and atomically: a config it rejects leaves the running one in
place. Rendering the whole table every time (rather than PATCHing routes by @id) means there is
no drift to reconcile after a crash, and the file on disk is what Caddy loads on a restart.

Two HTTP servers:
  hosbah_http   :80   hosts with tls=off (plain HTTP), then a 404 for any other host
  hosbah_https  :443  hosts with tls=internal (Caddy's local CA) or tls=auto (a public certificate,
                      issued on demand — only after the control plane's /internal/tls/allow says
                      the domain is verified). Caddy's automatic HTTPS adds the :80 → :443
                      redirects for these hosts itself.
Reverse proxies flush immediately (flush_interval -1) and nothing is compressed, so SSE and
WebSocket traffic of the apps is never buffered.

Server names and route ids were cloudy_… / cloudy-… before the rename. Nothing needs to remove
those: every change loads the WHOLE config (POST /load replaces it), so the first load by this
agent leaves none of the old ids behind. Caddy keeps its listeners across the swap.
*/

const routesPath = agentproto.WorkDir + "/routes.json"

// The two HTTP servers of the config.
const (
	caddyHTTP  = "hosbah_http"
	caddyHTTPS = "hosbah_https"
	// caddyOrigin (MH-576 D8) is the loopback-only server Varnish always dials; see cacheOriginAddr
	// in cache.go and the comment above where originRoutes is built in renderCaddy.
	caddyOrigin = "hosbah_origin"
)

// accessLogDir is where Caddy writes its JSON access log. It is deliberately NOT under WorkDir
// (/var/lib/hosbah, root-owned and mode 0700): Caddy runs as its own "caddy" system user (see
// tools.go's unit file), which cannot create files there, and a Caddy that cannot open its log
// writer refuses to load the config at all — breaking every route.set/proxy.set on the server.
// /var/log/hosbah-caddy is prepared and, when a caddy user exists, chowned to it by
// prepareAccessLogDir (called by root, before any config referencing it is rendered).
const accessLogDir = "/var/log/hosbah-caddy"

// accessLogPath is where Caddy writes one JSON-lines access log of every request through either
// server (apps and proxy hosts alike); internal/agent's traffic aggregator (analytics.go) tails it,
// running as root, so it can read the file regardless of who owns it. Caddy itself keeps it bounded
// (roll_size_mb/roll_keep below), so a disconnected agent never lets it grow past a few hundred MB.
const accessLogPath = accessLogDir + "/access.log"

// accessLoggerName is the shared logger both servers write to.
const accessLoggerName = "hosbah_access"

// prepareAccessLogDir creates accessLogDir and, when a "caddy" system user exists (the Caddy unit
// runs as User=caddy), hands it ownership so Caddy can create and roll access.log itself. When
// there is no caddy user — Caddy running as root, e.g. a minimal container — the directory is left
// root-owned, which Caddy-as-root can already write to. Any error here means the caller must render
// the config WITHOUT the logging block: analytics can never be allowed to break routing.
func prepareAccessLogDir() error {
	if err := os.MkdirAll(accessLogDir, 0o755); err != nil {
		return err
	}
	u, err := user.Lookup("caddy")
	if err != nil {
		return nil // no caddy user: Caddy runs as root, root already owns this directory
	}
	uid, uerr := strconv.Atoi(u.Uid)
	gid, gerr := strconv.Atoi(u.Gid)
	if uerr != nil || gerr != nil {
		return fmt.Errorf("caddy user has a non-numeric uid/gid: %s/%s", u.Uid, u.Gid)
	}
	return os.Chown(accessLogDir, uid, gid)
}

// wantAccessLogging prepares accessLogDir and reports whether renderCaddy should wire the logging
// block in. A failure is logged as a warning and traffic analytics stay off until it is fixed;
// routing itself is never affected.
func wantAccessLogging(oc *OpContext) bool {
	if err := prepareAccessLogDir(); err != nil {
		oc.Info("warning: could not prepare %s for Caddy's access log (%v) — traffic analytics are off until this is fixed; routing is unaffected", accessLogDir, err)
		return false
	}
	return true
}

type routeEntry struct {
	Hosts    []agentproto.Route `json:"hosts"`
	Upstream string             `json:"upstream"`
	// Realtime: the app's websocket server (a worker with a published port). Requests of the app's
	// hostnames whose path starts with Realtime.Path go there instead of Upstream.
	Realtime *agentproto.RealtimeRoute `json:"realtime,omitempty"`
	// Cache (MH-576 D8): when set and Enabled, the app's public route dials the shared Varnish
	// container (cacheVarnishAddr) instead of Upstream directly. Preserved across route.set by
	// setRoute, which only ever changes Hosts/Upstream/Realtime.
	Cache *cacheConfig `json:"cache,omitempty"`
}

// realtimeStreamCloseDelay keeps upgraded (websocket) connections of a realtime route open across
// a config load. Without it Caddy closes every stream of the old config the moment a new one is
// loaded — and this agent loads a whole config for every route change of any app on the server.
// With it a socket lives until its own upstream goes away: when its release's container drains.
const realtimeStreamCloseDelay = "24h"

// realtimeCaddyRoute is the route that sends Path* of hosts to the realtime worker. It is placed
// BEFORE the app's catch-all route of the same hosts (both are terminal; the first match wins).
// Websocket needs: nothing buffered (Caddy buffers neither requests nor responses unless
// request_buffers/response_buffers are set — they are not), flushed at once (flush_interval -1),
// the Upgrade/Connection headers left to reverse_proxy's own handling, and streams that survive
// config loads (stream_close_delay).
func realtimeCaddyRoute(id, suffix string, hosts []string, rt agentproto.RealtimeRoute) map[string]any {
	pattern := rt.Path + "*"
	if rt.Path == "/" {
		pattern = "/*"
	}
	return map[string]any{
		"@id":      agentproto.RealtimeRouteID(id) + suffix,
		"match":    []any{map[string]any{"host": hosts, "path": []string{pattern}}},
		"terminal": true,
		"handle": []any{map[string]any{
			"handler":            "reverse_proxy",
			"upstreams":          []any{map[string]any{"dial": rt.Upstream}},
			"flush_interval":     -1,
			"stream_close_delay": realtimeStreamCloseDelay,
		}},
	}
}

type routeTable struct {
	AskURL string                `json:"ask_url,omitempty"`
	Apps   map[string]routeEntry `json:"apps"`
	// Proxies are the proxy manager's hosts (proxy.set), keyed by proxy id.
	Proxies map[string]proxyEntry `json:"proxies,omitempty"`
}

var routesMu sync.Mutex

// routesFile is overridable in tests.
var routesFile = func() string { return routesPath }

func loadRoutes() (routeTable, error) {
	t := routeTable{Apps: map[string]routeEntry{}}
	b, err := os.ReadFile(routesFile())
	if errors.Is(err, os.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal(b, &t); err != nil {
		return t, fmt.Errorf("%s is corrupt: %w", routesFile(), err)
	}
	if t.Apps == nil {
		t.Apps = map[string]routeEntry{}
	}
	return t, nil
}

func saveRoutes(t routeTable) error {
	if err := os.MkdirAll(filepath.Dir(routesFile()), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(t, "", "  ")
	tmp := routesFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, routesFile())
}

func setAskURL(u string) error {
	routesMu.Lock()
	defer routesMu.Unlock()
	t, err := loadRoutes()
	if err != nil {
		return err
	}
	t.AskURL = u
	return saveRoutes(t)
}

// proxyEntry is one proxy host: a hostname in front of any upstream the server can reach.
type proxyEntry struct {
	Host       agentproto.Route `json:"host"`
	Dial       string           `json:"dial"`
	TLS        bool             `json:"tls,omitempty"`
	SkipVerify bool             `json:"skip_verify,omitempty"`
}

// renderCaddy turns the route table into Caddy's JSON config. accessLog wires the access-log
// writer and both servers' logs block in only when true — the caller has already made sure Caddy
// can actually write there (wantAccessLogging); when it could not, the logging block is left out
// entirely so a permissions problem never keeps Caddy from loading the config at all.
func renderCaddy(t routeTable, accessLog bool) map[string]any {
	ids := make([]string, 0, len(t.Apps))
	for id := range t.Apps {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var httpRoutes, httpsRoutes, originRoutes []any
	var internalHosts, autoHosts []string
	route := func(id, suffix string, hosts []string, upstream string) map[string]any {
		return map[string]any{
			"@id":      agentproto.RouteID(id) + suffix,
			"match":    []any{map[string]any{"host": hosts}},
			"terminal": true,
			"handle": []any{map[string]any{
				"handler":        "reverse_proxy",
				"upstreams":      []any{map[string]any{"dial": upstream}},
				"flush_interval": -1,
			}},
		}
	}
	for _, id := range ids {
		e := t.Apps[id]
		if e.Upstream == "" {
			continue
		}
		var plain, secure []string
		for _, h := range e.Hosts {
			switch h.TLS {
			case "off":
				plain = append(plain, h.Hostname)
			case "internal":
				secure = append(secure, h.Hostname)
				internalHosts = append(internalHosts, h.Hostname)
			default:
				secure = append(secure, h.Hostname)
				autoHosts = append(autoHosts, h.Hostname)
			}
		}
		// A realtime route that does not validate is never rendered (the table is a file on disk).
		rt := e.Realtime
		if rt != nil && rt.Validate() != nil {
			rt = nil
		}
		// The origin server (127.0.0.1:8180, loopback only) always mirrors the app's real upstream,
		// by the same hostnames, regardless of the cache toggle: it is what Varnish itself dials, so
		// its backend never needs reconfiguring when a deploy rotates Upstream's ephemeral port —
		// only this mirror route (re-rendered on every route.set, like the rest of the table) does.
		if len(e.Hosts) > 0 {
			originHosts := append(append([]string{}, plain...), secure...)
			originRoutes = append(originRoutes, route(id, "-origin", originHosts, e.Upstream))
		}
		// The public route dials Varnish instead of the app directly once its edge cache is on
		// (MH-576 D8); Varnish's own backend is always the origin mirror above.
		dial := e.Upstream
		if e.Cache != nil && e.Cache.Enabled {
			dial = cacheVarnishAddr
		}
		if len(plain) > 0 {
			if rt != nil {
				httpRoutes = append(httpRoutes, realtimeCaddyRoute(id, "-http", plain, *rt))
			}
			httpRoutes = append(httpRoutes, route(id, "-http", plain, dial))
		}
		if len(secure) > 0 {
			if rt != nil {
				httpsRoutes = append(httpsRoutes, realtimeCaddyRoute(id, "-https", secure, *rt))
			}
			httpsRoutes = append(httpsRoutes, route(id, "-https", secure, dial))
		}
	}
	pids := make([]string, 0, len(t.Proxies))
	for id := range t.Proxies {
		pids = append(pids, id)
	}
	sort.Strings(pids)
	for _, id := range pids {
		p := t.Proxies[id]
		proxy := map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": p.Dial}}, "flush_interval": -1}
		if p.TLS {
			tr := map[string]any{"protocol": "http", "tls": map[string]any{}}
			if p.SkipVerify {
				tr["tls"] = map[string]any{"insecure_skip_verify": true}
			}
			proxy["transport"] = tr
		}
		r := map[string]any{"@id": agentproto.ProxyRouteID(id), "match": []any{map[string]any{"host": []string{p.Host.Hostname}}},
			"terminal": true, "handle": []any{proxy}}
		switch p.Host.TLS {
		case "off":
			httpRoutes = append(httpRoutes, r)
		case "internal":
			httpsRoutes = append(httpsRoutes, r)
			internalHosts = append(internalHosts, p.Host.Hostname)
		default:
			httpsRoutes = append(httpsRoutes, r)
			autoHosts = append(autoHosts, p.Host.Hostname)
		}
	}
	httpRoutes = append(httpRoutes, map[string]any{
		"@id": agentproto.NamePrefix + "fallback",
		"handle": []any{map[string]any{
			"handler": "static_response", "status_code": 404,
			"body": "No app is routed at this hostname.\n",
		}},
	})

	httpServer := map[string]any{"listen": []string{":80"}, "routes": httpRoutes}
	if accessLog {
		httpServer["logs"] = map[string]any{"default_logger_name": accessLoggerName}
	}
	servers := map[string]any{caddyHTTP: httpServer}
	if len(httpsRoutes) > 0 {
		httpsServer := map[string]any{"listen": []string{":443"}, "routes": httpsRoutes}
		if accessLog {
			httpsServer["logs"] = map[string]any{"default_logger_name": accessLoggerName}
		}
		servers[caddyHTTPS] = httpsServer
	}
	if len(originRoutes) > 0 {
		// No access-log block: the origin mirror is never a request's public entry point.
		servers[caddyOrigin] = map[string]any{"listen": []string{cacheOriginAddr}, "routes": originRoutes}
	}
	apps := map[string]any{"http": map[string]any{"servers": servers}}

	var policies []any
	if len(internalHosts) > 0 {
		policies = append(policies, map[string]any{"subjects": internalHosts, "issuers": []any{map[string]any{"module": "internal"}}})
	}
	if len(autoHosts) > 0 {
		p := map[string]any{"subjects": autoHosts}
		if t.AskURL != "" {
			p["on_demand"] = true
		}
		policies = append(policies, p)
	}
	if len(policies) > 0 || t.AskURL != "" {
		automation := map[string]any{}
		if len(policies) > 0 {
			automation["policies"] = policies
		}
		if t.AskURL != "" {
			automation["on_demand"] = map[string]any{"permission": map[string]any{"module": "http", "endpoint": t.AskURL}}
		}
		apps["tls"] = map[string]any{"automation": automation}
	}
	cfg := map[string]any{
		"admin": map[string]any{"listen": "localhost:2019"},
		"apps":  apps,
	}
	if accessLog {
		cfg["logging"] = map[string]any{
			"logs": map[string]any{
				accessLoggerName: map[string]any{
					"writer": map[string]any{
						"output":       "file",
						"filename":     accessLogPath,
						"roll_size_mb": 64,
						"roll_keep":    3,
						// A day's rolled-off logs are dropped: the traffic aggregator only ever reads the
						// live tail, never replays old files, so nothing but disk space is lost.
						"roll_keep_days": 3,
					},
					"encoder": map[string]any{"format": "json"},
					// Only the access log goes here (not Caddy's admin/startup chatter): request handling
					// emits "http.log.access", nothing else does.
					"include": []string{"http.log.access"},
				},
			},
		}
	}
	return cfg
}

func caddyUp(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, caddyAdmin+"/config/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// caddyLoad hands Caddy a whole config. Caddy validates it and keeps the old one on any error.
func caddyLoad(ctx context.Context, cfg map[string]any) error {
	b, _ := json.Marshal(cfg)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodPost, caddyAdmin+"/load", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Caddy's admin API is not answering (run the caddy.install provisioning step): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("Caddy refused the configuration (the previous one is still serving): %s", bytes.TrimSpace(msg))
	}
	return nil
}

func writeConfigFile(oc *OpContext) error {
	routesMu.Lock()
	defer routesMu.Unlock()
	t, err := loadRoutes()
	if err != nil {
		return err
	}
	return writeCaddyFile(renderCaddy(t, wantAccessLogging(oc)))
}

func writeCaddyFile(cfg map[string]any) error {
	caddyConfig := installedCaddy().config
	if err := os.MkdirAll(filepath.Dir(caddyConfig), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	tmp := caddyConfig + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil { //#nosec G306 -- Caddy (its own user) must read it; no secrets inside
		return err
	}
	return os.Rename(tmp, caddyConfig)
}

// applyRoutes changes the route table (change may be nil: re-apply as is), loads the result into
// Caddy, and only then persists it — so the file and the table never describe a config Caddy
// refused.
func applyRoutes(oc *OpContext, change func(*routeTable)) error {
	routesMu.Lock()
	defer routesMu.Unlock()
	t, err := loadRoutes()
	if err != nil {
		return err
	}
	if change != nil {
		change(&t)
	}
	cfg := renderCaddy(t, wantAccessLogging(oc))
	if err := caddyLoad(oc.Ctx, cfg); err != nil {
		return err
	}
	if err := saveRoutes(t); err != nil {
		return err
	}
	return writeCaddyFile(cfg)
}
