// Package schedules is the "Schedules" of a site (app): cron jobs that run inside the app's live
// release.
//
// The scheduler is a control-plane worker like the monitors' evaluator: every TickEvery it claims
// the schedules that are due (system scope, FOR UPDATE SKIP LOCKED) and moves each one's
// next_run_at on in that same transaction — BEFORE anything runs, so a crash between the claim and
// the run loses one firing and never doubles one. Each claimed schedule then gets a run in its own
// team (Scope{System: true, TeamID: …}: `operations` has no system INSERT) and an app.exec
// operation for the app's server.
//
// A run learns how its operation ended from the ops broker (ops.OpTopic — the wake-up the agent's
// events already publish; see watch), and from a sweep of stranded runs on every tick, which covers
// a control plane that restarted in between. A server that stays offline is the ops queue's own
// timeout: the operation expires (QueueTTL) and the run is a timeout with that error.
package schedules

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/app"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

const (
	// TickEvery is how often the scheduler looks for due schedules.
	TickEvery = 15 * time.Second
	// QueueTTL is how long a run's operation waits for an offline server before it times out. A
	// cron job that would start much later than its minute is better reported than run.
	QueueTTL = 5 * time.Minute
	// opGrace is what the operation is allowed on top of the command's own timeout.
	opGrace = time.Minute
	// systemActor is operations.created_by of a scheduled (not manual) run.
	systemActor = "hosbah"

	claimBatch = 50
	watchPoll  = 5 * time.Second
)

// Service is the scheduler.
type Service struct {
	db  *sql.DB
	ops *ops.Service
	log *slog.Logger

	mu  sync.RWMutex
	now func() time.Time

	loopCancel context.CancelFunc
	loopWG     sync.WaitGroup

	base       context.Context // of the run watchers: they outlive Stop (a test ticks by hand), not Close
	baseCancel context.CancelFunc
	watchers   sync.WaitGroup
	watching   sync.Map // run id → struct{}
}

// New builds the service.
func New(a *app.App, o *ops.Service) *Service {
	log := a.Log
	if log == nil {
		log = slog.Default()
	}
	base, cancel := context.WithCancel(context.Background())
	return &Service{db: a.SQLDB, ops: o, log: log.With("component", "schedules"), now: time.Now, base: base, baseCancel: cancel}
}

// SetClock replaces the scheduler's clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.mu.Lock(); s.now = now; s.mu.Unlock() }

// Now is the scheduler's clock.
func (s *Service) Now() time.Time { s.mu.RLock(); defer s.mu.RUnlock(); return s.now() }

// Start launches the scheduler loop. The first tick comes TickEvery after Start.
func (s *Service) Start() {
	ctx, cancel := context.WithCancel(s.base)
	s.loopCancel = cancel
	s.loopWG.Add(1)
	go func() {
		defer s.loopWG.Done()
		t := time.NewTicker(TickEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
					s.log.Warn("schedule tick failed", "err", err)
				}
			}
		}
	}()
}

// Stop ends the loop; runs that are going are still followed to their end. Safe to call twice.
func (s *Service) Stop() {
	if s.loopCancel != nil {
		s.loopCancel()
		s.loopCancel = nil
	}
	s.loopWG.Wait()
}

// Close is Stop, and the end of following runs (the next process sweeps what is left).
func (s *Service) Close() {
	s.Stop()
	s.baseCancel()
	s.watchers.Wait()
}

// Tick finishes stranded runs, then fires every schedule that is due. It returns how many fired.
func (s *Service) Tick(ctx context.Context) (int, error) {
	if err := s.sweep(ctx); err != nil && ctx.Err() == nil {
		s.log.Warn("stranded runs not swept", "err", err)
	}
	total := 0
	for round := 0; round < 20; round++ {
		now := s.Now()
		var due []db.Schedule
		err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
			rows, err := q.ClaimDueSchedules(ctx, db.ClaimDueSchedulesParams{Now: now, Lim: claimBatch})
			if err != nil {
				return err
			}
			for _, sc := range rows {
				// After NOW, not after the fire that was due: whatever was missed while the control
				// plane was down collapses into this one firing.
				var next *time.Time
				if spec, err := ParseSpec(sc.Cron, sc.Timezone); err == nil {
					if n := spec.Next(now); !n.IsZero() {
						next = &n
					}
				} else {
					s.log.Warn("a stored schedule no longer parses; it will not fire again", "schedule", sc.ID, "err", err)
				}
				if err := q.AdvanceSchedule(ctx, db.AdvanceScheduleParams{ID: sc.ID, NextRunAt: next}); err != nil {
					return err
				}
			}
			due = rows
			return nil
		})
		if err != nil {
			return total, fmt.Errorf("claim: %w", err)
		}
		// next_run_at is committed. Only now does anything run.
		for _, sc := range due {
			if _, err := s.fire(ctx, sc); err != nil && ctx.Err() == nil {
				s.log.Warn("schedule not fired", "schedule", sc.ID, "err", err)
			}
		}
		total += len(due)
		if len(due) < claimBatch {
			break
		}
	}
	return total, nil
}

// fire is one scheduled firing, in the schedule's team.
func (s *Service) fire(ctx context.Context, sc db.Schedule) (db.ScheduleRun, error) {
	var run db.ScheduleRun
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true, TeamID: sc.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		run, err = s.startRun(ctx, q, sc, TriggerSchedule, systemActor)
		return err
	})
	var gone *deploy.ActionError
	if errors.As(err, &gone) {
		return run, nil // deleted or switched off since it was claimed: nothing to fire, nothing to report
	}
	if err != nil {
		return run, err
	}
	s.Follow(run)
	return run, nil
}

// RunNow fires a schedule at once (POST …/run) in the CALLER's team transaction; the caller checked
// the role and calls Follow(run) after its commit. Unlike a scheduled firing, what cannot start is
// refused rather than recorded: 409 while a run is still going or nothing is live, 422 for an
// imported app. A disabled schedule can be run by hand. Audited.
func (s *Service) RunNow(ctx context.Context, q *db.Queries, actor deploy.Actor, a db.App, sc db.Schedule) (db.ScheduleRun, error) {
	if err := refuseImported(a); err != nil {
		return db.ScheduleRun{}, err
	}
	run, err := s.startRun(ctx, q, sc, TriggerManual, actor.UserID)
	if err != nil {
		return run, err
	}
	return run, tenancy.Record(ctx, q, sc.TeamID, actor.UserID, "schedule.run", "app", a.ID,
		auditOf(actor, map[string]any{"schedule": sc.ID, "name": sc.Name, "run": run.ID}))
}

// startRun creates the run of one firing and queues its operation, under the schedule's row lock
// (so two firings never both find that nothing is running).
func (s *Service) startRun(ctx context.Context, q *db.Queries, sc db.Schedule, trigger, createdBy string) (db.ScheduleRun, error) {
	now := s.Now()
	manual := trigger == TriggerManual
	cur, err := q.LockSchedule(ctx, db.LockScheduleParams{ID: sc.ID, TeamID: sc.TeamID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.ScheduleRun{}, notFound("schedule") // deleted since it was claimed
	}
	if err != nil {
		return db.ScheduleRun{}, err
	}
	if !manual && !cur.Enabled {
		return db.ScheduleRun{}, conflict("the schedule was switched off") // since it was claimed
	}
	// ended records a firing that is over before it began.
	ended := func(status, msg string) (db.ScheduleRun, error) {
		zero := int64(0)
		run, err := q.InsertScheduleRun(ctx, db.InsertScheduleRunParams{TeamID: cur.TeamID, ScheduleID: cur.ID, AppID: cur.AppID,
			Trigger: trigger, Status: status, StartedAt: now, FinishedAt: &now, DurationMs: &zero, Error: msg})
		if err != nil {
			return run, err
		}
		return run, s.recorded(ctx, q, cur, run)
	}

	_, err = q.ActiveRunOfSchedule(ctx, db.ActiveRunOfScheduleParams{ScheduleID: cur.ID, TeamID: cur.TeamID})
	switch {
	case err == nil && manual:
		return db.ScheduleRun{}, conflict("a run of this schedule is still going")
	case err == nil:
		return ended(StatusSkipped, "skipped: the previous run was still going")
	case !errors.Is(err, sql.ErrNoRows):
		return db.ScheduleRun{}, err
	}

	a, err := deploy.AppByRef(ctx, q, cur.TeamID, cur.AppID)
	if err != nil {
		return db.ScheduleRun{}, err
	}
	if deploy.IsImported(a) {
		return ended(StatusFailed, "the app is imported: it has no release to run in")
	}
	live, err := q.LiveReleaseOfApp(ctx, a.ID)
	if err != nil || live.TeamID != cur.TeamID {
		const msg = "nothing is live to run in — deploy the app first"
		if manual {
			return db.ScheduleRun{}, conflict(msg)
		}
		return ended(StatusFailed, msg)
	}

	params := agentproto.ExecParams{AppID: a.ID, DeploymentID: live.DeploymentID, Argv: agentproto.ShellArgv(cur.Command),
		TimeoutSeconds: int(cur.TimeoutSeconds)}
	if err := params.Validate(); err != nil {
		return ended(StatusFailed, "the command cannot be run: "+err.Error())
	}
	raw, _ := json.Marshal(params)
	run, err := q.InsertScheduleRun(ctx, db.InsertScheduleRunParams{TeamID: cur.TeamID, ScheduleID: cur.ID, AppID: cur.AppID,
		Trigger: trigger, Status: StatusQueued, StartedAt: now})
	if err != nil {
		return run, err
	}
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: cur.TeamID, ServerID: a.ServerID, Kind: agentproto.KindAppExec, Params: raw,
		CreatedBy: createdBy, Timeout: time.Duration(cur.TimeoutSeconds)*time.Second + opGrace, QueueTTL: QueueTTL})
	if err != nil {
		return run, err
	}
	if err := q.SetScheduleRunOperation(ctx, db.SetScheduleRunOperationParams{ID: run.ID, TeamID: cur.TeamID, OperationID: op.ID}); err != nil {
		return run, err
	}
	run.OperationID = &op.ID
	return run, s.recorded(ctx, q, cur, run)
}

// recorded: the schedule mirrors its newest run, and keeps KeepRuns of them.
func (s *Service) recorded(ctx context.Context, q *db.Queries, sc db.Schedule, run db.ScheduleRun) error {
	if err := q.RecordScheduleRun(ctx, db.RecordScheduleRunParams{ID: sc.ID, TeamID: sc.TeamID, LastRunAt: run.StartedAt, LastStatus: run.Status}); err != nil {
		return err
	}
	_, err := q.PruneScheduleRuns(ctx, db.PruneScheduleRunsParams{ScheduleID: sc.ID, TeamID: sc.TeamID, Keep: KeepRuns})
	return err
}

// ---- how a run ends --------------------------------------------------------------------------------

func finished(status string) bool { return status == "ok" || status == "failed" || status == "timeout" }

// Follow watches a run's operation to its end and copies the outcome onto the run. It wakes on the
// ops broker's topic of the operation — what the agent's events publish — and re-reads the
// operation in the run's team; the slow timer only covers an event another replica received.
func (s *Service) Follow(run db.ScheduleRun) {
	if run.OperationID == nil || (run.Status != StatusQueued && run.Status != StatusRunning) {
		return
	}
	if _, dup := s.watching.LoadOrStore(run.ID, struct{}{}); dup {
		return
	}
	s.watchers.Add(1)
	go func() {
		defer s.watchers.Done()
		defer s.watching.Delete(run.ID)
		wake, unsub := s.ops.Broker().Subscribe(ops.OpTopic(*run.OperationID))
		defer unsub()
		tries := 0
		for {
			done, missing, err := s.settle(s.base, run)
			if err != nil && s.base.Err() == nil {
				s.log.Warn("run not settled", "run", run.ID, "err", err)
			}
			// A run started inside a caller's transaction (the MCP tool) may not be committed yet.
			if missing && tries < 5 {
				tries++
				done = false
			}
			if done {
				return
			}
			select {
			case <-s.base.Done():
				return
			case <-wake:
			case <-time.After(watchPoll):
			}
		}
	}()
}

// sweep settles the runs nobody follows: their operation finished (or is gone) while no watcher of
// this process was there — a restart, or another replica's run.
func (s *Service) sweep(ctx context.Context) error {
	var stranded []db.ScheduleRun
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		stranded, err = q.StrandedScheduleRuns(ctx, 100)
		return err
	})
	for _, run := range stranded {
		if _, busy := s.watching.Load(run.ID); busy {
			continue
		}
		if _, _, err := s.settle(ctx, run); err != nil && ctx.Err() == nil {
			s.log.Warn("stranded run not settled", "run", run.ID, "err", err)
		}
	}
	return err
}

// settle reads the run's operation and, when it is over, copies status, exit code and the sealed
// tail of its output onto the run (once: FinishScheduleRun only finishes a run that is going).
// done = there is nothing left to wait for.
func (s *Service) settle(ctx context.Context, run db.ScheduleRun) (done, missing bool, err error) {
	appTopic := ""
	err = tenancy.Tx(ctx, s.db, tenancy.Scope{System: true, TeamID: run.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		cur, err := q.ScheduleRunOfSchedule(ctx, db.ScheduleRunOfScheduleParams{ID: run.ID, ScheduleID: run.ScheduleID, TeamID: run.TeamID})
		if errors.Is(err, sql.ErrNoRows) {
			done, missing = true, true // pruned or deleted with its schedule — or its transaction has not committed yet
			return nil
		}
		if err == nil && cur.Status != StatusQueued && cur.Status != StatusRunning {
			done = true // settled by someone else
			return nil
		}
		if err != nil {
			return err
		}
		now := s.Now()
		end := func(status string, exit *int64, output, msg string, took *int64) error {
			if took == nil {
				ms := max(now.Sub(cur.StartedAt).Milliseconds(), 0)
				took = &ms
			}
			fin, err := q.FinishScheduleRun(ctx, db.FinishScheduleRunParams{ID: cur.ID, TeamID: cur.TeamID, Status: status, ExitCode: exit,
				FinishedAt: now, DurationMs: *took, Output: output, Error: clipText(msg, 1000)})
			if errors.Is(err, sql.ErrNoRows) {
				done = true
				return nil
			}
			if err != nil {
				return err
			}
			done, appTopic = true, ops.AppTopic(cur.AppID)
			return q.RecordScheduleStatus(ctx, db.RecordScheduleStatusParams{ID: cur.ScheduleID, TeamID: cur.TeamID, LastStatus: status,
				RunID: fin.ID, StartedAt: fin.StartedAt})
		}
		if cur.OperationID == nil {
			return end(StatusFailed, nil, "", "its operation is gone (the server was removed?)", nil)
		}
		op, err := q.OperationInTeam(ctx, db.OperationInTeamParams{ID: *cur.OperationID, TeamID: cur.TeamID})
		if errors.Is(err, sql.ErrNoRows) {
			return end(StatusFailed, nil, "", "its operation is gone (the server was removed?)", nil)
		}
		if err != nil {
			return err
		}
		if !finished(op.Status) {
			if op.Status == "running" && cur.Status == StatusQueued {
				_, err = q.MarkScheduleRunRunning(ctx, db.MarkScheduleRunRunningParams{ID: cur.ID, TeamID: cur.TeamID})
				appTopic = ops.AppTopic(cur.AppID)
			}
			return err
		}

		dek, err := s.ops.TeamDEK(ctx, q, cur.TeamID)
		if err != nil {
			return err
		}
		tail, err := outputTail(ctx, q, dek, op)
		if err != nil {
			return err
		}
		sealed := ""
		if tail != "" {
			if sealed, err = SealOutput(dek, cur.TeamID, cur.ID, tail); err != nil {
				return err
			}
		}
		var res agentproto.ExecResult
		ran := op.Result != nil && json.Unmarshal([]byte(*op.Result), &res) == nil && res.Container != ""
		var exit *int64
		if ran {
			exit = ptr(int64(res.ExitCode))
		}
		msg := ""
		if op.Error != nil {
			msg = *op.Error
		}
		var took *int64
		if op.StartedAt != nil && op.FinishedAt != nil {
			took = ptr(max(op.FinishedAt.Sub(*op.StartedAt).Milliseconds(), 0))
		}
		switch {
		case op.Status == "ok":
			return end(StatusSucceeded, exit, sealed, "", took)
		case op.Status == "timeout" || (ran && res.TimedOut):
			if op.Status == "timeout" && op.SentAt == nil {
				msg = "the server was offline: " + msg
			}
			return end(StatusTimeout, exit, sealed, msg, took)
		default:
			return end(StatusFailed, exit, sealed, msg, took)
		}
	})
	if err == nil && appTopic != "" {
		s.ops.Broker().Publish(appTopic)
	}
	return done, missing, err
}

// outputTail is the last MaxOutput bytes of what the command printed: the operation's stdout and
// stderr lines in order (the agent's own info lines are not the command's output).
func outputTail(ctx context.Context, q *db.Queries, dek []byte, op db.Operation) (string, error) {
	var (
		lines []string
		size  int
		after int64
		cut   bool
	)
	for {
		rows, err := q.OperationLogsAfter(ctx, db.OperationLogsAfterParams{OperationID: op.ID, TeamID: op.TeamID, Seq: after, Limit: 1000})
		if err != nil {
			return "", err
		}
		for _, row := range rows {
			after = row.Seq
			if row.Stream != "stdout" && row.Stream != "stderr" {
				continue
			}
			l, err := ops.OpenLog(dek, op.TeamID, row)
			if err != nil {
				return "", err
			}
			lines = append(lines, l.Line)
			size += len(l.Line) + 1
			for size > MaxOutput && len(lines) > 1 {
				size -= len(lines[0]) + 1
				lines = lines[1:]
				cut = true
			}
		}
		if len(rows) < 1000 {
			break
		}
	}
	if len(lines) == 0 {
		return "", nil
	}
	out := strings.Join(lines, "\n") + "\n"
	if len(out) > MaxOutput { // one enormous line
		out, cut = strings.ToValidUTF8(out[len(out)-MaxOutput:], ""), true
	}
	if cut {
		out = "[… earlier output dropped]\n" + out
	}
	return out, nil
}

// A run's output is sealed with the team's DEK, bound to the team and to this run's output field:
// copied onto another run, or another team, it does not open.
func outputContext(runID string) string { return "schedulerun:" + runID + ":output" }

// SealOutput seals a run's output for storage.
func SealOutput(dek []byte, teamID, runID, output string) (string, error) {
	return crypto.Seal(dek, teamID, outputContext(runID), []byte(output))
}

// Output opens a run's output in the caller's team transaction ("" = it printed nothing).
func (s *Service) Output(ctx context.Context, q *db.Queries, run db.ScheduleRun) (string, error) {
	if run.Output == "" {
		return "", nil
	}
	dek, err := s.ops.TeamDEK(ctx, q, run.TeamID)
	if err != nil {
		return "", err
	}
	pt, err := crypto.Open(dek, run.TeamID, outputContext(run.ID), run.Output)
	return string(pt), err
}

func clipText(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func ptr[T any](v T) *T { return &v }
