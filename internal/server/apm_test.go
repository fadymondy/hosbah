package server

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

/*
APM: traces and exceptions (MH-575 H7) end to end: enabling mints a secret ingest key shown once,
the public OTLP/HTTP JSON ingest endpoints accept a valid traces payload (a root span plus an error
span carrying an "exception" event) and a logs payload (an ERROR-severity record), authenticated by
`Authorization: Bearer hbt_...` or `x-hosbah-key`. The team API then reflects that traffic: an
endpoints summary, a trace waterfall, and grouped exceptions (two identical exceptions collapse into
one issue with count 2). Team isolation: another team cannot read team A's APM data. The MCP tools
mirror the REST surface.
*/
func TestAppApmTracesAndExceptions(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner := newClient(t, ts.URL)
	owner.register("apm-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "APM " + suffix}, 201, &team)

	stranger := newClient(t, ts.URL)
	stranger.register("apm-stranger-" + suffix + "@example.test")
	var teamB struct{ ID string }
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere APM " + suffix}, 201, &teamB)

	srv, _ := enrolServer(t, owner, ts.URL, team.ID, "apm-"+short)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "apm-app-" + short, "server": srv,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	base := "/api/teams/" + team.ID + "/apps/" + app.App.ID + "/apm"

	// Not enabled yet: no key.
	var settings struct {
		Enabled bool `json:"enabled"`
		HasKey  bool `json:"has_key"`
	}
	owner.mustJSON("GET", base+"/settings", nil, 200, &settings)
	if settings.Enabled || settings.HasKey {
		t.Fatalf("a new app should start with APM off and no key: %+v", settings)
	}

	// A viewer may read settings but not enable APM.
	viewerEmail := "apm-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)
	if code, _ := viewer.do("PUT", base+"/settings", map[string]any{"enabled": true}); code != 403 {
		t.Fatalf("viewer enabling APM -> %d, want 403", code)
	}

	// Owner enables it: a key and a snippet come back once, and never again after that.
	var enabled struct {
		Enabled bool    `json:"enabled"`
		HasKey  bool    `json:"has_key"`
		Key     *string `json:"key"`
		Snippet *string `json:"snippet"`
	}
	owner.mustJSON("PUT", base+"/settings", map[string]any{"enabled": true}, 200, &enabled)
	if !enabled.Enabled || !enabled.HasKey || enabled.Key == nil || *enabled.Key == "" || enabled.Snippet == nil || !strings.Contains(*enabled.Snippet, *enabled.Key) {
		t.Fatalf("enabling APM should mint and show a key once: %+v", enabled)
	}
	key := *enabled.Key
	if !strings.HasPrefix(key, "hbt_") {
		t.Fatalf("the ingest key should have the hbt_ prefix, got %q", key)
	}
	owner.mustJSON("GET", base+"/settings", nil, 200, &settings)
	if !settings.Enabled || !settings.HasKey {
		t.Fatalf("settings after enabling: %+v", settings)
	}

	otlpPost := func(t *testing.T, path, k string, body []byte, useHeader bool) int {
		t.Helper()
		req, err := http.NewRequest("POST", ts.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if useHeader {
			req.Header.Set("x-hosbah-key", k)
		} else {
			req.Header.Set("Authorization", "Bearer "+k)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	traceID1 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := time.Now().UnixNano()
	nano := func(offsetMs int64) string { return fmt.Sprintf("%d", now+offsetMs*1_000_000) }
	tracesPayload := func(traceID string, includeException bool) []byte {
		exceptionEvent := ""
		if includeException {
			exceptionEvent = fmt.Sprintf(`,"events":[{"name":"exception","timeUnixNano":%q,"attributes":[
				{"key":"exception.type","value":{"stringValue":"PaymentError"}},
				{"key":"exception.message","value":{"stringValue":"card declined for order 12345"}},
				{"key":"exception.stacktrace","value":{"stringValue":"at charge (payment.go:42)\nat handle (orders.go:10)"}}
			]}]`, nano(2))
		}
		return []byte(fmt.Sprintf(`{
			"resourceSpans": [{
				"resource": {"attributes": [{"key":"service.name","value":{"stringValue":"apm-test-svc"}}]},
				"scopeSpans": [{"spans": [
					{
						"traceId": %q, "spanId": "root0001", "parentSpanId": "",
						"name": "GET /orders", "kind": 2,
						"startTimeUnixNano": %q, "endTimeUnixNano": %q,
						"attributes": [
							{"key":"http.request.method","value":{"stringValue":"GET"}},
							{"key":"http.route","value":{"stringValue":"/orders"}},
							{"key":"http.response.status_code","value":{"intValue":"200"}}
						]
					},
					{
						"traceId": %q, "spanId": "child001", "parentSpanId": "root0001",
						"name": "payment.charge", "kind": 1,
						"startTimeUnixNano": %q, "endTimeUnixNano": %q
						%s
					}
				]}]
			}]
		}`, traceID, nano(0), nano(250), traceID, nano(10), nano(200), exceptionEvent))
	}
	logsPayload := []byte(fmt.Sprintf(`{
		"resourceLogs": [{
			"scopeLogs": [{"logRecords": [{
				"severityNumber": 17, "severityText": "ERROR", "timeUnixNano": %q,
				"body": {"stringValue": "background job failed"},
				"attributes": [
					{"key":"exception.type","value":{"stringValue":"JobError"}},
					{"key":"exception.message","value":{"stringValue":"queue worker crashed"}}
				]
			}]}]
		}]
	}`, nano(5)))

	// A bad key is rejected with 401.
	if code := otlpPost(t, "/api/otlp/v1/traces", "hbt_doesnotexist00000000000000000000000000", tracesPayload(traceID1, false), false); code != 401 {
		t.Fatalf("unknown key -> %d, want 401", code)
	}
	// A malformed body is rejected with 400.
	if code := otlpPost(t, "/api/otlp/v1/traces", key, []byte("not json"), false); code != 400 {
		t.Fatalf("malformed body -> %d, want 400", code)
	}
	// A valid traces payload with an exception, via the Authorization header.
	if code := otlpPost(t, "/api/otlp/v1/traces", key, tracesPayload(traceID1, true), false); code != 200 {
		t.Fatalf("valid traces ingest -> %d, want 200", code)
	}
	// The same exception again, via the x-hosbah-key header this time — should collapse into the same issue.
	traceID2 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if code := otlpPost(t, "/api/otlp/v1/traces", key, tracesPayload(traceID2, true), true); code != 200 {
		t.Fatalf("second traces ingest (x-hosbah-key) -> %d, want 200", code)
	}
	// A logs payload with an ERROR-severity record.
	if code := otlpPost(t, "/api/otlp/v1/logs", key, logsPayload, false); code != 200 {
		t.Fatalf("valid logs ingest -> %d, want 200", code)
	}

	// The endpoints summary reflects the root span traffic.
	var epSummary struct {
		Endpoints []struct {
			Method  string  `json:"method"`
			Route   string  `json:"route"`
			Samples int64   `json:"samples"`
			P50Ms   float64 `json:"p50_ms"`
		} `json:"endpoints"`
	}
	owner.mustJSON("GET", base+"/endpoints?window=24h", nil, 200, &epSummary)
	found := false
	for _, e := range epSummary.Endpoints {
		if e.Method == "GET" && e.Route == "/orders" {
			found = true
			if e.Samples != 2 {
				t.Fatalf("expected 2 samples for GET /orders, got %d", e.Samples)
			}
		}
	}
	if !found {
		t.Fatalf("expected GET /orders in the endpoints summary: %+v", epSummary.Endpoints)
	}

	// A trace's waterfall.
	var trace struct {
		Spans []struct {
			SpanID string `json:"span_id"`
			Name   string `json:"name"`
			Error  bool   `json:"error"`
		} `json:"spans"`
	}
	owner.mustJSON("GET", base+"/traces/"+traceID1, nil, 200, &trace)
	if len(trace.Spans) != 2 {
		t.Fatalf("expected 2 stored spans (root + error) for the trace, got %d: %+v", len(trace.Spans), trace.Spans)
	}
	var sawError bool
	for _, s := range trace.Spans {
		if s.Error {
			sawError = true
		}
	}
	if !sawError {
		t.Fatalf("expected the exception-carrying span to be marked as error: %+v", trace.Spans)
	}

	// Recent traces for the route.
	var traceList struct {
		Traces []struct {
			TraceID string `json:"trace_id"`
		} `json:"traces"`
	}
	owner.mustJSON("GET", base+"/traces?method=GET&route=/orders", nil, 200, &traceList)
	if len(traceList.Traces) != 2 {
		t.Fatalf("expected 2 recent traces for GET /orders, got %d", len(traceList.Traces))
	}

	// No query params -> empty list, not an error (the isolation sweep relies on this).
	var emptyTraceList struct {
		Traces []struct{ TraceID string } `json:"traces"`
	}
	owner.mustJSON("GET", base+"/traces", nil, 200, &emptyTraceList)
	if len(emptyTraceList.Traces) != 0 {
		t.Fatalf("expected an empty trace list without method/route query params, got %+v", emptyTraceList.Traces)
	}

	// Two identical exceptions (PaymentError, same message/stack) collapsed into one issue, count 2. The
	// JobError from the logs payload is a second, distinct issue.
	var issues struct {
		Issues []struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Count   int32  `json:"count"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"issues"`
	}
	owner.mustJSON("GET", base+"/issues", nil, 200, &issues)
	if len(issues.Issues) != 2 {
		t.Fatalf("expected 2 grouped issues (PaymentError x2 collapsed, JobError x1), got %d: %+v", len(issues.Issues), issues.Issues)
	}
	var paymentIssue *struct {
		ID      string
		Type    string
		Count   int32
		Status  string
		Message string
	}
	for i := range issues.Issues {
		if issues.Issues[i].Type == "PaymentError" {
			paymentIssue = &struct {
				ID      string
				Type    string
				Count   int32
				Status  string
				Message string
			}{issues.Issues[i].ID, issues.Issues[i].Type, issues.Issues[i].Count, issues.Issues[i].Status, issues.Issues[i].Message}
		}
	}
	if paymentIssue == nil {
		t.Fatalf("expected a PaymentError issue: %+v", issues.Issues)
	}
	if paymentIssue.Count != 2 {
		t.Fatalf("expected the PaymentError issue to have count 2 (two identical occurrences), got %d", paymentIssue.Count)
	}
	if paymentIssue.Status != "open" {
		t.Fatalf("a fresh issue should start open, got %q", paymentIssue.Status)
	}

	// Issue detail includes the latest stacktrace.
	var detail struct {
		LatestStacktrace string `json:"latest_stacktrace"`
	}
	owner.mustJSON("GET", base+"/issues/"+paymentIssue.ID, nil, 200, &detail)
	if !strings.Contains(detail.LatestStacktrace, "payment.go:42") {
		t.Fatalf("expected the latest stacktrace to be stored: %q", detail.LatestStacktrace)
	}

	// Resolving the issue.
	var updated struct {
		Status string `json:"status"`
	}
	owner.mustJSON("PUT", base+"/issues/"+paymentIssue.ID+"/status", map[string]any{"status": "resolved"}, 200, &updated)
	if updated.Status != "resolved" {
		t.Fatalf("expected the issue to be resolved, got %q", updated.Status)
	}

	// Team isolation: team B gets 404 on everything under team A's app.
	if code, _ := stranger.do("GET", base+"/settings", nil); code != 404 {
		t.Fatalf("stranger reading team A's APM settings -> %d, want 404", code)
	}
	if code, _ := stranger.do("GET", base+"/endpoints", nil); code != 404 {
		t.Fatalf("stranger reading team A's APM endpoints -> %d, want 404", code)
	}
	if code, _ := stranger.do("GET", base+"/issues", nil); code != 404 {
		t.Fatalf("stranger reading team A's APM issues -> %d, want 404", code)
	}
	if code, _ := stranger.do("PUT", base+"/settings", map[string]any{"enabled": false}); code != 404 {
		t.Fatalf("stranger disabling team A's APM -> %d, want 404", code)
	}

	// MCP tools mirror the REST surface for the owner.
	var tokA struct{ ID, Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "apm-mcp", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &tokA)

	if res := mcpCall(t, ts.URL, "/api/mcp", tokA.Token, "app_endpoints", map[string]any{"app": app.App.ID, "window": "24h"}); res.IsError || !strings.Contains(res.Text, "/orders") {
		t.Fatalf("app_endpoints: %+v", res)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", tokA.Token, "app_traces", map[string]any{"app": app.App.ID, "method": "GET", "route": "/orders"}); res.IsError || !strings.Contains(res.Text, traceID1) {
		t.Fatalf("app_traces (route): %+v", res)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", tokA.Token, "app_traces", map[string]any{"app": app.App.ID, "trace_id": traceID1}); res.IsError || !strings.Contains(res.Text, "root0001") {
		t.Fatalf("app_traces (trace_id): %+v", res)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", tokA.Token, "app_errors", map[string]any{"app": app.App.ID}); res.IsError || !strings.Contains(res.Text, "PaymentError") || !strings.Contains(res.Text, "JobError") {
		t.Fatalf("app_errors (list): %+v", res)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", tokA.Token, "app_errors", map[string]any{"app": app.App.ID, "issue_id": paymentIssue.ID}); res.IsError || !strings.Contains(res.Text, "payment.go:42") {
		t.Fatalf("app_errors (detail): %+v", res)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", tokA.Token, "app_error_status", map[string]any{"app": app.App.ID, "issue_id": paymentIssue.ID, "status": "ignored"}); res.IsError || !strings.Contains(res.Text, "ignored") {
		t.Fatalf("app_error_status: %+v", res)
	}

	// Rotating replaces the key: the old one stops accepting traces.
	var rotated struct {
		Key *string `json:"key"`
	}
	owner.mustJSON("POST", base+"/settings/rotate", nil, 200, &rotated)
	if rotated.Key == nil || *rotated.Key == key {
		t.Fatalf("rotate should mint a fresh key: %+v", rotated)
	}
	if code := otlpPost(t, "/api/otlp/v1/traces", key, tracesPayload(traceID1, false), false); code != 401 {
		t.Fatalf("ingest with the rotated-away key -> %d, want 401", code)
	}
	if code := otlpPost(t, "/api/otlp/v1/traces", *rotated.Key, tracesPayload(traceID1, false), false); code != 200 {
		t.Fatalf("ingest with the new key -> %d, want 200", code)
	}
}
