// Package scripts is the control plane of MH-576 D4/D5: a team's own script library (D5) and the
// built-in one-click installs (D1, internal/scripts/builtin), run on one or many servers with the
// agent's script.run operation.
//
// A run creates one script_runs row and one script_run_servers row per target — the operations
// table is still the queue. Each run-server is followed to its end exactly like a schedule's run
// (internal/schedules/service.go): woken by the ops broker's per-operation topic, and swept for
// what nobody followed (a restart). Secret env values never sit in operations.params: they are
// opened with the team's own DEK and re-sealed to the target agent's key (agentproto.SealBox),
// exactly like a deploy's env (internal/deploy's envBox).
package scripts

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
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/scripts/builtin"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Run statuses (script_runs.status / script_run_servers.status; the DB CHECK constraints are the
// source of truth, these are just the Go names for them).
const (
	StatusRunning = "running"
	StatusOK      = "ok"
	StatusFailed  = "failed"
	StatusTimeout = "timeout"
	StatusPending = "pending"
	StatusSent    = "sent"

	// MaxTargets bounds one run's fan-out.
	MaxTargets = 50
	// MaxOutput is the tail kept of a run-server's output.
	MaxOutput = 1 << 20
	// opGrace is added to a script's own timeout for the enqueued operation.
	opGrace = 30 * time.Second
	// QueueTTL: how long a run-server's operation waits for an offline server.
	QueueTTL = 30 * time.Minute

	watchPoll = 5 * time.Second
)

// Service runs scripts on servers and follows them to completion.
type Service struct {
	db  *sql.DB
	ops *ops.Service
	a   *app.App
	log *slog.Logger

	base       context.Context
	baseCancel context.CancelFunc
	watchers   sync.WaitGroup
	watching   sync.Map // script_run_server id -> struct{}

	loopCancel context.CancelFunc
	loopWG     sync.WaitGroup
}

// New builds the service.
func New(a *app.App, o *ops.Service) *Service {
	log := a.Log
	if log == nil {
		log = slog.Default()
	}
	base, cancel := context.WithCancel(context.Background())
	return &Service{db: a.SQLDB, ops: o, a: a, log: log.With("component", "scripts"), base: base, baseCancel: cancel}
}

// sweepEvery is how often Start's loop sweeps for run-servers nobody is following (a restart).
const sweepEvery = 15 * time.Second

// Start launches the sweep loop, covering a control-plane restart mid-run.
func (s *Service) Start() {
	ctx, cancel := context.WithCancel(s.base)
	s.loopCancel = cancel
	s.loopWG.Add(1)
	go func() {
		defer s.loopWG.Done()
		t := time.NewTicker(sweepEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
					s.log.Warn("script run sweep failed", "err", err)
				}
			}
		}
	}()
}

// Stop ends the sweep loop (run-following continues until Close).
func (s *Service) Stop() {
	if s.loopCancel != nil {
		s.loopCancel()
	}
	s.loopWG.Wait()
}

// Close ends run-following; the next process's sweep picks up whatever is left going.
func (s *Service) Close() {
	s.Stop()
	s.baseCancel()
	s.watchers.Wait()
}

// Target is one server + its resolved agent key and DEK, gathered before StartRun so a bad target
// (no enrolled agent) fails the whole call before anything is written.
type Target struct {
	ServerID string
	AgentKey string
}

// ResolveTargets loads the agent key of every server id, in the caller's team scope. Every id must
// belong to the team and have an enrolled agent, or the whole call fails (nothing partially run).
func ResolveTargets(ctx context.Context, q *db.Queries, teamID string, serverIDs []string) ([]Target, error) {
	if len(serverIDs) == 0 {
		return nil, errors.New("choose at least one server")
	}
	if len(serverIDs) > MaxTargets {
		return nil, fmt.Errorf("at most %d servers per run", MaxTargets)
	}
	seen := map[string]bool{}
	out := make([]Target, 0, len(serverIDs))
	for _, id := range serverIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: id, TeamID: teamID})
		if err != nil {
			return nil, fmt.Errorf("server %s not found", id)
		}
		k, err := q.ServerTeamKey(ctx, srv.ID)
		if err != nil || k.AgentPublicKey == nil {
			return nil, fmt.Errorf("server %s has no enrolled agent", srv.Name)
		}
		out = append(out, Target{ServerID: srv.ID, AgentKey: *k.AgentPublicKey})
	}
	return out, nil
}

// StartInput is what starts a run.
type StartInput struct {
	ScriptID       *string // exactly one of ScriptID and BuiltinName
	BuiltinName    *string
	Name           string // for logging on the agent
	Body           string
	Args           map[string]string // non-secret; becomes env vars as given
	SecretEnv      map[string]string // sealed to each target agent; never stored in operations.params
	TimeoutSeconds int
	Targets        []Target
	CreatedBy      string
}

// StartRun creates the run and one row + operation per target, in the caller's team transaction
// (tenancy.Scope{TeamID: …} or a member's own scope — script_runs/script_run_servers allow a
// team's own INSERT for this reason, see the RLS migration). Call Follow after the commit.
func (s *Service) StartRun(ctx context.Context, q *db.Queries, teamID string, in StartInput) (db.ScriptRun, []db.ScriptRunServer, error) {
	if (in.ScriptID == nil) == (in.BuiltinName == nil) {
		return db.ScriptRun{}, nil, errors.New("exactly one of script_id and builtin_name")
	}
	paramsRaw, _ := json.Marshal(map[string]any{"args": in.Args})
	run, err := q.CreateTeamScriptRun(ctx, db.CreateTeamScriptRunParams{
		TeamID: teamID, ScriptID: in.ScriptID, BuiltinName: in.BuiltinName, Params: string(paramsRaw), CreatedBy: in.CreatedBy,
	})
	if err != nil {
		return db.ScriptRun{}, nil, err
	}

	rows := make([]db.ScriptRunServer, 0, len(in.Targets))
	for _, t := range in.Targets {
		rs, err := q.CreateTeamScriptRunServer(ctx, db.CreateTeamScriptRunServerParams{TeamID: teamID, ScriptRunID: run.ID, ServerID: t.ServerID})
		if err != nil {
			return run, rows, err
		}

		envBox := ""
		if len(in.SecretEnv) > 0 {
			raw, _ := json.Marshal(in.SecretEnv)
			envBox, err = agentproto.SealBox(t.AgentKey, agentproto.ScriptEnvContext(rs.ID), raw)
			if err != nil {
				return run, rows, fmt.Errorf("could not seal the run's secret env: %w", err)
			}
		}
		params := agentproto.ScriptRunParams{ScriptRunID: rs.ID, Name: in.Name, Body: in.Body, Args: in.Args, EnvBox: envBox,
			TimeoutSeconds: in.TimeoutSeconds}
		if err := params.Validate(); err != nil {
			return run, rows, fmt.Errorf("the script cannot be run: %w", err)
		}
		opRaw, _ := json.Marshal(params)
		op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: t.ServerID, Kind: agentproto.KindScriptRun, Params: opRaw,
			CreatedBy: in.CreatedBy, Timeout: time.Duration(in.TimeoutSeconds)*time.Second + opGrace, QueueTTL: QueueTTL})
		if err != nil {
			return run, rows, err
		}
		rs, err = q.SetTeamScriptRunServerOp(ctx, db.SetTeamScriptRunServerOpParams{ID: rs.ID, TeamID: teamID, OperationID: &op.ID})
		if err != nil {
			return run, rows, err
		}
		rows = append(rows, rs)
	}
	return run, rows, nil
}

// BodyAndDefaults resolves a run's source (a team script or a built-in) to its body, run_as,
// default timeout and param allow list, and validates the caller's params against it. For a
// built-in, params become the returned Args map (HOSBAH_PARAM_<NAME> names); for a team script,
// the caller's args are passed through as given (its own env_names is documentation only — the
// agent does not enforce it, the team that wrote the script is trusted with its own body).
func BodyAndDefaults(ctx context.Context, q *db.Queries, teamID string, scriptID, builtinName *string, given map[string]string) (
	name, body, runAs string, timeoutSeconds int, args map[string]string, err error) {
	switch {
	case builtinName != nil:
		b, ok := builtin.Get(*builtinName)
		if !ok {
			return "", "", "", 0, nil, fmt.Errorf("no built-in script %q", *builtinName)
		}
		body, err = b.Body()
		if err != nil {
			return "", "", "", 0, nil, err
		}
		args, err = b.ValidateParams(given)
		if err != nil {
			return "", "", "", 0, nil, err
		}
		return b.Name, body, b.RunAs, b.TimeoutSeconds, args, nil
	case scriptID != nil:
		sc, err := q.GetTeamScript(ctx, db.GetTeamScriptParams{TeamID: teamID, ID: *scriptID})
		if err != nil {
			return "", "", "", 0, nil, fmt.Errorf("script not found")
		}
		return sc.Name, sc.Body, sc.RunAs, int(sc.TimeoutSeconds), given, nil
	default:
		return "", "", "", 0, nil, errors.New("exactly one of script_id and builtin_name")
	}
}

// ---- following a run to its end -------------------------------------------------------------

func finished(status string) bool {
	return status == StatusOK || status == StatusFailed || status == StatusTimeout
}

// Follow watches every not-yet-finished row of a run to its end. Call it once, after the
// transaction that created the run (and its operations) has committed.
func (s *Service) Follow(teamID string, rows []db.ScriptRunServer) {
	for _, rs := range rows {
		s.followOne(teamID, rs)
	}
}

func (s *Service) followOne(teamID string, rs db.ScriptRunServer) {
	if rs.OperationID == nil || finished(rs.Status) {
		return
	}
	if _, dup := s.watching.LoadOrStore(rs.ID, struct{}{}); dup {
		return
	}
	s.watchers.Add(1)
	go func() {
		defer s.watchers.Done()
		defer s.watching.Delete(rs.ID)
		wake, unsub := s.ops.Broker().Subscribe(ops.OpTopic(*rs.OperationID))
		defer unsub()
		tries := 0
		for {
			done, missing, err := s.settle(s.base, teamID, rs.ID)
			if err != nil && s.base.Err() == nil {
				s.log.Warn("script run not settled", "run_server", rs.ID, "err", err)
			}
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

// Sweep settles the run-servers nobody is following (a restart, or another replica's run). Call
// this periodically (e.g. from the same ticker as another sweep, or its own short one).
func (s *Service) Sweep(ctx context.Context) error {
	var stranded []db.ScriptRunServer
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		rows, err := q.ListScriptRunServers(ctx, db.ListScriptRunServersParams{Limit: 200, Offset: 0})
		if err != nil {
			return err
		}
		for _, r := range rows {
			if !finished(r.Status) {
				stranded = append(stranded, r)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, rs := range stranded {
		if _, busy := s.watching.Load(rs.ID); busy {
			continue
		}
		if _, _, err := s.settle(ctx, rs.TeamID, rs.ID); err != nil && ctx.Err() == nil {
			s.log.Warn("stranded script run not settled", "run_server", rs.ID, "err", err)
		}
	}
	return nil
}

// settle reads one run-server's operation and, when it is over, records the outcome and rolls the
// parent run's status up once every target is done.
func (s *Service) settle(ctx context.Context, teamID, runServerID string) (done, missing bool, err error) {
	err = tenancy.Tx(ctx, s.db, tenancy.Scope{System: true, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		rs, err := q.GetScriptRunServer(ctx, runServerID)
		if errors.Is(err, sql.ErrNoRows) {
			done, missing = true, true
			return nil
		}
		if err != nil {
			return err
		}
		if finished(rs.Status) {
			done = true
			return nil
		}
		if rs.OperationID == nil {
			return s.finishOne(ctx, q, rs, StatusFailed, nil, "", &done)
		}
		op, err := q.OperationInTeam(ctx, db.OperationInTeamParams{ID: *rs.OperationID, TeamID: teamID})
		if errors.Is(err, sql.ErrNoRows) {
			return s.finishOne(ctx, q, rs, StatusFailed, nil, "its operation is gone (the server was removed?)", &done)
		}
		if err != nil {
			return err
		}
		if op.Status == "running" && rs.Status != "running" {
			_, err = q.SetTeamScriptRunServerRunning(ctx, db.SetTeamScriptRunServerRunningParams{ID: rs.ID, TeamID: teamID})
			return err
		}
		if op.Status != "ok" && op.Status != "failed" && op.Status != "timeout" {
			return nil // still pending/sent/running: wait for the next wake
		}

		dek, err := s.ops.TeamDEK(ctx, q, teamID)
		if err != nil {
			return err
		}
		tail, err := outputTail(ctx, q, dek, op)
		if err != nil {
			return err
		}
		sealed := ""
		if tail != "" {
			if sealed, err = SealOutput(dek, teamID, rs.ID, tail); err != nil {
				return err
			}
		}
		var res agentproto.ScriptRunResult
		_ = json.Unmarshal(safeResult(op), &res)
		msg := ""
		if op.Error != nil {
			msg = *op.Error
		}
		status := StatusFailed
		switch {
		case op.Status == "ok":
			status = StatusOK
		case op.Status == "timeout" || res.TimedOut:
			status = StatusTimeout
		}
		if msg != "" && sealed == "" {
			sealed, err = SealOutput(dek, teamID, rs.ID, msg)
			if err != nil {
				return err
			}
		}
		return s.finishOneSealed(ctx, q, rs, status, exitPtr(res), sealed, msg, &done)
	})
	return done, missing, err
}

func safeResult(op db.Operation) []byte {
	if op.Result == nil {
		return nil
	}
	return []byte(*op.Result)
}

func exitPtr(res agentproto.ScriptRunResult) *int64 {
	if res.ExitCode == 0 && res.Bytes == 0 && !res.TimedOut {
		return nil
	}
	v := int64(res.ExitCode)
	return &v
}

// finishOne finishes a run-server with no output (its operation vanished before it ran).
func (s *Service) finishOne(ctx context.Context, q *db.Queries, rs db.ScriptRunServer, status string, exit *int64, _ string, done *bool) error {
	return s.finishOneSealed(ctx, q, rs, status, exit, "", "", done)
}

func (s *Service) finishOneSealed(ctx context.Context, q *db.Queries, rs db.ScriptRunServer, status string, exit *int64, output, _msg string, done *bool) error {
	if _, err := q.SetTeamScriptRunServerDone(ctx, db.SetTeamScriptRunServerDoneParams{ID: rs.ID, TeamID: rs.TeamID, Status: status, ExitCode: exit, Output: output}); err != nil {
		return err
	}
	*done = true
	return s.rollUp(ctx, q, rs.TeamID, rs.ScriptRunID)
}

// rollUp sets the parent run's status once every target is finished: ok only if every target is
// ok, failed otherwise (a timeout at any target makes the run "failed" overall — see it per server).
func (s *Service) rollUp(ctx context.Context, q *db.Queries, teamID, runID string) error {
	rows, err := q.ListTeamScriptRunServers(ctx, db.ListTeamScriptRunServersParams{TeamID: teamID, ScriptRunID: runID})
	if err != nil {
		return err
	}
	allDone, allOK := true, true
	for _, r := range rows {
		if !finished(r.Status) {
			allDone = false
			break
		}
		if r.Status != StatusOK {
			allOK = false
		}
	}
	if !allDone {
		return nil
	}
	status := StatusFailed
	if allOK {
		status = StatusOK
	}
	_, err = q.SetTeamScriptRunStatus(ctx, db.SetTeamScriptRunStatusParams{ID: runID, TeamID: teamID, Status: status})
	return err
}

// outputTail is the last MaxOutput bytes of what the script printed, in order.
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
	if len(out) > MaxOutput {
		out, cut = strings.ToValidUTF8(out[len(out)-MaxOutput:], ""), true
	}
	if cut {
		out = "[… earlier output dropped]\n" + out
	}
	return out, nil
}

// outputContext binds a run-server's sealed output to itself alone.
func outputContext(runServerID string) string { return "scriptrun:" + runServerID + ":output" }

// SealOutput seals a run-server's output for storage.
func SealOutput(dek []byte, teamID, runServerID, output string) (string, error) {
	return crypto.Seal(dek, teamID, outputContext(runServerID), []byte(output))
}

// Output opens a run-server's stored output in the caller's team transaction ("" = nothing stored
// yet, e.g. the run is still going).
func (s *Service) Output(ctx context.Context, q *db.Queries, rs db.ScriptRunServer) (string, error) {
	if rs.Output == "" {
		return "", nil
	}
	dek, err := s.ops.TeamDEK(ctx, q, rs.TeamID)
	if err != nil {
		return "", err
	}
	pt, err := crypto.Open(dek, rs.TeamID, outputContext(rs.ID), rs.Output)
	return string(pt), err
}
