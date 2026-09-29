package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// A target with no rules renders byte-identical to the pre-MH-578 single route, both when Rules is
// nil and when it is a non-nil but empty value (e.g. every list emptied by an update).
func TestRenderTargetRoutesNoRulesIsByteIdentical(t *testing.T) {
	handler := map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": "127.0.0.1:4000"}}}
	want := []any{simpleRoute("hosbah-app-a-http", []string{"go.localhost"}, []any{handler})}

	got := renderTargetRoutes("hosbah-app-a-http", []string{"go.localhost"}, []any{handler}, nil)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nil rules: got %#v, want %#v", got, want)
	}

	empty := &agentproto.HTTPRules{}
	got = renderTargetRoutes("hosbah-app-a-http", []string{"go.localhost"}, []any{handler}, empty)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("empty rules: got %#v, want %#v", got, want)
	}
}

// renderCaddy for a whole route table is unaffected by the Rules field when it is unset — the
// end-to-end version of the test above, through the same path production traffic takes.
func TestRenderCaddyUnaffectedByAbsentRules(t *testing.T) {
	table := routeTable{
		AskURL: "http://cp.test/internal/tls/allow",
		Apps: map[string]routeEntry{
			"a": {Upstream: "127.0.0.1:40001", Hosts: []agentproto.Route{{Hostname: "go.localhost", TLS: "off"}}},
		},
	}
	withNilRules := renderCaddy(table, true)

	e := table.Apps["a"]
	e.Rules = &agentproto.HTTPRules{}
	table.Apps["a"] = e
	withEmptyRules := renderCaddy(table, true)

	a, _ := json.Marshal(withNilRules)
	b, _ := json.Marshal(withEmptyRules)
	if string(a) != string(b) {
		t.Fatalf("an empty (but non-nil) Rules changed the rendered config:\nnil:   %s\nempty: %s", a, b)
	}
}

func TestRedirectRoute(t *testing.T) {
	hosts := []string{"shop.example.com"}
	r := agentproto.RedirectRule{From: "/old", To: "https://new.example.com/here", Code: 301, PreserveQuery: true}
	route := redirectRoute("hosbah-app-a-http-redirect-0", hosts, r)
	b, _ := json.Marshal(route)
	s := string(b)
	for _, want := range []string{
		`"status_code":301`,
		`"Location":["https://new.example.com/here?{http.request.uri.query}"]`,
		`"path":["/old"]`,
		`"terminal":true`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("redirect route lacks %s:\n%s", want, s)
		}
	}
}

func TestRedirectRouteDefaultCodeAndNoPreserveQuery(t *testing.T) {
	r := agentproto.RedirectRule{From: "/old", To: "/new"}
	route := redirectRoute("id", []string{"h"}, r)
	b, _ := json.Marshal(route)
	s := string(b)
	if !strings.Contains(s, `"status_code":302`) {
		t.Errorf("default redirect code should be 302: %s", s)
	}
	if strings.Contains(s, "http.request.uri.query") {
		t.Errorf("preserve_query=false should not append the query placeholder: %s", s)
	}
}

func TestHeaderHandlersSetAddRemove(t *testing.T) {
	rules := []agentproto.HeaderRule{
		{Action: "set", Name: "Strict-Transport-Security", Value: "max-age=63072000"},
		{Action: "add", Name: "X-Frame-Options", Value: "DENY"},
		{Action: "remove", Name: "Server"},
	}
	h := headerHandlers(rules)
	if len(h) != 1 {
		t.Fatalf("expected exactly one headers handler, got %d", len(h))
	}
	b, _ := json.Marshal(h[0])
	s := string(b)
	for _, want := range []string{
		`"handler":"headers"`,
		`"Strict-Transport-Security":["max-age=63072000"]`,
		`"X-Frame-Options":["DENY"]`,
		`"delete":["Server"]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("header handler lacks %s:\n%s", want, s)
		}
	}
}

func TestHeaderHandlersEmpty(t *testing.T) {
	if h := headerHandlers(nil); h != nil {
		t.Errorf("no header rules should build no handler, got %#v", h)
	}
}

func TestAccessBlockRouteIPDenyAndBadBots(t *testing.T) {
	route := accessBlockRoute("hosbah-app-a-http", []string{"go.localhost"}, &agentproto.AccessRules{
		IPDeny: []string{"10.0.0.0/8"}, BlockBadBots: true,
	})
	if route == nil {
		t.Fatal("expected a block route")
	}
	b, _ := json.Marshal(route)
	s := string(b)
	for _, want := range []string{
		`"status_code":403`,
		`"ranges":["10.0.0.0/8"]`,
		`"User-Agent"`,
		`"terminal":true`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("block route lacks %s:\n%s", want, s)
		}
	}
}

func TestAccessBlockRouteIPAllowInverts(t *testing.T) {
	route := accessBlockRoute("id", []string{"h"}, &agentproto.AccessRules{IPAllow: []string{"192.168.1.0/24"}})
	b, _ := json.Marshal(route)
	s := string(b)
	if !strings.Contains(s, `"not":[{"remote_ip":{"ranges":["192.168.1.0/24"]}}]`) {
		t.Errorf("ip_allow should be a negated match: %s", s)
	}
}

func TestAccessBlockRouteNilWhenNothingToBlock(t *testing.T) {
	if route := accessBlockRoute("id", []string{"h"}, nil); route != nil {
		t.Errorf("no access rules should build no block route, got %#v", route)
	}
	if route := accessBlockRoute("id", []string{"h"}, &agentproto.AccessRules{}); route != nil {
		t.Errorf("empty access rules should build no block route, got %#v", route)
	}
}

func TestServingRoutesBasicAuthWholeSite(t *testing.T) {
	handler := map[string]any{"handler": "reverse_proxy"}
	rules := &agentproto.HTTPRules{BasicAuth: &agentproto.BasicAuthRule{
		Enabled: true,
		Users:   []agentproto.BasicAuthUser{{Username: "alice", BcryptHash: "$2a$10$" + strings.Repeat("a", 53)}},
	}}
	routes := servingRoutes("hosbah-app-a-http", []string{"go.localhost"}, []any{handler}, rules)
	if len(routes) != 1 {
		t.Fatalf("whole-site auth should be a single route, got %d", len(routes))
	}
	b, _ := json.Marshal(routes[0])
	s := string(b)
	if !strings.Contains(s, `"handler":"authentication"`) || !strings.Contains(s, `"alice"`) {
		t.Errorf("route missing basic auth: %s", s)
	}
}

func TestServingRoutesBasicAuthExemptPaths(t *testing.T) {
	handler := map[string]any{"handler": "reverse_proxy"}
	rules := &agentproto.HTTPRules{BasicAuth: &agentproto.BasicAuthRule{
		Enabled:     true,
		ExemptPaths: []string{"/api/health"},
		Users:       []agentproto.BasicAuthUser{{Username: "alice", BcryptHash: "$2a$10$" + strings.Repeat("a", 53)}},
	}}
	routes := servingRoutes("id", []string{"h"}, []any{handler}, rules)
	if len(routes) != 2 {
		t.Fatalf("exempt path + auth-gated route, got %d: %#v", len(routes), routes)
	}
	exempt, _ := json.Marshal(routes[0])
	if strings.Contains(string(exempt), "authentication") {
		t.Errorf("exempt route must not require auth: %s", exempt)
	}
	if !strings.Contains(string(exempt), `"/api/health"`) {
		t.Errorf("exempt route missing its path: %s", exempt)
	}
}

func TestServingRoutesBasicAuthPathPrefix(t *testing.T) {
	handler := map[string]any{"handler": "reverse_proxy"}
	rules := &agentproto.HTTPRules{BasicAuth: &agentproto.BasicAuthRule{
		Enabled:    true,
		PathPrefix: "/admin",
		Users:      []agentproto.BasicAuthUser{{Username: "alice", BcryptHash: "$2a$10$" + strings.Repeat("a", 53)}},
	}}
	routes := servingRoutes("hosbah-app-a-http", []string{"h"}, []any{handler}, rules)
	if len(routes) != 2 {
		t.Fatalf("prefix-scoped auth route + plain catch-all, got %d", len(routes))
	}
	authRoute, _ := json.Marshal(routes[0])
	if !strings.Contains(string(authRoute), `"/admin/*"`) || !strings.Contains(string(authRoute), "authentication") {
		t.Errorf("first route should be the prefix-scoped auth route: %s", authRoute)
	}
	plain, _ := json.Marshal(routes[1])
	if strings.Contains(string(plain), "authentication") {
		t.Errorf("catch-all route must not require auth: %s", plain)
	}
}

func TestServingRoutesNoAuth(t *testing.T) {
	handler := map[string]any{"handler": "reverse_proxy"}
	rules := &agentproto.HTTPRules{Headers: []agentproto.HeaderRule{{Action: "set", Name: "X-Test", Value: "1"}}}
	routes := servingRoutes("id", []string{"h"}, []any{handler}, rules)
	if len(routes) != 1 {
		t.Fatalf("no basic auth should be a single route, got %d", len(routes))
	}
	b, _ := json.Marshal(routes[0])
	if strings.Contains(string(b), "authentication") {
		t.Errorf("no basic auth configured, route should not require it: %s", b)
	}
}

// Bad values that slipped past validation on the way in (a corrupt or hand-edited routes.json) must
// never make it into the rendered Caddy config.
func TestDefenseInDepthAgainstBadValues(t *testing.T) {
	if h := headerHandlers([]agentproto.HeaderRule{{Action: "set", Name: "X: evil\r\nSet-Cookie", Value: "1"}}); h != nil {
		t.Errorf("an invalid header name must be dropped, got %#v", h)
	}
	if got := validCIDRs([]string{"not-a-cidr", "10.0.0.0/8"}); len(got) != 1 || got[0] != "10.0.0.0/8" {
		t.Errorf("an invalid CIDR must be dropped, got %#v", got)
	}
	if got := validPaths([]string{"relative/path", "/ok"}); len(got) != 1 || got[0] != "/ok" {
		t.Errorf("a path that does not start with / must be dropped, got %#v", got)
	}
	ba := basicAuthHandler(&agentproto.BasicAuthRule{Users: []agentproto.BasicAuthUser{
		{Username: "ok", BcryptHash: "$2a$10$" + strings.Repeat("a", 53)},
		{Username: "bad", BcryptHash: "not-a-bcrypt-hash"},
	}})
	b, _ := json.Marshal(ba)
	if strings.Contains(string(b), "not-a-bcrypt-hash") || strings.Contains(string(b), `"bad"`) {
		t.Errorf("a bad bcrypt hash must be dropped, got %s", b)
	}
}
