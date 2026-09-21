package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

const (
	brID    = "00000000-0000-4000-8000-0000000000b1"
	brToken = "TokenMarker0123456789TokenMarker01234567" // 40 alphanumerics
)

func newBrowserFake(t *testing.T, answers map[string]string) (*browserHost, *fakeRunner, *[]string) {
	t.Helper()
	f := &fakeRunner{answers: answers, fail: map[string]string{}}
	var listened []string
	h := &browserHost{run: f.run, dir: t.TempDir(), gateways: map[string]*browserGateway{},
		ready: func(context.Context, int) error { return nil },
		listen: func(addr string) (net.Listener, error) { // the test host has no 172.17.0.1
			listened = append(listened, addr)
			return net.Listen("tcp4", "127.0.0.1:0")
		}}
	h.stream = func(oc *OpContext, name string, args ...string) (int, error) {
		oc.log("stdout", "151.0.7922.109: Pulling from chromedp/headless-shell")
		if _, err := f.run(oc.Ctx, nil, name, args...); err != nil {
			return 1, err
		}
		return 0, nil
	}
	t.Cleanup(func() {
		for id := range h.gateways {
			h.closeGateway(id)
		}
	})
	return h, f, &listened
}

func browserParams(t *testing.T) (agentproto.BrowserCreateParams, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	box, err := agentproto.SealBox(pubB64(pub), agentproto.BrowserTokenContext(brID), []byte(`{"token":"`+brToken+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	return agentproto.BrowserCreateParams{BrowserID: brID, AppID: stAppID, Image: agentproto.BrowserImage, Bind: "172.17.0.1", Port: 19222,
		MemoryMB: 1024, CPUs: 1.5, MaxSessions: 5, IdleTimeoutSeconds: 300, Token: box}, priv
}

const brName = "cloudy-browser-" + brID

// The exact docker argv: loopback-only ephemeral publish, limits, no-new-privileges, every
// capability dropped and none added, the pinned image — and no token anywhere near docker.
func TestBrowserCreateRunsALockedDownContainerAndKeepsTheTokenAwayFromDocker(t *testing.T) {
	h, f, listened := newBrowserFake(t, map[string]string{"docker port " + brName + " 9222/tcp": "127.0.0.1:49153\n"})
	f.fail["docker image inspect"] = "Error: No such image"
	p, priv := browserParams(t)
	oc, lines := opCtx(t, p)
	oc.priv = priv
	res, _, err := h.create(oc)
	if err != nil {
		t.Fatal(err)
	}
	if r := res.(agentproto.BrowserCreateResult); r.HostPort != 19222 || r.Container != brName {
		t.Fatalf("result: %+v", r)
	}
	want := "docker run -d --restart unless-stopped --name " + brName +
		" --label cloudy.browser=" + brID + " --label cloudy.browser-app=" + stAppID +
		" --log-opt max-size=10m --log-opt max-file=3 --memory 1024m --cpus 1.5 --pids-limit 1024 --shm-size 256m" +
		" --security-opt no-new-privileges --cap-drop ALL -p 127.0.0.1::9222 " + agentproto.BrowserImage
	var run string
	pulled := false
	for _, c := range f.calls {
		if strings.HasPrefix(c.argv, "docker run ") {
			run = c.argv
		}
		pulled = pulled || c.argv == "docker pull "+agentproto.BrowserImage
		if strings.Contains(c.argv, brToken) || strings.Contains(strings.Join(c.env, " "), brToken) {
			t.Errorf("the token reached docker: %s %v", c.argv, c.env)
		}
		if strings.Contains(c.argv, "0.0.0.0") || strings.Contains(c.argv, "--cap-add") || strings.Contains(c.argv, "--privileged") {
			t.Errorf("forbidden in an argv: %s", c.argv)
		}
		if strings.Contains(c.argv, "cloudy.app=") {
			t.Errorf("a browser must not carry cloudy.app (a deploy's drain would stop it): %s", c.argv)
		}
	}
	if run != want {
		t.Errorf("docker run argv\n got: %s\nwant: %s", run, want)
	}
	if !pulled {
		t.Error("a missing image must be pulled")
	}
	log := strings.Join(*lines, "\n")
	if strings.Contains(log, brToken) {
		t.Error("the token is in the op log")
	}
	if !strings.Contains(log, "Pulling from") {
		t.Errorf("pull progress must reach the op log: %s", log)
	}
	if raw, _ := json.Marshal(res); strings.Contains(string(raw), brToken) {
		t.Error("the token is in the result")
	}
	if len(*listened) != 1 || (*listened)[0] != "172.17.0.1:19222" {
		t.Errorf("the gateway listens on the private bind: %v", *listened)
	}
	// On disk: a hash, mode 0600, never the token.
	b, err := os.ReadFile(filepath.Join(h.dir, brID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(brToken))
	if strings.Contains(string(b), brToken) || !strings.Contains(string(b), fmt.Sprintf("%x", sum)) {
		t.Errorf("state file: %s", b)
	}
}

func TestBrowserCreateRefusesWhatTheControlPlaneWouldRefuse(t *testing.T) {
	base, priv := browserParams(t)
	for name, mut := range map[string]func(*agentproto.BrowserCreateParams){
		"another image":   func(p *agentproto.BrowserCreateParams) { p.Image = "evil/chromium:latest" },
		"an image option": func(p *agentproto.BrowserCreateParams) { p.Image = agentproto.BrowserImage + " --privileged" },
		"every interface": func(p *agentproto.BrowserCreateParams) { p.Bind = "0.0.0.0" },
		"a public bind":   func(p *agentproto.BrowserCreateParams) { p.Bind = "8.8.8.8" },
		"an IPv6 any":     func(p *agentproto.BrowserCreateParams) { p.Bind = "::" },
		"a low port":      func(p *agentproto.BrowserCreateParams) { p.Port = 80 },
		"too much memory": func(p *agentproto.BrowserCreateParams) { p.MemoryMB = 8192 },
		"too little":      func(p *agentproto.BrowserCreateParams) { p.MemoryMB = 64 },
		"too many cpus":   func(p *agentproto.BrowserCreateParams) { p.CPUs = 16 },
		"sessions":        func(p *agentproto.BrowserCreateParams) { p.MaxSessions = 50 },
		"idle":            func(p *agentproto.BrowserCreateParams) { p.IdleTimeoutSeconds = 5 },
		"a bad id":        func(p *agentproto.BrowserCreateParams) { p.BrowserID = "x; rm -rf /" },
		"no token":        func(p *agentproto.BrowserCreateParams) { p.Token = "" },
	} {
		p := base
		mut(&p)
		h, f, _ := newBrowserFake(t, nil)
		oc, _ := opCtx(t, p)
		oc.priv = priv
		if _, code, err := h.create(oc); err == nil || code != 2 || len(f.calls) != 0 {
			t.Errorf("%s: accepted (code %d, err %v, calls %v)", name, code, err, f.calls)
		}
	}
	// A box for another browser does not open.
	p := base
	p.BrowserID = "00000000-0000-4000-8000-0000000000b2"
	h, f, _ := newBrowserFake(t, nil)
	oc, _ := opCtx(t, p)
	oc.priv = priv
	if _, _, err := h.create(oc); err == nil || len(f.calls) != 0 {
		t.Errorf("a token box bound to another browser must not open: %v", err)
	}
}

func TestBrowserCreateThatFailsLeavesNothing(t *testing.T) {
	h, f, _ := newBrowserFake(t, nil)
	f.fail["docker port"] = "Error: No public port '9222/tcp' published"
	p, priv := browserParams(t)
	oc, _ := opCtx(t, p)
	oc.priv = priv
	if _, _, err := h.create(oc); err == nil {
		t.Fatal("must fail")
	}
	if last := f.calls[len(f.calls)-1].argv; last != "docker rm -f "+brName {
		t.Errorf("the container must be removed: %s", last)
	}
	if len(h.gateways) != 0 || len(h.states()) != 0 {
		t.Error("no gateway and no state must be left")
	}
}

func TestBrowserRemoveIsIdempotent(t *testing.T) {
	h, f, _ := newBrowserFake(t, map[string]string{"docker port " + brName + " 9222/tcp": "127.0.0.1:49153\n"})
	p, priv := browserParams(t)
	oc, _ := opCtx(t, p)
	oc.priv = priv
	if _, _, err := h.create(oc); err != nil {
		t.Fatal(err)
	}
	addr := h.gateways[brID].ln.Addr().String()
	f.calls = nil
	oc, _ = opCtx(t, agentproto.BrowserRemoveParams{BrowserID: brID, AppID: stAppID})
	res, _, err := h.remove(oc)
	if err != nil || !res.(agentproto.BrowserRemoveResult).Removed {
		t.Fatalf("remove: %+v %v", res, err)
	}
	if got := f.mutating(); len(got) != 2 || got[0] != "docker stop -t 5 "+brName || got[1] != "docker rm -f "+brName {
		t.Errorf("argv: %v", got)
	}
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c.Close()
		t.Error("the gateway must be closed")
	}
	if len(h.states()) != 0 {
		t.Error("the state file must be gone")
	}
	f.fail["docker rm -f"] = "Error response from daemon: No such container: " + brName
	oc, _ = opCtx(t, agentproto.BrowserRemoveParams{BrowserID: brID, AppID: stAppID})
	if res, _, err := h.remove(oc); err != nil || res.(agentproto.BrowserRemoveResult).Removed {
		t.Errorf("already gone is fine: %+v %v", res, err)
	}
}

func TestBrowserStatusReadsInspectAndStats(t *testing.T) {
	other := "00000000-0000-4000-8000-0000000000b2"
	h, f, _ := newBrowserFake(t, map[string]string{
		"docker inspect --type container --format {{.State.Running}} {{.RestartCount}} " + brName: "true 3\n",
		"docker stats --no-stream --format {{.Name}}\t{{.MemUsage}} " + brName:                    brName + "\t212.5MiB / 1GiB\n",
	})
	f.fail["docker inspect --type container --format {{.State.Running}} {{.RestartCount}} cloudy-browser-"+other] = "No such container"
	oc, _ := opCtx(t, agentproto.BrowserStatusParams{AppID: stAppID, BrowserIDs: []string{brID, other}})
	res, _, err := h.status(oc)
	if err != nil {
		t.Fatal(err)
	}
	st := res.(agentproto.BrowserStatusResult).Browsers
	if len(st) != 2 || !st[0].Running || st[0].Restarts != 3 || st[0].MemoryBytes == nil || *st[0].MemoryBytes != int64(212.5*(1<<20)) {
		t.Errorf("first: %+v", st)
	}
	if st[1].Running || st[1].MemoryBytes != nil {
		t.Errorf("a missing container is not running and has no memory: %+v", st[1])
	}
	if len(f.mutating()) != 0 {
		t.Errorf("status changes nothing: %v", f.mutating())
	}
}

// app.remove takes the app's browsers with it: by state file and by label.
func TestAppRemoveTakesTheBrowsersOfTheApp(t *testing.T) {
	labelled := "00000000-0000-4000-8000-0000000000b3"
	h, f, _ := newBrowserFake(t, map[string]string{
		"docker port " + brName + " 9222/tcp":                       "127.0.0.1:49153\n",
		"docker ps -a --filter label=cloudy.browser-app=" + stAppID: labelled + "\n",
	})
	p, priv := browserParams(t)
	oc, _ := opCtx(t, p)
	oc.priv = priv
	if _, _, err := h.create(oc); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	oc, _ = opCtx(t, agentproto.RemoveParams{AppID: stAppID, App: "shop"})
	res, _, err := h.withAppRemove(func(*OpContext) (any, int, error) { return map[string]any{}, 0, nil })(oc)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(f.mutating(), "\n")
	for _, id := range []string{brID, labelled} {
		if !strings.Contains(argv, "docker rm -f cloudy-browser-"+id) {
			t.Errorf("%s was not removed:\n%s", id, argv)
		}
	}
	if len(res.(map[string]any)["browsers"].([]string)) != 2 || len(h.gateways) != 0 {
		t.Errorf("result %v, gateways %d", res, len(h.gateways))
	}
}

// fakeChromium is what headless-shell looks like on its loopback port: /json/version names the
// browser endpoint, which echoes WebSocket messages. It refuses a Host that is not an IP, as
// Chromium does, and fails the test if a token ever reaches it.
func fakeChromium(t *testing.T) (port int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "token") || r.Header.Get("Authorization") != "" {
			t.Errorf("the token reached the browser: %s %v", r.URL, r.Header)
		}
		if !strings.HasPrefix(r.Host, "127.0.0.1:") {
			http.Error(w, "Host header is specified and is not an IP address or localhost", 500)
			return
		}
		switch r.URL.Path {
		case "/json/version":
			fmt.Fprintf(w, `{"Browser":"HeadlessChrome","webSocketDebuggerUrl":"ws://%s/devtools/browser/abc"}`, r.Host)
		case "/devtools/browser/abc":
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			for {
				typ, msg, err := c.Read(context.Background())
				if err != nil {
					return
				}
				_ = c.Write(context.Background(), typ, append([]byte("echo:"), msg...))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

func TestBrowserGatewayChecksTheTokenCapsSessionsAndDropsIdleOnes(t *testing.T) {
	upstream := fakeChromium(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stale := 1 // a port nothing listens on: the gateway must re-read the published port
	g := newBrowserGateway(ln, sha256.Sum256([]byte(brToken)), 2, 700*time.Millisecond, stale, func() (int, bool) { return upstream, true })
	defer g.Close()
	host := fmt.Sprintf("127.0.0.1:%d", g.port())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	get := func(url string, hdr map[string]string) (int, string) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for _, u := range []string{"/json/version", "/json/version?token=wrong", "/json/version?token="} {
		if code, body := get("http://"+host+u, nil); code != 401 || strings.Contains(body, "HeadlessChrome") {
			t.Errorf("%s: %d %s", u, code, body)
		}
	}
	if _, _, err := websocket.Dial(ctx, "ws://"+host+"/devtools/browser/abc", nil); err == nil {
		t.Error("a WebSocket without the token must be refused")
	}
	code, body := get("http://"+host+"/json/version", map[string]string{"Authorization": "Bearer " + brToken})
	if code != 200 || !strings.Contains(body, "ws://"+host+"/devtools/browser/abc") {
		t.Errorf("with the token, and answers rewritten to the gateway's address: %d %s", code, body)
	}

	// ws://host:port?token=… is all a client needs.
	dial := func() (*websocket.Conn, error) {
		c, _, err := websocket.Dial(ctx, "ws://"+host+"?token="+brToken, nil)
		return c, err
	}
	a, err := dial()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Write(ctx, websocket.MessageText, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if _, msg, err := a.Read(ctx); err != nil || string(msg) != "echo:hi" {
		t.Fatalf("echo through the gateway: %q %v", msg, err)
	}
	b, err := dial()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dial(); err == nil {
		t.Error("a third session must be refused (max 2)")
	}
	// Both go silent: the gateway closes them, and their places are free again.
	rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
	defer rcancel()
	if _, _, err := a.Read(rctx); err == nil || rctx.Err() != nil {
		t.Errorf("an idle session must be closed by the gateway: %v", err)
	}
	b.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for g.sessions.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := g.sessions.Load(); n != 0 {
		t.Fatalf("sessions after idle: %d", n)
	}
	c, err := dial()
	if err != nil {
		t.Fatalf("a place must be free again: %v", err)
	}
	c.CloseNow()
}

func TestBrowserLanesAndKinds(t *testing.T) {
	raw, _ := json.Marshal(agentproto.BrowserRemoveParams{BrowserID: brID, AppID: stAppID})
	if l := laneOf(agentproto.SignedOp{Kind: agentproto.KindBrowserCreate, Params: raw}); l != "browser:"+brID {
		t.Errorf("create lane: %q", l)
	}
	if l := laneOf(agentproto.SignedOp{Kind: agentproto.KindBrowserStatus}); l != "" {
		t.Errorf("status runs concurrently: %q", l)
	}
	e := NewExecutor("s", nil, "v", "")
	for _, k := range []string{agentproto.KindBrowserCreate, agentproto.KindBrowserRemove, agentproto.KindBrowserStatus} {
		if e.handlers[k] == nil {
			t.Errorf("no handler for %s", k)
		}
	}
}
