package agent

import (
	"github.com/fadymondy/hosbah/internal/agentproto"
)

// proxySet applies or removes one proxy host (the Caddy proxy manager). Like route.set it goes
// through applyRoutes, so Caddy validates the whole config before the table is saved.
func proxySet(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.ProxyParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	var up agentproto.Upstream
	if !p.Remove {
		up, _ = agentproto.ParseUpstream(p.Upstream)
	}
	err = applyRoutes(oc, func(t *routeTable) {
		if t.Proxies == nil {
			t.Proxies = map[string]proxyEntry{}
		}
		if p.Remove {
			delete(t.Proxies, p.ProxyID)
			return
		}
		t.Proxies[p.ProxyID] = proxyEntry{Host: agentproto.Route{Hostname: p.Hostname, TLS: p.TLS}, Dial: up.Dial, TLS: up.TLS, SkipVerify: p.SkipVerify}
	})
	if err != nil {
		return nil, 1, err
	}
	if p.Remove {
		oc.Info("proxy host removed")
		return map[string]any{"removed": true}, 0, nil
	}
	oc.Info("%s → %s (%s TLS at the edge)", p.Hostname, p.Upstream, p.TLS)
	return map[string]any{"hostname": p.Hostname, "dial": up.Dial, "upstream_tls": up.TLS}, 0, nil
}
