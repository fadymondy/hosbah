package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func TestRenderCaddySplitsByTLSAndAsks(t *testing.T) {
	cfg := renderCaddy(routeTable{
		AskURL: "http://cp.test/internal/tls/allow",
		Apps: map[string]routeEntry{
			"a": {Upstream: "127.0.0.1:40001", Hosts: []agentproto.Route{{Hostname: "go.localhost", TLS: "off"}, {Hostname: "go.internal.test", TLS: "internal"}}},
			"b": {Upstream: "127.0.0.1:40002", Hosts: []agentproto.Route{{Hostname: "shop.example.com", TLS: "auto"}}},
			"c": {Upstream: "", Hosts: []agentproto.Route{{Hostname: "gone.localhost", TLS: "off"}}}, // removed: no upstream
		},
	}, true)
	b, _ := json.Marshal(cfg)
	s := string(b)

	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	plain := servers["hosbah_http"].(map[string]any)
	secure := servers["hosbah_https"].(map[string]any)
	if got := plain["listen"].([]string); got[0] != ":80" {
		t.Fatalf("hosbah_http listens on %v", got)
	}
	pj, _ := json.Marshal(plain)
	sj, _ := json.Marshal(secure)
	if !strings.Contains(string(pj), "go.localhost") || strings.Contains(string(pj), "shop.example.com") || strings.Contains(string(pj), "go.internal.test") {
		t.Errorf("plain server routes: %s", pj)
	}
	if !strings.Contains(string(sj), "go.internal.test") || !strings.Contains(string(sj), "shop.example.com") || strings.Contains(string(sj), "go.localhost") {
		t.Errorf("secure server routes: %s", sj)
	}
	if strings.Contains(s, "gone.localhost") {
		t.Error("an app without an upstream is still routed")
	}
	// The last plain route is the 404 fallback.
	routes := plain["routes"].([]any)
	if last := routes[len(routes)-1].(map[string]any); last["@id"] != "hosbah-fallback" {
		t.Errorf("no 404 fallback last: %v", last)
	}
	for _, want := range []string{
		`"issuers":[{"module":"internal"}]`,
		`"on_demand":true`,
		`"permission":{"endpoint":"http://cp.test/internal/tls/allow","module":"http"}`,
		`"flush_interval":-1`,
		`"@id":"hosbah-app-a-http"`,
		`"@id":"hosbah-app-a-https"`,
		`"listen":"localhost:2019"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("config lacks %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, `"encode"`) {
		t.Error("responses must never be compressed (SSE/WebSocket)")
	}
}

func TestRenderCaddyEmpty(t *testing.T) {
	cfg := renderCaddy(routeTable{Apps: map[string]routeEntry{}}, true)
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	if _, ok := servers["hosbah_https"]; ok {
		t.Error(":443 should not be bound with no TLS routes")
	}
	if _, ok := cfg["apps"].(map[string]any)["tls"]; ok {
		t.Error("no tls app expected without hosts or an ask URL")
	}
}

func TestRenderCaddyProxyHosts(t *testing.T) {
	cfg := renderCaddy(routeTable{
		AskURL: "http://cp.test/internal/tls/allow",
		Apps:   map[string]routeEntry{},
		Proxies: map[string]proxyEntry{
			"p1": {Host: agentproto.Route{Hostname: "nas.example.com", TLS: "auto"}, Dial: "10.0.0.5:5000"},
			"p2": {Host: agentproto.Route{Hostname: "pve.example.com", TLS: "internal"}, Dial: "10.0.0.2:8006", TLS: true, SkipVerify: true},
			"p3": {Host: agentproto.Route{Hostname: "printer.lan.test", TLS: "off"}, Dial: "10.0.0.9:80"},
		},
	}, true)
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	pj, _ := json.Marshal(servers["hosbah_http"])
	sj, _ := json.Marshal(servers["hosbah_https"])
	if !strings.Contains(string(pj), "printer.lan.test") || strings.Contains(string(pj), "nas.example.com") {
		t.Errorf("plain: %s", pj)
	}
	if !strings.Contains(string(sj), `"dial":"10.0.0.5:5000"`) || !strings.Contains(string(sj), "hosbah-proxy-p2") {
		t.Errorf("secure: %s", sj)
	}
	if !strings.Contains(string(sj), `"insecure_skip_verify":true`) {
		t.Errorf("https upstream transport missing: %s", sj)
	}
	all, _ := json.Marshal(cfg)
	if !strings.Contains(string(all), `"subjects":["nas.example.com"]`) {
		t.Errorf("auto TLS subjects: %s", all)
	}
	// The 404 fallback stays last on :80.
	routes := servers["hosbah_http"].(map[string]any)["routes"].([]any)
	if routes[len(routes)-1].(map[string]any)["@id"] != "hosbah-fallback" {
		t.Error("fallback is not last")
	}
}

// A proxy host with ForwardAuth set (a desktop browser's own hostname) must never route straight
// to its upstream: the rendered route's handle chain starts with a reverse_proxy to the auth URL,
// gated by a handle_response that only lets a 2xx answer fall through to the real upstream, and
// returns anything else (a redirect to Hosbah's login) to the client as-is.
func TestRenderCaddyForwardAuth(t *testing.T) {
	cfg := renderCaddy(routeTable{
		Apps: map[string]routeEntry{},
		Proxies: map[string]proxyEntry{
			"b1": {
				Host:        agentproto.Route{Hostname: "browser-jane.example.com", TLS: "auto"},
				Dial:        "127.0.0.1:9222",
				ForwardAuth: "https://cp.example.com/api/browsers/auth",
			},
		},
	}, true)
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	secure := servers["hosbah_https"].(map[string]any)
	routes := secure["routes"].([]any)

	var target map[string]any
	for _, r := range routes {
		rm := r.(map[string]any)
		if rm["@id"] == "hosbah-proxy-b1" {
			target = rm
		}
	}
	if target == nil {
		t.Fatalf("no route for hosbah-proxy-b1: %v", routes)
	}
	handle := target["handle"].([]any)
	if len(handle) != 2 {
		t.Fatalf("forward_auth route must have 2 handlers (auth check, then upstream); got %d: %v", len(handle), handle)
	}
	auth := handle[0].(map[string]any)
	if auth["handler"] != "reverse_proxy" {
		t.Fatalf("first handler must be reverse_proxy to the auth URL: %v", auth)
	}
	ups := auth["upstreams"].([]any)[0].(map[string]any)
	if ups["dial"] != "cp.example.com:443" {
		t.Errorf("auth upstream dial = %v, want cp.example.com:443", ups["dial"])
	}
	rewrite := auth["rewrite"].(map[string]any)
	if rewrite["method"] != "GET" || rewrite["uri"] != "/api/browsers/auth" {
		t.Errorf("auth subrequest rewrite = %v", rewrite)
	}
	hj, _ := json.Marshal(auth)
	if !strings.Contains(string(hj), "X-Forwarded-Method") || !strings.Contains(string(hj), "X-Forwarded-Uri") {
		t.Errorf("auth handler must carry the real method/uri: %s", hj)
	}
	hr := auth["handle_response"].([]any)
	if len(hr) != 2 {
		t.Fatalf("handle_response must have exactly two entries (2xx, else); got %d", len(hr))
	}
	okEntry := hr[0].(map[string]any)
	if okEntry["match"].(map[string]any)["status_code"].([]any)[0] != 2 {
		t.Errorf("first handle_response entry must match status class 2xx: %v", okEntry)
	}
	// 2xx falls straight through to the upstream with none of the auth answer's headers: copying them
	// put its Content-Length on the upstream's response and Caddy aborted every page with a 502.
	if routes := okEntry["routes"].([]any); len(routes) != 0 {
		t.Errorf("2xx must fall through with no routes: %v", routes)
	}
	elseEntry := hr[1].(map[string]any)
	if _, hasMatch := elseEntry["match"]; hasMatch {
		t.Errorf("the else entry must be unconditional (catch-all): %v", elseEntry)
	}
	elseHandler := elseEntry["routes"].([]any)[0].(map[string]any)["handle"].([]any)[0].(map[string]any)
	if elseHandler["handler"] != "copy_response" {
		t.Errorf("anything but 2xx must return the auth response verbatim: %v", elseHandler)
	}
	upstream := handle[1].(map[string]any)
	if upstream["handler"] != "reverse_proxy" || upstream["upstreams"].([]any)[0].(map[string]any)["dial"] != "127.0.0.1:9222" {
		t.Errorf("second handler must be the real upstream: %v", upstream)
	}
}

// A proxy host with no ForwardAuth renders exactly as before — a single reverse_proxy handler.
func TestRenderCaddyNoForwardAuthIsUnchanged(t *testing.T) {
	cfg := renderCaddy(routeTable{
		Apps: map[string]routeEntry{},
		Proxies: map[string]proxyEntry{
			"p1": {Host: agentproto.Route{Hostname: "nas.example.com", TLS: "auto"}, Dial: "10.0.0.5:5000"},
		},
	}, true)
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	secure := servers["hosbah_https"].(map[string]any)
	routes := secure["routes"].([]any)
	var target map[string]any
	for _, r := range routes {
		rm := r.(map[string]any)
		if rm["@id"] == "hosbah-proxy-p1" {
			target = rm
		}
	}
	if target == nil {
		t.Fatalf("no route for hosbah-proxy-p1")
	}
	handle := target["handle"].([]any)
	if len(handle) != 1 {
		t.Fatalf("no forward_auth: expected exactly 1 handler, got %d: %v", len(handle), handle)
	}
}

// A Caddy that cannot write its access log must still load: renderCaddy(accessLog=false) omits the
// top-level logging block and both servers' "logs" keys entirely, rather than reference a directory
// Caddy cannot create — the bug that broke every route.set/proxy.set when /var/lib/hosbah (root-only)
// was used as the log directory.
func TestRenderCaddyOmitsLoggingWhenDisabled(t *testing.T) {
	table := routeTable{Apps: map[string]routeEntry{
		"a": {Upstream: "127.0.0.1:40001", Hosts: []agentproto.Route{{Hostname: "go.localhost", TLS: "off"}, {Hostname: "go.internal.test", TLS: "internal"}}},
	}}

	enabled, _ := json.Marshal(renderCaddy(table, true))
	if !strings.Contains(string(enabled), `"logging"`) || !strings.Contains(string(enabled), `"logs"`) {
		t.Fatalf("accessLog=true must render a logging block: %s", enabled)
	}

	disabled := renderCaddy(table, false)
	if _, ok := disabled["logging"]; ok {
		t.Error("accessLog=false must not render a top-level logging block")
	}
	servers := disabled["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	for name, s := range servers {
		if _, ok := s.(map[string]any)["logs"]; ok {
			t.Errorf("%s must not reference a logger when accessLog=false", name)
		}
	}
	db, _ := json.Marshal(disabled)
	if strings.Contains(string(db), accessLoggerName) || strings.Contains(string(db), accessLogPath) {
		t.Errorf("accessLog=false must not mention the logger or its path at all: %s", db)
	}
}

// A wildcard never shadows an exact hostname: Caddy's first matching route wins, so every wildcard
// route sits after all exact ones (and before the 404 fallback), whatever the app ids sort as.
func TestRenderCaddyPutsWildcardsAfterExactHosts(t *testing.T) {
	cfg := renderCaddy(routeTable{Apps: map[string]routeEntry{
		"a": {Upstream: "127.0.0.1:40001", Hosts: []agentproto.Route{{Hostname: "*.example.com", TLS: "off"}, {Hostname: "example.com", TLS: "off"}}},
		"b": {Upstream: "127.0.0.1:40002", Hosts: []agentproto.Route{{Hostname: "shop.example.com", TLS: "off"}}},
	}}, false)
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	routes := servers["hosbah_http"].(map[string]any)["routes"].([]any)
	var ids []string
	for _, r := range routes {
		ids = append(ids, r.(map[string]any)["@id"].(string))
	}
	want := []string{"hosbah-app-a-http", "hosbah-app-b-http", "hosbah-app-a-http-wild", "hosbah-fallback"}
	if strings.Join(ids, " ") != strings.Join(want, " ") {
		t.Fatalf("route order %v, want %v", ids, want)
	}
	b, _ := json.Marshal(routes[2])
	if !strings.Contains(string(b), `"host":["*.example.com"]`) {
		t.Errorf("wildcard route: %s", b)
	}
}
