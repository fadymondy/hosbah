package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// browserGateway stands between a site's containers and one headless Chromium. The browser image
// speaks bare CDP with no authentication, so its port is published on 127.0.0.1 only; this gateway
// listens on the server's private address and lets a request through only with the browser's token
// (?token=… or Authorization: Bearer …). It holds the token's SHA-256, never the token. It also caps
// concurrent WebSocket sessions (429 beyond max) and closes a session silent for longer than idle.
//
// A WebSocket to "/" is routed to the browser's own endpoint (webSocketDebuggerUrl of /json/version),
// so ws://host:port?token=… is all a client needs. Every other path is passed as it is.
type browserGateway struct {
	ln        net.Listener
	srv       *http.Server
	tokenHash [32]byte
	max       int
	idle      time.Duration
	upstream  atomic.Int64       // the container's CDP port on 127.0.0.1
	refresh   func() (int, bool) // re-reads it (docker hands out a new one when the container restarts)
	sessions  atomic.Int64
	mu        sync.Mutex
	conns     map[net.Conn]struct{}
}

func newBrowserGateway(ln net.Listener, tokenHash [32]byte, maxSessions int, idle time.Duration, upstream int, refresh func() (int, bool)) *browserGateway {
	g := &browserGateway{ln: ln, tokenHash: tokenHash, max: maxSessions, idle: idle, refresh: refresh, conns: map[net.Conn]struct{}{}}
	g.upstream.Store(int64(upstream))
	g.srv = &http.Server{Handler: g, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = g.srv.Serve(ln) }()
	return g
}

func (g *browserGateway) port() int { return g.ln.Addr().(*net.TCPAddr).Port }

// Close stops listening and drops every open session.
func (g *browserGateway) Close() {
	_ = g.srv.Close()
	g.mu.Lock()
	for c := range g.conns {
		_ = c.Close()
	}
	g.mu.Unlock()
}

func (g *browserGateway) authorized(r *http.Request) bool {
	tok := r.URL.Query().Get("token")
	if tok == "" {
		tok, _ = strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	sum := sha256.Sum256([]byte(tok))
	return tok != "" && subtle.ConstantTimeCompare(sum[:], g.tokenHash[:]) == 1
}

// dial reaches the browser; a refused connection re-reads the published port once.
func (g *browserGateway) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	c, err := d.DialContext(ctx, "tcp", "127.0.0.1:"+strconv.FormatInt(g.upstream.Load(), 10))
	if err != nil && g.refresh != nil {
		if p, ok := g.refresh(); ok {
			g.upstream.Store(int64(p))
			c, err = d.DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(p))
		}
	}
	return c, err
}

// cleaned is the request as the browser gets it: no token, and the Host Chromium insists on (an IP
// or localhost).
func (g *browserGateway) cleaned(r *http.Request, path string) *http.Request {
	out := r.Clone(r.Context())
	q := out.URL.Query()
	q.Del("token")
	out.URL = &url.URL{Path: path, RawQuery: q.Encode()}
	out.RequestURI = ""
	out.Host = "127.0.0.1:" + strconv.FormatInt(g.upstream.Load(), 10)
	out.Header.Del("Authorization")
	return out
}

func (g *browserGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.authorized(r) {
		http.Error(w, "a browser needs its token (?token=… or Authorization: Bearer …)", http.StatusUnauthorized)
		return
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		g.session(w, r)
		return
	}
	up, err := g.dial(r.Context())
	if err != nil {
		http.Error(w, "the browser is not answering", http.StatusBadGateway)
		return
	}
	defer up.Close()
	_ = up.SetDeadline(time.Now().Add(30 * time.Second))
	req := g.cleaned(r, r.URL.Path)
	req.Close = true
	if err := req.Write(up); err != nil {
		http.Error(w, "the browser is not answering", http.StatusBadGateway)
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(up), req)
	if err != nil {
		http.Error(w, "the browser is not answering", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	// /json/* answers name the browser's own address; a client must come back through the gateway.
	body = []byte(strings.ReplaceAll(string(body), req.Host, r.Host))
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// browserPath asks the browser for its own WebSocket path (/devtools/browser/<id>).
func (g *browserGateway) browserPath(ctx context.Context) (string, error) {
	up, err := g.dial(ctx)
	if err != nil {
		return "", err
	}
	defer up.Close()
	_ = up.SetDeadline(time.Now().Add(10 * time.Second))
	host := "127.0.0.1:" + strconv.FormatInt(g.upstream.Load(), 10)
	if _, err := fmt.Fprintf(up, "GET /json/version HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host); err != nil {
		return "", err
	}
	resp, err := http.ReadResponse(bufio.NewReader(up), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var v struct {
		URL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v); err != nil {
		return "", err
	}
	u, err := url.Parse(v.URL)
	if err != nil || !strings.HasPrefix(u.Path, "/devtools/") {
		return "", errors.New("the browser did not name its endpoint")
	}
	return u.Path, nil
}

func (g *browserGateway) session(w http.ResponseWriter, r *http.Request) {
	if n := g.sessions.Add(1); n > int64(g.max) {
		g.sessions.Add(-1)
		http.Error(w, fmt.Sprintf("this browser allows %d sessions at a time", g.max), http.StatusTooManyRequests)
		return
	}
	defer g.sessions.Add(-1)
	path := r.URL.Path
	if path == "" || path == "/" {
		p, err := g.browserPath(r.Context())
		if err != nil {
			http.Error(w, "the browser is not answering", http.StatusBadGateway)
			return
		}
		path = p
	}
	up, err := g.dial(r.Context())
	if err != nil {
		http.Error(w, "the browser is not answering", http.StatusBadGateway)
		return
	}
	defer up.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot upgrade", http.StatusInternalServerError)
		return
	}
	if err := g.cleaned(r, path).Write(up); err != nil {
		http.Error(w, "the browser is not answering", http.StatusBadGateway)
		return
	}
	client, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	g.track(client, true)
	g.track(up, true)
	defer g.track(client, false)
	defer g.track(up, false)

	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	done := make(chan struct{}, 2)
	go func() { g.pipe(up, brw, client, &last); done <- struct{}{} }()
	go func() { g.pipe(client, up, up, &last); done <- struct{}{} }()
	<-done // one side ended (or the session went idle): the deferred closes end the other
}

func (g *browserGateway) track(c net.Conn, add bool) {
	g.mu.Lock()
	if add {
		g.conns[c] = struct{}{}
	} else {
		delete(g.conns, c)
	}
	g.mu.Unlock()
}

// pipe copies src to dst until an error, or until NEITHER direction has moved a byte for g.idle.
func (g *browserGateway) pipe(dst io.Writer, src io.Reader, srcConn net.Conn, last *atomic.Int64) {
	buf := make([]byte, 32<<10)
	for {
		_ = srcConn.SetReadDeadline(time.Now().Add(g.idle))
		n, err := src.Read(buf)
		if n > 0 {
			last.Store(time.Now().UnixNano())
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() && time.Since(time.Unix(0, last.Load())) < g.idle {
				continue // this direction was quiet, the other was not
			}
			return
		}
	}
}
