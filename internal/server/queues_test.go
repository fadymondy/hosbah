package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

type queuesJSON struct {
	Driver     string   `json:"driver"`
	Confidence string   `json:"confidence"`
	Evidence   []string `json:"evidence"`
	Readable   bool     `json:"readable"`
	Reason     *string  `json:"reason"`
	Horizon    *struct {
		Installed bool    `json:"installed"`
		Status    *string `json:"status"`
	} `json:"horizon"`
	Queues []struct {
		Name    string `json:"name"`
		Pending *int64 `json:"pending"`
		Running *int64 `json:"running"`
		Failed  *int64 `json:"failed"`
	} `json:"queues"`
	FailedTotal *int64 `json:"failed_total"`
	Operation   string `json:"operation"`
}

type failedJobsJSON struct {
	Jobs []struct {
		ID         string  `json:"id"`
		Queue      string  `json:"queue"`
		Connection string  `json:"connection"`
		Name       string  `json:"name"`
		FailedAt   string  `json:"failed_at"`
		Exception  *string `json:"exception"`
	} `json:"jobs"`
	Truncated bool   `json:"truncated"`
	Operation string `json:"operation"`
}

func queuesFixture(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "queues", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

/*
"Queues" end to end with a fake agent that answers app.exec the way a Laravel (or celery) container
would: detection from what the control plane knows, the exact argv of every command (never a shell),
the fallback from --json to the line form, what is unreadable staying unreadable, ids that are
refused before anything is queued, roles, other teams, imported apps, nothing live, an offline
server, a failing command, MCP and the audit trail.
*/
func TestAppQueues(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ctx := context.Background()

	owner := newClient(t, ts.URL)
	owner.register("queue-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Queues " + suffix}, 201, &team)
	var readTok, writeTok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "q-read", "scopes": []string{"read"}, "expires_in_days": 30}, 201, &readTok)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "q-write", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &writeTok)

	join := func(role string) *client {
		email := "queue-" + role + "-" + suffix + "@example.test"
		c := newClient(t, ts.URL)
		c.register(email)
		var inv struct {
			AcceptURL string `json:"accept_url"`
		}
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": email, "role": role}, 201, &inv)
		c.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)
		return c
	}
	viewer, deployer := join("viewer"), join("deployer")
	stranger := newClient(t, ts.URL)
	stranger.register("queue-stranger-" + suffix + "@example.test")
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, nil)

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "queue-"+short, "203.0.113.31", []agentproto.Service{
		{Kind: "container", Name: "imp-queue-" + short, Image: "nginx:1.27", Status: "running", Ports: []string{"0.0.0.0:8083->80/tcp"}}})
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "queue-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	appBase := func(id string) string { return "/api/teams/" + team.ID + "/apps/" + id + "/queues" }
	base := appBase(app.App.ID)

	// What the app looks like to detection: its runtime and the live release's processes.
	shape := func(runtime string, processes map[string]string) {
		t.Helper()
		cfg, _ := json.Marshal(map[string]any{"processes": processes})
		err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true, TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
			if _, err := tx.Exec(`UPDATE apps SET runtime = $1 WHERE id = $2`, runtime, app.App.ID); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE releases SET config = $1 WHERE app_id = $2`, string(cfg), app.App.ID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// The fake container: what each command prints and how it exits, keyed by the word that names it.
	type answer struct {
		lines []string
		exit  int
	}
	var (
		mu      sync.Mutex
		answers = map[string]answer{}
	)
	script := func(key string, lines []string, exit int) {
		mu.Lock()
		answers[key] = answer{lines, exit}
		mu.Unlock()
	}
	keyOf := func(op agentproto.SignedOp) (string, agentproto.ExecParams) {
		var p agentproto.ExecParams
		_ = json.Unmarshal(op.Params, &p)
		key := ""
		switch {
		case len(p.Argv) >= 2 && p.Argv[1] == "-r":
			key = "sizes"
		case len(p.Argv) >= 5 && p.Argv[0] == "celery":
			key = "celery " + p.Argv[4]
		case len(p.Argv) >= 4 && p.Argv[2] == "queue:failed" && p.Argv[3] == "--json":
			key = "queue:failed --json"
		case len(p.Argv) >= 3:
			key = p.Argv[2]
		}
		return key, p
	}
	answerOf := func(op agentproto.SignedOp) answer {
		key, _ := keyOf(op)
		mu.Lock()
		defer mu.Unlock()
		a, ok := answers[key]
		if !ok {
			return answer{[]string{"sh: command not scripted: " + key}, 127}
		}
		return a
	}
	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		if op.Kind != agentproto.KindAppExec {
			return nil, "this fake agent does not run " + op.Kind
		}
		a := answerOf(op)
		res := agentproto.ExecResult{Container: "hosbah-queue-web-1", ExitCode: a.exit}
		if a.exit != 0 {
			return res, fmt.Sprintf("the command exited with status %d", a.exit)
		}
		return res, ""
	})
	agent.mu.Lock()
	agent.events = func(op agentproto.SignedOp) []agentproto.Event {
		if op.Kind != agentproto.KindAppExec {
			return nil
		}
		var evs []agentproto.Event
		for _, l := range answerOf(op).lines {
			evs = append(evs, agentproto.Event{Kind: agentproto.EventLog, Stream: "stdout", Line: l})
		}
		return evs
	}
	agent.mu.Unlock()
	execCount := func() int { return len(agentOps(agent, agentproto.KindAppExec)) }
	argvOf := func(opID string) []string {
		t.Helper()
		op := agent.waitOp(t, opID)
		_, p := keyOf(op)
		if op.Kind != agentproto.KindAppExec || p.AppID != app.App.ID || p.Validate() != nil {
			t.Fatalf("typed params: %s %+v", op.Kind, p)
		}
		return p.Argv
	}

	t.Run("nothing live, other teams, imported apps", func(t *testing.T) {
		owner.mustJSON("GET", base, nil, 409, nil)
		owner.mustJSON("GET", base+"/failed", nil, 409, nil)
		owner.mustJSON("POST", base+"/failed/17/retry", nil, 409, nil)
		owner.mustJSON("DELETE", base+"/failed", nil, 409, nil)
		for _, c := range [][2]string{{"GET", base}, {"GET", base + "/failed"}, {"POST", base + "/failed/17/retry"}, {"DELETE", base + "/failed/17"},
			{"POST", base + "/failed/retry-all"}, {"DELETE", base + "/failed"}} {
			stranger.mustJSON(c[0], c[1], nil, 404, nil)
		}
		apps, _ := listImported(t, owner, team.ID, srv)
		imp, ok := apps["imp-queue-"+short]
		if !ok {
			t.Fatalf("no imported app: %+v", apps)
		}
		owner.mustJSON("GET", appBase(imp.ID), nil, 422, nil)
		owner.mustJSON("GET", appBase(imp.ID)+"/failed", nil, 422, nil)
		owner.mustJSON("POST", appBase(imp.ID)+"/failed/retry-all", nil, 422, nil)
		if n := execCount(); n != 0 {
			t.Fatalf("%d commands were queued for refused requests", n)
		}
	})

	deployment := seedLiveRelease(t, team.ID, app.App.ID, srv, app.App.Name)

	t.Run("drivers that are detected only", func(t *testing.T) {
		for name, c := range map[string]struct {
			runtime    string
			processes  map[string]string
			driver     string
			confidence string
		}{
			"bullmq":                {"railpack:node", map[string]string{"web": "", "worker:jobs": "node dist/bullmq-worker.js"}, "bullmq", "detected"},
			"asynq":                 {"railpack:golang", map[string]string{"web": "", "worker:jobs": "/app/bin/asynq-worker"}, "asynq", "detected"},
			"celery without -A":     {"railpack:python", map[string]string{"web": "", "worker:c": `celery -A "$APP" worker`}, "celery", "detected"},
			"nothing names a queue": {"railpack:node", map[string]string{"web": ""}, "none", "unknown"},
		} {
			shape(c.runtime, c.processes)
			var q queuesJSON
			owner.mustJSON("GET", base, nil, 200, &q)
			if q.Driver != c.driver || q.Confidence != c.confidence || q.Readable || q.Reason == nil || *q.Reason == "" || len(q.Evidence) == 0 ||
				q.Queues == nil || len(q.Queues) != 0 || q.FailedTotal != nil || q.Horizon != nil || q.Operation != "" {
				t.Errorf("%s: %+v", name, q)
			}
			owner.mustJSON("GET", base+"/failed", nil, 422, nil)
			owner.mustJSON("POST", base+"/failed/retry-all", nil, 422, nil)
		}
		if n := execCount(); n != 0 {
			t.Fatalf("an unreadable driver must run nothing, but %d commands were queued", n)
		}
	})

	t.Run("celery is read through celery inspect", func(t *testing.T) {
		shape("railpack:python", map[string]string{"web": "", "worker:celery": "celery -A shop.celery:app worker -l info"})
		script("celery active", queuesFixture(t, "celery_active.json"), 0)
		script("celery reserved", queuesFixture(t, "celery_reserved.json"), 0)
		var q queuesJSON
		owner.mustJSON("GET", base, nil, 200, &q)
		if q.Driver != "celery" || q.Confidence != "detected" || !q.Readable || q.Reason != nil || q.FailedTotal != nil || q.Horizon != nil || len(q.Queues) != 2 {
			t.Fatalf("celery: %+v", q)
		}
		d, s := q.Queues[0], q.Queues[1]
		if d.Name != "default" || *d.Running != 1 || *d.Pending != 2 || d.Failed != nil || s.Name != "slow" || *s.Running != 1 || *s.Pending != 0 {
			t.Fatalf("celery queues: %+v", q.Queues)
		}
		if got, want := argvOf(q.Operation), []string{"celery", "-A", "shop.celery:app", "inspect", "active", "--json", "--timeout", "5"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("argv: %q", got)
		}
		script("celery reserved", queuesFixture(t, "celery_no_nodes.txt"), 69)
		owner.mustJSON("GET", base, nil, 200, &q)
		if q.Readable || q.Reason == nil || !strings.Contains(*q.Reason, "No nodes replied") || len(q.Queues) != 0 {
			t.Fatalf("celery without workers: %+v", q)
		}
		owner.mustJSON("GET", base+"/failed", nil, 422, nil)
	})

	shape("railpack:php", map[string]string{"web": "", "worker:queue": "php artisan queue:work --tries=3"})
	script("queue:failed --json", queuesFixture(t, "failed.json"), 0)
	script("queue:failed", queuesFixture(t, "failed_list_v10.txt"), 0)
	script("sizes", queuesFixture(t, "sizes.txt"), 0)
	script("horizon:status", queuesFixture(t, "horizon_running.txt"), 0)

	t.Run("laravel: the overview", func(t *testing.T) {
		before := execCount()
		var q queuesJSON
		deployer.mustJSON("GET", base, nil, 200, &q)
		if q.Driver != "laravel" || q.Confidence != "detected" || !q.Readable || q.Reason != nil || q.FailedTotal == nil || *q.FailedTotal != 3 ||
			q.Horizon == nil || !q.Horizon.Installed || q.Horizon.Status == nil || *q.Horizon.Status != "running" {
			t.Fatalf("overview: %+v", q)
		}
		got := map[string][3]*int64{}
		for _, x := range q.Queues {
			got[x.Name] = [3]*int64{x.Pending, x.Running, x.Failed}
		}
		n := func(p *int64) int64 {
			if p == nil {
				return -1
			}
			return *p
		}
		if len(got) != 3 || n(got["default"][0]) != 42 || n(got["default"][2]) != 2 || n(got["emails"][0]) != 0 || n(got["emails"][2]) != 1 ||
			got["reports"][0] != nil || n(got["reports"][2]) != 0 || got["default"][1] != nil {
			t.Fatalf("queues (a name that fails the charset is dropped, an unread size is null, running is null): %+v", q.Queues)
		}
		if execCount()-before != 3 {
			t.Fatalf("want 3 commands, got %d", execCount()-before)
		}
		if got, want := argvOf(q.Operation), []string{"php", "artisan", "queue:failed", "--json", "--no-ansi", "--no-interaction"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("argv: %q", got)
		}
		for _, op := range agentOps(agent, agentproto.KindAppExec)[before:] {
			_, p := keyOf(op)
			if p.DeploymentID != deployment || p.Argv[0] == "sh" || p.TimeoutSeconds != 30 || op.TimeoutSeconds != 60 {
				t.Errorf("a read: %+v (op timeout %d)", p, op.TimeoutSeconds)
			}
		}
		// Nothing of a payload reaches the answer.
		if _, text := deployer.do("GET", base, nil); strings.Contains(text, "customer@example.test") {
			t.Fatal("the overview carries a payload")
		}

		// The command's output is the site's own: through the operation log a deployer reads it, a
		// viewer gets the operation and a notice instead of the lines.
		var servers struct {
			Servers []struct{ ID string }
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/servers", nil, 200, &servers)
		opServer := servers.Servers[0].ID
		opURL := "/api/teams/" + team.ID + "/servers/" + opServer + "/operations/" + q.Operation
		if status, text := viewer.do("GET", opURL, nil); status != 200 || !strings.Contains(text, "output is hidden") || strings.Contains(text, "customer@example.test") {
			t.Fatalf("a viewer reading an app.exec operation: %d %s", status, text)
		}
		if status, text := deployer.do("GET", opURL, nil); status != 200 || strings.Contains(text, "output is hidden") {
			t.Fatalf("a deployer reading an app.exec operation: %d %s", status, text)
		}

		// No Horizon, and sizes that cannot be read: null, not a guess.
		script("horizon:status", queuesFixture(t, "horizon_missing.txt"), 1)
		script("sizes", []string{"PHP Fatal error:  Uncaught Error: Class \"Redis\" not found"}, 255)
		owner.mustJSON("GET", base, nil, 200, &q)
		if !q.Readable || q.Horizon == nil || q.Horizon.Installed || q.Horizon.Status != nil || len(q.Queues) != 2 {
			t.Fatalf("without horizon and sizes: %+v", q)
		}
		for _, x := range q.Queues {
			if x.Pending != nil || x.Failed == nil {
				t.Errorf("sizes must be null: %+v", x)
			}
		}
		script("horizon:status", []string{"something nobody expected"}, 0)
		owner.mustJSON("GET", base, nil, 200, &q)
		if !q.Readable || q.Horizon != nil {
			t.Fatalf("an unreadable horizon:status is null: %+v", q.Horizon)
		}
		script("sizes", queuesFixture(t, "sizes.txt"), 0)
		script("horizon:status", queuesFixture(t, "horizon_running.txt"), 0)
	})

	t.Run("laravel: failed jobs, json first, the line form when --json is not there", func(t *testing.T) {
		var f failedJobsJSON
		deployer.mustJSON("GET", base+"/failed", nil, 200, &f)
		if len(f.Jobs) != 3 || f.Truncated || f.Jobs[0].ID != "9f3c1a52-7d0e-4c55-b0a1-5a7d3f1e8c21" || f.Jobs[0].Queue != "default" || f.Jobs[0].Connection != "redis" ||
			f.Jobs[0].Name != `App\Jobs\SendInvoice` || f.Jobs[0].FailedAt != "2026-09-20 03:00:12" || f.Jobs[0].Exception == nil ||
			strings.Contains(*f.Jobs[0].Exception, "Stack trace") || len([]rune(*f.Jobs[0].Exception)) > 300 || f.Jobs[2].ID != "17" {
			t.Fatalf("failed jobs: %+v", f)
		}
		_, text := deployer.do("GET", base+"/failed?limit=2", nil)
		if strings.Contains(text, "customer@example.test") || strings.Contains(text, "payload") || strings.Contains(text, "commandName") {
			t.Fatalf("a payload leaked: %s", text)
		}
		deployer.mustJSON("GET", base+"/failed?limit=2", nil, 200, &f)
		if len(f.Jobs) != 2 || !f.Truncated {
			t.Fatalf("limit: %+v", f)
		}
		owner.mustJSON("GET", base+"/failed?limit=0", nil, 422, nil)
		owner.mustJSON("GET", base+"/failed?limit=201", nil, 422, nil)

		// An older Laravel: --json is not an option. The line form is read instead, without exceptions.
		script("queue:failed --json", []string{"", `  The "--json" option does not exist.`, ""}, 1)
		before := execCount()
		deployer.mustJSON("GET", base+"/failed", nil, 200, &f)
		if len(f.Jobs) != 3 || f.Jobs[1].Queue != "emails" || f.Jobs[1].Exception != nil || execCount()-before != 2 {
			t.Fatalf("fallback: %+v (%d commands)", f, execCount()-before)
		}
		if got, want := argvOf(f.Operation), []string{"php", "artisan", "queue:failed", "--no-ansi", "--no-interaction"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("argv: %q", got)
		}
		script("queue:failed", queuesFixture(t, "failed_none_v10.txt"), 0)
		deployer.mustJSON("GET", base+"/failed", nil, 200, &f)
		if f.Jobs == nil || len(f.Jobs) != 0 || f.Truncated {
			t.Fatalf("no failed jobs: %+v", f)
		}

		// Garbage in both forms: not readable, with the fixed reason — and no half list anywhere.
		script("queue:failed --json", queuesFixture(t, "garbage.txt"), 0)
		script("queue:failed", append(queuesFixture(t, "failed_list_v10.txt"), "PHP Fatal error: allowed memory size exhausted"), 0)
		var q queuesJSON
		owner.mustJSON("GET", base, nil, 200, &q)
		if q.Driver != "laravel" || q.Readable || q.Reason == nil || *q.Reason != "could not read the output" || len(q.Queues) != 0 || q.FailedTotal != nil {
			t.Fatalf("garbage: %+v", q)
		}
		owner.mustJSON("GET", base+"/failed", nil, 502, nil)
		// Not a Laravel app after all (a "likely" that was wrong): artisan is not there.
		script("queue:failed --json", []string{"Could not open input file: artisan"}, 1)
		script("queue:failed", []string{"Could not open input file: artisan"}, 1)
		owner.mustJSON("GET", base, nil, 200, &q)
		if q.Readable || q.Reason == nil || !strings.Contains(*q.Reason, "Could not open input file") {
			t.Fatalf("no artisan: %+v", q)
		}

		script("queue:failed --json", queuesFixture(t, "failed.json"), 0)
		script("queue:failed", queuesFixture(t, "failed_list_v10.txt"), 0)
	})

	t.Run("roles: reading is for whoever reads the app's logs, writing for whoever manages the app", func(t *testing.T) {
		before := execCount()
		viewer.mustJSON("POST", "/api/teams/"+team.ID+"/apps/"+app.App.ID+"/logs", map[string]any{}, 403, nil) // the ability this section borrows
		viewer.mustJSON("GET", base, nil, 403, nil)
		viewer.mustJSON("GET", base+"/failed", nil, 403, nil)
		for _, c := range []*client{viewer, deployer} {
			c.mustJSON("POST", base+"/failed/17/retry", nil, 403, nil)
			c.mustJSON("DELETE", base+"/failed/17", nil, 403, nil)
			c.mustJSON("POST", base+"/failed/retry-all", nil, 403, nil)
			c.mustJSON("DELETE", base+"/failed", nil, 403, nil)
		}
		if n := execCount() - before; n != 0 {
			t.Fatalf("%d commands were queued for refused requests", n)
		}
	})

	type actionJSON struct {
		OK         bool   `json:"ok"`
		OutputTail string `json:"output_tail"`
		Operation  string `json:"operation"`
	}
	const jobID = "9f3c1a52-7d0e-4c55-b0a1-5a7d3f1e8c21"
	t.Run("actions: the exact argv, never a shell", func(t *testing.T) {
		script("queue:retry", []string{"", "   INFO  Pushing failed queue jobs back onto the queue.", "", "  " + jobID + " ...... DONE"}, 0)
		script("queue:forget", []string{"   INFO  Failed job deleted successfully."}, 0)
		script("queue:flush", []string{"   INFO  All failed jobs deleted successfully."}, 0)
		common := []string{"--no-ansi", "--no-interaction"}
		for name, c := range map[string]struct {
			method, path string
			want         []string
		}{
			"retry":     {"POST", base + "/failed/" + jobID + "/retry", append([]string{"php", "artisan", "queue:retry"}, append(common, jobID)...)},
			"forget":    {"DELETE", base + "/failed/17", append([]string{"php", "artisan", "queue:forget"}, append(common, "17")...)},
			"retry all": {"POST", base + "/failed/retry-all", append([]string{"php", "artisan", "queue:retry"}, append(common, "all")...)},
			"flush":     {"DELETE", base + "/failed", append([]string{"php", "artisan", "queue:flush"}, common...)},
		} {
			var res actionJSON
			owner.mustJSON(c.method, c.path, nil, 200, &res)
			if !res.OK || res.Operation == "" || !strings.Contains(res.OutputTail, "INFO") {
				t.Errorf("%s: %+v", name, res)
			}
			if got := argvOf(res.Operation); !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s argv: %q", name, got)
			}
			if op := agent.waitOp(t, res.Operation); op.TimeoutSeconds != 150 {
				t.Errorf("%s: op timeout %d", name, op.TimeoutSeconds)
			}
		}

		// The command ran and said no: 502, with what it said.
		script("queue:forget", []string{"   ERROR  No failed job matches the given ID."}, 1)
		if status, text := owner.do("DELETE", base+"/failed/404", nil); status != 502 || !strings.Contains(text, "No failed job matches") {
			t.Fatalf("a failing command: %d %s", status, text)
		}
	})

	t.Run("ids that are refused before anything is queued", func(t *testing.T) {
		before := execCount()
		for _, id := range []string{"; rm -rf /", "1; rm -rf", "$(id)", "`id`", "a b", "-1", "--queue=x", "all", "a|b", "a&b", "'x'", "a=b", strings.Repeat("a", 121)} {
			esc := strings.NewReplacer("%", "%25", " ", "%20", ";", "%3B", "/", "%2F", "$", "%24", "(", "%28", ")", "%29", "`", "%60", "|", "%7C", "&", "%26", "'", "%27", "=", "%3D").Replace(id)
			for _, c := range [][2]string{{"POST", base + "/failed/" + esc + "/retry"}, {"DELETE", base + "/failed/" + esc}} {
				if status, text := owner.do(c[0], c[1], nil); status != 422 && status != 404 && status != 405 {
					t.Errorf("%s %q: %d %s", c[0], id, status, text)
				}
			}
			for _, tool := range []string{"retry_app_failed_job", "forget_app_failed_job"} {
				if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, tool, map[string]any{"app": app.App.Name, "job": id}); !res.IsError {
					t.Errorf("MCP %s %q was accepted: %s", tool, id, res.Text)
				}
			}
		}
		if n := execCount() - before; n != 0 {
			t.Fatalf("%d commands were queued for refused ids", n)
		}
	})

	t.Run("MCP", func(t *testing.T) {
		res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "get_app_queues", map[string]any{"app": app.App.Name})
		if res.IsError || !strings.Contains(res.Text, "driver laravel (detected)") || !strings.Contains(res.Text, "pending 42") || !strings.Contains(res.Text, "failed jobs in total: 3") {
			t.Fatalf("get_app_queues: %+v", res)
		}
		res = mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "list_app_failed_jobs", map[string]any{"app": app.App.ID})
		if res.IsError || !strings.Contains(res.Text, jobID) || !strings.Contains(res.Text, "redis@emails") || strings.Contains(res.Text, "customer@example.test") {
			t.Fatalf("list_app_failed_jobs: %+v", res)
		}
		for _, tool := range []string{"retry_app_failed_job", "forget_app_failed_job", "retry_all_app_failed_jobs", "flush_app_failed_jobs"} {
			if res = mcpCall(t, ts.URL, "/api/mcp", readTok.Token, tool, map[string]any{"app": app.App.Name, "job": "17"}); !res.IsError || !strings.Contains(res.Text, `"write" scope`) {
				t.Errorf("%s with a read token: %+v", tool, res)
			}
		}
		before := execCount()
		if res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "retry_app_failed_job", map[string]any{"app": app.App.Name, "job": jobID}); res.IsError || !strings.Contains(res.Text, "DONE") {
			t.Fatalf("retry_app_failed_job: %+v", res)
		}
		if res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "flush_app_failed_jobs", map[string]any{"app": app.App.Name}); res.IsError {
			t.Fatalf("flush_app_failed_jobs: %+v", res)
		}
		ops := agentOps(agent, agentproto.KindAppExec)[before:]
		if len(ops) != 2 {
			t.Fatalf("want 2 commands, got %d", len(ops))
		}
		_, p := keyOf(ops[0])
		if want := []string{"php", "artisan", "queue:retry", "--no-ansi", "--no-interaction", jobID}; !reflect.DeepEqual(p.Argv, want) {
			t.Fatalf("argv through MCP: %q", p.Argv)
		}
	})

	t.Run("audit", func(t *testing.T) {
		var audit struct {
			Entries []struct {
				Action string `json:"action"`
			} `json:"entries"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/audit?limit=200", nil, 200, &audit)
		got := map[string]int{}
		for _, e := range audit.Entries {
			got[e.Action]++
		}
		// REST: one of each, and the forget that failed. MCP: one retry and one flush. Refused ids: none.
		if got["queue.retry"] != 2 || got["queue.forget"] != 2 || got["queue.retry_all"] != 1 || got["queue.flush"] != 2 {
			t.Errorf("audit: %v", got)
		}
	})

	t.Run("an offline server is a 409, and nothing is queued", func(t *testing.T) {
		agent.stop()
		if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true, TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE servers SET last_seen_at = now() - interval '1 hour' WHERE id = $1`, srv)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for svcOf(t).Connected(srv) && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		before := execCount()
		if status, text := owner.do("GET", base, nil); status != 409 || !strings.Contains(text, "offline") {
			t.Fatalf("offline: %d %s", status, text)
		}
		owner.mustJSON("GET", base+"/failed", nil, 409, nil)
		owner.mustJSON("POST", base+"/failed/retry-all", nil, 409, nil)
		if execCount() != before {
			t.Fatal("a command was queued for an offline server")
		}
	})
}
