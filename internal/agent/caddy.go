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
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/fadymondy/cloudy/internal/agentproto"
)

/*
Routing. The agent owns Caddy's whole configuration: a route table (app → hostnames + upstream)
persisted in /var/lib/cloudy/routes.json, rendered into one JSON document and handed to Caddy's
admin API with POST /load. Caddy swaps configs gracefully — requests in flight finish on the old
upstream, new ones go to the new — and atomically: a config it rejects leaves the running one in
place. Rendering the whole table every time (rather than PATCHing routes by @id) means there is
no drift to reconcile after a crash, and the file on disk is what Caddy loads on a restart.

Two HTTP servers:
  cloudy_http   :80   hosts with tls=off (plain HTTP), then a 404 for any other host
  cloudy_https  :443  hosts with tls=internal (Caddy's local CA) or tls=auto (a public certificate,
                      issued on demand — only after the control plane's /internal/tls/allow says
                      the domain is verified). Caddy's automatic HTTPS adds the :80 → :443
                      redirects for these hosts itself.
Reverse proxies flush immediately (flush_interval -1) and nothing is compressed, so SSE and
WebSocket traffic of the apps is never buffered.
*/

const routesPath = "/var/lib/cloudy/routes.json"

type routeEntry struct {
	Hosts    []agentproto.Route `json:"hosts"`
	Upstream string             `json:"upstream"`
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

// renderCaddy turns the route table into Caddy's JSON config.
func renderCaddy(t routeTable) map[string]any {
	ids := make([]string, 0, len(t.Apps))
	for id := range t.Apps {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var httpRoutes, httpsRoutes []any
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
		if len(plain) > 0 {
			httpRoutes = append(httpRoutes, route(id, "-http", plain, e.Upstream))
		}
		if len(secure) > 0 {
			httpsRoutes = append(httpsRoutes, route(id, "-https", secure, e.Upstream))
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
		"@id": "cloudy-fallback",
		"handle": []any{map[string]any{
			"handler": "static_response", "status_code": 404,
			"body": "No app is routed at this hostname.\n",
		}},
	})

	servers := map[string]any{
		"cloudy_http": map[string]any{"listen": []string{":80"}, "routes": httpRoutes},
	}
	if len(httpsRoutes) > 0 {
		servers["cloudy_https"] = map[string]any{"listen": []string{":443"}, "routes": httpsRoutes}
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
	return map[string]any{
		"admin": map[string]any{"listen": "localhost:2019"},
		"apps":  apps,
	}
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

func writeConfigFile() error {
	routesMu.Lock()
	defer routesMu.Unlock()
	t, err := loadRoutes()
	if err != nil {
		return err
	}
	return writeCaddyFile(renderCaddy(t))
}

func writeCaddyFile(cfg map[string]any) error {
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
	cfg := renderCaddy(t)
	if err := caddyLoad(oc.Ctx, cfg); err != nil {
		return err
	}
	if err := saveRoutes(t); err != nil {
		return err
	}
	return writeCaddyFile(cfg)
}
