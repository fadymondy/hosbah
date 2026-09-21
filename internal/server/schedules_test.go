package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

type scheduleJSON struct {
	ID             string     `json:"id"`
	AppID          string     `json:"app_id"`
	Name           string     `json:"name"`
	Cron           string     `json:"cron"`
	Timezone       string     `json:"timezone"`
	Command        string     `json:"command"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	Enabled        bool       `json:"enabled"`
	Overlap        string     `json:"overlap"`
	NextRunAt      *time.Time `json:"next_run_at"`
	LastRunAt      *time.Time `json:"last_run_at"`
	LastStatus     *string    `json:"last_status"`
	LastRun        *struct {
		ID         string    `json:"id"`
		Status     string    `json:"status"`
		StartedAt  time.Time `json:"started_at"`
		DurationMs *int64    `json:"duration_ms"`
	} `json:"last_run"`
	NextRuns []time.Time `json:"next_runs"`
}

type scheduleRunJSON struct {
	ID          string     `json:"id"`
	ScheduleID  string     `json:"schedule_id"`
	AppID       string     `json:"app_id"`
	OperationID *string    `json:"operation_id"`
	Trigger     string     `json:"trigger"`
	Status      string     `json:"status"`
	ExitCode    *int64     `json:"exit_code"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	DurationMs  *int64     `json:"duration_ms"`
	Error       string     `json:"error"`
	Output      *string    `json:"output"`
}

type scheduleRunsJSON struct {
	Runs       []scheduleRunJSON `json:"runs"`
	NextCursor *string           `json:"next_cursor"`
}

// seedScheduleRun inserts a finished run of a schedule (its output is a stand-in, not a real seal)
// and returns its id.
func seedScheduleRun(t *testing.T, teamID, scheduleID, appID, status, output string) string {
	t.Helper()
	var id string
	err := tenancy.Tx(context.Background(), testDB(t), tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
		return tx.QueryRow(`INSERT INTO schedule_runs (team_id, schedule_id, app_id, trigger, status, started_at, finished_at, duration_ms, output)
			VALUES ($1, $2, $3, 'schedule', $4, now() - interval '1 day', now() - interval '1 day', 5, $5) RETURNING id`,
			teamID, scheduleID, appID, status, output).Scan(&id)
	})
	if err != nil {
		t.Fatalf("seed a schedule run: %v", err)
	}
	return id
}

// seedLiveRelease makes the app look deployed: a succeeded deployment and its live release.
func seedLiveRelease(t *testing.T, teamID, appID, serverID, app string) (deploymentID string) {
	t.Helper()
	err := tenancy.Tx(context.Background(), testDB(t), tenancy.Scope{System: true, TeamID: teamID}, func(_ *db.Queries, tx *sql.Tx) error {
		if err := tx.QueryRow(`INSERT INTO deployments (team_id, app_id, server_id, number, ref, trigger, kind, status, created_by)
			VALUES ($1, $2, $3, 1, 'main', 'manual', 'deploy', 'succeeded', 'test') RETURNING id`, teamID, appID, serverID).Scan(&deploymentID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO releases (team_id, app_id, deployment_id, version, image, port, config, status, activated_at)
			VALUES ($1, $2, $3, 1, $4, 8080, '{}', 'live', now())`, teamID, appID, deploymentID, "cloudy/"+app+":"+deploymentID)
		return err
	})
	if err != nil {
		t.Fatalf("seed a live release: %v", err)
	}
	return deploymentID
}

/*
"Schedules" end to end, with the scheduler on a fake clock and a fake agent: what a cron expression
and a timezone must be (and the preview), a due schedule queueing exactly one typed app.exec with
next_run_at already moved on, the overlap skip, the outcome copied onto the run with its output
sealed at rest, a timeout, an offline server, a manual run, a disabled schedule, imported apps,
roles, other teams, MCP, the 200-run cap and the cascade.
*/
func TestAppSchedules(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ctx := context.Background()

	cron := lastSchedules
	cron.Stop() // the test ticks
	// A fixed instant in the future: nothing another test left behind is due before the fake "now"
	// unless it is due for real as well (and those are switched off in their clean-up).
	clock := &fakeClock{t: time.Date(2031, 1, 15, 0, 0, 30, 0, time.UTC)}
	cron.SetClock(clock.now)

	owner := newClient(t, ts.URL)
	owner.register("cron-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Cron " + suffix}, 201, &team)
	t.Cleanup(func() { // whatever is left must not be fired by a later test's scheduler
		_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE schedules SET enabled = false, next_run_at = NULL WHERE team_id = $1`, team.ID)
			return err
		})
	})
	var readTok, writeTok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "cron-read", "scopes": []string{"read"}, "expires_in_days": 30}, 201, &readTok)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "cron-write", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &writeTok)

	viewerEmail := "cron-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	stranger := newClient(t, ts.URL)
	stranger.register("cron-stranger-" + suffix + "@example.test")
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, nil)

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "cron-"+short, "203.0.113.30", []agentproto.Service{
		{Kind: "container", Name: "imp-cron-" + short, Image: "nginx:1.27", Status: "running", Ports: []string{"0.0.0.0:8082->80/tcp"}}})
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "cron-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	appBase := func(id string) string { return "/api/teams/" + team.ID + "/apps/" + id }
	base := appBase(app.App.ID)

	// The fake agent: app.exec prints two lines, then answers what the test scripted — after the
	// gate opens, when the test holds one.
	const secretLine = "cron-output-marker-" + "s3cr3t"
	var (
		mu     sync.Mutex
		result = agentproto.ExecResult{Container: "cloudy-cron-web-1"}
		errMsg string
		gate   chan struct{}
	)
	script := func(r agentproto.ExecResult, msg string, g chan struct{}) {
		mu.Lock()
		result, errMsg, gate = r, msg, g
		mu.Unlock()
	}
	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		if op.Kind != agentproto.KindAppExec {
			return nil, "this fake agent does not run " + op.Kind
		}
		mu.Lock()
		r, msg, g := result, errMsg, gate
		mu.Unlock()
		if g != nil {
			<-g
		}
		return r, msg
	})
	agent.mu.Lock()
	agent.events = func(op agentproto.SignedOp) []agentproto.Event {
		if op.Kind != agentproto.KindAppExec {
			return nil
		}
		return []agentproto.Event{{Kind: agentproto.EventLog, Stream: "stdout", Line: "hello from cron"},
			{Kind: agentproto.EventLog, Stream: "stderr", Line: secretLine}}
	}
	agent.mu.Unlock()
	execOps := func() []agentproto.SignedOp { return agentOps(agent, agentproto.KindAppExec) }

	tick := func() int {
		t.Helper()
		n, err := cron.Tick(ctx)
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		return n
	}
	get := func(c *client, ref string) scheduleJSON {
		t.Helper()
		var s scheduleJSON
		c.mustJSON("GET", base+"/schedules/"+ref, nil, 200, &s)
		return s
	}
	runs := func(ref, query string) scheduleRunsJSON {
		t.Helper()
		var out scheduleRunsJSON
		owner.mustJSON("GET", base+"/schedules/"+ref+"/runs"+query, nil, 200, &out)
		return out
	}
	waitRun := func(ref, runID string, want ...string) scheduleRunJSON {
		t.Helper()
		var r scheduleRunJSON
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			owner.mustJSON("GET", base+"/schedules/"+ref+"/runs/"+runID, nil, 200, &r)
			for _, w := range want {
				if r.Status == w {
					return r
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("run %s never became %v: %+v", runID, want, r)
		return r
	}

	t.Run("preview and what an expression must be", func(t *testing.T) {
		var out struct {
			NextRuns []time.Time `json:"next_runs"`
		}
		viewer.mustJSON("POST", base+"/schedules/preview", map[string]any{"cron": "0 9 * * *", "timezone": "America/New_York"}, 200, &out)
		if len(out.NextRuns) != 5 || !out.NextRuns[0].Equal(time.Date(2031, 1, 15, 14, 0, 0, 0, time.UTC)) ||
			!out.NextRuns[1].Equal(time.Date(2031, 1, 16, 14, 0, 0, 0, time.UTC)) {
			t.Fatalf("09:00 New York in January is 14:00 UTC: %v", out.NextRuns)
		}
		owner.mustJSON("POST", base+"/schedules/preview", map[string]any{"cron": "  */15   * * * * "}, 200, &out)
		if len(out.NextRuns) != 5 || !out.NextRuns[0].Equal(time.Date(2031, 1, 15, 0, 15, 0, 0, time.UTC)) {
			t.Fatalf("default UTC: %v", out.NextRuns)
		}
		for name, body := range map[string]map[string]any{
			"four fields":           {"cron": "* * * *"},
			"six fields (seconds)":  {"cron": "0 * * * * *"},
			"a descriptor":          {"cron": "@daily"},
			"every":                 {"cron": "@every 1s"},
			"a zone in the cron":    {"cron": "TZ=UTC * * * *"},
			"a CRON_TZ prefix":      {"cron": "CRON_TZ=UTC * * * * *"},
			"minute 60":             {"cron": "60 * * * *"},
			"words":                 {"cron": "every five minutes or so"},
			"empty":                 {"cron": ""},
			"30 February":           {"cron": "0 0 30 2 *"},
			"an unknown timezone":   {"cron": "* * * * *", "timezone": "Mars/Olympus"},
			"the host's local zone": {"cron": "* * * * *", "timezone": "Local"},
			"a path as a timezone":  {"cron": "* * * * *", "timezone": "../../etc/passwd"},
		} {
			if status, text := owner.do("POST", base+"/schedules/preview", body); status != 422 {
				t.Errorf("%s: %d (want 422): %s", name, status, text)
			}
		}
		stranger.mustJSON("POST", base+"/schedules/preview", map[string]any{"cron": "* * * * *"}, 404, nil)
	})

	t.Run("what a write refuses", func(t *testing.T) {
		sched := func(over map[string]any) map[string]any {
			b := map[string]any{"name": "refused", "cron": "* * * * *", "command": "true"}
			for k, v := range over {
				b[k] = v
			}
			return b
		}
		for name, body := range map[string]map[string]any{
			"a bad expression":      sched(map[string]any{"cron": "* * * *"}),
			"a bad timezone":        sched(map[string]any{"timezone": "Nowhere/Land"}),
			"two lines":             sched(map[string]any{"command": "true\nrm -rf /"}),
			"a carriage return":     sched(map[string]any{"command": "true\rfalse"}),
			"a NUL":                 sched(map[string]any{"command": "true\x00"}),
			"only spaces":           sched(map[string]any{"command": "   "}),
			"1001 characters":       sched(map[string]any{"command": strings.Repeat("a", 1001)}),
			"a timeout under 10 s":  sched(map[string]any{"timeout_seconds": 9}),
			"a timeout over 1 hour": sched(map[string]any{"timeout_seconds": 3601}),
		} {
			if status, text := owner.do("POST", base+"/schedules", body); status != 422 {
				t.Errorf("%s: %d (want 422): %s", name, status, text)
			}
		}
		var list struct{ Schedules []scheduleJSON }
		owner.mustJSON("GET", base+"/schedules", nil, 200, &list)
		if list.Schedules == nil || len(list.Schedules) != 0 {
			t.Fatalf("nothing should have been created: %+v", list.Schedules)
		}
	})

	t.Run("an imported app has no release to run in", func(t *testing.T) {
		apps, _ := listImported(t, owner, team.ID, srv)
		imp, ok := apps["imp-cron-"+short]
		if !ok {
			t.Fatalf("no imported app: %+v", apps)
		}
		owner.mustJSON("POST", appBase(imp.ID)+"/schedules", map[string]any{"name": "imported", "cron": "* * * * *", "command": "true"}, 422, nil)
		var list struct{ Schedules []scheduleJSON }
		owner.mustJSON("GET", appBase(imp.ID)+"/schedules", nil, 200, &list)
		if len(list.Schedules) != 0 {
			t.Fatalf("imported app's schedules: %+v", list.Schedules)
		}
	})

	const command = `php artisan schedule:run >> /dev/null 2>&1; echo "$HOME" 'a b' $(id)`
	var every5, off scheduleJSON
	t.Run("create, roles and other teams", func(t *testing.T) {
		body := map[string]any{"name": "every-five", "cron": "*/5  * * * *", "command": "  " + command + "  ", "timeout_seconds": 120}
		viewer.mustJSON("POST", base+"/schedules", body, 403, nil)
		stranger.mustJSON("POST", base+"/schedules", body, 404, nil)
		owner.mustJSON("POST", base+"/schedules", body, 201, &every5)
		want := time.Date(2031, 1, 15, 0, 5, 0, 0, time.UTC)
		if every5.ID == "" || every5.AppID != app.App.ID || every5.Cron != "*/5 * * * *" || every5.Timezone != "UTC" || every5.Command != command ||
			every5.TimeoutSeconds != 120 || !every5.Enabled || every5.Overlap != "skip" || every5.NextRunAt == nil || !every5.NextRunAt.Equal(want) ||
			every5.LastRun != nil || every5.LastStatus != nil || len(every5.NextRuns) != 3 || !every5.NextRuns[0].Equal(want) ||
			!every5.NextRuns[2].Equal(want.Add(10*time.Minute)) {
			t.Fatalf("created: %+v", every5)
		}
		owner.mustJSON("POST", base+"/schedules", body, 409, nil) // the name is taken

		owner.mustJSON("POST", base+"/schedules", map[string]any{"name": "switched-off", "cron": "* * * * *", "command": "true", "enabled": false}, 201, &off)
		if off.Enabled || off.NextRunAt != nil || off.TimeoutSeconds != 300 || len(off.NextRuns) != 3 {
			t.Fatalf("a disabled schedule: %+v", off)
		}

		if s := get(viewer, "every-five"); s.ID != every5.ID {
			t.Errorf("a viewer reads a schedule by name: %+v", s)
		}
		stranger.mustJSON("GET", base+"/schedules/"+every5.ID, nil, 404, nil)
		stranger.mustJSON("GET", base+"/schedules/"+every5.ID+"/runs", nil, 404, nil)
		viewer.mustJSON("PATCH", base+"/schedules/"+every5.ID, map[string]any{"enabled": false}, 403, nil)
		viewer.mustJSON("DELETE", base+"/schedules/"+every5.ID, nil, 403, nil)
		viewer.mustJSON("POST", base+"/schedules/"+every5.ID+"/run", nil, 403, nil)
		stranger.mustJSON("POST", base+"/schedules/"+every5.ID+"/run", nil, 404, nil)
		stranger.mustJSON("PATCH", base+"/schedules/"+every5.ID, map[string]any{"enabled": false}, 404, nil)
		stranger.mustJSON("DELETE", base+"/schedules/"+every5.ID, nil, 404, nil)

		// Nothing is live yet: a manual run is refused, and nothing was queued.
		owner.mustJSON("POST", base+"/schedules/"+every5.ID+"/run", nil, 409, nil)
		if n := len(runs(every5.ID, "").Runs); n != 0 {
			t.Fatalf("a refused manual run leaves no run: %d", n)
		}
	})

	deployment := seedLiveRelease(t, team.ID, app.App.ID, srv, app.App.Name)

	var first scheduleRunJSON
	t.Run("a due schedule queues exactly one typed operation, next_run_at first", func(t *testing.T) {
		tick() // 00:00:30 — nothing is due
		if n := len(execOps()); n != 0 {
			t.Fatalf("not due yet, but %d operations were queued", n)
		}
		held := make(chan struct{})
		script(agentproto.ExecResult{Container: "cloudy-cron-web-1", ExitCode: 0, Bytes: 40}, "", held)
		defer func() {
			select {
			case <-held:
			default:
				close(held)
			}
		}()

		clock.advance(5 * time.Minute) // 00:05:30
		tick()
		s := get(owner, every5.ID)
		if s.NextRunAt == nil || !s.NextRunAt.Equal(time.Date(2031, 1, 15, 0, 10, 0, 0, time.UTC)) {
			t.Fatalf("next_run_at must have moved on before the run: %v", s.NextRunAt)
		}
		list := runs(every5.ID, "")
		if len(list.Runs) != 1 || list.Runs[0].Trigger != "schedule" || list.Runs[0].OperationID == nil {
			t.Fatalf("runs: %+v", list.Runs)
		}
		first = list.Runs[0]
		op := agent.waitOp(t, *first.OperationID)
		var p agentproto.ExecParams
		if err := json.Unmarshal(op.Params, &p); err != nil {
			t.Fatal(err)
		}
		if op.Kind != agentproto.KindAppExec || p.AppID != app.App.ID || p.DeploymentID != deployment || p.TimeoutSeconds != 120 ||
			!reflect.DeepEqual(p.Argv, []string{"sh", "-lc", command}) || p.Validate() != nil || op.TimeoutSeconds != 180 {
			t.Fatalf("typed params: %+v (op timeout %d)", p, op.TimeoutSeconds)
		}

		// The same minute again, and again: claimed once, fired once.
		tick()
		tick()
		if n := len(execOps()); n != 1 {
			t.Fatalf("want exactly one operation, got %d", n)
		}
		waitRun(every5.ID, first.ID, "running")

		// The next fire comes while the first run is still going: skipped, nothing queued.
		clock.advance(5 * time.Minute) // 00:10:30
		tick()
		list = runs(every5.ID, "")
		if len(list.Runs) != 2 || list.Runs[0].Status != "skipped" || list.Runs[0].OperationID != nil || list.Runs[0].FinishedAt == nil ||
			!strings.Contains(list.Runs[0].Error, "still going") || list.Runs[1].ID != first.ID {
			t.Fatalf("overlap: %+v", list.Runs)
		}
		if n := len(execOps()); n != 1 {
			t.Fatalf("a skipped run queues nothing: %d operations", n)
		}
		owner.mustJSON("POST", base+"/schedules/"+every5.ID+"/run", nil, 409, nil) // nor by hand

		close(held)
		done := waitRun(every5.ID, first.ID, "succeeded")
		if done.ExitCode == nil || *done.ExitCode != 0 || done.FinishedAt == nil || done.DurationMs == nil || done.Error != "" ||
			done.Output == nil || *done.Output != "hello from cron\n"+secretLine+"\n" {
			t.Fatalf("the finished run: %+v (output %q)", done, deref(done.Output))
		}
		// Sealed at rest: the column is not what the command printed.
		var stored string
		_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			return tx.QueryRow(`SELECT output FROM schedule_runs WHERE id = $1`, first.ID).Scan(&stored)
		})
		if stored == "" || strings.Contains(stored, secretLine) || strings.Contains(stored, "hello from cron") {
			t.Fatalf("the output must be sealed at rest: %q", stored)
		}
		// The feed never carries output; a viewer reads the feed but not what a run printed.
		var raw map[string]any
		viewer.mustJSON("GET", base+"/schedules/"+every5.ID+"/runs", nil, 200, &raw)
		if b, _ := json.Marshal(raw); strings.Contains(string(b), "output") || strings.Contains(string(b), secretLine) {
			t.Fatalf("the feed leaks output: %s", b)
		}
		viewer.mustJSON("GET", base+"/schedules/"+every5.ID+"/runs/"+first.ID, nil, 403, nil)
		stranger.mustJSON("GET", base+"/schedules/"+every5.ID+"/runs/"+first.ID, nil, 404, nil)
		owner.mustJSON("GET", base+"/schedules/"+off.ID+"/runs/"+first.ID, nil, 404, nil) // not that schedule's run

		// The schedule mirrors its newest run (the skipped one).
		s = get(owner, every5.ID)
		if s.LastRun == nil || s.LastRun.Status != "skipped" || s.LastStatus == nil || *s.LastStatus != "skipped" || s.LastRunAt == nil {
			t.Fatalf("last run: %+v", s.LastRun)
		}
	})

	t.Run("a disabled schedule never fires", func(t *testing.T) {
		before := len(execOps())
		owner.mustJSON("PATCH", base+"/schedules/"+every5.ID, map[string]any{"enabled": false}, 200, nil)
		clock.advance(time.Hour)
		tick()
		tick()
		if n := len(execOps()); n != before {
			t.Fatalf("disabled schedules fired: %d operations, want %d", n, before)
		}
		if n := len(runs(off.ID, "").Runs); n != 0 {
			t.Fatalf("the switched-off schedule ran %d times", n)
		}
		var s scheduleJSON
		owner.mustJSON("PATCH", base+"/schedules/"+every5.ID, map[string]any{"enabled": true, "cron": "0 12 * * *", "timezone": "Asia/Tokyo"}, 200, &s)
		if s.NextRunAt == nil || !s.NextRunAt.Equal(time.Date(2031, 1, 15, 3, 0, 0, 0, time.UTC)) { // 12:00 Tokyo; now is 01:10:30 UTC
			t.Fatalf("switched back on: next_run_at %v", s.NextRunAt)
		}
	})

	t.Run("a manual run, an exit code, a timeout", func(t *testing.T) {
		script(agentproto.ExecResult{Container: "cloudy-cron-web-1", ExitCode: 3}, "the command exited with status 3", nil)
		before := get(owner, every5.ID).NextRunAt
		var run scheduleRunJSON
		owner.mustJSON("POST", base+"/schedules/"+every5.ID+"/run", nil, 202, &run)
		if run.ID == "" || run.Trigger != "manual" || run.Status != "queued" || run.OperationID == nil {
			t.Fatalf("manual run: %+v", run)
		}
		failed := waitRun(every5.ID, run.ID, "failed")
		if failed.ExitCode == nil || *failed.ExitCode != 3 || !strings.Contains(failed.Error, "status 3") {
			t.Fatalf("a failed run: %+v", failed)
		}
		if s := get(owner, every5.ID); s.NextRunAt == nil || !s.NextRunAt.Equal(*before) || s.LastRun == nil || s.LastRun.ID != run.ID || s.LastRun.Status != "failed" {
			t.Fatalf("a manual run does not move next_run_at, and is the last run: %+v", s)
		}
		// A disabled schedule can still be run by hand.
		script(agentproto.ExecResult{Container: "cloudy-cron-web-1", ExitCode: agentproto.ExecExitTimeout, TimedOut: true}, "the command was killed after its timeout of 300 s", nil)
		owner.mustJSON("POST", base+"/schedules/"+off.ID+"/run", nil, 202, &run)
		timedOut := waitRun(off.ID, run.ID, "timeout")
		if timedOut.ExitCode == nil || *timedOut.ExitCode != 124 || !strings.Contains(timedOut.Error, "killed after its timeout") {
			t.Fatalf("a timed-out run: %+v", timedOut)
		}
	})

	t.Run("MCP", func(t *testing.T) {
		script(agentproto.ExecResult{Container: "cloudy-cron-web-1"}, "", nil)
		res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "list_app_schedules", map[string]any{"app": app.App.Name})
		if res.IsError || !strings.Contains(res.Text, "every-five") || !strings.Contains(res.Text, "switched-off") {
			t.Fatalf("list_app_schedules: %+v", res)
		}
		res = mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "list_schedule_runs", map[string]any{"app": app.App.Name, "schedule": "every-five"})
		if res.IsError || !strings.Contains(res.Text, "skipped") || !strings.Contains(res.Text, "exit 3") || strings.Contains(res.Text, secretLine) {
			t.Fatalf("list_schedule_runs: %+v", res)
		}
		args := map[string]any{"app": app.App.Name, "name": "from-mcp", "cron": "30 2 * * 1", "timezone": "Europe/Berlin", "command": "bin/cleanup"}
		if res = mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "create_app_schedule", args); !res.IsError {
			t.Fatalf("a read token must not create: %+v", res)
		}
		if res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "create_app_schedule", args); res.IsError || !strings.Contains(res.Text, "from-mcp") {
			t.Fatalf("create_app_schedule: %+v", res)
		}
		if res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "create_app_schedule", map[string]any{"app": app.App.Name, "name": "bad", "cron": "nope", "command": "x"}); !res.IsError {
			t.Fatalf("a bad expression over MCP: %+v", res)
		}
		if res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "update_app_schedule", map[string]any{"app": app.App.Name, "schedule": "from-mcp", "timeout_seconds": 60}); res.IsError || !strings.Contains(res.Text, "timeout 60 s") {
			t.Fatalf("update_app_schedule: %+v", res)
		}
		if res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "run_app_schedule", map[string]any{"app": app.App.Name, "schedule": "from-mcp"}); res.IsError || !strings.Contains(res.Text, "manual") {
			t.Fatalf("run_app_schedule: %+v", res)
		}
		list := runs("from-mcp", "")
		if len(list.Runs) != 1 {
			t.Fatalf("the MCP run: %+v", list.Runs)
		}
		waitRun("from-mcp", list.Runs[0].ID, "succeeded") // followed although its transaction committed after Follow
		if res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "delete_app_schedule", map[string]any{"app": app.App.Name, "schedule": "from-mcp"}); res.IsError {
			t.Fatalf("delete_app_schedule: %+v", res)
		}
		owner.mustJSON("GET", base+"/schedules/from-mcp", nil, 404, nil)
	})

	t.Run("an offline server fails the run instead of queueing forever", func(t *testing.T) {
		agent.stop()
		deadline := time.Now().Add(15 * time.Second)
		for svcOf(t).Connected(srv) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		var run scheduleRunJSON
		owner.mustJSON("POST", base+"/schedules/"+every5.ID+"/run", nil, 202, &run)
		// Its place in the queue is short (schedules.QueueTTL, not the queue's hour)…
		var ttl float64
		_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			return tx.QueryRow(`SELECT extract(epoch FROM expires_at - created_at) FROM operations WHERE id = $1`, *run.OperationID).Scan(&ttl)
		})
		if ttl < 200 || ttl > 400 {
			t.Fatalf("the operation should expire after about 5 minutes, not %.0f s", ttl)
		}
		// …and once it is over (the ops sweeper's own timeout), the run is a timeout that says why.
		_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE operations SET expires_at = now() - interval '1 second' WHERE id = $1`, *run.OperationID)
			return err
		})
		r := waitRun(every5.ID, run.ID, "timeout")
		if r.ExitCode != nil || !strings.Contains(r.Error, "offline") {
			t.Fatalf("an offline run: %+v", r)
		}
	})

	t.Run("a control plane that restarted settles what it finds", func(t *testing.T) {
		// A run whose operation is gone and that nobody follows: the tick's sweep ends it.
		var id string
		_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			return tx.QueryRow(`INSERT INTO schedule_runs (team_id, schedule_id, app_id, trigger, status, started_at)
				VALUES ($1, $2, $3, 'schedule', 'running', now()) RETURNING id`, team.ID, off.ID, app.App.ID).Scan(&id)
		})
		tick()
		if r := waitRun(off.ID, id, "failed"); !strings.Contains(r.Error, "operation is gone") {
			t.Fatalf("a stranded run: %+v", r)
		}
	})

	t.Run("the newest 200 runs are kept, and the feed pages", func(t *testing.T) {
		_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO schedule_runs (team_id, schedule_id, app_id, trigger, status, started_at, finished_at, duration_ms)
				SELECT $1, $2, $3, 'schedule', 'succeeded', now() - interval '2 days' - g * interval '1 minute', now(), 1 FROM generate_series(1, 230) g`,
				team.ID, every5.ID, app.App.ID)
			return err
		})
		var run scheduleRunJSON
		owner.mustJSON("POST", base+"/schedules/"+every5.ID+"/run", nil, 202, &run) // adding a run prunes
		var n int
		_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			return tx.QueryRow(`SELECT count(*) FROM schedule_runs WHERE schedule_id = $1`, every5.ID).Scan(&n)
		})
		if n != 200 {
			t.Fatalf("want 200 runs kept, got %d", n)
		}
		seen := map[string]bool{}
		query := "?limit=100"
		for page := 0; page < 5; page++ {
			f := runs(every5.ID, query)
			for _, r := range f.Runs {
				if seen[r.ID] {
					t.Fatalf("run %s on two pages", r.ID)
				}
				seen[r.ID] = true
			}
			if f.NextCursor == nil {
				break
			}
			query = "?limit=100&before=" + *f.NextCursor
		}
		if len(seen) != 200 || !seen[run.ID] {
			t.Fatalf("paged through %d runs", len(seen))
		}
		owner.mustJSON("GET", base+"/schedules/"+every5.ID+"/runs?before=nonsense", nil, 422, nil)
	})

	t.Run("the cap of 30", func(t *testing.T) {
		var list struct{ Schedules []scheduleJSON }
		owner.mustJSON("GET", base+"/schedules", nil, 200, &list)
		for i := len(list.Schedules); i < 30; i++ {
			owner.mustJSON("POST", base+"/schedules", map[string]any{"name": fmt.Sprintf("fill-%d", i), "cron": "0 0 1 1 *", "command": "true", "enabled": false}, 201, nil)
		}
		owner.mustJSON("POST", base+"/schedules", map[string]any{"name": "one-too-many", "cron": "0 0 1 1 *", "command": "true"}, 409, nil)
	})

	t.Run("deleting the app takes its schedules and runs", func(t *testing.T) {
		count := func() (schedules, runs int) {
			_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
				_ = tx.QueryRow(`SELECT count(*) FROM schedules WHERE app_id = $1`, app.App.ID).Scan(&schedules)
				return tx.QueryRow(`SELECT count(*) FROM schedule_runs WHERE app_id = $1`, app.App.ID).Scan(&runs)
			})
			return
		}
		if s, r := count(); s != 30 || r < 200 {
			t.Fatalf("before: %d schedules, %d runs", s, r)
		}
		owner.mustJSON("DELETE", base, map[string]any{"confirm": app.App.Name}, 204, nil)
		if s, r := count(); s != 0 || r != 0 {
			t.Fatalf("after deleting the app: %d schedules, %d runs", s, r)
		}
	})
}
