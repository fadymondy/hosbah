package queues

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDetect(t *testing.T) {
	for name, c := range map[string]struct {
		in         Inputs
		driver     string
		confidence string
		celeryApp  string
		readable   bool
	}{
		"a laravel worker process": {Inputs{Runtime: "railpack:php", Processes: map[string]string{"web": "", "worker:queue": "php artisan queue:work --tries=3"}},
			DriverLaravel, ConfidenceDetected, "", true},
		"horizon as a worker": {Inputs{Runtime: "dockerfile", Processes: map[string]string{"worker:horizon": "exec php /app/artisan horizon"}},
			DriverLaravel, ConfidenceDetected, "", true},
		"artisan only in the release command": {Inputs{Runtime: "railpack:php", Release: "php artisan migrate --force"},
			DriverLaravel, ConfidenceDetected, "", true},
		"php and APP_KEY": {Inputs{Runtime: "railpack:php", EnvNames: []string{"APP_KEY", "DB_HOST"}},
			DriverLaravel, ConfidenceLikely, "", true},
		"QUEUE_CONNECTION alone": {Inputs{Runtime: "dockerfile", EnvNames: []string{"QUEUE_CONNECTION"}},
			DriverLaravel, ConfidenceLikely, "", true},
		"APP_KEY without a php runtime says nothing": {Inputs{Runtime: "railpack:node", EnvNames: []string{"APP_KEY"}},
			DriverNone, ConfidenceUnknown, "", false},
		"celery with its app": {Inputs{Runtime: "railpack:python", Processes: map[string]string{"worker:celery": "celery -A shop.celery:app worker -l info"}},
			DriverCelery, ConfidenceDetected, "shop.celery:app", true},
		"celery --app=": {Inputs{Processes: map[string]string{"worker:c": "python -m celery --app=shop worker"}},
			DriverCelery, ConfidenceDetected, "shop", true},
		"celery whose app cannot be parsed": {Inputs{Processes: map[string]string{"worker:c": `celery -A "$CELERY_APP" worker`}},
			DriverCelery, ConfidenceDetected, "", false},
		"celery whose app is an injection": {Inputs{Processes: map[string]string{"worker:c": "celery -A shop;id worker"}},
			DriverCelery, ConfidenceDetected, "shop", true}, // the word ends at the ;, as it does for a shell
		"celery by environment only": {Inputs{Runtime: "railpack:python", EnvNames: []string{"CELERY_BROKER_URL"}},
			DriverCelery, ConfidenceLikely, "", false},
		"bullmq in a worker": {Inputs{Runtime: "railpack:node", Processes: map[string]string{"worker:jobs": "node dist/bullmq-worker.js"}},
			DriverBullMQ, ConfidenceDetected, "", false},
		"bullmq by environment": {Inputs{Runtime: "railpack:node", EnvNames: []string{"BULLMQ_PREFIX"}},
			DriverBullMQ, ConfidenceLikely, "", false},
		"asynq in a worker": {Inputs{Runtime: "railpack:golang", Processes: map[string]string{"worker:asynq": "/app/bin/asynq-worker"}},
			DriverAsynq, ConfidenceDetected, "", false},
		"a node app with redis and a worker is not called bullmq": {Inputs{Runtime: "railpack:node", Processes: map[string]string{"worker:jobs": "node worker.js"}, EnvNames: []string{"REDIS_URL"}},
			DriverNone, ConfidenceUnknown, "", false},
		"nothing at all": {Inputs{}, DriverNone, ConfidenceUnknown, "", false},
		"a process beats an environment variable": {Inputs{Processes: map[string]string{"worker:c": "celery -A shop worker"}, EnvNames: []string{"QUEUE_CONNECTION"}},
			DriverCelery, ConfidenceDetected, "shop", true},
	} {
		d := Detect(c.in)
		ok, why := d.Readable()
		if d.Driver != c.driver || d.Confidence != c.confidence || d.CeleryApp != c.celeryApp || ok != c.readable {
			t.Errorf("%s: %+v readable=%v (want %s/%s app=%q readable=%v)", name, d, ok, c.driver, c.confidence, c.celeryApp, c.readable)
		}
		if len(d.Evidence) == 0 {
			t.Errorf("%s: no evidence", name)
		}
		if ok == (why != "") {
			t.Errorf("%s: readable=%v with reason %q", name, ok, why)
		}
	}
	// Evidence names variables, never anything else of them; a process command is not echoed.
	d := Detect(Inputs{Processes: map[string]string{"worker:q": "php artisan queue:work --secret=hunter2"}, EnvNames: []string{"QUEUE_CONNECTION"}})
	if s := strings.Join(d.Evidence, "|"); strings.Contains(s, "hunter2") || !strings.Contains(s, "worker:q") || !strings.Contains(s, "QUEUE_CONNECTION") {
		t.Errorf("evidence: %q", s)
	}
}

func TestParseFailedJobs(t *testing.T) {
	want := []FailedJob{
		{ID: "9f3c1a52-7d0e-4c55-b0a1-5a7d3f1e8c21", Connection: "redis", Queue: "default", Name: `App\Jobs\SendInvoice`, FailedAt: "2026-09-20 03:00:12"},
		{ID: "0b8e6d1f-2c4a-4f7e-9d3b-6e1a2c3d4e5f", Connection: "redis", Queue: "emails", Name: `App\Jobs\SyncCatalogue`, FailedAt: "2026-09-19 22:41:07"},
		{ID: "17", Connection: "database", Queue: "default", Name: `App\Jobs\PruneExports`, FailedAt: "2026-09-18 01:15:00"},
	}
	for _, f := range []string{"failed_table_v8.txt", "failed_list_v10.txt"} {
		got, err := ParseFailedTable(fixture(t, f))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v %+v", f, err, got)
		}
	}
	for _, f := range []string{"failed_none_v8.txt", "failed_none_v10.txt"} {
		got, err := ParseFailedTable(fixture(t, f))
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("%s: %v %+v", f, err, got)
		}
	}

	got, err := ParseFailedJSON(fixture(t, "failed.json"))
	if err != nil || len(got) != 3 {
		t.Fatalf("json: %v %+v", err, got)
	}
	for i := range got {
		ex := got[i].Exception
		got[i].Exception = nil
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("json job %d: %+v", i, got[i])
		}
		switch i {
		case 0:
			if ex == nil || !strings.HasPrefix(*ex, `Symfony\Component\Mailer\Exception\TransportException: Connection could not`) ||
				strings.Contains(*ex, "Stack trace") || len([]rune(*ex)) > maxException {
				t.Errorf("exception is the first line, clipped: %v", ex)
			}
		case 2:
			if ex != nil {
				t.Errorf("an empty exception is null: %q", *ex)
			}
		}
	}
	// The payload is read for the display name only and appears nowhere in what is returned.
	if s := strings.ToLower(strings.Join(flat(got), "|")); strings.Contains(s, "customer@example.test") || strings.Contains(s, "commandname") {
		t.Errorf("a payload leaked: %s", s)
	}
	if got, err := ParseFailedJSON("[]"); err != nil || got == nil || len(got) != 0 {
		t.Errorf("an empty list: %v %+v", err, got)
	}
}

func flat(jobs []FailedJob) []string {
	var out []string
	for _, j := range jobs {
		out = append(out, j.ID, j.Queue, j.Connection, j.Name, j.FailedAt)
		if j.Exception != nil {
			out = append(out, *j.Exception)
		}
	}
	return out
}

// Garbage, another command's output, a cut document, or a good list with one bad line: the whole
// output is unreadable. Never a panic, never half a list.
func TestParsersRefuseWhatTheyDoNotKnow(t *testing.T) {
	list := fixture(t, "failed_list_v10.txt")
	doc := fixture(t, "failed.json")
	bad := map[string]string{
		"garbage":                 fixture(t, "garbage.txt"),
		"empty":                   "",
		"spaces":                  " \n\t\n",
		"binary":                  "\x00\xff\xfe\x01",
		"horizon's answer":        fixture(t, "horizon_running.txt"),
		"celery's answer":         fixture(t, "celery_active.json"),
		"a list and a stray line": list + "PHP Fatal error: allowed memory size exhausted\n",
		"a cut JSON document":     doc[:len(doc)/2],
		"a JSON object":           `{"id": 1}`,
		"a job without an id":     `[{"queue":"default"}]`,
		"a table header only":     "+----+\n| ID |\n+----+\n",
		"a row of another width":  "+----+-------+\n| ID | Queue |\n+----+-------+\n| 1 |\n+----+-------+\n",
		"an unclosed bracket":     "[[[[[[",
		"a very long line":        strings.Repeat("A", 1<<20),
	}
	for name, out := range bad {
		if jobs, err := ParseFailedTable(out); !errors.Is(err, ErrUnreadable) || jobs != nil {
			t.Errorf("table/%s: %v %+v", name, err, jobs)
		}
		if name != "celery's answer" { // an object is refused below as "a JSON object"
			if jobs, err := ParseFailedJSON(out); !errors.Is(err, ErrUnreadable) || jobs != nil {
				t.Errorf("json/%s: %v %+v", name, err, jobs)
			}
		}
		if name != "horizon's answer" {
			if h, err := ParseHorizonStatus(out); !errors.Is(err, ErrUnreadable) || h.Installed {
				t.Errorf("horizon/%s: %v %+v", name, err, h)
			}
		}
		if s, err := ParseSizes(out); !errors.Is(err, ErrUnreadable) || s != nil {
			t.Errorf("sizes/%s: %v %+v", name, err, s)
		}
		if name != "celery's answer" {
			if m, n, err := ParseCeleryInspect(out); !errors.Is(err, ErrUnreadable) || m != nil || n != 0 {
				t.Errorf("celery/%s: %v %+v", name, err, m)
			}
		}
	}
	for name, out := range map[string]string{
		"no nodes":           fixture(t, "celery_no_nodes.txt"),
		"an error object":    `{"error": "no nodes"}`,
		"a task without id":  `{"celery@w": [{"name": "x"}]}`,
		"two documents":      `{"celery@w": []} {"celery@w": []}`,
		"sizes that are not": sizesMarker + `[1,2,3]`,
	} {
		if m, _, err := ParseCeleryInspect(out); !errors.Is(err, ErrUnreadable) || m != nil {
			t.Errorf("celery/%s: %v %+v", name, err, m)
		}
	}
	if s, err := ParseSizes(sizesMarker + `[1,2,3]`); !errors.Is(err, ErrUnreadable) || s != nil {
		t.Errorf("sizes that are a list: %v %+v", err, s)
	}
}

func TestParseHorizonStatus(t *testing.T) {
	for f, want := range map[string]string{"horizon_running.txt": "running", "horizon_paused.txt": "paused", "horizon_inactive.txt": "inactive"} {
		h, err := ParseHorizonStatus(fixture(t, f))
		if err != nil || !h.Installed || h.Status == nil || *h.Status != want {
			t.Errorf("%s: %v %+v", f, err, h)
		}
	}
	h, err := ParseHorizonStatus(fixture(t, "horizon_missing.txt"))
	if err != nil || h.Installed || h.Status != nil {
		t.Errorf("an app without Horizon: %v %+v", err, h)
	}
}

func TestParseSizes(t *testing.T) {
	s, err := ParseSizes(fixture(t, "sizes.txt"))
	if err != nil || len(s) != 3 || s["default"] == nil || *s["default"] != 42 || s["emails"] == nil || *s["emails"] != 0 {
		t.Fatalf("sizes: %v %+v", err, s)
	}
	if v, ok := s["reports"]; !ok || v != nil {
		t.Errorf("a size that could not be read is null, not 0: %v %v", v, ok)
	}
}

func TestParseCeleryInspect(t *testing.T) {
	active, workers, err := ParseCeleryInspect(fixture(t, "celery_active.json"))
	if err != nil || workers != 2 || !reflect.DeepEqual(active, map[string]int64{"default": 1, "slow": 1}) {
		t.Fatalf("active: %v %d %+v", err, workers, active)
	}
	reserved, _, err := ParseCeleryInspect(fixture(t, "celery_reserved.json"))
	if err != nil || !reflect.DeepEqual(reserved, map[string]int64{"default": 2}) {
		t.Fatalf("reserved: %v %+v", err, reserved)
	}
}

// The argv of every catalogue entry, element by element — and each one is a valid app.exec that
// needs no shell.
func TestCatalogueArgv(t *testing.T) {
	const id = "9f3c1a52-7d0e-4c55-b0a1-5a7d3f1e8c21"
	action := func(a, job string) Command {
		c, err := LaravelAction(a, job)
		if err != nil {
			t.Fatalf("%s: %v", a, err)
		}
		return c
	}
	for name, c := range map[string]struct {
		cmd  Command
		want []string
	}{
		"failed json":     {laravelFailedJSON(), []string{"php", "artisan", "queue:failed", "--json", "--no-ansi", "--no-interaction"}},
		"failed table":    {laravelFailedTable(), []string{"php", "artisan", "queue:failed", "--no-ansi", "--no-interaction"}},
		"sizes":           {laravelSizes(), []string{"php", "-r", laravelSizesScript}},
		"horizon":         {laravelHorizonStatus(), []string{"php", "artisan", "horizon:status", "--no-ansi", "--no-interaction"}},
		"celery":          {celeryInspect("shop.celery:app", "active"), []string{"celery", "-A", "shop.celery:app", "inspect", "active", "--json", "--timeout", "5"}},
		"retry":           {action(ActionRetry, id), []string{"php", "artisan", "queue:retry", "--no-ansi", "--no-interaction", id}},
		"retry by number": {action(ActionRetry, "17"), []string{"php", "artisan", "queue:retry", "--no-ansi", "--no-interaction", "17"}},
		"forget":          {action(ActionForget, id), []string{"php", "artisan", "queue:forget", "--no-ansi", "--no-interaction", id}},
		"retry all":       {action(ActionRetryAll, "ignored; rm -rf /"), []string{"php", "artisan", "queue:retry", "--no-ansi", "--no-interaction", "all"}},
		"flush":           {action(ActionFlush, "$(ignored)"), []string{"php", "artisan", "queue:flush", "--no-ansi", "--no-interaction"}},
	} {
		if !reflect.DeepEqual(c.cmd.Argv, c.want) {
			t.Errorf("%s: %q", name, c.cmd.Argv)
		}
		p := agentproto.ExecParams{AppID: "00000000-0000-4000-8000-000000000001", DeploymentID: "00000000-0000-4000-8000-000000000002",
			Argv: c.cmd.Argv, TimeoutSeconds: c.cmd.Timeout}
		if err := p.Validate(); err != nil {
			t.Errorf("%s is not a valid app.exec: %v", name, err)
		}
		if c.cmd.Argv[0] == "sh" || c.cmd.Argv[0] == "bash" {
			t.Errorf("%s goes through a shell", name)
		}
	}
	// A catalogue entry is a fresh copy: appending to one never changes the next.
	a := laravelFailedTable()
	a.Argv[2] = "down"
	if laravelFailedTable().Argv[2] != "queue:failed" {
		t.Error("the catalogue was modified through a returned argv")
	}
}

func TestJobIDsThatAreRefused(t *testing.T) {
	for _, id := range []string{"", " ", "; rm -rf /", "1; rm -rf /", "$(id)", "`id`", "a b", "1 2", "-1", "--queue=default", "-", "all", "ALL",
		"a\nb", "a\x00b", "../../etc/passwd", "a/b", "a|b", "a&b", "a>b", "'x'", `"x"`, "a=b", "a,b", "é", strings.Repeat("a", 121)} {
		for _, action := range []string{ActionRetry, ActionForget} {
			if c, err := LaravelAction(action, id); !errors.Is(err, ErrBadJobID) || c.Argv != nil {
				t.Errorf("%s %q was accepted: %q", action, id, c.Argv)
			}
		}
	}
	for _, id := range []string{"17", "9f3c1a52-7d0e-4c55-b0a1-5a7d3f1e8c21", "job_1.2:3", strings.Repeat("a", 120)} {
		if _, err := CheckJobID(id); err != nil {
			t.Errorf("%q was refused: %v", id, err)
		}
	}
	if _, err := LaravelAction("drop", "1"); err == nil {
		t.Error("an unknown action was accepted")
	}
}
