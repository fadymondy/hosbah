package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
Edge (MH-1281). On a server whose Caddy is not the public entry point — the 3x1 box: Cloudflare →
Nginx Proxy Manager → Caddy — every request reaches Caddy from the proxy in front of it, and Caddy,
trusting no one, rewrites X-Forwarded-For to that proxy's address. Every app then sees one client for
all of its visitors: one shared bucket for its per-IP login limits, one address in its audit log.

edge.json names the proxies in front of Caddy and the header they put the visitor in:

	{"trusted_proxies": ["10.10.10.100/32"], "client_ip_header": "X-Real-IP"}

With it, the public servers resolve the client from that header — only on requests from those
proxies — and every public reverse_proxy hands the app exactly that one address as X-Forwarded-For.
The single value matters: apps (togo's auth ≤ v0.9.1 among them) read the leftmost hop, and an
appended chain would put a client-written value there. A request from anyone else resolves to its
own peer, which is what Caddy writes today. Without the file nothing in the config changes.

It is a server-local file the operator writes, not part of routes.json: which proxy fronts a server
is a fact of that server's network, not something the control plane knows.
*/

// edgeFile is overridable in tests.
var edgeFile = func() string { return agentproto.StateDir + "/edge.json" }

type edgeConfig struct {
	TrustedProxies []string `json:"trusted_proxies"`
	ClientIPHeader string   `json:"client_ip_header"`
}

// loadEdge reads edge.json. No file is no edge (nil, nil); a file that does not validate is an error
// the caller logs and renders past — a typo in it must never take routing down.
func loadEdge() (*edgeConfig, error) {
	b, err := os.ReadFile(edgeFile())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var e edgeConfig
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", edgeFile(), err)
	}
	if len(e.TrustedProxies) == 0 {
		return nil, fmt.Errorf("%s lists no trusted_proxies", edgeFile())
	}
	for i, r := range e.TrustedProxies {
		r = strings.TrimSpace(r)
		if _, err := netip.ParsePrefix(r); err != nil {
			if _, aerr := netip.ParseAddr(r); aerr != nil {
				return nil, fmt.Errorf("%s: trusted proxy %q is neither an address nor a CIDR", edgeFile(), r)
			}
		}
		e.TrustedProxies[i] = r
	}
	e.ClientIPHeader = http.CanonicalHeaderKey(strings.TrimSpace(e.ClientIPHeader))
	if e.ClientIPHeader == "" {
		return nil, fmt.Errorf("%s has no client_ip_header", edgeFile())
	}
	if strings.ContainsAny(e.ClientIPHeader, " \t:{}") {
		return nil, fmt.Errorf("%s: %q is not a header name", edgeFile(), e.ClientIPHeader)
	}
	return &e, nil
}

// wantEdge loads edge.json for a render, logging (not failing on) a file that does not validate.
func wantEdge(oc *OpContext) *edgeConfig {
	e, err := loadEdge()
	if err != nil {
		oc.Info("warning: ignoring %v — apps see Caddy's peer as the client until it is fixed; routing is unaffected", err)
		return nil
	}
	return e
}

// applyEdgeServer makes a public server resolve the client from the edge's header.
func applyEdgeServer(server map[string]any, e *edgeConfig) {
	if e == nil {
		return
	}
	server["trusted_proxies"] = map[string]any{"source": "static", "ranges": e.TrustedProxies}
	server["client_ip_headers"] = []string{e.ClientIPHeader}
}

// applyEdgeProxy makes a public reverse_proxy pass the resolved client as the one X-Forwarded-For
// value. Caddy applies these after its own forwarded headers, so this set wins.
func applyEdgeProxy(handler map[string]any, e *edgeConfig) map[string]any {
	if e == nil {
		return handler
	}
	handler["headers"] = map[string]any{"request": map[string]any{
		"set": map[string][]string{"X-Forwarded-For": {"{http.vars.client_ip}"}},
	}}
	return handler
}
