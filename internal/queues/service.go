package queues

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
One implementation for the REST API and the user MCP.

A read or an action is one or more rounds. A round is a team-scoped transaction the CALLER opens
(InTeam: it checks the member's role and resolves the app, so a stranger's 404 and a viewer's 403 are
the caller's own) in which the commands of the round are queued as app.exec operations — and, for an
action, the audit entry is written. Only after that transaction commits is an operation visible to
the dispatcher, so waiting happens between rounds, never inside one. What a command printed is read
back from the operation's sealed log, in a round of its own.

Errors are *deploy.ActionError: 422 (an imported app, a driver without failed jobs, a bad job id),
409 (nothing is live; the server is offline), 502 (the operation failed), 504 (no answer in time).
*/

const (
	// queueTTL: a command that could not be sent within a minute is dropped rather than run later —
	// nobody is waiting for a read any more, and a flush must not happen an hour after it was asked.
	queueTTL = time.Minute
	// opGrace is what the operation is allowed on top of the command's own timeout.
	opGrace = 30 * time.Second
	// maxOutput bounds what is read back of a command's output (the agent relays at most 256 KiB).
	maxOutput = agentproto.MaxExecOutput + 4096
	// tailBytes is how much of an action's output is returned.
	tailBytes = 2000

	// DefaultFailedLimit and MaxFailedLimit bound a page of failed jobs.
	DefaultFailedLimit = 50
	MaxFailedLimit     = 200
)

// reasonUnreadable is the reason of every output a parser refused.
const reasonUnreadable = "could not read the output"

// Round is one team-scoped transaction of the caller's.
type Round func(q *db.Queries, teamID string, actor deploy.Actor, a db.App) error

// InTeam opens a team-scoped transaction for the caller, checks what the caller may do, resolves the
// app, and runs fn in it. It is called once per round.
type InTeam func(ctx context.Context, fn Round) error

// Service queues the catalogue's commands and reads their answers.
type Service struct {
	ops *ops.Service
	now func() time.Time
}

// From builds the service from the application's operations service (nil when there is none).
func From(a *app.App) *Service {
	if a == nil {
		return nil
	}
	o, _ := a.Ops.(*ops.Service)
	if o == nil {
		return nil
	}
	return &Service{ops: o, now: time.Now}
}

func fail(status int, msg string) error { return &deploy.ActionError{Status: status, Msg: msg} }

// target is where commands run: the app's live release on its server.
type target struct {
	app          db.App
	deploymentID string
	det          Detection
}

// resolve checks that there is somewhere to run (in the caller's transaction) and detects the driver.
func (s *Service) resolve(ctx context.Context, q *db.Queries, teamID string, a db.App) (target, error) {
	if deploy.IsImported(a) {
		return target{}, fail(http.StatusUnprocessableEntity, "queues are read inside a release Hosbah deployed — "+a.Name+" is an imported app and has none")
	}
	live, err := q.LiveReleaseOfApp(ctx, a.ID)
	if err != nil || live.TeamID != teamID {
		return target{}, fail(http.StatusConflict, "nothing is live to look into — deploy the app first")
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil {
		return target{}, fail(http.StatusConflict, "the app's server is gone")
	}
	if srv.Status != "enrolled" || srv.AgentPublicKey == nil {
		return target{}, fail(http.StatusConflict, "the agent of "+srv.Name+" has not enrolled yet")
	}
	if !s.ops.Connected(srv.ID) && !ops.Online(srv.LastSeenAt, s.now()) {
		return target{}, fail(http.StatusConflict, srv.Name+" is offline — the app's queues can be read once its agent is connected")
	}

	in := Inputs{Processes: map[string]string{}}
	if a.Runtime != nil {
		in.Runtime = *a.Runtime
	}
	var cfg deploy.Effective
	if json.Unmarshal([]byte(live.Config), &cfg) == nil {
		in.Processes, in.Release = cfg.Processes, cfg.Release
	}
	vars, err := q.ListEnvVarsOfApp(ctx, db.ListEnvVarsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return target{}, err
	}
	for _, v := range vars {
		in.EnvNames = append(in.EnvNames, v.Name) // names only: values stay sealed
	}
	return target{app: a, deploymentID: live.DeploymentID, det: Detect(in)}, nil
}

// enqueue queues one catalogue command for the target, in the caller's transaction.
func (s *Service) enqueue(ctx context.Context, q *db.Queries, teamID string, actor deploy.Actor, t target, c Command) (db.Operation, error) {
	p := agentproto.ExecParams{AppID: t.app.ID, DeploymentID: t.deploymentID, Argv: c.Argv, TimeoutSeconds: c.Timeout}
	if err := p.Validate(); err != nil {
		return db.Operation{}, err
	}
	raw, _ := json.Marshal(p)
	createdBy := actor.UserID
	if createdBy == "" {
		createdBy = "via:" + actor.Via
	}
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: t.app.ServerID, Kind: agentproto.KindAppExec, Params: raw,
		CreatedBy: createdBy, Timeout: time.Duration(c.Timeout)*time.Second + opGrace, QueueTTL: queueTTL})
	return op, err
}

// outcome is how a command ended.
type outcome struct {
	op        db.Operation
	exitCode  int
	output    string // stdout and stderr, in order
	truncated bool   // the agent dropped output past its cap
}

// await waits for op (queued in a round that has committed) until the deadline of ctx, then reads
// what it printed in a round of its own. A command that RAN and exited non-zero is an outcome, not
// an error: its caller knows what that exit means.
func (s *Service) await(ctx context.Context, in InTeam, c Command, op db.Operation) (outcome, error) {
	done, err := s.ops.Await(ctx, op.ID, op.ServerID, nil)
	if err != nil {
		if ctx.Err() != nil {
			return outcome{op: op}, fail(http.StatusGatewayTimeout, "the server did not answer in time — is its agent connected? (operation "+op.ID+")")
		}
		return outcome{op: op}, err
	}
	var res agentproto.ExecResult
	ran := done.Result != nil && json.Unmarshal([]byte(*done.Result), &res) == nil && res.Container != ""
	msg := done.Status
	if done.Error != nil {
		msg = *done.Error
	}
	switch {
	case done.Status == "timeout" || (ran && res.TimedOut):
		return outcome{op: done}, fail(http.StatusGatewayTimeout, c.Name+" did not finish in time: "+msg+" (operation "+op.ID+")")
	case !ran && done.Status != "ok":
		return outcome{op: done}, &deploy.ActionError{Status: http.StatusBadGateway, Msg: msg, Code: "operation_failed"}
	}
	out := outcome{op: done, exitCode: res.ExitCode, truncated: res.Truncated}
	err = in(ctx, func(q *db.Queries, teamID string, _ deploy.Actor, _ db.App) error {
		var err error
		out.output, err = s.output(ctx, q, teamID, done)
		return err
	})
	return out, err
}

// output is what the command printed: the operation's stdout and stderr lines, opened with the
// team's key. The agent's own info lines are not the command's output.
func (s *Service) output(ctx context.Context, q *db.Queries, teamID string, op db.Operation) (string, error) {
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	after := int64(0)
	for {
		rows, err := q.OperationLogsAfter(ctx, db.OperationLogsAfterParams{OperationID: op.ID, TeamID: teamID, Seq: after, Limit: 1000})
		if err != nil {
			return "", err
		}
		for _, row := range rows {
			after = row.Seq
			if row.Stream != "stdout" && row.Stream != "stderr" {
				continue
			}
			l, err := ops.OpenLog(dek, teamID, row)
			if err != nil {
				return "", err
			}
			if b.Len()+len(l.Line)+1 > maxOutput {
				return b.String(), nil
			}
			b.WriteString(l.Line)
			b.WriteByte('\n')
		}
		if len(rows) < 1000 {
			return b.String(), nil
		}
	}
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "…" + strings.ToValidUTF8(s[len(s)-n:], "")
	}
	return s
}

func lastLine(s string) string {
	ls := lines(s)
	if len(ls) == 0 {
		return ""
	}
	return clean(ls[len(ls)-1], 200)
}

// ---- reads -----------------------------------------------------------------------------------------

// QueueState is one queue. A count this package cannot read dependably is null, never a guess.
type QueueState struct {
	Name    string `json:"name"`
	Pending *int64 `json:"pending" doc:"Jobs waiting. Laravel: Queue::size(). Celery: tasks workers have reserved (prefetched) — the broker's own backlog is not read."`
	Running *int64 `json:"running" doc:"Jobs being worked on. Celery: active tasks. Laravel: always null (no dependable source)."`
	Failed  *int64 `json:"failed" doc:"Failed jobs of this queue. Null when the failed list could not be read whole."`
}

// QueueOverview is the app's queues as the API shows them.
type QueueOverview struct {
	Driver      string   `json:"driver" enum:"laravel,bullmq,asynq,celery,none"`
	Confidence  string   `json:"confidence" enum:"detected,likely,unknown" doc:"detected: a process of the live release runs the framework's program. likely: only the runtime or names of environment variables point to it."`
	Evidence    []string `json:"evidence" doc:"Why: what was found (never a value of an environment variable)."`
	Readable    bool     `json:"readable" doc:"false: the driver has no dependable CLI in the image, or what it printed could not be read. queues is then empty and the counts null — nothing is guessed."`
	Reason      *string  `json:"reason" doc:"Why it is not readable; null when it is."`
	Horizon     *HorizonState `json:"horizon" doc:"Laravel only; null for other drivers and when horizon:status gave no answer."`
	Queues      []QueueState  `json:"queues"`
	FailedTotal *int64   `json:"failed_total" doc:"null when the failed list could not be read whole."`
	Operation   string   `json:"operation" doc:"The app.exec operation that was read (\"\" when nothing was run)."`
}

// budget bounds a whole read, all rounds included.
const budget = 50 * time.Second

// Overview reads the app's queues.
func (s *Service) Overview(ctx context.Context, in InTeam) (QueueOverview, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var (
		t    target
		cmds []Command
		opsq []db.Operation
	)
	err := in(ctx, func(q *db.Queries, teamID string, actor deploy.Actor, a db.App) error {
		var err error
		if t, err = s.resolve(ctx, q, teamID, a); err != nil {
			return err
		}
		if ok, _ := t.det.Readable(); !ok {
			return nil
		}
		switch t.det.Driver {
		case DriverLaravel:
			cmds = []Command{laravelFailedJSON(), laravelSizes(), laravelHorizonStatus()}
		case DriverCelery:
			cmds = []Command{celeryInspect(t.det.CeleryApp, "active"), celeryInspect(t.det.CeleryApp, "reserved")}
		}
		for _, c := range cmds {
			op, err := s.enqueue(ctx, q, teamID, actor, t, c)
			if err != nil {
				return err
			}
			opsq = append(opsq, op)
		}
		return nil
	})
	if err != nil {
		return QueueOverview{}, err
	}
	ov := QueueOverview{Driver: t.det.Driver, Confidence: t.det.Confidence, Evidence: t.det.Evidence, Queues: []QueueState{}}
	if ok, why := t.det.Readable(); !ok {
		ov.Reason = &why
		return ov, nil
	}
	unreadable := func(why string) (QueueOverview, error) {
		ov.Readable, ov.Reason, ov.Queues, ov.FailedTotal = false, &why, []QueueState{}, nil
		return ov, nil
	}
	ov.Operation = opsq[0].ID

	if t.det.Driver == DriverCelery {
		counts := make([]map[string]int64, 2)
		for i := range cmds {
			o, err := s.await(ctx, in, cmds[i], opsq[i])
			if err != nil {
				return QueueOverview{}, err
			}
			if o.exitCode != 0 {
				return unreadable(fmt.Sprintf("%s exited with status %d: %s", cmds[i].Name, o.exitCode, lastLine(o.output)))
			}
			if counts[i], _, err = ParseCeleryInspect(o.output); err != nil {
				return unreadable(reasonUnreadable)
			}
		}
		names := map[string]bool{}
		for _, m := range counts {
			for n := range m {
				names[n] = true
			}
		}
		for _, n := range sorted(names) {
			active, reserved := counts[0][n], counts[1][n]
			ov.Queues = append(ov.Queues, QueueState{Name: n, Running: &active, Pending: &reserved})
		}
		ov.Readable = true
		return ov, nil
	}

	// Laravel. The failed list decides whether the app answers at all; sizes and Horizon are extras.
	list, err := s.failedOf(ctx, in, t, cmds[0], opsq[0])
	if err != nil {
		return QueueOverview{}, err
	}
	ov.Operation = list.operation
	if list.reason != "" {
		return unreadable(list.reason)
	}
	ov.Readable = true
	perQueue := map[string]int64{}
	for _, j := range list.jobs {
		perQueue[j.Queue]++
	}
	names := map[string]bool{}
	var sizes map[string]*int64
	if o, err := s.await(ctx, in, cmds[1], opsq[1]); err == nil && o.exitCode == 0 && !o.truncated {
		sizes, _ = ParseSizes(o.output) // not robust → null sizes, not a guess
	}
	for n := range sizes {
		names[n] = true
	}
	for n := range perQueue {
		if n != "" {
			names[n] = true
		}
	}
	for _, n := range sorted(names) {
		qu := QueueState{Name: n, Pending: sizes[n]}
		if !list.truncated {
			f := perQueue[n]
			qu.Failed = &f
		}
		ov.Queues = append(ov.Queues, qu)
	}
	if !list.truncated {
		total := int64(len(list.jobs))
		ov.FailedTotal = &total
	}
	if o, err := s.await(ctx, in, cmds[2], opsq[2]); err == nil {
		if h, err := ParseHorizonStatus(o.output); err == nil {
			ov.Horizon = &h
		}
	}
	return ov, nil
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// failedList is Laravel's failed jobs, or the reason they could not be read.
type failedList struct {
	jobs      []FailedJob
	truncated bool // the agent cut the output: jobs is the part that arrived
	reason    string
	operation string
}

// failedOf reads the failed jobs from first (queue:failed --json, already queued). A version without
// --json, or a JSON document the agent had to cut, falls back to the line-based form in a new round.
func (s *Service) failedOf(ctx context.Context, in InTeam, t target, first Command, firstOp db.Operation) (failedList, error) {
	o, err := s.await(ctx, in, first, firstOp)
	if err != nil {
		return failedList{}, err
	}
	if o.exitCode == 0 && !o.truncated {
		if jobs, err := ParseFailedJSON(o.output); err == nil {
			return failedList{jobs: jobs, operation: o.op.ID}, nil
		}
	}
	table := laravelFailedTable()
	var op db.Operation
	err = in(ctx, func(q *db.Queries, teamID string, actor deploy.Actor, _ db.App) error {
		var err error
		op, err = s.enqueue(ctx, q, teamID, actor, t, table)
		return err
	})
	if err != nil {
		return failedList{}, err
	}
	if o, err = s.await(ctx, in, table, op); err != nil {
		return failedList{}, err
	}
	if o.exitCode != 0 {
		return failedList{operation: op.ID, reason: fmt.Sprintf("php artisan queue:failed exited with status %d: %s", o.exitCode, lastLine(o.output))}, nil
	}
	jobs, err := ParseFailedTable(o.output)
	if err != nil {
		return failedList{operation: op.ID, reason: reasonUnreadable}, nil
	}
	return failedList{jobs: jobs, truncated: o.truncated, operation: op.ID}, nil
}

// FailedJobsPage is a page of failed jobs — never their payloads.
type FailedJobsPage struct {
	Jobs      []FailedJob `json:"jobs"`
	Truncated bool        `json:"truncated" doc:"More failed jobs exist than are listed."`
	Operation string      `json:"operation"`
}

// needsFailedJobs: only Laravel keeps a failed-job list its CLI can read.
func needsFailedJobs(t target) error {
	if t.det.Driver == DriverLaravel {
		return nil
	}
	_, why := t.det.Readable()
	if t.det.Driver == DriverCelery {
		why = "celery keeps no list of failed jobs that its CLI can read"
	}
	return fail(http.StatusUnprocessableEntity, "failed jobs of "+t.app.Name+" cannot be read: "+why)
}

// Failed lists the app's failed jobs. 502 when what the app printed could not be read.
func (s *Service) Failed(ctx context.Context, in InTeam, limit int) (FailedJobsPage, error) {
	if limit < 1 || limit > MaxFailedLimit {
		limit = DefaultFailedLimit
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var (
		t  target
		op db.Operation
	)
	first := laravelFailedJSON()
	err := in(ctx, func(q *db.Queries, teamID string, actor deploy.Actor, a db.App) error {
		var err error
		if t, err = s.resolve(ctx, q, teamID, a); err != nil {
			return err
		}
		if err := needsFailedJobs(t); err != nil {
			return err
		}
		op, err = s.enqueue(ctx, q, teamID, actor, t, first)
		return err
	})
	if err != nil {
		return FailedJobsPage{}, err
	}
	list, err := s.failedOf(ctx, in, t, first, op)
	if err != nil {
		return FailedJobsPage{}, err
	}
	if list.reason != "" {
		return FailedJobsPage{}, &deploy.ActionError{Status: http.StatusBadGateway, Msg: list.reason + " (operation " + list.operation + ")", Code: "operation_failed"}
	}
	page := FailedJobsPage{Jobs: list.jobs, Truncated: list.truncated, Operation: list.operation}
	if len(page.Jobs) > limit {
		page.Jobs, page.Truncated = page.Jobs[:limit], true
	}
	return page, nil
}

// ---- actions ---------------------------------------------------------------------------------------

// QueueActionResult is how an action on failed jobs ended.
type QueueActionResult struct {
	OK         bool   `json:"ok"`
	OutputTail string `json:"output_tail" doc:"The end of what the command printed."`
	Operation  string `json:"operation"`
}

// Act runs one action on the app's failed jobs and waits for it. The job id is checked before
// anything else; the audit entry is written in the round that queues the command. A command that
// exits non-zero is a 502 carrying the end of what it printed.
func (s *Service) Act(ctx context.Context, in InTeam, action, jobID string) (QueueActionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, actionTimeout*time.Second+opGrace)
	defer cancel()
	var (
		op  db.Operation
		cmd Command
	)
	err := in(ctx, func(q *db.Queries, teamID string, actor deploy.Actor, a db.App) error {
		// Inside the round: who may not see the app learns nothing about what an id must look like.
		var err error
		if cmd, err = LaravelAction(action, strings.TrimSpace(jobID)); err != nil {
			return fail(http.StatusUnprocessableEntity, err.Error())
		}
		t, err := s.resolve(ctx, q, teamID, a)
		if err != nil {
			return err
		}
		if err := needsFailedJobs(t); err != nil {
			return err
		}
		if op, err = s.enqueue(ctx, q, teamID, actor, t, cmd); err != nil {
			return err
		}
		changes := map[string]any{"name": a.Name, "driver": t.det.Driver, "operation": op.ID}
		if action == ActionRetry || action == ActionForget {
			changes["job"] = cmd.Argv[len(cmd.Argv)-1]
		}
		if actor.Via != "" && actor.Via != "web" {
			changes["via"] = actor.Via
		}
		if actor.TokenID != "" {
			changes["mcp_token_id"] = actor.TokenID
		}
		return tenancy.Record(ctx, q, teamID, actor.UserID, AuditOf[action], "app", a.ID, changes)
	})
	if err != nil {
		return QueueActionResult{}, err
	}
	o, err := s.await(ctx, in, cmd, op)
	if err != nil {
		return QueueActionResult{}, err
	}
	if o.exitCode != 0 {
		return QueueActionResult{}, &deploy.ActionError{Status: http.StatusBadGateway, Code: "operation_failed",
			Msg: fmt.Sprintf("php artisan %s exited with status %d: %s (operation %s)", cmd.Name, o.exitCode, lastLine(o.output), op.ID)}
	}
	return QueueActionResult{OK: true, OutputTail: tail(o.output, tailBytes), Operation: op.ID}, nil
}
