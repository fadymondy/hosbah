package ops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Executors.
const (
	ExecutorAgent        = "agent"
	ExecutorControlPlane = "control_plane"
)

// Control-plane operation kinds (run by the control plane itself, logged like agent ops).
const (
	KindProviderCreate = "provider.create"
	// KindServerConnect logs into a server the user already has (root or sudo user; password or key),
	// installs Hosbah's key and the agent, and waits for it to enrol.
	KindServerConnect = "server.connect"
)

// Timeout is how long an operation may run once sent (the agent enforces it too, with exit 124).
func Timeout(kind string) time.Duration {
	switch kind {
	case agentproto.KindSystemInfo, agentproto.KindDockerCheck:
		return 2 * time.Minute
	case agentproto.KindAgentUpdate:
		return 5 * time.Minute
	case agentproto.KindProvisionStep, KindProviderCreate, KindServerConnect:
		return 30 * time.Minute
	case agentproto.KindAppBuild:
		return 45 * time.Minute
	case agentproto.KindAppRelease:
		return 20 * time.Minute
	case agentproto.KindAppStart, agentproto.KindAppRollback:
		return 15 * time.Minute // a 600 s health gate plus an image start
	case agentproto.KindRouteSet, agentproto.KindOpCancel, agentproto.KindProxySet:
		return 2 * time.Minute
	case agentproto.KindTunnelFrp, agentproto.KindTunnelTailscale:
		return 5 * time.Minute
	case agentproto.KindAppLogs:
		return 6 * time.Minute
	case agentproto.KindDBProvision, agentproto.KindDBRestore:
		return 20 * time.Minute // pull an image, start the engine, wait for readiness, restore
	case agentproto.KindDBBackup:
		return 30 * time.Minute
	case agentproto.KindDBCreate, agentproto.KindDBRemove, agentproto.KindDBDrop:
		return 5 * time.Minute
	}
	return 10 * time.Minute
}

// QueueTTL: a pending operation whose server stays offline this long times out.
const QueueTTL = time.Hour

// signedFor: how long a signed op may be accepted as NEW by the agent. It is re-signed on every
// send, so this only bounds the replay window of one captured frame.
const signedFor = 5 * time.Minute

var (
	// ErrOpIDTaken: an explicit op_id already names a different operation (idempotency key reuse).
	ErrOpIDTaken = errors.New("that op_id already names a different operation")
	// ErrKind: not an operation kind this control plane can run.
	ErrKind = errors.New("unknown operation kind")
)

// EnqueueSpec is one operation to queue.
type EnqueueSpec struct {
	OpID      string // optional idempotency key (a uuid); generated when empty
	TeamID    string
	ServerID  string
	Kind      string
	Params    json.RawMessage
	CreatedBy string
	Executor  string // default agent
	// DeploymentID ties an agent operation to a deployment: its events are also appended to the
	// deployment's own log.
	DeploymentID string
}

// Enqueue inserts an operation in the caller's team transaction. It is idempotent by op_id: the
// same op_id with the same server, kind and params returns the existing operation (created=false);
// with different content it is ErrOpIDTaken.
func (s *Service) Enqueue(ctx context.Context, q *db.Queries, sp EnqueueSpec) (op db.Operation, created bool, err error) {
	if sp.Executor == "" {
		sp.Executor = ExecutorAgent
	}
	if sp.Executor == ExecutorAgent && !isAgentKind(sp.Kind) || sp.Executor == ExecutorControlPlane && sp.Kind != KindProviderCreate && sp.Kind != KindServerConnect {
		return db.Operation{}, false, ErrKind
	}
	canon, err := agentproto.CanonicalParams(sp.Params)
	if err != nil {
		return db.Operation{}, false, err
	}
	if !strings.HasPrefix(string(canon), "{") {
		return db.Operation{}, false, errors.New("params must be a JSON object")
	}
	hash, _ := agentproto.ParamsHash(canon)
	if sp.OpID == "" {
		sp.OpID = newUUID()
	}
	op, err = q.InsertOperation(ctx, db.InsertOperationParams{
		ID: sp.OpID, TeamID: sp.TeamID, ServerID: sp.ServerID, Kind: sp.Kind, Executor: sp.Executor,
		Params: string(canon), ParamsHash: hash, CreatedBy: sp.CreatedBy,
		TimeoutSeconds: int64(Timeout(sp.Kind) / time.Second), ExpiresAt: s.now().Add(QueueTTL).UTC(),
		DeploymentID: optional(sp.DeploymentID),
	})
	if errors.Is(err, sql.ErrNoRows) { // ON CONFLICT DO NOTHING: the op_id exists
		existing, gErr := q.OperationInTeam(ctx, db.OperationInTeamParams{ID: sp.OpID, TeamID: sp.TeamID})
		if gErr != nil || existing.ServerID != sp.ServerID || existing.Kind != sp.Kind || existing.ParamsHash != hash {
			return db.Operation{}, false, ErrOpIDTaken
		}
		return existing, false, nil
	}
	if err != nil {
		return db.Operation{}, false, err
	}
	s.Wake()
	s.broker.Publish(ServerTopic(sp.ServerID))
	return op, true, nil
}

// Await blocks until an operation has finished (ok, failed or timeout) or ctx ends. between runs
// on every poll while it is still going (the deploy pipeline checks for a cancel request there).
// System scope: only the control plane's own workers call it.
func (s *Service) Await(ctx context.Context, opID, serverID string, between func()) (db.Operation, error) {
	wake, unsub := s.broker.Subscribe(OpTopic(opID))
	defer unsub()
	for {
		var op db.Operation
		err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
			var err error
			op, err = q.OperationForServer(ctx, db.OperationForServerParams{ID: opID, ServerID: serverID})
			return err
		})
		if err != nil {
			return op, err
		}
		if op.Status == "ok" || op.Status == "failed" || op.Status == "timeout" {
			return op, nil
		}
		if between != nil {
			between()
		}
		select {
		case <-ctx.Done():
			return op, ctx.Err()
		case <-wake:
		case <-time.After(2 * time.Second):
		}
	}
}

func isAgentKind(k string) bool {
	for _, a := range agentproto.AgentKinds {
		if a == k {
			return true
		}
	}
	return false
}

// Sign produces the frame an agent will verify. It re-derives the hash from the stored params
// and refuses to sign if they no longer match what was queued.
func (s *Service) Sign(op db.Operation) (*agentproto.SignedOp, error) {
	so := &agentproto.SignedOp{
		OpID: op.ID, ServerID: op.ServerID, Kind: op.Kind, Params: json.RawMessage(op.Params),
		Exp: s.now().Add(signedFor).Unix(), TimeoutSeconds: op.TimeoutSeconds,
	}
	if err := agentproto.SignOp(s.signer, so); err != nil {
		return nil, err
	}
	if so.ParamsHash != op.ParamsHash {
		return nil, fmt.Errorf("operation %s: params changed since they were queued — not signing", op.ID)
	}
	return so, nil
}

// ---- dispatcher --------------------------------------------------------------------------------

func (s *Service) dispatchLoop(ctx context.Context) {
	defer s.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
		if err := s.dispatchOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("dispatch failed", "err", err)
		}
	}
}

// dispatchOnce claims pending operations of connected agents with FOR UPDATE SKIP LOCKED — so
// concurrent dispatchers (goroutines, or replicas each holding different agents) never claim the
// same row — signs each, hands it to the agent's connection and marks it sent, in one transaction.
func (s *Service) dispatchOnce(ctx context.Context) error {
	ids := s.hub.ids()
	if len(ids) == 0 {
		return nil
	}
	var touched []db.Operation
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		ops, err := q.ClaimAgentOperations(ctx, db.ClaimAgentOperationsParams{ServerIds: strings.Join(ids, ","), Lim: 50})
		if err != nil {
			return err
		}
		for _, op := range ops {
			c := s.hub.get(op.ServerID)
			if c == nil {
				continue
			}
			signed, err := s.Sign(op)
			if err != nil {
				s.log.Error("refusing to sign", "op", op.ID, "err", err)
				continue
			}
			select {
			case c.send <- agentproto.Frame{Type: agentproto.TypeOp, Op: signed}:
			default:
				continue // the connection is backed up; the op stays pending for the next round
			}
			// If this transaction then fails to commit, the op stays pending and is sent again —
			// the agent recognises the op_id and does not run it twice.
			if err := q.MarkOperationSent(ctx, op.ID); err != nil {
				return err
			}
			touched = append(touched, op)
		}
		return nil
	})
	for _, op := range touched {
		s.broker.Publish(OpTopic(op.ID), ServerTopic(op.ServerID))
	}
	return err
}

// ---- timeout sweeper ---------------------------------------------------------------------------

func (s *Service) sweepLoop(ctx context.Context) {
	defer s.wg.Done()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := s.sweepOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("timeout sweep failed", "err", err)
		}
	}
}

func (s *Service) sweepOnce(ctx context.Context) error {
	var timedOut []db.TimeOutOperationsRow
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		rows, err := q.TimeOutOperations(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			_, dek, err := s.systemDEK(ctx, q, r.ServerID)
			if err != nil {
				return err
			}
			msg := "timed out"
			if r.Error != nil {
				msg = *r.Error
			}
			op := db.Operation{ID: r.ID, TeamID: r.TeamID, ServerID: r.ServerID}
			if err := appendLog(ctx, q, dek, op, LogLine{Kind: agentproto.EventStatus, Status: "timeout",
				Stream: "error", Line: statusLine("timeout", nil, msg)}); err != nil {
				return err
			}
		}
		timedOut = rows
		return nil
	})
	for _, r := range timedOut {
		s.broker.Publish(OpTopic(r.ID), ServerTopic(r.ServerID))
	}
	return err
}

// ---- control-plane operations ------------------------------------------------------------------

func (s *Service) controlPlaneLoop(ctx context.Context) {
	defer s.wg.Done()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
		for ctx.Err() == nil {
			op, ok, err := s.claimControlPlane(ctx)
			if err != nil {
				s.log.Warn("control-plane claim failed", "err", err)
				break
			}
			if !ok {
				break
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.runControlPlane(ctx, op)
			}()
		}
	}
}

func (s *Service) claimControlPlane(ctx context.Context) (db.Operation, bool, error) {
	var op db.Operation
	found := false
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		o, err := q.ClaimControlPlaneOperation(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := q.MarkOperationRunning(ctx, db.MarkOperationRunningParams{ID: o.ID, ServerID: o.ServerID}); err != nil {
			return err
		}
		op, found = o, true
		return nil
	})
	return op, found, err
}

func (s *Service) runControlPlane(ctx context.Context, op db.Operation) {
	s.runnersMu.RLock()
	run := s.runners[op.Kind]
	s.runnersMu.RUnlock()
	s.broker.Publish(OpTopic(op.ID), ServerTopic(op.ServerID))

	logf := func(stream, line string) { s.controlPlaneLog(ctx, op, stream, line) }
	var (
		result any
		err    error
	)
	if run == nil {
		err = fmt.Errorf("no runner for %s on this control plane", op.Kind)
	} else {
		rctx, cancel := context.WithTimeout(ctx, time.Duration(op.TimeoutSeconds)*time.Second)
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("the operation panicked: %v", r)
				}
			}()
			result, err = run(rctx, op, logf)
		}()
		cancel()
	}
	s.finishControlPlane(ctx, op, result, err)
}

func (s *Service) finishControlPlane(ctx context.Context, op db.Operation, result any, runErr error) {
	status, code := agentproto.StatusOK, 0
	var errText *string
	if runErr != nil {
		status, code = agentproto.StatusFailed, 1
		errText = ptr(runErr.Error())
	}
	var res *string
	if result != nil {
		if b, err := json.Marshal(result); err == nil {
			res = ptr(string(b))
		}
	}
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		_, dek, err := s.systemDEK(ctx, q, op.ServerID)
		if err != nil {
			return err
		}
		msg := ""
		if errText != nil {
			msg = *errText
		}
		if err := appendLog(ctx, q, dek, op, LogLine{Kind: agentproto.EventStatus, Status: status, ExitCode: &code,
			Error: msg, Line: statusLine(status, &code, msg)}); err != nil {
			return err
		}
		_, err = q.FinishOperation(ctx, db.FinishOperationParams{Status: status, Result: res,
			ExitCode: ptr(int64(code)), Error: errText, ID: op.ID, ServerID: op.ServerID})
		return err
	})
	if err != nil {
		s.log.Error("could not finish a control-plane operation", "op", op.ID, "err", err)
	}
	s.broker.Publish(OpTopic(op.ID), ServerTopic(op.ServerID))
}
