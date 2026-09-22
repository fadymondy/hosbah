// Package queues is the "Queues" of a git-deployed site: its job queues and its failed jobs.
//
// Nothing is stored. What a site's queues hold is read live, from the site's own release, by running
// the framework's own CLI inside the live web container (the agent's app.exec). Which CLI that is
// — the driver — is decided here, on the control plane, from what Hosbah already knows about the
// app: the runtime its build detected, the processes of its live release (hosbah.toml) and the NAMES
// of its environment variables. Never their values, and never anything a caller typed.
//
// Every command that runs is one of a small fixed catalogue (catalogue.go): constants, handed to
// docker exec as a real argv — no shell reads them, on the host or in the container. The only
// variable elements are a failed job's id and celery's app module, each checked against a strict
// charset and passed as an argv element of its own.
package queues

import (
	"regexp"
	"sort"
	"strings"
)

// Drivers.
const (
	DriverLaravel = "laravel"
	DriverBullMQ  = "bullmq"
	DriverAsynq   = "asynq"
	DriverCelery  = "celery"
	DriverNone    = "none"
)

// How sure the detection is.
const (
	// ConfidenceDetected: a process of the live release runs the framework's own program.
	ConfidenceDetected = "detected"
	// ConfidenceLikely: only indirect signs (the runtime, names of environment variables).
	ConfidenceLikely = "likely"
	// ConfidenceUnknown: nothing names a queue system.
	ConfidenceUnknown = "unknown"
)

// argRe is the charset of every variable argv element (a job id, a queue name, celery's app).
var argRe = regexp.MustCompile(`^[A-Za-z0-9_.:\-]{1,120}$`)

// ValidArg is the rule of a variable argv element: the strict charset, and no leading dash — it must
// never read as an option of the program it is handed to.
func ValidArg(s string) bool { return argRe.MatchString(s) && !strings.HasPrefix(s, "-") }

// Inputs is what detection reads. All of it is the control plane's own knowledge of the app.
type Inputs struct {
	Runtime   string            // apps.runtime: "dockerfile", "railpack" or "railpack:<provider>"
	Processes map[string]string // the live release's processes (hosbah.toml): name → command
	Release   string            // the live release's release command
	EnvNames  []string          // names of the app's environment variables — never values
}

// Detection is the driver, how sure it is, and why.
type Detection struct {
	Driver     string
	Confidence string
	Evidence   []string
	// CeleryApp is celery's app module (-A), when a process command names one that passes ValidArg.
	CeleryApp string
}

// provider is railpack's provider ("php", "node", "python", "golang"…), or "".
func provider(runtime string) string {
	_, p, _ := strings.Cut(strings.ToLower(strings.TrimSpace(runtime)), ":")
	return p
}

// words splits a process command the way a reader would, not the way a shell does: enough to find
// the program names in it. Nothing here is ever executed.
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

// celeryApp finds `-A x`, `--app x` or `--app=x` after the celery program.
func celeryApp(ws []string) string {
	for i, w := range ws {
		var v string
		switch {
		case (w == "-A" || w == "--app") && i+1 < len(ws):
			v = ws[i+1]
		case strings.HasPrefix(w, "--app="):
			v = strings.TrimPrefix(w, "--app=")
		default:
			continue
		}
		if ValidArg(v) {
			return v
		}
	}
	return ""
}

// Detect decides the driver. A process that runs the framework's own program is "detected";
// a runtime or the name of an environment variable only makes it "likely"; with neither the driver
// is "none" and the confidence "unknown" — and the evidence says what was looked at.
func Detect(in Inputs) Detection {
	type hit struct {
		evidence []string
		celery   string
	}
	detected, likely := map[string]*hit{}, map[string]*hit{}
	add := func(m map[string]*hit, driver, ev string) *hit {
		h := m[driver]
		if h == nil {
			h = &hit{}
			m[driver] = h
		}
		h.evidence = append(h.evidence, ev)
		return h
	}

	names := make([]string, 0, len(in.Processes))
	for n := range in.Processes {
		names = append(names, n)
	}
	sort.Strings(names)
	commands := make([][2]string, 0, len(names)+1)
	for _, n := range names {
		commands = append(commands, [2]string{"process " + n, in.Processes[n]})
	}
	commands = append(commands, [2]string{"the release command", in.Release})
	for _, c := range commands {
		ws := words(c[1])
		for i, w := range ws {
			lw := strings.ToLower(w)
			switch b := strings.ToLower(baseName(w)); {
			case b == "artisan":
				add(detected, DriverLaravel, c[0]+" runs artisan")
			case b == "celery" && (i == 0 || !strings.HasPrefix(ws[i-1], "-") || ws[i-1] == "-m"):
				h := add(detected, DriverCelery, c[0]+" runs celery")
				if app := celeryApp(ws[i+1:]); app != "" && h.celery == "" {
					h.celery = app
				}
			case strings.Contains(lw, "bullmq"):
				add(detected, DriverBullMQ, c[0]+" names bullmq")
			case strings.Contains(lw, "asynq"):
				add(detected, DriverAsynq, c[0]+" names asynq")
			}
		}
	}

	prov := provider(in.Runtime)
	env := append([]string(nil), in.EnvNames...)
	sort.Strings(env)
	for _, n := range env {
		u := strings.ToUpper(n)
		switch {
		case u == "QUEUE_CONNECTION" || strings.HasPrefix(u, "HORIZON_"):
			add(likely, DriverLaravel, "environment variable "+n+" is set")
		case u == "APP_KEY" && prov == "php":
			add(likely, DriverLaravel, "PHP runtime with environment variable "+n)
		case strings.HasPrefix(u, "CELERY_"):
			add(likely, DriverCelery, "environment variable "+n+" is set")
		case strings.Contains(u, "BULLMQ") || strings.HasPrefix(u, "BULL_"):
			add(likely, DriverBullMQ, "environment variable "+n+" is set")
		case strings.Contains(u, "ASYNQ"):
			add(likely, DriverAsynq, "environment variable "+n+" is set")
		}
	}

	order := []string{DriverLaravel, DriverCelery, DriverBullMQ, DriverAsynq}
	for _, d := range order {
		if h := detected[d]; h != nil {
			ev := h.evidence
			if l := likely[d]; l != nil {
				ev = append(ev, l.evidence...)
			}
			return Detection{Driver: d, Confidence: ConfidenceDetected, Evidence: dedupe(ev), CeleryApp: h.celery}
		}
	}
	for _, d := range order {
		if h := likely[d]; h != nil {
			return Detection{Driver: d, Confidence: ConfidenceLikely, Evidence: dedupe(h.evidence)}
		}
	}
	looked := "no process, release command or environment variable name of this app names a queue system"
	if prov != "" {
		looked = "the runtime is " + prov + ", but " + looked
	}
	return Detection{Driver: DriverNone, Confidence: ConfidenceUnknown, Evidence: []string{looked}}
}

func dedupe(in []string) []string {
	seen, out := map[string]bool{}, []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Readable says whether the driver's state can be read through a CLI that is normally in the image,
// and why not when it cannot. It never pretends: an unreadable driver is reported, not guessed at.
func (d Detection) Readable() (bool, string) {
	switch d.Driver {
	case DriverLaravel:
		return true, ""
	case DriverCelery:
		if d.CeleryApp == "" {
			return false, "celery was detected, but its app module (-A) could not be read from a worker process command in hosbah.toml (or cloudy.toml), so celery inspect cannot be run"
		}
		return true, ""
	case DriverBullMQ:
		return false, "BullMQ keeps its queues in Redis and ships no command-line tool in a normal image, so there is nothing dependable to run"
	case DriverAsynq:
		return false, "the asynq CLI is a separate binary that is not part of a normal app image, so there is nothing dependable to run"
	}
	return false, "no queue system was detected for this app"
}
