package agentproto

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// H4: searching runtime logs. Nothing is stored on the control plane — app.logs.search asks the
// agent to run `docker logs --since/--until` over the app's current containers (selectable by
// process), filter the lines THERE (plain substring or an RE2 regular expression, optionally
// case-insensitive, plus an error/warn level heuristic) and relay back at most Limit matching
// lines. Bounded in time window, output size and — by the operation's own timeout — in wall time.
const KindAppLogsSearch = "app.logs.search"

// Limits.
const (
	MaxLogSearchLines    = 1000
	MaxLogSearchWindow   = 7 * 24 * time.Hour
	MaxLogSearchQueryLen = 500
	MaxLogSearchRegexLen = 200
	// MaxLogSearchScanLines bounds how many raw lines the agent will read per container before
	// giving up — independent of how many of them match — so an unbounded query over a chatty
	// container cannot run forever.
	MaxLogSearchScanLines = 200_000
)

// Level heuristics.
const (
	LogLevelError = "error"
	LogLevelWarn  = "warn"
)

// LogsSearchParams: app.logs.search.
type LogsSearchParams struct {
	AppID   string `json:"app_id"`
	Process string `json:"process,omitempty"` // "" = every process of the app
	Query   string `json:"query,omitempty"`   // "" = every line (still bounded by since/until/limit)
	Regex   bool   `json:"regex,omitempty"`   // Query is an RE2 pattern, not a plain substring
	// CaseInsensitive folds a plain-substring query, or is prepended to a regex as (?i).
	CaseInsensitive bool `json:"case_insensitive,omitempty"`
	// Level, when set, only keeps lines the heuristic reads as this level (error or warn) — on top
	// of any Query.
	Level string `json:"level,omitempty"`
	// Since/Until are RFC 3339 timestamps. Since defaults to Until (or now) minus one hour when
	// empty; Until defaults to now when empty. The window can never exceed MaxLogSearchWindow.
	Since string `json:"since,omitempty"`
	Until string `json:"until,omitempty"`
	Limit int    `json:"limit,omitempty"` // 1–MaxLogSearchLines, default 200
}

// Validate checks the params on both sides of the wire.
func (p LogsSearchParams) Validate() error {
	if !ValidID(p.AppID) {
		return errors.New("bad app id")
	}
	if p.Process != "" && !ValidProcess(p.Process) {
		return fmt.Errorf("bad process %q", p.Process)
	}
	if len(p.Query) > MaxLogSearchQueryLen {
		return fmt.Errorf("the query is at most %d characters", MaxLogSearchQueryLen)
	}
	if p.Regex {
		if len(p.Query) > MaxLogSearchRegexLen {
			return fmt.Errorf("a regex query is at most %d characters", MaxLogSearchRegexLen)
		}
		if _, err := CompileLogSearchRegex(p.Query, p.CaseInsensitive); p.Query != "" && err != nil {
			return fmt.Errorf("bad regex: %w", err)
		}
	}
	switch p.Level {
	case "", LogLevelError, LogLevelWarn:
	default:
		return errors.New("level is error or warn")
	}
	since, until, err := ParseLogSearchWindow(p.Since, p.Until)
	if err != nil {
		return err
	}
	if until.Before(since) {
		return errors.New("until must not be before since")
	}
	if until.Sub(since) > MaxLogSearchWindow {
		return fmt.Errorf("the time window is at most %s", MaxLogSearchWindow)
	}
	if p.Limit < 0 || p.Limit > MaxLogSearchLines {
		return fmt.Errorf("limit is 0–%d", MaxLogSearchLines)
	}
	return nil
}

// CompileLogSearchRegex compiles a query as RE2, optionally case-insensitive. The length cap in
// Validate keeps compilation itself bounded; RE2 (unlike PCRE/backtracking engines) has no
// catastrophic-backtracking case to worry about beyond that.
func CompileLogSearchRegex(query string, caseInsensitive bool) (*regexp.Regexp, error) {
	pat := query
	if caseInsensitive {
		pat = "(?i)" + pat
	}
	return regexp.Compile(pat)
}

// logSearchRelativeRE: a bare duration ("15m", "1h", "6h", "24h", "7d" — the web's canned ranges) as
// well as an RFC 3339 time.
var logSearchRelativeRE = regexp.MustCompile(`^([0-9]{1,4})([smhd])$`)

// parseLogSearchTime resolves one bound: a relative duration is that much before now; anything else
// must be RFC 3339.
func parseLogSearchTime(s string) (time.Time, error) {
	if m := logSearchRelativeRE.FindStringSubmatch(s); m != nil {
		var n int
		fmt.Sscanf(m[1], "%d", &n) //nolint:errcheck // m[1] is \d{1,4} by the regexp
		unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
		return time.Now().UTC().Add(-time.Duration(n) * unit), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be a duration (15m, 1h, 6h, 24h, 7d) or an RFC 3339 time: %w", err)
	}
	return t, nil
}

// ParseLogSearchWindow resolves since/until to concrete bounds: until defaults to now, since
// defaults to one hour before until. Either may be a relative duration (15m, 1h, 6h, 24h, 7d) or an
// RFC 3339 time.
func ParseLogSearchWindow(sinceS, untilS string) (since, until time.Time, err error) {
	until = time.Now().UTC()
	if untilS != "" {
		if until, err = parseLogSearchTime(untilS); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("until %w", err)
		}
	}
	since = until.Add(-time.Hour)
	if sinceS != "" {
		if since, err = parseLogSearchTime(sinceS); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("since %w", err)
		}
	}
	return since.UTC(), until.UTC(), nil
}

// LogSearchLine is one matching line the agent relays back.
type LogSearchLine struct {
	At      time.Time `json:"at"`
	Process string    `json:"process"`
	Stream  string    `json:"stream"`
	Level   string    `json:"level,omitempty"`
	Line    string    `json:"line"`
}

// LogsSearchResult: app.logs.search's result.
type LogsSearchResult struct {
	Lines              []LogSearchLine `json:"lines"`
	Truncated          bool            `json:"truncated" doc:"true: Limit was reached, or a container's scan was cut off — narrow the query or window"`
	SearchedContainers []string        `json:"searched_containers,omitempty"`
}
