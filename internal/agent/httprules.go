package agent

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Per-site HTTP rules (MH-578 E4-E6). rules.set replaces the whole ruleset of one target — an app
// (routeEntry.Rules) or a proxy host (proxyEntry.Rules) — the same shape as cache.set: the agent
// looks the target's hosts up in the route table rather than being told them again, and renderCaddy
// (caddy.go) is the only place that turns Rules into actual Caddy config, via renderTargetRoutes
// below. A target with no rules (Rules == nil, or Empty()) renders byte-identical to before this
// feature existed — renderTargetRoutes returns exactly the one route the old `route()` helper built.

// httpRulesSet handles rules.set.
func httpRulesSet(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.HTTPRulesSetParams](oc)
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
	rules := p.Rules
	switch p.TargetType {
	case agentproto.HTTPRuleTargetApp:
		if _, ok := t.Apps[p.TargetID]; !ok {
			return nil, 2, errors.New("this app has no route on this server yet — deploy it first")
		}
	case agentproto.HTTPRuleTargetProxy:
		if _, ok := t.Proxies[p.TargetID]; !ok {
			return nil, 2, errors.New("this proxy host has no route on this server yet")
		}
	default:
		return nil, 2, errors.New("bad target_type")
	}

	if err := applyRoutes(oc, func(t *routeTable) {
		switch p.TargetType {
		case agentproto.HTTPRuleTargetApp:
			e := t.Apps[p.TargetID]
			if rules.Empty() {
				e.Rules = nil
			} else {
				e.Rules = &rules
			}
			t.Apps[p.TargetID] = e
		case agentproto.HTTPRuleTargetProxy:
			e := t.Proxies[p.TargetID]
			if rules.Empty() {
				e.Rules = nil
			} else {
				e.Rules = &rules
			}
			t.Proxies[p.TargetID] = e
		}
	}); err != nil {
		return nil, 1, err
	}
	oc.Info("applied HTTP rules for this %s", p.TargetType)
	return map[string]any{"applied": true}, 0, nil
}

// renderTargetRoutes builds every Caddy route needed to serve hosts through mainHandlers (already
// fully built by the caller — the real reverse_proxy handler, optionally preceded by a
// forwardAuthHandler that gates it), honouring rules. baseID is the @id the single route would have
// had before this feature existed (RouteID(id)+suffix for an app, ProxyRouteID(id) for a proxy
// host) — when rules is nil/Empty, the return is exactly that one route, byte-identical to the old
// behaviour (mainHandlers is almost always a single-element slice; a forward_auth'd proxy host is
// the only caller that passes two).
func renderTargetRoutes(baseID string, hosts []string, mainHandlers []any, rules *agentproto.HTTPRules) []any {
	if rules == nil || rules.Empty() {
		return []any{simpleRoute(baseID, hosts, mainHandlers)}
	}

	var routes []any

	// 1) Access rules (IP allow/deny, bad bots) come first: a blocked request never reaches a
	// redirect, basic auth, or the upstream.
	if block := accessBlockRoute(baseID, hosts, rules.Access); block != nil {
		routes = append(routes, block)
	}

	// 2) Redirects: each is its own terminal route, checked in the order they were saved.
	for i, r := range rules.Redirects {
		if !agentproto.ValidHTTPRulePath(r.From) {
			continue // defense in depth: never trust a hand-edited/corrupt routes.json
		}
		routes = append(routes, redirectRoute(fmt.Sprintf("%s-redirect-%d", baseID, i), hosts, r))
	}

	// 3) Everything else finally reaches mainHandlers, wrapped with response headers and, unless the
	// path is exempt, basic auth.
	routes = append(routes, servingRoutes(baseID, hosts, mainHandlers, rules)...)
	return routes
}

func simpleRoute(id string, hosts []string, handlers []any) map[string]any {
	return map[string]any{
		"@id":      id,
		"match":    []any{map[string]any{"host": hosts}},
		"terminal": true,
		"handle":   handlers,
	}
}

// accessBlockRoute returns a terminal 403 route matched by whichever access condition should refuse
// the request, or nil when there is nothing to block on.
func accessBlockRoute(baseID string, hosts []string, a *agentproto.AccessRules) map[string]any {
	if a == nil {
		return nil
	}
	var matches []any
	if len(a.IPDeny) > 0 {
		if ranges := validCIDRs(a.IPDeny); len(ranges) > 0 {
			matches = append(matches, map[string]any{"host": hosts, "remote_ip": map[string]any{"ranges": ranges}})
		}
	}
	if len(a.IPAllow) > 0 {
		if ranges := validCIDRs(a.IPAllow); len(ranges) > 0 {
			matches = append(matches, map[string]any{"host": hosts, "not": []any{map[string]any{"remote_ip": map[string]any{"ranges": ranges}}}})
		}
	}
	if a.BlockBadBots {
		matches = append(matches, map[string]any{"host": hosts, "header_regexp": map[string]any{"User-Agent": map[string]any{"pattern": badBotPattern()}}})
	}
	if len(matches) == 0 {
		return nil
	}
	return map[string]any{
		"@id":      baseID + "-block",
		"match":    matches,
		"terminal": true,
		"handle":   []any{map[string]any{"handler": "static_response", "status_code": 403, "body": "Forbidden\n"}},
	}
}

// validCIDRs re-checks every entry with the same validator used on both sides of the wire
// (agentproto.ValidCIDR), so a corrupt or hand-edited routes.json can never turn into bad Caddy
// config even if something upstream forgot to validate.
func validCIDRs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, c := range in {
		if agentproto.ValidCIDR(c) {
			out = append(out, agentproto.NormalizeCIDR(c))
		}
	}
	return out
}

// badBotPattern builds one case-insensitive alternation regex out of agentproto.BadBotUserAgents.
// Every entry is a fixed, curated literal (see that list) — never user input — so no escaping is
// needed beyond the regex metacharacters those literals happen to contain (there are none today).
func badBotPattern() string {
	return "(?i)(" + strings.Join(agentproto.BadBotUserAgents, "|") + ")"
}

// redirectRoute builds one terminal redirect route for a RedirectRule.
func redirectRoute(id string, hosts []string, r agentproto.RedirectRule) map[string]any {
	match := map[string]any{"host": hosts, "path": []string{r.From}}
	loc := r.To
	if r.PreserveQuery {
		if strings.Contains(loc, "?") {
			loc += "&{http.request.uri.query}"
		} else {
			loc += "?{http.request.uri.query}"
		}
	}
	return map[string]any{
		"@id":      id,
		"match":    []any{match},
		"terminal": true,
		"handle": []any{map[string]any{
			"handler":     "static_response",
			"status_code": r.EffectiveCode(),
			"headers":     map[string]any{"Location": []string{loc}},
		}},
	}
}

// servingRoutes builds the route(s) that end in mainHandlers: an always-open exempt route (if basic
// auth is on and has exempt paths), then either a path-scoped auth route plus a plain catch-all, or
// a single route (auth-gated whole-site, or no auth at all).
func servingRoutes(baseID string, hosts []string, mainHandlers []any, rules *agentproto.HTTPRules) []any {
	ba := rules.BasicAuth
	chain := func(withAuth bool) []any {
		var h []any
		if withAuth && ba != nil {
			h = append(h, basicAuthHandler(ba))
		}
		h = append(h, headerHandlers(rules.Headers)...)
		h = append(h, mainHandlers...)
		return h
	}

	if ba == nil || !ba.Enabled {
		return []any{map[string]any{
			"@id": baseID, "match": []any{map[string]any{"host": hosts}}, "terminal": true, "handle": chain(false),
		}}
	}

	var routes []any
	if len(ba.ExemptPaths) > 0 {
		paths := validPaths(ba.ExemptPaths)
		if len(paths) > 0 {
			routes = append(routes, map[string]any{
				"@id":      baseID + "-exempt",
				"match":    []any{map[string]any{"host": hosts, "path": paths}},
				"terminal": true,
				"handle":   chain(false),
			})
		}
	}

	prefix := strings.TrimSpace(ba.PathPrefix)
	if prefix == "" || prefix == "/" {
		routes = append(routes, map[string]any{
			"@id": baseID, "match": []any{map[string]any{"host": hosts}}, "terminal": true, "handle": chain(true),
		})
		return routes
	}
	if !agentproto.ValidHTTPRulePath(prefix) {
		// defense in depth: a corrupt prefix protects nothing rather than risk malformed config
		routes = append(routes, map[string]any{
			"@id": baseID, "match": []any{map[string]any{"host": hosts}}, "terminal": true, "handle": chain(false),
		})
		return routes
	}
	pathMatch := prefix
	if !strings.HasSuffix(pathMatch, "*") {
		pathMatch = strings.TrimSuffix(pathMatch, "/") + "/*"
	}
	routes = append(routes, map[string]any{
		"@id":      baseID + "-auth",
		"match":    []any{map[string]any{"host": hosts, "path": []string{pathMatch}}},
		"terminal": true,
		"handle":   chain(true),
	})
	routes = append(routes, map[string]any{
		"@id": baseID, "match": []any{map[string]any{"host": hosts}}, "terminal": true, "handle": chain(false),
	})
	return routes
}

func validPaths(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		if agentproto.ValidHTTPRulePath(p) {
			out = append(out, p)
		}
	}
	return out
}

// basicAuthHandler builds Caddy's authentication handler with the http_basic provider. Every
// account's password is a pre-computed bcrypt hash (agentproto.ValidBcryptHash on both sides of the
// wire refuses anything else) — never a plaintext password, and never hashed here.
func basicAuthHandler(ba *agentproto.BasicAuthRule) map[string]any {
	users := append([]agentproto.BasicAuthUser{}, ba.Users...)
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	accounts := make([]any, 0, len(users))
	for _, u := range users {
		if !agentproto.ValidBasicAuthUsername(u.Username) || !agentproto.ValidBcryptHash(u.BcryptHash) {
			continue // defense in depth
		}
		accounts = append(accounts, map[string]any{"username": u.Username, "password": u.BcryptHash})
	}
	return map[string]any{
		"handler": "authentication",
		"providers": map[string]any{
			"http_basic": map[string]any{
				"accounts": accounts,
				"hash":     map[string]any{"algorithm": "bcrypt"},
			},
		},
	}
}

// headerHandlers builds at most one "headers" middleware handler (Caddy allows one set/add/delete
// block per handler); returns nil when there is nothing to change.
func headerHandlers(rules []agentproto.HeaderRule) []any {
	if len(rules) == 0 {
		return nil
	}
	set := map[string]any{}
	add := map[string]any{}
	var del []string
	for _, r := range rules {
		if !agentproto.ValidHeaderName(r.Name) {
			continue // defense in depth
		}
		switch r.Action {
		case agentproto.HeaderActionSet:
			set[r.Name] = []string{r.Value}
		case agentproto.HeaderActionAdd:
			add[r.Name] = []string{r.Value}
		case agentproto.HeaderActionRemove:
			del = append(del, r.Name)
		}
	}
	if len(set) == 0 && len(add) == 0 && len(del) == 0 {
		return nil
	}
	resp := map[string]any{}
	if len(set) > 0 {
		resp["set"] = set
	}
	if len(add) > 0 {
		resp["add"] = add
	}
	if len(del) > 0 {
		resp["delete"] = del
	}
	return []any{map[string]any{"handler": "headers", "response": map[string]any{"headers": resp}}}
}
