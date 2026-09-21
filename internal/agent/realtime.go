package agent

import (
	"errors"
	"fmt"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Realtime: the app's websocket worker gets a published port in startArgs/startProcess (apps.go) and
// a path route in front of the app's catch-all in renderCaddy (caddy.go). This file is the part in
// between: changing only the realtime half of an app's route.

// currentRealtime is the realtime route the app has in the table now, or nil.
func currentRealtime(appID string) *agentproto.RealtimeRoute {
	routesMu.Lock()
	defer routesMu.Unlock()
	t, err := loadRoutes()
	if err != nil {
		return nil
	}
	if e, ok := t.Apps[appID]; ok && e.Realtime != nil {
		rt := *e.Realtime
		return &rt
	}
	return nil
}

// setRealtime sets (rt != nil) or removes the realtime route of an app that is routed. It reports
// how many hostnames the app has; an app without a route is an error when setting and nothing to
// do when removing.
func setRealtime(oc *OpContext, appID string, rt *agentproto.RealtimeRoute) (hosts int, err error) {
	if rt != nil {
		if err := rt.Validate(); err != nil {
			return 0, err
		}
	}
	missing := false
	err = applyRoutes(oc, func(t *routeTable) { hosts, missing = changeRealtime(t, appID, rt) })
	if err != nil {
		return 0, err
	}
	if missing && rt != nil {
		return 0, errors.New("this app is not routed on this server — deploy it first")
	}
	return hosts, nil
}

// switchRealtime is app.rollback's last routing step: of the workers just started, the one with
// Realtime params (at most one) takes the realtime route; with none, the route goes.
func switchRealtime(oc *OpContext, appID string, params []agentproto.StartParams, started []agentproto.StartResult) (int, error) {
	var rt *agentproto.RealtimeRoute
	for i, w := range params {
		if w.Realtime == nil || i >= len(started) {
			continue
		}
		if rt != nil {
			return 2, errors.New("only one process of a release is the realtime process")
		}
		if started[i].HostPort == 0 {
			return 1, fmt.Errorf("%s did not report a published port", w.Process)
		}
		rt = &agentproto.RealtimeRoute{Path: w.Realtime.Path, WholeSite: w.Realtime.WholeSite,
			Upstream: fmt.Sprintf("127.0.0.1:%d", started[i].HostPort)}
	}
	if rt == nil && currentRealtime(appID) == nil {
		return 0, nil
	}
	if _, err := setRealtime(oc, appID, rt); err != nil {
		return 1, err
	}
	if rt != nil {
		oc.Info("realtime: %s* → %s", rt.Path, rt.Upstream)
	} else {
		oc.Info("realtime route removed — this release has no realtime process")
	}
	return 0, nil
}

// realtimeRoute is realtime.route: the realtime half of an app's route, changed at once — removed
// when the section is switched off, set when the live release's worker already publishes its port.
func realtimeRoute(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.RealtimeRouteParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	var rt *agentproto.RealtimeRoute
	if p.Enabled {
		rt = p.Route
	}
	hosts, err := setRealtime(oc, p.AppID, rt)
	if err != nil {
		return nil, 1, err
	}
	if rt == nil {
		oc.Info("realtime route removed; open connections end when their container stops")
		return agentproto.RealtimeRouteResult{Routed: false, Hosts: hosts}, 0, nil
	}
	oc.Info("realtime: %s* of %d hostname(s) → %s", rt.Path, hosts, rt.Upstream)
	return agentproto.RealtimeRouteResult{Routed: true, Hosts: hosts}, 0, nil
}

// changeRealtime is the table change behind setRealtime: only the realtime half of the app's entry.
// missing: the app has no route here (nothing was changed).
func changeRealtime(t *routeTable, appID string, rt *agentproto.RealtimeRoute) (hosts int, missing bool) {
	e, ok := t.Apps[appID]
	if !ok || e.Upstream == "" {
		return 0, true
	}
	e.Realtime = rt
	t.Apps[appID] = e
	return len(e.Hosts), false
}
