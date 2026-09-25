package agent

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
Traffic analytics: Caddy already writes one JSON-lines access log of every request (caddy.go's
accessLogPath, enabled on both servers). This file tails it, attributes each line to an app by its
Host header against the current route table, and folds requests into one bucket per app per
finished minute — count, status classes, bytes, p50/p95 latency, top paths, top referrers, and a
count of distinct visitors (sha256(ip+user-agent), the hash discarded immediately after counting:
the raw IP and user agent never leave this function, let alone the server). agent.go's writer sends
finished buckets to the control plane as "traffic" frames, the same shape as the metrics readings.

Log rotation: Caddy's roll writer keeps the same filename for the live file, so a rotation is
detected as "the file got smaller than our offset" and read from the start again.
*/

// accessLogFile is overridable in tests.
var accessLogFile = func() string { return accessLogPath }

// accessEvent is the handful of fields read out of one Caddy JSON access log line. Caddy's default
// JSON encoder nests the request under "request" and lists header values as arrays.
type accessEvent struct {
	Ts      float64 `json:"ts"`
	Status  int     `json:"status"`
	Size    int64   `json:"size"`
	Dur     float64 `json:"duration"` // seconds
	Request struct {
		Host     string              `json:"host"`
		URI      string              `json:"uri"`
		RemoteIP string              `json:"remote_ip"`
		ClientIP string              `json:"client_ip"`
		Headers  map[string][]string `json:"headers"`
	} `json:"request"`
}

func (e accessEvent) userAgent() string {
	if v := e.Request.Headers["User-Agent"]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func (e accessEvent) referrer() string {
	if v := e.Request.Headers["Referer"]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// ip is the visitor's address. Behind a proxy on a private network (Nginx Proxy Manager, a
// Cloudflare tunnel) every request comes from the proxy, so the forwarded headers are used, but only
// then: from a public peer they are client-controlled and ignored.
func (e accessEvent) ip() string {
	ip := e.Request.ClientIP
	if ip == "" {
		ip = e.Request.RemoteIP
	}
	if addr, err := netip.ParseAddr(ip); err != nil || !(addr.IsPrivate() || addr.IsLoopback()) {
		return ip
	}
	for _, h := range []string{"Cf-Connecting-Ip", "X-Real-Ip", "X-Forwarded-For"} {
		if v := e.Request.Headers[h]; len(v) > 0 {
			first := strings.TrimSpace(strings.Split(v[0], ",")[0])
			if _, err := netip.ParseAddr(first); err == nil {
				return first
			}
		}
	}
	return ip
}

// path strips a query string, so /x?a=1 and /x?a=2 count as the same top path.
func (e accessEvent) path() string {
	p := e.Request.URI
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		return "/"
	}
	return p
}

// referrerHost keeps only the host of a referrer (never the full URL: it can carry query strings
// with tracking parameters or, on a same-site referrer, another visitor's path).
func referrerHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	s := raw
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return ""
	}
	return s
}

func visitorHash(ip, ua string) string {
	sum := sha256.Sum256([]byte(ip + "\x00" + ua))
	return hex.EncodeToString(sum[:])
}

// appMinute accumulates one app's requests for one minute.
type appMinute struct {
	at                              time.Time
	requests, s2, s3, s4, s5, bytes int64
	durationsMs                     []float64
	visitors                        map[string]struct{}
	paths, refs                     map[string]int64
}

func newAppMinute(at time.Time) *appMinute {
	return &appMinute{at: at, visitors: map[string]struct{}{}, paths: map[string]int64{}, refs: map[string]int64{}}
}

func (m *appMinute) add(e accessEvent, at time.Time) {
	m.requests++
	switch {
	case e.Status >= 200 && e.Status < 300:
		m.s2++
	case e.Status >= 300 && e.Status < 400:
		m.s3++
	case e.Status >= 400 && e.Status < 500:
		m.s4++
	case e.Status >= 500:
		m.s5++
	}
	m.bytes += e.Size
	m.durationsMs = append(m.durationsMs, e.Dur*1000)
	if ip := e.ip(); ip != "" {
		m.visitors[visitorHash(ip, e.userAgent())] = struct{}{}
	}
	m.paths[e.path()]++
	if rh := referrerHost(e.referrer()); rh != "" {
		m.refs[rh]++
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

const topN = 10

func topKeyCounts(counts map[string]int64) []agentproto.KeyCount {
	out := make([]agentproto.KeyCount, 0, len(counts))
	for k, c := range counts {
		out = append(out, agentproto.KeyCount{Key: k, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}

func (m *appMinute) finish(appID string) agentproto.TrafficMinute {
	sort.Float64s(m.durationsMs)
	return agentproto.TrafficMinute{
		AppID: appID, At: m.at, Requests: m.requests, Status2xx: m.s2, Status3xx: m.s3, Status4xx: m.s4, Status5xx: m.s5,
		BytesOut: m.bytes, DurationP50Ms: round1(percentile(m.durationsMs, 0.5)), DurationP95Ms: round1(percentile(m.durationsMs, 0.95)),
		UniqueVisitors: int64(len(m.visitors)), TopPaths: topKeyCounts(m.paths), TopReferrers: topKeyCounts(m.refs),
	}
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

// trafficCollector tails Caddy's access log and folds it into per-app minute buckets.
type trafficCollector struct {
	mu      sync.Mutex
	offset  int64
	buckets map[string]map[string]*appMinute // minute (RFC3339) -> app id -> bucket
}

func newTrafficCollector() *trafficCollector {
	return &trafficCollector{buckets: map[string]map[string]*appMinute{}}
}

// hostApp maps every routed hostname to its app id, from the route table the agent already
// maintains for Caddy (caddy.go). Proxy hosts (not apps) are left out: analytics is app traffic.
func hostApp() map[string]string {
	t, err := loadRoutes()
	if err != nil {
		return nil
	}
	return hostAppOf(t)
}

// hostAppOf credits a hostname two apps both claim to the one Caddy actually serves it from: the
// first in renderCaddy's order (sorted app ids), not whichever a map walk happens to reach last.
func hostAppOf(t routeTable) map[string]string {
	ids := make([]string, 0, len(t.Apps))
	for id := range t.Apps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := map[string]string{}
	for _, id := range ids {
		for _, h := range t.Apps[id].Hosts {
			host := strings.ToLower(h.Hostname)
			if _, taken := out[host]; !taken {
				out[host] = id
			}
		}
	}
	return out
}

// poll reads whatever is new in the access log since the last call and folds it into buckets. It
// never returns an error: a log line it cannot parse, or a log file that does not exist yet
// (Caddy has not been (re)loaded since analytics.go started shipping the config), is skipped.
func (c *trafficCollector) poll() {
	f, err := os.Open(accessLogFile())
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	c.mu.Lock()
	if st.Size() < c.offset { // rotated
		c.offset = 0
	}
	offset := c.offset
	c.mu.Unlock()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return
	}
	hosts := hostApp()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var read int64
	end := st.Size()
	for sc.Scan() {
		line := sc.Bytes()
		if offset+read+int64(len(line)) >= end { // no newline yet: Caddy is mid-write, read it next time
			break
		}
		read += int64(len(line)) + 1
		var e accessEvent
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		host := strings.ToLower(e.Request.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		appID := hosts[host]
		if appID == "" {
			continue
		}
		at := time.Unix(int64(e.Ts), 0).UTC().Truncate(time.Minute)
		c.add(at, appID, e)
	}
	c.mu.Lock()
	c.offset = offset + read
	c.mu.Unlock()
}

func (c *trafficCollector) add(at time.Time, appID string, e accessEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := at.Format(time.RFC3339)
	byApp := c.buckets[key]
	if byApp == nil {
		byApp = map[string]*appMinute{}
		c.buckets[key] = byApp
	}
	m := byApp[appID]
	if m == nil {
		m = newAppMinute(at)
		byApp[appID] = m
	}
	m.add(e, at)
}

// collect pops every bucket whose minute has fully elapsed (strictly before now's minute), ready
// to send. Buckets are removed once collected — a minute is only ever shipped once.
func (c *trafficCollector) collect(now time.Time) []agentproto.TrafficMinute {
	cur := now.UTC().Truncate(time.Minute)
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []agentproto.TrafficMinute
	for key, byApp := range c.buckets {
		at, err := time.Parse(time.RFC3339, key)
		if err != nil || !at.Before(cur) {
			continue
		}
		for appID, m := range byApp {
			out = append(out, m.finish(appID))
		}
		delete(c.buckets, key)
	}
	return out
}
