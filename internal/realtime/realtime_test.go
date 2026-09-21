package realtime

import (
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
)

func TestDetect(t *testing.T) {
	for name, tc := range map[string]struct {
		in         Inputs
		kind       string
		process    string
		port       int
		path       string
		confidence string
		evidence   string // a substring of the joined evidence
	}{
		"reverb:start, default port": {
			in:   Inputs{Runtime: "railpack:php", Processes: map[string]string{"web": "", "worker:reverb": "php artisan reverb:start --host=0.0.0.0"}},
			kind: KindReverb, process: "reverb", port: 8080, path: "/app", confidence: ConfidenceDetected, evidence: "default, 8080",
		},
		"reverb:start with --port=": {
			in:   Inputs{Processes: map[string]string{"worker:sockets": "php artisan reverb:start --port=6001"}},
			kind: KindReverb, process: "sockets", port: 6001, path: "/app", confidence: ConfidenceDetected, evidence: "--port 6001",
		},
		"reverb:start with --port N": {
			in:   Inputs{Processes: map[string]string{"worker:reverb": "php artisan reverb:start --port 9000"}},
			kind: KindReverb, process: "reverb", port: 9000, path: "/app", confidence: ConfidenceDetected,
		},
		"reverb wins over a socket.io worker": {
			in:   Inputs{Processes: map[string]string{"worker:a-io": "node socket.io-server.js", "worker:reverb": "php artisan reverb:start"}},
			kind: KindReverb, process: "reverb", port: 8080, path: "/app", confidence: ConfidenceDetected,
		},
		"reverb:start on web is not a worker": {
			in:         Inputs{Processes: map[string]string{"web": "php artisan reverb:start"}},
			confidence: ConfidenceUnknown,
		},
		"only REVERB_ names": {
			in:   Inputs{Runtime: "railpack:php", Processes: map[string]string{"web": ""}, EnvNames: []string{"APP_KEY", "REVERB_APP_KEY", "REVERB_APP_SECRET"}},
			kind: KindReverb, port: 8080, path: "/app", confidence: ConfidenceLikely, evidence: "REVERB_APP_KEY",
		},
		"a command that mentions socket.io": {
			in:   Inputs{Runtime: "railpack:node", Processes: map[string]string{"worker:io": "node node_modules/socket.io/bin/server.js --port 3001"}},
			kind: KindSocketIO, process: "io", port: 3001, path: "/socket.io", confidence: ConfidenceLikely, evidence: "socket.io",
		},
		"a websocket server file": {
			in:   Inputs{Runtime: "railpack:node", Processes: map[string]string{"worker:live": "node ./src/ws-server.js"}},
			kind: KindSocketIO, process: "live", port: 0, path: "/socket.io", confidence: ConfidenceLikely, evidence: "ws-server.js",
		},
		"ws.js": {
			in:   Inputs{Processes: map[string]string{"worker:live": "node ws.js"}},
			kind: KindSocketIO, process: "live", path: "/socket.io", confidence: ConfidenceLikely, evidence: "choose ws",
		},
		"a file that only contains ws is not one": {
			in:         Inputs{Processes: map[string]string{"worker:news": "node news.js"}},
			confidence: ConfidenceUnknown,
		},
		"go with a worker named ws": {
			in:   Inputs{Runtime: "railpack:golang", Processes: map[string]string{"web": "", "worker:ws": "./bin/ws"}},
			kind: KindGo, process: "ws", path: "/ws", confidence: ConfidenceLikely, evidence: "Go",
		},
		"go with a worker named realtime": {
			in:   Inputs{Runtime: "railpack:golang", Processes: map[string]string{"worker:realtime": "./bin/rt --port 9001"}},
			kind: KindGo, process: "realtime", port: 9001, path: "/ws", confidence: ConfidenceLikely,
		},
		"a worker named socket, not Go": {
			in:         Inputs{Runtime: "railpack:python", Processes: map[string]string{"worker:socket": "python serve.py"}},
			confidence: ConfidenceUnknown,
		},
		"go with ordinary workers": {
			in:         Inputs{Runtime: "railpack:golang", Processes: map[string]string{"worker:jobs": "./bin/jobs"}},
			confidence: ConfidenceUnknown,
		},
		"nothing at all": {
			in:         Inputs{},
			confidence: ConfidenceUnknown, evidence: "no process of the live release",
		},
		"a dockerfile app with a queue worker": {
			in:         Inputs{Runtime: "dockerfile", Processes: map[string]string{"web": "", "worker:queue": "php artisan queue:work"}, EnvNames: []string{"REDIS_URL"}},
			confidence: ConfidenceUnknown, evidence: "worker:queue",
		},
	} {
		got := Detect(tc.in)
		if got.Kind != tc.kind || got.Process != tc.process || got.Port != tc.port || got.Path != tc.path || got.Confidence != tc.confidence {
			t.Errorf("%s: %+v", name, got)
		}
		if len(got.Evidence) == 0 {
			t.Errorf("%s: a suggestion always says what it looked at", name)
		}
		if !strings.Contains(strings.Join(got.Evidence, " | "), tc.evidence) {
			t.Errorf("%s: evidence %q lacks %q", name, got.Evidence, tc.evidence)
		}
		if got.Process != "" && !agentproto.ValidRealtimeProcess(got.Process) {
			t.Errorf("%s: suggested a process that could not be saved: %q", name, got.Process)
		}
	}
}

// Names of environment variables only: a value can never reach the evidence, because Detect never
// sees one. What it does see is capped.
func TestDetectEvidenceCapsEnvNames(t *testing.T) {
	var names []string
	for _, s := range []string{"A", "B", "C", "D", "E", "F", "G"} {
		names = append(names, "REVERB_"+s)
	}
	got := Detect(Inputs{EnvNames: names})
	if strings.Contains(got.Evidence[0], "REVERB_F") || !strings.Contains(got.Evidence[0], "REVERB_E") {
		t.Errorf("evidence: %q", got.Evidence[0])
	}
}

func TestClean(t *testing.T) {
	yes, no := true, false
	ok := Input{Kind: "reverb", Process: "reverb", Port: 8080}

	for name, tc := range map[string]struct {
		in   Input
		path string
	}{
		"reverb's default path":   {ok, "/app"},
		"socketio's default path": {Input{Kind: "socketio", Process: "io", Port: 3000}, "/socket.io"},
		"ws's default path":       {Input{Kind: "ws", Process: "ws", Port: 3000}, "/ws"},
		"go's default path":       {Input{Kind: "go", Process: "rt", Port: 3000}, "/ws"},
		"custom's default path":   {Input{Kind: "custom", Process: "rt", Port: 3000}, "/ws"},
		"normalised":              {Input{Kind: "ws", Process: "ws_1-a", Port: 1, Path: "//live//socket/"}, "/live/socket"},
		"dots inside a segment":   {Input{Kind: "ws", Process: "ws", Port: 65535, Path: "/socket.io/v4.x"}, "/socket.io/v4.x"},
		"the whole site":          {Input{Kind: "custom", Process: "ws", Port: 3000, Path: "/", WholeSite: true}, "/"},
		"disabled":                {Input{Enabled: &no, Kind: "ws", Process: "ws", Port: 3000}, "/ws"},
	} {
		p, err := Clean(tc.in)
		if err != nil || p.Path != tc.path {
			t.Errorf("%s: %+v, %v", name, p, err)
		}
		if tc.in.Enabled == nil && !p.Enabled {
			t.Errorf("%s: enabled is the default", name)
		}
	}
	if p, _ := Clean(Input{Enabled: &yes, Kind: "ws", Process: "ws", Port: 1}); !p.Enabled {
		t.Error("enabled: true")
	}
	if p, _ := Clean(Input{Enabled: &no, Kind: "ws", Process: "ws", Port: 1}); p.Enabled {
		t.Error("enabled: false")
	}

	with := func(f func(*Input)) Input {
		in := ok
		f(&in)
		return in
	}
	for name, in := range map[string]Input{
		"no kind":                  with(func(i *Input) { i.Kind = "" }),
		"unknown kind":             with(func(i *Input) { i.Kind = "pusher" }),
		"web":                      with(func(i *Input) { i.Process = "web" }),
		"worker: prefix":           with(func(i *Input) { i.Process = "worker:reverb" }),
		"option as process":        with(func(i *Input) { i.Process = "-p" }),
		"long option as process":   with(func(i *Input) { i.Process = "--network=host" }),
		"process with a slash":     with(func(i *Input) { i.Process = "a/b" }),
		"process with a dot":       with(func(i *Input) { i.Process = "a.b" }),
		"empty process":            with(func(i *Input) { i.Process = "" }),
		"32 characters":            with(func(i *Input) { i.Process = "a" + strings.Repeat("b", 31) }),
		"port 0":                   with(func(i *Input) { i.Port = 0 }),
		"port -1":                  with(func(i *Input) { i.Port = -1 }),
		"port 65536":               with(func(i *Input) { i.Port = 65536 }),
		"relative":                 with(func(i *Input) { i.Path = "app" }),
		"traversal":                with(func(i *Input) { i.Path = "/app/../../etc/passwd" }),
		"traversal to the root":    with(func(i *Input) { i.Path = "/app/.." }),
		"a single dot":             with(func(i *Input) { i.Path = "/./app" }),
		"an option":                with(func(i *Input) { i.Path = "/app --to evil:80" }),
		"a dash-first path":        with(func(i *Input) { i.Path = "-p" }),
		"a caddy placeholder":      with(func(i *Input) { i.Path = "/{http.request.host}" }),
		"a wildcard":               with(func(i *Input) { i.Path = "/app*" }),
		"a query":                  with(func(i *Input) { i.Path = "/app?x" }),
		"a fragment":               with(func(i *Input) { i.Path = "/app#x" }),
		"a percent":                with(func(i *Input) { i.Path = "/app%2e%2e" }),
		"a backslash":              with(func(i *Input) { i.Path = `/app\..\x` }),
		"a quote":                  with(func(i *Input) { i.Path = `/app"` }),
		"a newline":                with(func(i *Input) { i.Path = "/app\n/x" }),
		"a nul":                    with(func(i *Input) { i.Path = "/app\x00" }),
		"unicode":                  with(func(i *Input) { i.Path = "/äpp" }),
		"101 characters":           with(func(i *Input) { i.Path = "/" + strings.Repeat("a", 100) }),
		"the root":                 with(func(i *Input) { i.Path = "/" }),
		"the root, with the flag":  with(func(i *Input) { i.Path = "/"; i.WholeSite = true }), // not custom
		"the root, custom, unsaid": {Kind: "custom", Process: "ws", Port: 1, Path: "/"},
		"the root by cleaning":     {Kind: "custom", Process: "ws", Port: 1, Path: "///"},
		"whole_site elsewhere":     {Kind: "custom", Process: "ws", Port: 1, Path: "/x", WholeSite: true},
	} {
		if _, err := Clean(in); err == nil {
			t.Errorf("%s: accepted", name)
		} else if ae, ok := err.(*deploy.ActionError); !ok || ae.Status != 422 {
			t.Errorf("%s: %v is not a 422", name, err)
		}
	}
	if p, err := Clean(Input{Kind: "ws", Process: "ws", Port: 1, Path: "/" + strings.Repeat("a", 99)}); err != nil || len(p.Path) != 100 {
		t.Errorf("100 characters is the most: %v", err)
	}
}

// routed is read from what the live release recorded, never from the configuration alone.
func TestRoutedComesFromTheReleaseRecord(t *testing.T) {
	cfg := &db.AppRealtime{Enabled: true, Kind: "reverb", Process: "reverb", Port: 8080, Path: "/app"}
	rel := func(containers string) *db.Release { return &db.Release{Containers: &containers} }
	recorded := `{"web":"w","workers":["r"],"volumes":[],"realtime":{"process":"reverb","container":"r","port":8080,"host_port":41002,"path":"/app"}}`

	if rt := deploy.RealtimeRouteOf(cfg, rel(recorded)); rt == nil || rt.Upstream != "127.0.0.1:41002" || rt.Path != "/app" {
		t.Fatalf("recorded and enabled: %+v", rt)
	}
	moved := *cfg
	moved.Path = "/sockets"
	if rt := deploy.RealtimeRouteOf(&moved, rel(recorded)); rt == nil || rt.Path != "/sockets" {
		t.Errorf("a changed path is served by the same published port: %+v", rt)
	}
	for name, tc := range map[string]struct {
		cfg *db.AppRealtime
		rel *db.Release
	}{
		"no configuration":            {nil, rel(recorded)},
		"nothing live":                {cfg, nil},
		"a release without record":    {cfg, rel(`{"web":"w","workers":["r"],"volumes":[]}`)},
		"a release without a port":    {cfg, rel(`{"web":"w","realtime":{"process":"reverb","port":8080,"host_port":0}}`)},
		"disabled":                    {&db.AppRealtime{Enabled: false, Kind: "reverb", Process: "reverb", Port: 8080, Path: "/app"}, rel(recorded)},
		"another process":             {&db.AppRealtime{Enabled: true, Kind: "ws", Process: "ws", Port: 8080, Path: "/app"}, rel(recorded)},
		"another port":                {&db.AppRealtime{Enabled: true, Kind: "reverb", Process: "reverb", Port: 9090, Path: "/app"}, rel(recorded)},
		"a path that is not valid":    {&db.AppRealtime{Enabled: true, Kind: "reverb", Process: "reverb", Port: 8080, Path: "/app/../x"}, rel(recorded)},
		"the root without custom":     {&db.AppRealtime{Enabled: true, Kind: "reverb", Process: "reverb", Port: 8080, Path: "/"}, rel(recorded)},
		"containers that are no JSON": {cfg, rel(`nope`)},
	} {
		if rt := deploy.RealtimeRouteOf(tc.cfg, tc.rel); rt != nil {
			t.Errorf("%s: routed %+v", name, rt)
		}
	}
}
