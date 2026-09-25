package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func writeAccessLog(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "access.log")
	if err := os.WriteFile(p, []byte(joinLines(lines)), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func accessLine(host, uri string, status int, ts float64, dur float64, ua, ref, ip string) string {
	e := map[string]any{
		"ts": ts, "status": status, "size": 1234, "duration": dur,
		"request": map[string]any{
			"host": host, "uri": uri, "remote_ip": ip, "client_ip": ip,
			"headers": map[string][]string{"User-Agent": {ua}, "Referer": {ref}},
		},
	}
	b, _ := json.Marshal(e)
	return string(b)
}

// withRoutes points the package's route-table file at a fresh temp file holding one app's hosts,
// and restores the original path (and accessLogFile) on cleanup.
func withRoutes(t *testing.T, appID string, hosts ...string) {
	t.Helper()
	dir := t.TempDir()
	origRoutes := routesFile
	routesFile = func() string { return filepath.Join(dir, "routes.json") }
	t.Cleanup(func() { routesFile = origRoutes })
	var rs []agentproto.Route
	for _, h := range hosts {
		rs = append(rs, agentproto.Route{Hostname: h, TLS: "off"})
	}
	if err := saveRoutes(routeTable{Apps: map[string]routeEntry{appID: {Hosts: rs, Upstream: "127.0.0.1:8080"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestTrafficCollector_ParsesAndBucketsByApp(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	lines := []string{
		accessLine("app1.example.com", "/a?x=1", 200, float64(base.Unix()), 50, "ua1", "https://google.com/search?q=x", "1.1.1.1"),
		accessLine("app1.example.com", "/a", 200, float64(base.Unix())+1, 100, "ua2", "https://google.com/", "2.2.2.2"),
		accessLine("app1.example.com", "/b", 404, float64(base.Unix())+2, 20, "ua1", "", "1.1.1.1"), // same visitor as the first line
		accessLine("other.example.com", "/", 200, float64(base.Unix()), 10, "ua3", "", "3.3.3.3"),   // no app routed at this host: skipped
	}
	accessLogFile = func() string { return writeAccessLog(t, lines...) }
	t.Cleanup(func() { accessLogFile = func() string { return accessLogPath } })
	withRoutes(t, "app-1", "app1.example.com")

	c := newTrafficCollector()
	c.poll()
	buckets := c.collect(base.Add(2 * time.Minute))
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	b := buckets[0]
	if b.AppID != "app-1" {
		t.Errorf("app id = %q, want app-1", b.AppID)
	}
	if b.Requests != 3 {
		t.Errorf("requests = %d, want 3 (the unrouted host must not count)", b.Requests)
	}
	if b.Status2xx != 2 || b.Status4xx != 1 {
		t.Errorf("status classes = 2xx:%d 4xx:%d, want 2 and 1", b.Status2xx, b.Status4xx)
	}
	if b.UniqueVisitors != 2 {
		t.Errorf("unique visitors = %d, want 2 (1.1.1.1+ua1 repeats)", b.UniqueVisitors)
	}
	if len(b.TopPaths) != 2 {
		t.Fatalf("top paths = %v, want 2 distinct paths (/a and /b, the query string stripped)", b.TopPaths)
	}
	if b.TopPaths[0].Key != "/a" || b.TopPaths[0].Count != 2 {
		t.Errorf("top path[0] = %+v, want /a with count 2", b.TopPaths[0])
	}
	if len(b.TopReferrers) != 1 || b.TopReferrers[0].Key != "google.com" || b.TopReferrers[0].Count != 2 {
		t.Errorf("top referrers = %v, want [google.com:2] (both referrers collapse to the host)", b.TopReferrers)
	}
	if b.DurationP50Ms <= 0 || b.DurationP95Ms <= 0 {
		t.Errorf("expected non-zero p50/p95, got %v/%v", b.DurationP50Ms, b.DurationP95Ms)
	}
}

func TestTrafficCollector_CollectOnlyReturnsFinishedMinutes(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 5, 0, 0, time.UTC)
	accessLogFile = func() string {
		return writeAccessLog(t, accessLine("app1.example.com", "/", 200, float64(base.Unix()), 5, "ua", "", "9.9.9.9"))
	}
	t.Cleanup(func() { accessLogFile = func() string { return accessLogPath } })
	withRoutes(t, "app-1", "app1.example.com")

	c := newTrafficCollector()
	c.poll()
	if got := c.collect(base); len(got) != 0 {
		t.Fatalf("the current (still open) minute must not be collected yet, got %d buckets", len(got))
	}
	if got := c.collect(base.Add(time.Minute)); len(got) != 1 {
		t.Fatalf("the minute is over: expected 1 bucket, got %d", len(got))
	}
	if got := c.collect(base.Add(2 * time.Minute)); len(got) != 0 {
		t.Fatalf("a bucket must not be shipped twice, got %d", len(got))
	}
}

func TestReferrerHost(t *testing.T) {
	cases := map[string]string{
		"https://google.com/search?q=x": "google.com",
		"http://sub.example.com/path":   "sub.example.com",
		"":                              "",
		"not-a-url":                     "not-a-url",
	}
	for in, want := range cases {
		if got := referrerHost(in); got != want {
			t.Errorf("referrerHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVisitorHashNeverStoresRawInput(t *testing.T) {
	h := visitorHash("1.2.3.4", "Mozilla/5.0")
	if len(h) != 64 {
		t.Fatalf("expected a 32-byte hex hash, got %d chars", len(h))
	}
	if h == "1.2.3.4Mozilla/5.0" {
		t.Fatal("hash must not equal the raw input")
	}
}

func TestHostAppOf_SharedHostGoesToTheAppCaddyServes(t *testing.T) {
	rt := routeTable{Apps: map[string]routeEntry{
		"b-app": {Hosts: []agentproto.Route{{Hostname: "shared.example.com"}, {Hostname: "b.example.com"}}},
		"a-app": {Hosts: []agentproto.Route{{Hostname: "Shared.example.com"}}},
	}}
	for i := 0; i < 20; i++ { // map order varies run to run; the answer must not
		m := hostAppOf(rt)
		if m["shared.example.com"] != "a-app" || m["b.example.com"] != "b-app" {
			t.Fatalf("hostAppOf = %v, want shared->a-app (first in sorted order), b->b-app", m)
		}
	}
}

func TestAccessEventIP_ForwardedOnlyBehindPrivateProxy(t *testing.T) {
	ev := func(remote string, h map[string][]string) accessEvent {
		var e accessEvent
		e.Request.RemoteIP, e.Request.ClientIP, e.Request.Headers = remote, remote, h
		return e
	}
	fwd := map[string][]string{"X-Forwarded-For": {"105.1.2.3, 172.71.0.1"}}
	if got := ev("10.10.10.100", fwd).ip(); got != "105.1.2.3" {
		t.Errorf("behind a private proxy ip = %q, want the forwarded client", got)
	}
	if got := ev("10.10.10.100", map[string][]string{"Cf-Connecting-Ip": {"9.9.9.9"}, "X-Real-Ip": {"8.8.8.8"}}).ip(); got != "9.9.9.9" {
		t.Errorf("ip = %q, want Cf-Connecting-Ip first", got)
	}
	if got := ev("203.0.113.5", fwd).ip(); got != "203.0.113.5" {
		t.Errorf("from a public peer ip = %q, want the peer (forwarded headers are spoofable there)", got)
	}
}

func TestTrafficCollector_LeavesAPartialLastLineForNextPoll(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	full := accessLine("app1.example.com:443", "/a", 200, float64(base.Unix()), 5, "ua", "", "1.1.1.1")
	next := accessLine("app1.example.com", "/b", 200, float64(base.Unix())+1, 5, "ua", "", "1.1.1.1")
	p := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(p, []byte(full+"\n"+next[:10]), 0o600); err != nil {
		t.Fatal(err)
	}
	accessLogFile = func() string { return p }
	t.Cleanup(func() { accessLogFile = func() string { return accessLogPath } })
	withRoutes(t, "app-1", "app1.example.com")

	c := newTrafficCollector()
	c.poll()
	if err := os.WriteFile(p, []byte(full+"\n"+next+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.poll()
	b := c.collect(base.Add(2 * time.Minute))
	if len(b) != 1 || b[0].Requests != 2 {
		t.Fatalf("buckets = %+v, want one bucket with 2 requests (host:port matched, the half line read once finished)", b)
	}
}
