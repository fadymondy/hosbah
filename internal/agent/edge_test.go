package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func useEdgeFile(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "edge.json")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := edgeFile
	edgeFile = func() string { return path }
	t.Cleanup(func() { edgeFile = old })
}

func edgeTable(e *edgeConfig) routeTable {
	return routeTable{
		Apps: map[string]routeEntry{
			"a": {
				Upstream: "127.0.0.1:40001",
				Hosts:    []agentproto.Route{{Hostname: "a.localhost", TLS: "off"}, {Hostname: "a.example.com", TLS: "auto"}},
				Realtime: &agentproto.RealtimeRoute{Path: "/ws", Upstream: "127.0.0.1:40009"},
			},
		},
		Proxies: map[string]proxyEntry{"p": {Host: agentproto.Route{Hostname: "p.example.com", TLS: "auto"}, Dial: "10.0.0.5:8080"}},
		Edge:    e,
	}
}

func TestLoadEdge(t *testing.T) {
	useEdgeFile(t, "")
	if e, err := loadEdge(); e != nil || err != nil {
		t.Fatalf("no file: %v %v", e, err)
	}
	useEdgeFile(t, `{"trusted_proxies":[" 10.10.10.100/32","::1"],"client_ip_header":"x-real-ip"}`)
	e, err := loadEdge()
	if err != nil || e == nil {
		t.Fatalf("valid file: %v %v", e, err)
	}
	if e.ClientIPHeader != "X-Real-Ip" || e.TrustedProxies[0] != "10.10.10.100/32" {
		t.Errorf("normalised to %+v", e)
	}
	for _, bad := range []string{
		`not json`,
		`{"client_ip_header":"X-Real-IP"}`,
		`{"trusted_proxies":["10.10.10.300/32"],"client_ip_header":"X-Real-IP"}`,
		`{"trusted_proxies":["10.10.10.100/32"]}`,
		`{"trusted_proxies":["10.10.10.100/32"],"client_ip_header":"X-{evil}"}`,
	} {
		useEdgeFile(t, bad)
		if e, err := loadEdge(); err == nil || e != nil {
			t.Errorf("%s: accepted as %+v", bad, e)
		}
	}
}

func TestRenderCaddyWithoutEdgeIsUnchanged(t *testing.T) {
	b, _ := json.Marshal(renderCaddy(edgeTable(nil), false))
	for _, absent := range []string{"client_ip_headers", "X-Forwarded-For", `"trusted_proxies"`} {
		if strings.Contains(string(b), absent) {
			t.Errorf("no edge.json, yet the config has %s:\n%s", absent, b)
		}
	}
}

func TestRenderCaddyWithEdge(t *testing.T) {
	cfg := renderCaddy(edgeTable(&edgeConfig{TrustedProxies: []string{"10.10.10.100/32"}, ClientIPHeader: "X-Real-Ip"}), false)
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	for _, name := range []string{caddyHTTP, caddyHTTPS} {
		srv, _ := json.Marshal(servers[name])
		for _, want := range []string{
			`"trusted_proxies":{"ranges":["10.10.10.100/32"],"source":"static"}`,
			`"client_ip_headers":["X-Real-Ip"]`,
		} {
			if !strings.Contains(string(srv), want) {
				t.Errorf("%s lacks %s:\n%s", name, want, srv)
			}
		}
	}
	// Every public reverse_proxy — app, realtime and proxy host — passes the one resolved client.
	all, _ := json.Marshal(servers)
	proxies := strings.Count(string(all), `"handler":"reverse_proxy"`)
	set := strings.Count(string(all), `"set":{"X-Forwarded-For":["{http.vars.client_ip}"]}`)
	if proxies == 0 || set != proxies {
		t.Errorf("%d of %d reverse_proxy handlers set X-Forwarded-For:\n%s", set, proxies, all)
	}
}
