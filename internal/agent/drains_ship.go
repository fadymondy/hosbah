package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/monitors"
)

// ---- H8: shipping one batch of log lines to a drain's sink -------------------------------------
//
// Everything here is pure or takes its network dependency as a parameter, so batching, backoff and
// the wire formats are unit tested without a real syslog/HTTP/Loki collector (drains_ship_test.go).

// DrainLine is one log line ready to ship: the agent's own view (never the wire protocol — no
// drain configuration crosses the operation's result).
type DrainLine struct {
	At        time.Time
	Server    string
	App       string
	Process   string
	Container string
	Stream    string
	Line      string
}

// ---- bounded buffer, oldest-drops-first --------------------------------------------------------

// lineBuffer is a drain's pending batch: bounded, so a sink that is down for a while cannot grow
// the agent's memory without limit — the OLDEST lines are dropped to make room, and the count of
// drops is kept so the status the operator sees says so.
type lineBuffer struct {
	mu      sync.Mutex
	max     int
	items   []DrainLine
	dropped int64
}

func newLineBuffer(max int) *lineBuffer {
	if max <= 0 {
		max = 2000
	}
	return &lineBuffer{max: max}
}

// Push adds one line, dropping the oldest when full.
func (b *lineBuffer) Push(l DrainLine) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.items) >= b.max {
		b.items = b.items[1:]
		b.dropped++
	}
	b.items = append(b.items, l)
}

// Drain takes every buffered line (up to max) and the drop count since the last Drain, clearing
// both.
func (b *lineBuffer) Drain() ([]DrainLine, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	items := b.items
	b.items = nil
	dropped := b.dropped
	b.dropped = 0
	return items, dropped
}

// Len reports how many lines are buffered right now (tests, and a status reading).
func (b *lineBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.items)
}

// ---- backoff --------------------------------------------------------------------------------

// backoffDelay is the shipping loop's retry delay after N consecutive failures: doubling from 1s,
// capped at 60s. Deterministic (no jitter) so it is simple to assert on in tests; the agent adds a
// small jitter itself when it sleeps (see ship loop) so many drains failing at once do not retry
// in lockstep.
func backoffDelay(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	d := time.Second
	for i := 1; i < failures && d < 60*time.Second; i++ {
		d *= 2
	}
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// ---- syslog ---------------------------------------------------------------------------------

// syslogPriority: facility 1 (user-level), severity from the line's heuristic level.
func syslogPriority(level string) int {
	severity := 6 // informational
	switch level {
	case agentproto.LogLevelError:
		severity = 3
	case agentproto.LogLevelWarn:
		severity = 4
	}
	return 1*8 + severity
}

// buildSyslogLine renders one RFC 5424 line. appName identifies the process (app:process); msgid
// is the container, truncated to RFC 5424's 32-byte NILVALUE-safe form.
func buildSyslogLine(l DrainLine) string {
	ts := l.At.UTC().Format(time.RFC3339Nano)
	host := syslogToken(l.Server, "server")
	app := syslogToken(l.App+":"+l.Process, "app")
	msgid := syslogToken(l.Container, "-")
	return fmt.Sprintf("<%d>1 %s %s %s - %s - %s", syslogPriority(detectLevel(l.Line)), ts, host, app, msgid, l.Line)
}

func syslogToken(s, fallback string) string {
	s = strings.Map(func(r rune) rune {
		if r <= 32 || r == 127 {
			return '_'
		}
		return r
	}, strings.TrimSpace(s))
	if s == "" {
		s = fallback
	}
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

func dialJudged(ctx context.Context, network, host string, port int, internal bool) (net.Conn, error) {
	ip, err := monitors.ResolveDialable(ctx, nil, host, internal)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	return d.DialContext(ctx, network, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
}

// shipSyslog opens one connection and writes every line, LF-framed (RFC 6587 non-transparent
// framing — the framing every syslog collector accepts, octet-counting or not).
func shipSyslog(ctx context.Context, sink agentproto.SyslogSink, internal bool, lines []DrainLine) error {
	network := "udp"
	if sink.Transport == agentproto.SyslogTransportTCPTLS {
		network = "tcp"
	}
	conn, err := dialJudged(ctx, network, sink.Host, sink.Port, internal)
	if err != nil {
		return err
	}
	defer conn.Close()
	if sink.Transport == agentproto.SyslogTransportTCPTLS {
		tconn := tls.Client(conn, &tls.Config{ServerName: sink.Host, MinVersion: tls.VersionTLS12})
		if err := tconn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("tls handshake: %w", err)
		}
		conn = tconn
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	var buf bytes.Buffer
	for _, l := range lines {
		buf.WriteString(buildSyslogLine(l))
		buf.WriteByte('\n')
	}
	_, err = conn.Write(buf.Bytes())
	return err
}

// ---- http (NDJSON) ---------------------------------------------------------------------------

// httpLogRecord is one line of the NDJSON batch shipHTTP posts.
type httpLogRecord struct {
	At        time.Time `json:"at"`
	Server    string    `json:"server"`
	App       string    `json:"app"`
	Process   string    `json:"process"`
	Container string    `json:"container"`
	Stream    string    `json:"stream"`
	Level     string    `json:"level,omitempty"`
	Line      string    `json:"line"`
}

// buildHTTPBatch renders lines as NDJSON (one JSON object per line, newline-separated) — easy to
// stream-parse on the collector's side, and a partial write is still valid up to the last full
// line.
func buildHTTPBatch(lines []DrainLine) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, l := range lines {
		_ = enc.Encode(httpLogRecord{At: l.At, Server: l.Server, App: l.App, Process: l.Process, Container: l.Container,
			Stream: l.Stream, Level: detectLevel(l.Line), Line: l.Line})
	}
	return buf.Bytes()
}

// httpDoer is satisfied by *http.Client — swapped in tests for one dialing httptest's loopback
// listener without the SSRF judgement getting in the way.
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func judgedClient(internal bool) *http.Client {
	return &http.Client{Transport: monitors.SafeTransport(nil), Timeout: 15 * time.Second}
}

func authHeader(name, value string) func(*http.Request) {
	return func(r *http.Request) {
		if name != "" && value != "" {
			r.Header.Set(name, value)
		}
	}
}

func postJudged(ctx context.Context, client httpDoer, internal bool, rawURL, contentType string, body []byte, setHeaders ...func(*http.Request)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	for _, set := range setHeaders {
		set(req)
	}
	if internal {
		req = req.WithContext(monitors.AllowPrivateContext(req.Context(), []string{req.URL.Hostname()}))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("the collector answered %s", resp.Status)
	}
	return nil
}

func shipHTTP(ctx context.Context, sink agentproto.HTTPSink, internal bool, authValue string, lines []DrainLine) error {
	return postJudged(ctx, judgedClient(internal), internal, sink.URL, "application/x-ndjson", buildHTTPBatch(lines), authHeader(sink.AuthHeader, authValue))
}

// ---- loki ------------------------------------------------------------------------------------

// lokiStream is one labelled stream of Loki's push API.
type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"` // [unix_nano_as_string, line]
}

type lokiPushRequest struct {
	Streams []lokiStream `json:"streams"`
}

// lokiStreamKey groups lines into one Loki stream: everything Loki should treat as one label set.
// Grouping by container (not just app/process) would create a fresh stream on every deploy, which
// Loki tolerates but need not pay for — app/process/stream is enough to find the lines, and the
// container still travels as part of the line's own JSON-ish prefix is not needed since it is not
// structured; callers that want it can add it to Labels.
func lokiStreamKey(l DrainLine) string { return l.App + "\x00" + l.Process + "\x00" + l.Stream }

// buildLokiPayload groups lines into streams (app, process, stream, server, plus any extra labels)
// and sorts each stream's values by time — Loki refuses out-of-order entries within a stream.
func buildLokiPayload(lines []DrainLine, extraLabels map[string]string) lokiPushRequest {
	groups := map[string][]DrainLine{}
	var order []string
	for _, l := range lines {
		k := lokiStreamKey(l)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], l)
	}
	sort.Strings(order)
	req := lokiPushRequest{}
	for _, k := range order {
		ls := groups[k]
		sort.SliceStable(ls, func(i, j int) bool { return ls[i].At.Before(ls[j].At) })
		labels := map[string]string{"app": ls[0].App, "process": ls[0].Process, "stream": ls[0].Stream, "server": ls[0].Server}
		for k, v := range extraLabels {
			labels[k] = v
		}
		values := make([][2]string, 0, len(ls))
		for _, l := range ls {
			values = append(values, [2]string{strconv.FormatInt(l.At.UnixNano(), 10), l.Line})
		}
		req.Streams = append(req.Streams, lokiStream{Stream: labels, Values: values})
	}
	return req
}

func shipLoki(ctx context.Context, sink agentproto.LokiSink, internal bool, authValue string, lines []DrainLine) error {
	body, err := json.Marshal(buildLokiPayload(lines, sink.Labels))
	if err != nil {
		return err
	}
	return postJudged(ctx, judgedClient(internal), internal, sink.URL, "application/json", body, authHeader(sink.AuthHeader, authValue))
}

// shipBatch ships lines to whichever sink the config names.
func shipBatch(ctx context.Context, cfg agentproto.DrainConfig, authValue string, lines []DrainLine) error {
	if len(lines) == 0 {
		return nil
	}
	switch cfg.Kind {
	case agentproto.DrainKindSyslog:
		if cfg.Syslog == nil {
			return errors.New("no syslog settings")
		}
		return shipSyslog(ctx, *cfg.Syslog, cfg.Internal, lines)
	case agentproto.DrainKindHTTP:
		if cfg.HTTP == nil {
			return errors.New("no http settings")
		}
		return shipHTTP(ctx, *cfg.HTTP, cfg.Internal, authValue, lines)
	case agentproto.DrainKindLoki:
		if cfg.Loki == nil {
			return errors.New("no loki settings")
		}
		return shipLoki(ctx, *cfg.Loki, cfg.Internal, authValue, lines)
	default:
		return fmt.Errorf("unknown drain kind %q", cfg.Kind)
	}
}
