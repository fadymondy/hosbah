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
		e := proxyEntry{Host: agentproto.Route{Hostname: p.Hostname, TLS: p.TLS}, Dial: up.Dial, TLS: up.TLS, SkipVerify: p.SkipVerify, ForwardAuth: p.ForwardAuth}
		keepFollowed(&e, t.Proxies[p.ProxyID])
		t.Proxies[p.ProxyID] = e
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

// keepFollowed keeps the container a proxy host follows (reconcile.go) when proxy.set re-sends the
// dial it was created with: the control plane still knows the port from before a restart moved it.
func keepFollowed(e *proxyEntry, prev proxyEntry) {
	if prev.Container == "" {
		return
	}
	asked := prev.Requested
	if asked == "" {
		asked = prev.Dial
	}
	if e.Dial != asked && e.Dial != prev.Dial {
		return // a different upstream: learn it afresh
	}
	e.Container, e.ContainerPort = prev.Container, prev.ContainerPort
	if e.Dial == prev.Dial {
		e.Requested = prev.Requested
	} else {
		e.Requested, e.Dial = e.Dial, prev.Dial
	}
}
