package apm

import "testing"

func TestReduceTraces_KeepsRootServerErrorSlowOnly(t *testing.T) {
	payload := TracesPayload{ResourceSpans: []otlpResourceSpans{{
		Resource: struct{ Attributes []otlpKV }{Attributes: []otlpKV{
			{Key: "service.name", Value: otlpAnyValue{StringValue: strPtr("checkout-api")}},
		}},
		ScopeSpans: []otlpScopeSpans{{Spans: []otlpSpan{
			{ // root span (no parent) - kept
				TraceID: "t1", SpanID: "root1", ParentSpanID: "",
				Name: "GET /orders", Kind: 2,
				StartTimeUnixNano: "1000000000", EndTimeUnixNano: "1010000000",
				Attributes: []otlpKV{
					{Key: "http.request.method", Value: otlpAnyValue{StringValue: strPtr("GET")}},
					{Key: "http.route", Value: otlpAnyValue{StringValue: strPtr("/orders")}},
					{Key: "http.response.status_code", Value: otlpAnyValue{IntValue: strPtr("200")}},
				},
			},
			{ // internal, fast, no error - dropped
				TraceID: "t1", SpanID: "child1", ParentSpanID: "root1",
				Name: "db.query", Kind: 1,
				StartTimeUnixNano: "1001000000", EndTimeUnixNano: "1002000000",
			},
			{ // internal, error span with exception event - kept
				TraceID: "t1", SpanID: "child2", ParentSpanID: "root1",
				Name: "payment.charge", Kind: 1,
				StartTimeUnixNano: "1002000000", EndTimeUnixNano: "1003000000",
				Events: []otlpEvent{{
					Name: "exception",
					Attributes: []otlpKV{
						{Key: "exception.type", Value: otlpAnyValue{StringValue: strPtr("PaymentError")}},
						{Key: "exception.message", Value: otlpAnyValue{StringValue: strPtr("card declined for order 12345")}},
						{Key: "exception.stacktrace", Value: otlpAnyValue{StringValue: strPtr("at charge (payment.go:42)\nat handle (orders.go:10)")}},
					},
				}},
			},
			{ // internal, slow (>1000ms), no error - kept
				TraceID: "t1", SpanID: "child3", ParentSpanID: "root1",
				Name: "slow.op", Kind: 1,
				StartTimeUnixNano: "1000000000", EndTimeUnixNano: "3500000000",
			},
		}}},
	}}}

	spans, exceptions := ReduceTraces(payload)
	if len(spans) != 3 {
		t.Fatalf("expected 3 kept spans (root, error, slow), got %d", len(spans))
	}
	if len(exceptions) != 1 {
		t.Fatalf("expected 1 exception occurrence, got %d", len(exceptions))
	}
	ex := exceptions[0]
	if ex.Type != "PaymentError" || ex.SpanID != "child2" {
		t.Fatalf("unexpected exception: %+v", ex)
	}

	var root, errSpan *ReducedSpan
	for i := range spans {
		if spans[i].SpanID == "root1" {
			root = &spans[i]
		}
		if spans[i].SpanID == "child2" {
			errSpan = &spans[i]
		}
	}
	if root == nil {
		t.Fatal("root span not kept")
	}
	if root.Kind != KindRoot {
		t.Fatalf("expected root kind, got %q", root.Kind)
	}
	if root.HTTPMethod != "GET" || root.HTTPRoute != "/orders" || root.HTTPStatus != 200 {
		t.Fatalf("http fields not extracted: %+v", root)
	}
	if errSpan == nil {
		t.Fatal("error span not kept")
	}
	if !errSpan.Error {
		t.Fatal("span with exception event should be marked as error")
	}
}

func TestReduceTraces_ScrubsSensitiveAttributes(t *testing.T) {
	payload := TracesPayload{ResourceSpans: []otlpResourceSpans{{
		ScopeSpans: []otlpScopeSpans{{Spans: []otlpSpan{
			{
				TraceID: "t1", SpanID: "s1", ParentSpanID: "",
				Name: "GET /", Kind: 2,
				StartTimeUnixNano: "1000000000", EndTimeUnixNano: "1001000000",
				Attributes: []otlpKV{
					{Key: "http.authorization", Value: otlpAnyValue{StringValue: strPtr("Bearer abc")}},
					{Key: "user.password", Value: otlpAnyValue{StringValue: strPtr("hunter2")}},
					{Key: "custom.safe", Value: otlpAnyValue{StringValue: strPtr("ok")}},
				},
			},
		}}},
	}}}
	spans, _ := ReduceTraces(payload)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	attrs := spans[0].Attrs
	if _, ok := attrs["http.authorization"]; ok {
		t.Fatal("authorization attribute should be scrubbed")
	}
	if _, ok := attrs["user.password"]; ok {
		t.Fatal("password attribute should be scrubbed")
	}
	if attrs["custom.safe"] != "ok" {
		t.Fatalf("safe attribute should survive scrubbing, got %+v", attrs)
	}
}

func TestReduceLogs_SeverityAndExceptionType(t *testing.T) {
	payload := LogsPayload{ResourceLogs: []otlpResourceLogs{{
		ScopeLogs: []otlpScopeLogs{{LogRecords: []otlpLogRecord{
			{ // below error severity, no exception type - dropped
				SeverityNumber: 9, TimeUnixNano: "1000000000",
				Body: &otlpAnyValue{StringValue: strPtr("info message")},
			},
			{ // ERROR severity - kept
				SeverityNumber: 17, SeverityText: "ERROR", TimeUnixNano: "1000000000",
				Body: &otlpAnyValue{StringValue: strPtr("something broke")},
			},
			{ // has exception.type attribute even at low severity - kept
				SeverityNumber: 5, TimeUnixNano: "1000000000",
				Attributes: []otlpKV{
					{Key: "exception.type", Value: otlpAnyValue{StringValue: strPtr("ValueError")}},
					{Key: "exception.message", Value: otlpAnyValue{StringValue: strPtr("bad value")}},
				},
			},
		}}},
	}}}
	occs := ReduceLogs(payload)
	if len(occs) != 2 {
		t.Fatalf("expected 2 exception occurrences, got %d: %+v", len(occs), occs)
	}
	if occs[1].Type != "ValueError" || occs[1].Message != "bad value" {
		t.Fatalf("unexpected second occurrence: %+v", occs[1])
	}
}

func strPtr(s string) *string { return &s }
