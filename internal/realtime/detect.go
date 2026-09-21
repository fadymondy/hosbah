// Package realtime is the "Realtime" section of a git-deployed site (app): its websocket server runs
// as one worker process of the release, and Caddy sends a path of the site's hostnames to it.
//
// The configuration is one app_realtime row. The deploy pipeline (internal/deploy/realtime.go) turns
// it into typed start params — the agent publishes that worker's port on loopback, as it does for
// web — and into a path route in front of the site's catch-all. Whether the live release is routed
// is read from what that release recorded (releases.containers.realtime), never guessed.
//
// detect.go only SUGGESTS a configuration, from what Hosbah already knows about the app: the
// runtime its build detected, the processes of its live release (cloudy.toml) and the NAMES of its
// environment variables — never their values. Nothing is saved from a suggestion: the caller saves.
package realtime

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Kinds.
const (
	KindReverb   = "reverb"
	KindSocketIO = "socketio"
	KindWS       = "ws"
	KindGo       = "go"
	KindCustom   = "custom"
)

// Kinds is every kind, in the order the API documents them.
var Kinds = []string{KindReverb, KindSocketIO, KindWS, KindGo, KindCustom}

// ValidKind says whether k is a kind.
func ValidKind(k string) bool {
	for _, x := range Kinds {
		if x == k {
			return true
		}
	}
	return false
}

// DefaultPath is the path a kind is usually served at.
func DefaultPath(kind string) string {
	switch kind {
	case KindReverb:
		return "/app"
	case KindSocketIO:
		return "/socket.io"
	}
	return "/ws"
}

// How sure a suggestion is (the words internal/queues uses).
const (
	// ConfidenceDetected: a process of the live release runs the framework's own realtime server.
	ConfidenceDetected = "detected"
	// ConfidenceLikely: only indirect signs — a file name in a command, a process's name, names of
	// environment variables.
	ConfidenceLikely = "likely"
	// ConfidenceUnknown: nothing names a realtime server.
	ConfidenceUnknown = "unknown"
)

// Inputs is what detection reads. All of it is the control plane's own knowledge of the app.
type Inputs struct {
	Runtime   string            // apps.runtime: "dockerfile", "railpack" or "railpack:<provider>"
	Processes map[string]string // the live release's processes (cloudy.toml): name → command
	EnvNames  []string          // names of the app's environment variables — never values
}

// RealtimeSuggestion is what the app looks like it needs. Kind "" (confidence unknown) suggests nothing.
// Process and Port are empty/0 when nothing dependable says them — the caller fills them in.
type RealtimeSuggestion struct {
	Kind       string   `json:"kind" doc:"reverb, socketio, ws or go; empty when nothing was found."`
	Process    string   `json:"process" doc:"The worker of the live release that looks like the realtime server (bare name: reverb is cloudy.toml's worker:reverb); empty when no process could be named."`
	Port       int      `json:"port" doc:"The port that process listens on inside its container: read from its command (--port) or the kind's default; 0 when unknown."`
	Path       string   `json:"path" doc:"The kind's usual path; empty when nothing was found."`
	Confidence string   `json:"confidence" enum:"detected,likely,unknown" doc:"detected: a process of the live release runs the framework's realtime server. likely: only indirect signs. unknown: nothing found."`
	Evidence   []string `json:"evidence" doc:"What was looked at and what it said. Names of environment variables only, never values."`
}

func provider(runtime string) string {
	_, p, _ := strings.Cut(strings.ToLower(strings.TrimSpace(runtime)), ":")
	return p
}

// words splits a command the way a reader would, not the way a shell does. Nothing is executed.
func words(command string) []string {
	f := strings.FieldsFunc(command, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ';' || r == '&' || r == '|' || r == '(' || r == ')'
	})
	for i := range f {
		f[i] = strings.Trim(f[i], `"'`)
	}
	return f
}

func baseName(w string) string {
	if i := strings.LastIndexAny(w, `/\`); i >= 0 {
		return w[i+1:]
	}
	return w
}

// portOf finds `--port N`, `--port=N` or `-p N` in a command (0: none, or not a port).
func portOf(ws []string) int {
	for i, w := range ws {
		var v string
		switch {
		case (w == "--port" || w == "-p") && i+1 < len(ws):
			v = ws[i+1]
		case strings.HasPrefix(w, "--port="):
			v = strings.TrimPrefix(w, "--port=")
		default:
			continue
		}
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 65535 {
			return n
		}
	}
	return 0
}

// wsFileRe: a script whose name says it is a websocket server (ws.js, ws-server.mjs, websocket.ts,
// socket-server.js, realtime.js…).
var wsFileRe = regexp.MustCompile(`^(ws|websocket|websockets|socket|sockets|realtime)([._-](server|srv))?\.(js|mjs|cjs|ts)$`)

// realtimeNameRe: a worker whose NAME says it is the realtime process.
var realtimeNameRe = regexp.MustCompile(`^(ws|websocket|websockets|realtime|socket|sockets)$`)

// workerName is the bare name of a worker ("" for web, or for a name realtime could not use).
func workerName(process string) string {
	name, ok := strings.CutPrefix(process, "worker:")
	if !ok || !agentproto.ValidRealtimeProcess(name) {
		return ""
	}
	return name
}

// Detect suggests a realtime configuration. A process that runs the framework's own server
// (`artisan reverb:start`) is "detected"; a file name, a process name or the names of environment
// variables only make it "likely"; with nothing the kind is empty and the confidence "unknown" —
// and the evidence always says what was looked at.
func Detect(in Inputs) RealtimeSuggestion {
	names := make([]string, 0, len(in.Processes))
	for n := range in.Processes {
		names = append(names, n)
	}
	sort.Strings(names)

	// 1. A worker that runs Laravel Reverb.
	for _, n := range names {
		w := workerName(n)
		if w == "" {
			continue
		}
		ws := words(in.Processes[n])
		for _, x := range ws {
			if x == "reverb:start" {
				s := RealtimeSuggestion{Kind: KindReverb, Process: w, Port: 8080, Path: DefaultPath(KindReverb), Confidence: ConfidenceDetected,
					Evidence: []string{"process " + n + " runs reverb:start"}}
				if p := portOf(ws); p != 0 {
					s.Port = p
					s.Evidence = append(s.Evidence, "its command says --port "+strconv.Itoa(p))
				} else {
					s.Evidence = append(s.Evidence, "no --port in its command: Reverb's default, 8080")
				}
				return s
			}
		}
	}

	// 2. A worker whose command mentions socket.io, or a websocket server file.
	for _, n := range names {
		w := workerName(n)
		if w == "" {
			continue
		}
		cmd := in.Processes[n]
		ws := words(cmd)
		if strings.Contains(strings.ToLower(cmd), "socket.io") {
			return RealtimeSuggestion{Kind: KindSocketIO, Process: w, Port: portOf(ws), Path: DefaultPath(KindSocketIO), Confidence: ConfidenceLikely,
				Evidence: []string{"process " + n + "'s command mentions socket.io", portEvidence(portOf(ws))}}
		}
		for _, x := range ws {
			if wsFileRe.MatchString(strings.ToLower(baseName(x))) {
				return RealtimeSuggestion{Kind: KindSocketIO, Process: w, Port: portOf(ws), Path: DefaultPath(KindSocketIO), Confidence: ConfidenceLikely,
					Evidence: []string{"process " + n + " runs " + baseName(x) + ", which is named like a websocket server",
						"socket.io is a guess: choose ws if it is a plain websocket server", portEvidence(portOf(ws))}}
			}
		}
	}

	// 3. A Go app with a worker named like a realtime process.
	prov := provider(in.Runtime)
	if prov == "golang" || prov == "go" {
		for _, n := range names {
			if w := workerName(n); w != "" && realtimeNameRe.MatchString(w) {
				ws := words(in.Processes[n])
				return RealtimeSuggestion{Kind: KindGo, Process: w, Port: portOf(ws), Path: DefaultPath(KindGo), Confidence: ConfidenceLikely,
					Evidence: []string{"the build detected Go", "process " + n + " is named like a realtime process", portEvidence(portOf(ws))}}
			}
		}
	}

	// 4. Only names of environment variables.
	var reverbEnv []string
	for _, e := range in.EnvNames {
		if strings.HasPrefix(e, "REVERB_") {
			reverbEnv = append(reverbEnv, e)
		}
	}
	if len(reverbEnv) > 0 {
		sort.Strings(reverbEnv)
		if len(reverbEnv) > 5 {
			reverbEnv = reverbEnv[:5]
		}
		return RealtimeSuggestion{Kind: KindReverb, Port: 8080, Path: DefaultPath(KindReverb), Confidence: ConfidenceLikely,
			Evidence: []string{"environment variables named " + strings.Join(reverbEnv, ", "),
				`no process runs reverb:start — declare one in cloudy.toml, for example "worker:reverb" = "php artisan reverb:start --host=0.0.0.0 --port=8080"`}}
	}

	looked := "no process of the live release"
	if len(names) > 0 {
		looked = "processes " + strings.Join(names, ", ")
	}
	return RealtimeSuggestion{Confidence: ConfidenceUnknown, Evidence: []string{
		looked + ": none runs reverb:start, mentions socket.io or a websocket server file, or (for a Go app) is named ws, realtime or socket",
		"no environment variable is named REVERB_*"}}
}

func portEvidence(p int) string {
	if p == 0 {
		return "its command names no --port: the port is yours to fill in"
	}
	return "its command says --port " + strconv.Itoa(p)
}
