package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// ---- app.logs.search -------------------------------------------------------------------------
//
// H4: nothing is stored on the control plane. The agent runs `docker logs --since/--until` over
// the app's current containers (the ones matching Process, or every one) and filters the lines
// itself, returning at most Limit matches. Filtering (logMatcher, detectLevel) is pure and unit
// tested without docker.

// logMatcher decides whether one already-timestamp-stripped log line matches a search.
type logMatcher func(line string) bool

// buildLogMatcher turns a validated LogsSearchParams into a matcher. An empty query matches every
// line (the level filter, if any, still applies on top).
func buildLogMatcher(p agentproto.LogsSearchParams) (logMatcher, error) {
	if p.Query == "" {
		return func(string) bool { return true }, nil
	}
	if p.Regex {
		re, err := agentproto.CompileLogSearchRegex(p.Query, p.CaseInsensitive)
		if err != nil {
			return nil, err
		}
		return re.MatchString, nil
	}
	needle := p.Query
	if p.CaseInsensitive {
		needle = strings.ToLower(needle)
		return func(line string) bool { return strings.Contains(strings.ToLower(line), needle) }, nil
	}
	return func(line string) bool { return strings.Contains(line, needle) }, nil
}

// errorWordRE / warnWordRE: whole-word, case-insensitive — the level heuristic. Deliberately
// simple (no structured-log parsing): "error" in "no errors found" still counts, which is the
// right side to err on for a search tool.
var (
	errorWordRE = regexp.MustCompile(`(?i)\b(error|err|fatal|panic|exception|failed|failure)\b`)
	warnWordRE  = regexp.MustCompile(`(?i)\b(warn|warning|deprecated)\b`)
)

// detectLevel is the level heuristic: error beats warn beats "" (no signal).
func detectLevel(line string) string {
	switch {
	case errorWordRE.MatchString(line):
		return agentproto.LogLevelError
	case warnWordRE.MatchString(line):
		return agentproto.LogLevelWarn
	default:
		return ""
	}
}

// splitDockerTimestamp separates `docker logs --timestamps`' leading RFC3339Nano timestamp from
// the rest of the line. ok is false for a line that somehow has none (kept, with a zero time,
// rather than dropped).
func splitDockerTimestamp(line string) (t time.Time, rest string, ok bool) {
	ts, msg, found := strings.Cut(line, " ")
	if !found {
		return time.Time{}, line, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, line, false
	}
	return parsed, msg, true
}

// dockerLogsSearchArgs is the docker argv app.logs.search runs per container.
func dockerLogsSearchArgs(name, since, until string) []string {
	args := []string{"logs", "--timestamps", "--since", since}
	if until != "" {
		args = append(args, "--until", until)
	}
	return append(args, name)
}

// searchContainer scans one container's log within [since,until), feeding matches to add until it
// returns false (the limit was reached). Returns how many raw lines were scanned and whether the
// scan was cut off by MaxLogSearchScanLines.
func searchContainer(ctx context.Context, name, process, since, until string, match logMatcher, level string,
	add func(agentproto.LogSearchLine) bool) (scanned int, cutOff bool, err error) {
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "docker", dockerLogsSearchArgs(name, since, until)...) //#nosec G204 -- fixed argv, validated name
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return 0, false, err
	}
	stop := false
	pump := func(r io.Reader, stream string) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			if stop {
				continue
			}
			scanned++
			if scanned > agentproto.MaxLogSearchScanLines {
				cutOff, stop = true, true
				cancel()
				continue
			}
			at, msg, _ := splitDockerTimestamp(sc.Text())
			lvl := detectLevel(msg)
			if level != "" && lvl != level {
				continue
			}
			if !match(msg) {
				continue
			}
			if !add(agentproto.LogSearchLine{At: at, Process: process, Stream: stream, Level: lvl, Line: clip(msg, maxLine)}) {
				stop = true
				cancel()
			}
		}
	}
	done := make(chan struct{}, 2)
	go func() { pump(stdout, "stdout"); done <- struct{}{} }()
	go func() { pump(stderr, "stderr"); done <- struct{}{} }()
	<-done
	<-done
	_ = cmd.Wait()
	return scanned, cutOff, nil
}

// appLogsSearch runs app.logs.search: one or every process's containers, filtered.
func appLogsSearch(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.LogsSearchParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	since, until, err := agentproto.ParseLogSearchWindow(p.Since, p.Until)
	if err != nil {
		return nil, 2, err
	}
	limit := p.Limit
	if limit == 0 {
		limit = 200
	}
	match, err := buildLogMatcher(p)
	if err != nil {
		return nil, 2, err
	}
	names, err := appContainers(oc, p.AppID, p.Process, true)
	if err != nil {
		return nil, 1, err
	}
	if len(names) == 0 {
		return agentproto.LogsSearchResult{Lines: []agentproto.LogSearchLine{}}, 0, nil
	}
	procOf, err := containerProcesses(oc, names)
	if err != nil {
		procOf = map[string]string{}
	}
	res := agentproto.LogsSearchResult{Lines: []agentproto.LogSearchLine{}}
	sinceS, untilS := since.UTC().Format(time.RFC3339Nano), until.UTC().Format(time.RFC3339Nano)
	oc.Info("searching %d container(s) between %s and %s", len(names), sinceS, untilS)
	for _, name := range names {
		if len(res.Lines) >= limit {
			res.Truncated = true
			break
		}
		proc := procOf[name]
		if proc == "" {
			proc = p.Process
		}
		res.SearchedContainers = append(res.SearchedContainers, name)
		_, cutOff, err := searchContainer(oc.Ctx, name, proc, sinceS, untilS, match, p.Level, func(l agentproto.LogSearchLine) bool {
			res.Lines = append(res.Lines, l)
			return len(res.Lines) < limit
		})
		if err != nil {
			oc.Info("%s: %v", name, err)
			continue
		}
		if cutOff {
			res.Truncated = true
		}
	}
	oc.Info("%d matching line(s)", len(res.Lines))
	return res, 0, nil
}

// containerProcesses reads the hosbah.process label of each container (docker inspect, one call).
func containerProcesses(oc *OpContext, names []string) (map[string]string, error) {
	raw, err := appDocker(oc.Ctx, nil, "docker", append([]string{"inspect"}, names...)...)
	if err != nil {
		return nil, errors.New("could not inspect the app's containers")
	}
	var raws []processInspect
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil, errors.New("could not read the app's container state")
	}
	out := map[string]string{}
	for _, r := range raws {
		name := strings.TrimPrefix(r.Name, "/")
		proc := r.Config.Labels[agentproto.LabelProcess]
		if proc == "" {
			proc = r.Config.Labels[agentproto.LegacyLabel(agentproto.LabelProcess)]
		}
		out[name] = proc
	}
	return out, nil
}
