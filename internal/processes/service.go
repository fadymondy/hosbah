// Package processes is the "Workers" page's manage actions: status, restart, stop and start of one
// or every process of an app's live release, run through the agent's app.processes operation (fixed
// docker argv — see internal/agentproto and internal/agent). One implementation for the REST API and
// the user MCP, mirroring internal/queues: every round is a transaction the CALLER opens (InTeam), so
// a stranger's 404 and a viewer's 403 are the caller's own, and an action's audit entry is written in
// the same transaction that queues the operation — before the wait, never after.
package processes

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// opTimeout bounds the wait for the agent: a handful of docker inspect/stats/restart calls, never a
// build or a health gate.
const opTimeout = 60 * time.Second

// opGrace is added on top of the agent's own timeout for the round trip.
const opGrace = 30 * time.Second

// queueTTL: an action nobody is waiting for any more (the caller gave up) is dropped rather than run
// minutes later.
const queueTTL = time.Minute

// AuditOf is the audit action of each write action.
var AuditOf = map[string]string{
	agentproto.ProcessActionRestart: "app.process_restart",
	agentproto.ProcessActionStop:    "app.process_stop",
	agentproto.ProcessActionStart:   "app.process_start",
}

// Round is one team-scoped transaction of the caller's, with the app resolved in it.
type Round func(q *db.Queries, teamID string, actor deploy.Actor, a db.App) error

// InTeam opens a team-scoped transaction for the caller, checks what the caller may do, resolves the
// app, and runs fn in it.
type InTeam func(ctx context.Context, fn Round) error

// Service queues app.processes operations and waits for their answer.
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

// resolve checks that there is somewhere to run: a live release, on a server whose agent is online.
func (s *Service) resolve(ctx context.Context, q *db.Queries, teamID string, a db.App) error {
	if deploy.IsImported(a) {
		return fail(http.StatusUnprocessableEntity, "processes are managed on a release Hosbah deployed — "+a.Name+" is an imported app and has none")
	}
	live, err := q.LiveReleaseOfApp(ctx, a.ID)
	if err != nil || live.TeamID != teamID {
		return fail(http.StatusConflict, "nothing is live — deploy the app first")
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil {
		return fail(http.StatusConflict, "the app's server is gone")
	}
	if srv.Status != "enrolled" || srv.AgentPublicKey == nil {
		return fail(http.StatusConflict, "the agent of "+srv.Name+" has not enrolled yet")
	}
	if !s.ops.Connected(srv.ID) && !ops.Online(srv.LastSeenAt, s.now()) {
		return fail(http.StatusConflict, srv.Name+" is offline — processes can be managed once its agent is connected")
	}
	return nil
}

// Result is one round's answer: the app's processes, and the operation that read (or changed) them.
type Result struct {
	Processes agentproto.ProcessesResult
	Operation string
}

// Status reads the app's processes (one, or every one when process is "").
func (s *Service) Status(ctx context.Context, in InTeam, process string) (Result, error) {
	return s.run(ctx, in, agentproto.ProcessActionStatus, process)
}

// Act restarts, stops or starts one process (restart alone may also mean "every process", when
// process is ""), waits for it, and returns the fresh status. Audited on the transaction that queues
// the operation.
func (s *Service) Act(ctx context.Context, in InTeam, action, process string) (Result, error) {
	return s.run(ctx, in, action, process)
}

func (s *Service) run(ctx context.Context, in InTeam, action, process string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout+opGrace)
	defer cancel()
	var op db.Operation
	err := in(ctx, func(q *db.Queries, teamID string, actor deploy.Actor, a db.App) error {
		if err := s.resolve(ctx, q, teamID, a); err != nil {
			return err
		}
		params := agentproto.ProcessesParams{AppID: a.ID, Action: action, Process: process}
		if err := params.Validate(); err != nil {
			return fail(http.StatusUnprocessableEntity, err.Error())
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		created, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: a.ServerID, Kind: agentproto.KindAppProcesses,
			Params: raw, CreatedBy: createdBy(actor), Timeout: opTimeout, QueueTTL: queueTTL})
		if err != nil {
			return err
		}
		op = created
		if action == agentproto.ProcessActionStatus {
			return nil
		}
		changes := map[string]any{"name": a.Name, "process": process, "operation": op.ID}
		if actor.Via != "" && actor.Via != "web" {
			changes["via"] = actor.Via
		}
		if actor.TokenID != "" {
			changes["mcp_token_id"] = actor.TokenID
		}
		return tenancy.Record(ctx, q, teamID, actor.UserID, AuditOf[action], "app", a.ID, changes)
	})
	if err != nil {
		return Result{}, err
	}
	done, err := s.ops.Await(ctx, op.ID, op.ServerID, nil)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, fail(http.StatusGatewayTimeout, "the server did not answer in time — is its agent connected? (operation "+op.ID+")")
		}
		return Result{}, err
	}
	if done.Status == "timeout" {
		return Result{}, fail(http.StatusGatewayTimeout, "the operation did not finish in time (operation "+op.ID+")")
	}
	if done.Status != "ok" {
		msg := done.Status
		if done.Error != nil {
			msg = *done.Error
		}
		return Result{}, &deploy.ActionError{Status: http.StatusBadGateway, Msg: msg, Code: "operation_failed"}
	}
	var res agentproto.ProcessesResult
	if done.Result != nil {
		_ = json.Unmarshal([]byte(*done.Result), &res)
	}
	return Result{Processes: res, Operation: op.ID}, nil
}

func createdBy(actor deploy.Actor) string {
	if actor.UserID != "" {
		return actor.UserID
	}
	return "via:" + actor.Via
}
