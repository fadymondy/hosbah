package apm

import (
	"strconv"
	"time"
)

// OTLP/HTTP JSON payload shapes (protobuf JSON mapping: lowerCamelCase field names), just the
// subset Hosbah reads. Only JSON is accepted — no protobuf content type — per MH-575 H7's scope, so
// any standard OTel SDK's OTLP/HTTP exporter set to json works with zero code.

type otlpAnyValue struct {
	StringValue *string       `json:"stringValue"`
	IntValue    *string       `json:"intValue"` // protobuf JSON encodes int64 as a string
	DoubleValue *float64      `json:"doubleValue"`
	BoolValue   *bool         `json:"boolValue"`
	ArrayValue  *otlpArrValue `json:"arrayValue"`
}

type otlpArrValue struct {
	Values []otlpAnyValue `json:"values"`
}

func (v otlpAnyValue) asAny() any {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return *v.IntValue
	case v.DoubleValue != nil:
		return *v.DoubleValue
	case v.BoolValue != nil:
		return *v.BoolValue
	default:
		return nil
	}
}

type otlpKV struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

func kvMap(kvs []otlpKV) map[string]any {
	m := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		if v := kv.Value.asAny(); v != nil {
			m[kv.Key] = v
		}
	}
	return m
}

func kvString(kvs []otlpKV, key string) string {
	for _, kv := range kvs {
		if kv.Key == key {
			if kv.Value.StringValue != nil {
				return *kv.Value.StringValue
			}
			if kv.Value.IntValue != nil {
				return *kv.Value.IntValue
			}
		}
	}
	return ""
}

type otlpStatus struct {
	Code    int    `json:"code"` // STATUS_CODE_ERROR = 2
	Message string `json:"message"`
}

type otlpEvent struct {
	TimeUnixNano string   `json:"timeUnixNano"`
	Name         string   `json:"name"`
	Attributes   []otlpKV `json:"attributes"`
}

type otlpSpan struct {
	TraceID           string      `json:"traceId"`
	SpanID            string      `json:"spanId"`
	ParentSpanID      string      `json:"parentSpanId"`
	Name              string      `json:"name"`
	Kind              int         `json:"kind"`
	StartTimeUnixNano string      `json:"startTimeUnixNano"`
	EndTimeUnixNano   string      `json:"endTimeUnixNano"`
	Attributes        []otlpKV    `json:"attributes"`
	Status            *otlpStatus `json:"status"`
	Events            []otlpEvent `json:"events"`
}

type otlpScopeSpans struct {
	Spans []otlpSpan `json:"spans"`
}

type otlpResourceSpans struct {
	Resource   struct{ Attributes []otlpKV } `json:"resource"`
	ScopeSpans []otlpScopeSpans              `json:"scopeSpans"`
}

// TracesPayload is POST /api/otlp/v1/traces' body.
type TracesPayload struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpLogRecord struct {
	TimeUnixNano   string        `json:"timeUnixNano"`
	SeverityNumber int           `json:"severityNumber"`
	SeverityText   string        `json:"severityText"`
	Body           *otlpAnyValue `json:"body"`
	Attributes     []otlpKV      `json:"attributes"`
	TraceID        string        `json:"traceId"`
	SpanID         string        `json:"spanId"`
}

type otlpScopeLogs struct {
	LogRecords []otlpLogRecord `json:"logRecords"`
}

type otlpResourceLogs struct {
	Resource  struct{ Attributes []otlpKV } `json:"resource"`
	ScopeLogs []otlpScopeLogs               `json:"scopeLogs"`
}

// LogsPayload is POST /api/otlp/v1/logs' body.
type LogsPayload struct {
	ResourceLogs []otlpResourceLogs `json:"resourceLogs"`
}

// kind strings stored in apm_spans.kind (matches the CHECK constraint in the schema).
const (
	KindUnspecified = "internal"
	KindInternal    = "internal"
	KindServer      = "server"
	KindClient      = "client"
	KindProducer    = "producer"
	KindConsumer    = "consumer"
	KindRoot        = "root"
)

func spanKindString(k int, parentEmpty bool) string {
	if parentEmpty {
		return KindRoot
	}
	switch k {
	case 2:
		return KindServer
	case 3:
		return KindClient
	case 4:
		return KindProducer
	case 5:
		return KindConsumer
	default:
		return KindInternal
	}
}

// SlowSpanMs: a span slower than this is kept even if it is not root/server/error, so a slow
// downstream call surfaces in the waterfall.
const SlowSpanMs = 1000

func unixNano(s string) time.Time {
	n, _ := strconv.ParseInt(s, 10, 64)
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// ReducedSpan is one span kept from an OTLP export, ready to insert.
type ReducedSpan struct {
	TraceID, SpanID, ParentSpanID string
	Name, Kind, Service           string
	HTTPMethod, HTTPRoute         string
	HTTPStatus                    int
	DurationMs                    float64
	Error                         bool
	StartTime                     time.Time
	Attrs                         map[string]any
	Exceptions                    []ExceptionOccurrence
}

// ExceptionOccurrence is one raw exception (a span "exception" event, or a log record), before
// grouping into an apm_issues row.
type ExceptionOccurrence struct {
	TraceID, SpanID      string
	Type, Message, Stack string
	At                   time.Time
}

// ReduceTraces flattens an OTLP traces payload into the spans worth storing (root/server + error +
// slow) and the exception events found in their "exception" span events, both already scrubbed.
func ReduceTraces(p TracesPayload) ([]ReducedSpan, []ExceptionOccurrence) {
	var spans []ReducedSpan
	var exceptions []ExceptionOccurrence
	for _, rs := range p.ResourceSpans {
		service := kvString(rs.Resource.Attributes, "service.name")
		if service == "" {
			service = "unknown_service"
		}
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				start := unixNano(sp.StartTimeUnixNano)
				end := unixNano(sp.EndTimeUnixNano)
				var durMs float64
				if !start.IsZero() && !end.IsZero() {
					durMs = end.Sub(start).Seconds() * 1000
				}
				isError := sp.Status != nil && sp.Status.Code == 2
				kind := spanKindString(sp.Kind, sp.ParentSpanID == "")
				attrs := kvMap(sp.Attributes)
				method := strOrEmpty(attrs["http.request.method"])
				if method == "" {
					method = strOrEmpty(attrs["http.method"])
				}
				route := strOrEmpty(attrs["http.route"])
				status := 0
				if v, ok := attrs["http.response.status_code"]; ok {
					status = toInt(v)
				} else if v, ok := attrs["http.status_code"]; ok {
					status = toInt(v)
				}

				var occs []ExceptionOccurrence
				for _, ev := range sp.Events {
					if ev.Name != "exception" {
						continue
					}
					occs = append(occs, ExceptionOccurrence{
						TraceID: sp.TraceID, SpanID: sp.SpanID,
						Type:    kvString(ev.Attributes, "exception.type"),
						Message: kvString(ev.Attributes, "exception.message"),
						Stack:   capString(kvString(ev.Attributes, "exception.stacktrace"), 16384),
						At:      unixNano(ev.TimeUnixNano),
					})
					isError = true // a span that raised an exception is an error span even without a status code
				}
				exceptions = append(exceptions, occs...)

				keep := kind == KindRoot || kind == KindServer || isError || durMs >= SlowSpanMs
				if !keep {
					continue
				}
				spans = append(spans, ReducedSpan{
					TraceID: sp.TraceID, SpanID: sp.SpanID, ParentSpanID: sp.ParentSpanID,
					Name: sp.Name, Kind: kind, Service: service,
					HTTPMethod: method, HTTPRoute: route, HTTPStatus: status,
					DurationMs: durMs, Error: isError, StartTime: start,
					Attrs: scrubAttrs(attrs),
				})
			}
		}
	}
	return spans, exceptions
}

// ReduceLogs pulls exception occurrences out of an OTLP logs payload: any record at ERROR severity
// (severityNumber >= 17 per the OTel spec) or above, or explicitly named "exception"/carrying an
// exception.type attribute.
func ReduceLogs(p LogsPayload) []ExceptionOccurrence {
	var out []ExceptionOccurrence
	for _, rl := range p.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				attrs := kvMap(lr.Attributes)
				typ := strOrEmpty(attrs["exception.type"])
				if lr.SeverityNumber < 17 && typ == "" {
					continue
				}
				msg := strOrEmpty(attrs["exception.message"])
				if msg == "" && lr.Body != nil {
					if s := lr.Body.asAny(); s != nil {
						if str, ok := s.(string); ok {
							msg = str
						}
					}
				}
				if typ == "" {
					typ = lr.SeverityText
					if typ == "" {
						typ = "error"
					}
				}
				out = append(out, ExceptionOccurrence{
					TraceID: lr.TraceID, SpanID: lr.SpanID,
					Type: typ, Message: msg,
					Stack: capString(strOrEmpty(attrs["exception.stacktrace"]), 16384),
					At:    unixNano(lr.TimeUnixNano),
				})
			}
		}
	}
	return out
}

func strOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}

func toInt(v any) int {
	switch t := v.(type) {
	case string:
		n, _ := strconv.Atoi(t)
		return n
	case float64:
		return int(t)
	default:
		return 0
	}
}

func capString(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}
