package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func TestLineBufferDropsOldest(t *testing.T) {
	b := newLineBuffer(3)
	for i := 0; i < 5; i++ {
		b.Push(DrainLine{Line: string(rune('a' + i))})
	}
	if got := b.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3", got)
	}
	lines, dropped := b.Drain()
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
	if len(lines) != 3 || lines[0].Line != "c" || lines[2].Line != "e" {
		t.Fatalf("lines = %+v, want the 3 most recent (c,d,e)", lines)
	}
	if b.Len() != 0 {
		t.Fatal("Drain should empty the buffer")
	}
	// A second Drain with nothing pushed reports zero, not a stale drop count.
	if _, dropped := b.Drain(); dropped != 0 {
		t.Fatalf("dropped after an empty period = %d, want 0", dropped)
	}
}

func TestBackoffDelay(t *testing.T) {
	if backoffDelay(0) != 0 {
		t.Error("0 failures: no delay")
	}
	if backoffDelay(1) != time.Second {
		t.Errorf("1 failure: got %s, want 1s", backoffDelay(1))
	}
	if backoffDelay(2) != 2*time.Second {
		t.Errorf("2 failures: got %s, want 2s", backoffDelay(2))
	}
	if got := backoffDelay(10); got != 60*time.Second {
		t.Errorf("10 failures: got %s, want capped at 60s", got)
	}
	if got := backoffDelay(1000); got != 60*time.Second {
		t.Errorf("many failures: got %s, want capped at 60s", got)
	}
}

func TestBuildSyslogLine(t *testing.T) {
	at := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	line := buildSyslogLine(DrainLine{At: at, Server: "srv-1", App: "myapp", Process: "web", Container: "hosbah-myapp-web-1", Line: "fatal: crashed"})
	if !strings.Contains(line, "2026-09-25T10:00:00Z") {
		t.Errorf("missing RFC 3339 timestamp: %s", line)
	}
	if !strings.Contains(line, "myapp:web") {
		t.Errorf("missing app:process token: %s", line)
	}
	if !strings.HasPrefix(line, "<11>1 ") { // facility 1 (user-level) * 8 + severity 3 (error, "fatal" matches)
		t.Errorf("bad PRI/version header: %s", line)
	}
	if !strings.Contains(line, "fatal: crashed") {
		t.Errorf("missing message: %s", line)
	}
	// A line with spaces or control characters in the hostname/app token is sanitised, not rejected.
	weird := buildSyslogLine(DrainLine{At: at, Server: "srv 1", App: "a b", Process: "c\td", Line: "ok"})
	if strings.Contains(weird, "srv 1") || strings.Contains(weird, "a b") {
		t.Errorf("tokens should not contain raw spaces: %s", weird)
	}
}

func TestBuildHTTPBatchNDJSON(t *testing.T) {
	lines := []DrainLine{
		{At: time.Unix(1, 0).UTC(), App: "a", Process: "web", Container: "c1", Stream: "stdout", Line: "hello"},
		{At: time.Unix(2, 0).UTC(), App: "a", Process: "web", Container: "c1", Stream: "stderr", Line: "panic: boom"},
	}
	body := buildHTTPBatch(lines)
	rows := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(rows) != 2 {
		t.Fatalf("got %d NDJSON rows, want 2", len(rows))
	}
	var first httpLogRecord
	if err := json.Unmarshal([]byte(rows[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Line != "hello" || first.Stream != "stdout" {
		t.Errorf("first row = %+v", first)
	}
	var second httpLogRecord
	if err := json.Unmarshal([]byte(rows[1]), &second); err != nil {
		t.Fatal(err)
	}
	if second.Level != agentproto.LogLevelError {
		t.Errorf("second row's level = %q, want error (heuristic on 'panic')", second.Level)
	}
}

func TestBuildLokiPayload(t *testing.T) {
	lines := []DrainLine{
		{At: time.Unix(2, 0).UTC(), Server: "s1", App: "a", Process: "web", Stream: "stdout", Line: "second"},
		{At: time.Unix(1, 0).UTC(), Server: "s1", App: "a", Process: "web", Stream: "stdout", Line: "first"},
		{At: time.Unix(3, 0).UTC(), Server: "s1", App: "a", Process: "worker:x", Stream: "stdout", Line: "other stream"},
	}
	req := buildLokiPayload(lines, map[string]string{"team": "t1"})
	if len(req.Streams) != 2 {
		t.Fatalf("got %d streams, want 2 (web and worker:x)", len(req.Streams))
	}
	web := req.Streams[0]
	if web.Stream["app"] != "a" || web.Stream["process"] != "web" || web.Stream["team"] != "t1" {
		t.Errorf("web stream labels = %+v", web.Stream)
	}
	if len(web.Values) != 2 {
		t.Fatalf("got %d values in the web stream, want 2", len(web.Values))
	}
	// Values within a stream must be sorted ascending by time — Loki refuses out-of-order entries.
	if web.Values[0][1] != "first" || web.Values[1][1] != "second" {
		t.Errorf("values not sorted by time: %+v", web.Values)
	}
	if web.Values[0][0] != "1000000000" {
		t.Errorf("timestamp not unix nanos: %s", web.Values[0][0])
	}
}

// postJudged's own logic (headers, status handling) is exercised directly against httptest's
// loopback listener with a plain client — shipHTTP/shipLoki always go through the real SSRF-safe
// dialer (monitors.SafeTransport), which refuses loopback addresses unconditionally (by design:
// "internal" only ever exempts private, RFC 1918-style addresses, never loopback), so they cannot
// be pointed at httptest directly. That refusal is covered by TestShipHTTPRefusesLoopback below.
func TestPostJudgedAgainstAFakeCollector(t *testing.T) {
	var gotAuth, gotBody, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	body := buildHTTPBatch([]DrainLine{{At: time.Now(), App: "a", Process: "web", Line: "hello from the test"}})
	err := postJudged(context.Background(), srv.Client(), false, srv.URL, "application/x-ndjson", body, authHeader("Authorization", "Bearer secret"))
	if err != nil {
		t.Fatalf("postJudged: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization header = %q", gotAuth)
	}
	if gotType != "application/x-ndjson" {
		t.Errorf("Content-Type = %q", gotType)
	}
	if !strings.Contains(gotBody, "hello from the test") {
		t.Errorf("body = %q", gotBody)
	}
}

func TestPostJudgedReportsANon2xxStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }))
	defer srv.Close()
	if err := postJudged(context.Background(), srv.Client(), false, srv.URL, "application/json", []byte("{}")); err == nil {
		t.Fatal("a 429 should be reported as an error")
	}
}

func TestPostJudgedLoki(t *testing.T) {
	var got lokiPushRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	body, _ := json.Marshal(buildLokiPayload([]DrainLine{{At: time.Now(), App: "a", Process: "web", Stream: "stdout", Line: "loki test"}}, nil))
	if err := postJudged(context.Background(), srv.Client(), false, srv.URL, "application/json", body); err != nil {
		t.Fatalf("postJudged: %v", err)
	}
	if len(got.Streams) != 1 || len(got.Streams[0].Values) != 1 || got.Streams[0].Values[0][1] != "loki test" {
		t.Fatalf("collector received %+v", got)
	}
}

func TestShipHTTPRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	// Loopback is refused even when the drain is marked internal — "internal" only ever exempts a
	// private (RFC 1918-style) address, never loopback, link-local or a metadata address.
	sink := agentproto.HTTPSink{URL: srv.URL}
	if err := shipHTTP(context.Background(), sink, true, "", []DrainLine{{Line: "x"}}); err == nil {
		t.Fatal("a loopback URL should always be refused")
	}
	if err := shipHTTP(context.Background(), sink, false, "", []DrainLine{{Line: "x"}}); err == nil {
		t.Fatal("a loopback URL should always be refused")
	}
}

func TestShipBatchDispatchesByKindAndRefusesUnreachableSinks(t *testing.T) {
	// shipBatch itself has no network of its own to fake (every real sink is judged by the SSRF-safe
	// dialer, which — correctly — refuses to dial a test's loopback listener); what it must get
	// right is picking the right shipper and being a no-op for an empty batch.
	if err := shipBatch(context.Background(), agentproto.DrainConfig{Kind: agentproto.DrainKindHTTP}, "", nil); err != nil {
		t.Fatalf("shipBatch with no lines should be a no-op: %v", err)
	}
	cfg := agentproto.DrainConfig{DrainID: "11111111-2222-4333-8444-555555555555", Kind: agentproto.DrainKindHTTP, HTTP: &agentproto.HTTPSink{URL: "http://127.0.0.1:1"}}
	if err := shipBatch(context.Background(), cfg, "", []DrainLine{{Line: "x"}}); err == nil {
		t.Fatal("a loopback sink should be refused")
	}
	if err := shipBatch(context.Background(), agentproto.DrainConfig{Kind: "bogus"}, "", []DrainLine{{Line: "x"}}); err == nil {
		t.Fatal("an unknown kind should error")
	}
}
