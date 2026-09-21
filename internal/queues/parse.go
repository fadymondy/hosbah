package queues

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
)

/*
Parsers: pure functions from what a command printed to what it means. Each either reads the WHOLE
output or returns ErrUnreadable — never a panic, never half a list. A caller turns ErrUnreadable into
readable:false, "could not read the output".
*/

// ErrUnreadable: the output is not in a form this package knows.
var ErrUnreadable = errors.New("could not read the output")

// maxException bounds a failed job's exception: its first line, at most this many characters.
const maxException = 300

// FailedJob is one failed job. It never carries the job's payload.
type FailedJob struct {
	ID         string  `json:"id"`
	Queue      string  `json:"queue"`
	Connection string  `json:"connection"`
	Name       string  `json:"name" doc:"The job's class or display name."`
	FailedAt   string  `json:"failed_at" doc:"As the framework printed it (its own timezone)."`
	Exception  *string `json:"exception" doc:"The first line of the exception, at most 300 characters; null when the installed version does not print it."`
}

func clean(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// firstLine is the first non-empty line of an exception, clipped.
func firstLine(s string) *string {
	for _, l := range strings.Split(s, "\n") {
		if l = clean(l, maxException); l != "" {
			return &l
		}
	}
	return nil
}

func lines(out string) []string {
	var ls []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			ls = append(ls, strings.TrimRight(l, " \t"))
		}
	}
	return ls
}

// scalar reads a JSON string or number as text.
func scalar(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// ParseFailedJSON reads `php artisan queue:failed --json`: a JSON array of failed jobs. The keys of
// the versions that have the option differ slightly, so id/uuid and name/class/display_name are all
// accepted; a payload, when the version prints one, is only looked at for the job's display name
// and is never returned.
func ParseFailedJSON(out string) ([]FailedJob, error) {
	doc := strings.TrimSpace(out)
	// A warning printed before the document (a deprecation notice) is not part of it.
	if i := strings.Index(doc, "["); i > 0 && !strings.ContainsAny(doc[:i], "{}") {
		doc = doc[i:]
	}
	if !strings.HasPrefix(doc, "[") {
		return nil, ErrUnreadable
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(doc), &rows); err != nil {
		return nil, ErrUnreadable
	}
	jobs := make([]FailedJob, 0, len(rows))
	for _, r := range rows {
		pick := func(keys ...string) string {
			for _, k := range keys {
				if v, ok := r[k]; ok {
					if s := scalar(v); s != "" {
						return s
					}
				}
			}
			return ""
		}
		j := FailedJob{ID: clean(pick("id", "uuid"), 120), Queue: clean(pick("queue"), 120), Connection: clean(pick("connection"), 120),
			Name: clean(pick("name", "class", "display_name", "displayName"), 300), FailedAt: clean(pick("failed_at", "failedAt"), 40)}
		if j.Name == "" {
			j.Name = clean(displayNameOf(r["payload"]), 300)
		}
		if raw, ok := r["exception"]; ok {
			j.Exception = firstLine(scalar(raw))
		}
		if j.ID == "" {
			return nil, ErrUnreadable
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

// displayNameOf reads displayName out of a job payload (an object, or that object as a JSON string).
func displayNameOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asText string
	if json.Unmarshal(raw, &asText) == nil {
		raw = json.RawMessage(asText)
	}
	var p struct {
		DisplayName string `json:"displayName"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	return p.DisplayName
}

var (
	// Laravel 9 and later: "  2026-09-20 03:00:12 <id> ......... redis@default App\Jobs\SendInvoice".
	failedRowRe = regexp.MustCompile(`^\s*(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\s+(\S+)\s+(?:\.+\s+)?([^\s@]+)@(\S+)\s+(\S+)$`)
	// "No failed jobs!" (up to 8) and "INFO  No failed jobs found." (9 and later).
	noFailedRe = regexp.MustCompile(`(?i)^\s*(INFO\s+)?No failed jobs( found)?[.!]?\s*$`)
	borderRe   = regexp.MustCompile(`^\+[-+]+\+$`)
)

// ParseFailedTable reads the human form of `php artisan queue:failed`: the box table of Laravel 8
// and earlier (ID, Connection, Queue, Class, Failed At) or the two-column list of 9 and later. A
// line that is neither makes the whole output unreadable.
func ParseFailedTable(out string) ([]FailedJob, error) {
	ls := lines(out)
	if len(ls) == 0 {
		return nil, ErrUnreadable
	}
	jobs := []FailedJob{}
	var header []string
	none := false
	for _, l := range ls {
		t := strings.TrimSpace(l)
		switch {
		case noFailedRe.MatchString(t):
			none = true
		case borderRe.MatchString(t):
		case strings.HasPrefix(t, "|") && strings.HasSuffix(t, "|"):
			cells := strings.Split(strings.Trim(t, "|"), "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			if header == nil {
				for i := range cells {
					cells[i] = strings.ToLower(cells[i])
				}
				header = cells
				continue
			}
			if len(cells) != len(header) {
				return nil, ErrUnreadable
			}
			j := FailedJob{}
			for i, h := range header {
				switch h {
				case "id", "uuid":
					j.ID = clean(cells[i], 120)
				case "connection":
					j.Connection = clean(cells[i], 120)
				case "queue":
					j.Queue = clean(cells[i], 120)
				case "class", "name":
					j.Name = clean(cells[i], 300)
				case "failed at":
					j.FailedAt = clean(cells[i], 40)
				}
			}
			if j.ID == "" {
				return nil, ErrUnreadable
			}
			jobs = append(jobs, j)
		default:
			m := failedRowRe.FindStringSubmatch(t)
			if m == nil {
				return nil, ErrUnreadable
			}
			jobs = append(jobs, FailedJob{FailedAt: m[1], ID: clean(m[2], 120), Connection: clean(m[3], 120), Queue: clean(m[4], 120), Name: clean(m[5], 300)})
		}
	}
	if none && len(jobs) > 0 {
		return nil, ErrUnreadable
	}
	if !none && len(jobs) == 0 {
		return nil, ErrUnreadable // a header and nothing else, or borders only
	}
	return jobs, nil
}

// HorizonState is what `php artisan horizon:status` said.
type HorizonState struct {
	Installed bool    `json:"installed"`
	Status    *string `json:"status" enum:"running,paused,inactive" doc:"null when Horizon is not installed."`
}

// ParseHorizonStatus reads horizon:status. An app without Horizon answers that the command (or its
// namespace) is not defined: installed=false. Anything else unknown is ErrUnreadable.
func ParseHorizonStatus(out string) (HorizonState, error) {
	low := strings.ToLower(out)
	is := func(s string) *string { return &s }
	switch {
	case strings.Contains(low, "horizon is running"):
		return HorizonState{Installed: true, Status: is("running")}, nil
	case strings.Contains(low, "horizon is paused"):
		return HorizonState{Installed: true, Status: is("paused")}, nil
	case strings.Contains(low, "horizon is inactive"):
		return HorizonState{Installed: true, Status: is("inactive")}, nil
	case strings.Contains(low, "horizon") && (strings.Contains(low, "is not defined") || strings.Contains(low, "no commands defined")):
		return HorizonState{}, nil
	}
	return HorizonState{}, ErrUnreadable
}

// ParseSizes reads laravelSizesScript's marked line: queue name → size (nil: that one could not be
// read). Names that do not pass ValidArg are dropped.
func ParseSizes(out string) (map[string]*int64, error) {
	for _, l := range lines(out) {
		doc, ok := strings.CutPrefix(strings.TrimSpace(l), sizesMarker)
		if !ok {
			continue
		}
		var raw map[string]*int64
		if err := json.Unmarshal([]byte(doc), &raw); err != nil {
			return nil, ErrUnreadable
		}
		sizes := map[string]*int64{}
		for name, n := range raw {
			if !ValidArg(name) || (n != nil && *n < 0) {
				continue
			}
			sizes[name] = n
		}
		return sizes, nil
	}
	return nil, ErrUnreadable
}

// ParseCeleryInspect reads `celery inspect active|reserved --json`: an object of worker → tasks. It
// returns how many tasks each queue (the task's routing key) holds, and how many workers answered.
func ParseCeleryInspect(out string) (byQueue map[string]int64, workers int, err error) {
	doc := strings.TrimSpace(out)
	if i := strings.Index(doc, "{"); i > 0 && !strings.ContainsAny(doc[:i], "[]") {
		doc = doc[i:]
	}
	if !strings.HasPrefix(doc, "{") {
		return nil, 0, ErrUnreadable
	}
	var reply map[string][]struct {
		ID           string `json:"id"`
		DeliveryInfo struct {
			RoutingKey string `json:"routing_key"`
		} `json:"delivery_info"`
	}
	dec := json.NewDecoder(strings.NewReader(doc))
	if err := dec.Decode(&reply); err != nil || len(reply) == 0 {
		return nil, 0, ErrUnreadable
	}
	if rest := strings.TrimSpace(doc[dec.InputOffset():]); rest != "" {
		return nil, 0, ErrUnreadable
	}
	byQueue = map[string]int64{}
	for worker, tasks := range reply {
		if !strings.Contains(worker, "@") {
			return nil, 0, ErrUnreadable // {"error": …} and the like are not worker replies
		}
		for _, t := range tasks {
			if t.ID == "" {
				return nil, 0, ErrUnreadable
			}
			q := clean(t.DeliveryInfo.RoutingKey, 120)
			if q == "" {
				q = "celery" // celery's default queue
			}
			byQueue[q]++
		}
	}
	return byQueue, len(reply), nil
}
