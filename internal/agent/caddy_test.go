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
	})
	b, _ := json.Marshal(cfg)
	s := string(b)

	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	plain := servers["cloudy_http"].(map[string]any)
	secure := servers["cloudy_https"].(map[string]any)
	if got := plain["listen"].([]string); got[0] != ":80" {
		t.Fatalf("cloudy_http listens on %v", got)
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
	if last := routes[len(routes)-1].(map[string]any); last["@id"] != "cloudy-fallback" {
		t.Errorf("no 404 fallback last: %v", last)
	}
	for _, want := range []string{
		`"issuers":[{"module":"internal"}]`,
		`"on_demand":true`,
		`"permission":{"endpoint":"http://cp.test/internal/tls/allow","module":"http"}`,
		`"flush_interval":-1`,
		`"@id":"cloudy-app-a-http"`,
		`"@id":"cloudy-app-a-https"`,
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
	cfg := renderCaddy(routeTable{Apps: map[string]routeEntry{}})
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	if _, ok := servers["cloudy_https"]; ok {
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
	})
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	pj, _ := json.Marshal(servers["cloudy_http"])
	sj, _ := json.Marshal(servers["cloudy_https"])
	if !strings.Contains(string(pj), "printer.lan.test") || strings.Contains(string(pj), "nas.example.com") {
		t.Errorf("plain: %s", pj)
	}
	if !strings.Contains(string(sj), `"dial":"10.0.0.5:5000"`) || !strings.Contains(string(sj), "cloudy-proxy-p2") {
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
	routes := servers["cloudy_http"].(map[string]any)["routes"].([]any)
	if routes[len(routes)-1].(map[string]any)["@id"] != "cloudy-fallback" {
		t.Error("fallback is not last")
	}
}
