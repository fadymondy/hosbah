package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

const rtApp, rtDep = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"

func rtStart(process string, rt *agentproto.RealtimePublish) agentproto.StartParams {
	p := agentproto.StartParams{AppID: rtApp, App: "shop", DeploymentID: rtDep, Image: "cloudy/shop:" + rtDep, Process: process, Realtime: rt}
	if process == "web" {
		p.Port = 8080
	}
	return p
}

// The docker argv publishes a port for web and for the realtime worker, and for no other process.
func TestStartArgsPublishTheRealtimePortForThatProcessOnly(t *testing.T) {
	published := func(args []string) (values []string) {
		for i, a := range args {
			if a == "-p" && i+1 < len(args) {
				values = append(values, args[i+1])
			}
		}
		return
	}
	for _, tc := range []struct {
		p    agentproto.StartParams
		want []string
	}{
		{rtStart("web", nil), []string{"127.0.0.1::8080"}},
		{rtStart("worker:reverb", &agentproto.RealtimePublish{Port: 6001, Path: "/app"}), []string{"127.0.0.1::6001"}},
		{rtStart("worker:jobs", nil), nil},
	} {
		if err := tc.p.Validate(); err != nil {
			t.Fatalf("%s: %v", tc.p.Process, err)
		}
		args, pairs := startArgs(tc.p, "c1", map[string]string{"A": "1"})
		got := published(args)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s publishes %v, want %v", tc.p.Process, got, tc.want)
		}
		for _, v := range got { // loopback only, and the kernel picks the host port
			if !strings.HasPrefix(v, "127.0.0.1::") {
				t.Errorf("%s publishes on %q", tc.p.Process, v)
			}
		}
		if tc.p.Process != "web" && strings.Contains(strings.Join(pairs, " "), "PORT=") {
			t.Errorf("%s: PORT belongs to web only: %v", tc.p.Process, pairs)
		}
		if args[len(args)-1] != tc.p.Image { // nothing realtime adds comes after the image
			t.Errorf("%s: the argv ends with %q", tc.p.Process, args[len(args)-1])
		}
	}
}

// The agent validates again: the process name, the port and the path.
func TestStartParamsRefuseBadRealtime(t *testing.T) {
	pub := func(port int, path string) *agentproto.RealtimePublish {
		return &agentproto.RealtimePublish{Port: port, Path: path}
	}
	for name, p := range map[string]agentproto.StartParams{
		"on web":               rtStart("web", pub(6001, "/app")),
		"port 0":               rtStart("worker:reverb", pub(0, "/app")),
		"port too large":       rtStart("worker:reverb", pub(65536, "/app")),
		"a digit-first worker": rtStart("worker:1reverb", pub(6001, "/app")),
		"a dash-first worker":  rtStart("worker:-p", pub(6001, "/app")),
		"relative path":        rtStart("worker:reverb", pub(6001, "app")),
		"traversal":            rtStart("worker:reverb", pub(6001, "/app/../admin")),
		"not normalised":       rtStart("worker:reverb", pub(6001, "/app/")),
		"an option":            rtStart("worker:reverb", pub(6001, "/app -p 0.0.0.0:80:80")),
		"a placeholder":        rtStart("worker:reverb", pub(6001, "/{http.request.uri}")),
		"a wildcard":           rtStart("worker:reverb", pub(6001, "/app*")),
		"the root, unasked":    rtStart("worker:reverb", pub(6001, "/")),
	} {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := rtStart("worker:reverb", &agentproto.RealtimePublish{Port: 6001, Path: "/", WholeSite: true}).Validate(); err != nil {
		t.Errorf("the whole site, asked for: %v", err)
	}
}

// routesOf returns the routes of one Caddy server, through JSON: exactly what Caddy would be sent.
func routesOf(t *testing.T, cfg map[string]any, server string) []map[string]any {
	t.Helper()
	b, _ := json.Marshal(cfg)
	var doc struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []map[string]any `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Apps.HTTP.Servers[server].Routes
}

func rtTable() routeTable {
	return routeTable{Apps: map[string]routeEntry{
		rtApp: {Upstream: "127.0.0.1:40001", Hosts: []agentproto.Route{{Hostname: "shop.localhost", TLS: "off"},
			{Hostname: "shop.internal.test", TLS: "internal"}, {Hostname: "shop.example.com", TLS: "auto"}},
			Realtime: &agentproto.RealtimeRoute{Path: "/app", Upstream: "127.0.0.1:40002"}},
		"other": {Upstream: "127.0.0.1:40009", Hosts: []agentproto.Route{{Hostname: "other.localhost", TLS: "off"}}},
	}}
}

// The config Caddy is sent: for EVERY hostname of the site, the path route comes before the site's
// catch-all, with what a long-lived websocket needs.
func TestRenderCaddyRoutesTheRealtimePathBeforeTheCatchAll(t *testing.T) {
	cfg := renderCaddy(rtTable())
	for server, hosts := range map[string][]string{"cloudy_http": {"shop.localhost"}, "cloudy_https": {"shop.internal.test", "shop.example.com"}} {
		suffix := "-" + strings.TrimPrefix(server, "cloudy_")
		routes := routesOf(t, cfg, server)
		rtAt, appAt := -1, -1
		for i, r := range routes {
			switch r["@id"] {
			case agentproto.RealtimeRouteID(rtApp) + suffix:
				rtAt = i
			case agentproto.RouteID(rtApp) + suffix:
				appAt = i
			}
		}
		if rtAt < 0 || appAt < 0 || rtAt > appAt {
			t.Fatalf("%s: realtime route at %d, the app's at %d: the path route must come first", server, rtAt, appAt)
		}
		raw, _ := json.Marshal(routes[rtAt])
		s := string(raw)
		for _, h := range hosts {
			if !strings.Contains(s, `"`+h+`"`) {
				t.Errorf("%s: the realtime route lacks %s: %s", server, h, s)
			}
		}
		for _, want := range []string{`"path":["/app*"]`, `"dial":"127.0.0.1:40002"`, `"flush_interval":-1`, `"stream_close_delay":"24h"`,
			`"terminal":true`, `"handler":"reverse_proxy"`} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: the realtime route lacks %s: %s", server, want, s)
			}
		}
		// Nothing buffers, nothing rewrites the upgrade headers, nothing compresses.
		for _, never := range []string{"request_buffers", "response_buffers", `"headers"`, `"encode"`, "rewrite"} {
			if strings.Contains(s, never) {
				t.Errorf("%s: the realtime route has %s: %s", server, never, s)
			}
		}
		// The catch-all is untouched: no path matcher, web's upstream.
		app, _ := json.Marshal(routes[appAt])
		if strings.Contains(string(app), `"path"`) || !strings.Contains(string(app), `"dial":"127.0.0.1:40001"`) {
			t.Errorf("%s: the app's own route: %s", server, app)
		}
	}
	b, _ := json.Marshal(cfg)
	if strings.Count(string(b), "cloudy-realtime-") != 2 {
		t.Errorf("exactly the two realtime routes (http, https) of one app: %s", b)
	}
}

func TestRenderCaddyWholeSiteAndInvalidRealtime(t *testing.T) {
	tb := rtTable()
	e := tb.Apps[rtApp]
	e.Realtime = &agentproto.RealtimeRoute{Path: "/", Upstream: "127.0.0.1:40002", WholeSite: true}
	tb.Apps[rtApp] = e
	if b, _ := json.Marshal(renderCaddy(tb)); !strings.Contains(string(b), `"path":["/*"]`) {
		t.Errorf("the whole site: %s", b)
	}
	// A table on disk that was tampered with never reaches Caddy.
	for name, rt := range map[string]agentproto.RealtimeRoute{
		"a remote upstream": {Path: "/app", Upstream: "10.0.0.5:80"},
		"a placeholder":     {Path: "/{http.request.host}", Upstream: "127.0.0.1:40002"},
		"the root, unasked": {Path: "/", Upstream: "127.0.0.1:40002"},
	} {
		rt := rt
		e.Realtime = &rt
		tb.Apps[rtApp] = e
		if b, _ := json.Marshal(renderCaddy(tb)); strings.Contains(string(b), "cloudy-realtime-") {
			t.Errorf("%s was rendered: %s", name, b)
		}
	}
}

// realtime.route {enabled:false}: the path route goes, the site's own route stays.
func TestChangeRealtimeRemovesAndSetsOnlyTheRealtimeHalf(t *testing.T) {
	tb := rtTable()
	hosts, missing := changeRealtime(&tb, rtApp, nil)
	if missing || hosts != 3 {
		t.Fatalf("hosts %d, missing %v", hosts, missing)
	}
	b, _ := json.Marshal(renderCaddy(tb))
	s := string(b)
	if strings.Contains(s, "cloudy-realtime-") || strings.Contains(s, "40002") || strings.Contains(s, `"path"`) {
		t.Errorf("the realtime route lingers: %s", s)
	}
	if !strings.Contains(s, agentproto.RouteID(rtApp)+"-http") || !strings.Contains(s, agentproto.RouteID(rtApp)+"-https") || !strings.Contains(s, "other.localhost") {
		t.Errorf("the site's own routes must stay: %s", s)
	}

	// Set again, with another path, at the port that is already published.
	if _, missing := changeRealtime(&tb, rtApp, &agentproto.RealtimeRoute{Path: "/ws", Upstream: "127.0.0.1:40002"}); missing {
		t.Fatal("the app is routed")
	}
	if b, _ := json.Marshal(renderCaddy(tb)); !strings.Contains(string(b), `"path":["/ws*"]`) {
		t.Errorf("set again: %s", b)
	}
	// An app that is not routed here cannot get a realtime route.
	if _, missing := changeRealtime(&tb, "33333333-3333-4333-8333-333333333333", &agentproto.RealtimeRoute{Path: "/ws", Upstream: "127.0.0.1:1"}); !missing {
		t.Error("an unrouted app took a realtime route")
	}
	// route.set describes the whole route: without Realtime it is gone (setRoute replaces the entry).
	tb.Apps[rtApp] = routeEntry{Hosts: tb.Apps[rtApp].Hosts, Upstream: "127.0.0.1:40003"}
	if b, _ := json.Marshal(renderCaddy(tb)); strings.Contains(string(b), "cloudy-realtime-") {
		t.Errorf("a route.set without realtime keeps it: %s", b)
	}
}

func TestRealtimeRouteParamsValidate(t *testing.T) {
	ok := agentproto.RealtimeRoute{Path: "/app", Upstream: "127.0.0.1:40002"}
	route := func(path, upstream string) *agentproto.RealtimeRoute {
		return &agentproto.RealtimeRoute{Path: path, Upstream: upstream}
	}
	for name, p := range map[string]agentproto.RealtimeRouteParams{
		"bad app":               {AppID: "x", Enabled: false},
		"enable without route":  {AppID: rtApp, Enabled: true},
		"disable with a route":  {AppID: rtApp, Enabled: false, Route: &ok},
		"a remote upstream":     {AppID: rtApp, Enabled: true, Route: route("/app", "10.0.0.5:6001")},
		"an upstream with path": {AppID: rtApp, Enabled: true, Route: route("/app", "127.0.0.1:6001/x")},
		"port 0":                {AppID: rtApp, Enabled: true, Route: route("/app", "127.0.0.1:0")},
		"port 70000":            {AppID: rtApp, Enabled: true, Route: route("/app", "127.0.0.1:70000")},
		"traversal":             {AppID: rtApp, Enabled: true, Route: route("/app/../x", "127.0.0.1:6001")},
	} {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, p := range []agentproto.RealtimeRouteParams{{AppID: rtApp}, {AppID: rtApp, Enabled: true, Route: &ok}} {
		if err := p.Validate(); err != nil {
			t.Errorf("%+v: %v", p, err)
		}
	}
	op := agentproto.SignedOp{Kind: agentproto.KindRealtimeRoute, Params: json.RawMessage(`{"app_id":"` + rtApp + `"}`)}
	if laneOf(op) != "app:"+rtApp {
		t.Error("realtime.route runs in its app's lane, behind a deploy's switch")
	}
}
