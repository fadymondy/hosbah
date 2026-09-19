package ops

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// LogLine is one stored operation event, as the API and the SSE stream return it.
type LogLine struct {
	Seq      int64     `json:"seq"`
	Kind     string    `json:"event"`            // log | progress | status
	Stream   string    `json:"stream,omitempty"` // stdout | stderr | info | error
	Line     string    `json:"line,omitempty"`
	Step     int       `json:"step,omitempty"`
	Total    int       `json:"total,omitempty"`
	Status   string    `json:"status,omitempty"`
	ExitCode *int      `json:"exit_code,omitempty"`
	Error    string    `json:"error,omitempty"`
	At       time.Time `json:"at"`
	// Op names the step a deployment-log line came from (app.build, route.set…, or "pipeline" for
	// the control plane's own lines). Empty in operation logs.
	Op string `json:"op,omitempty"`
}

// Deployment logs are sealed like operation logs, bound to the deployment.
func deployLogContext(deploymentID string) string { return "deploylog:" + deploymentID }

// SealDeployLog seals one deployment-log line.
func SealDeployLog(dek []byte, teamID, deploymentID string, l LogLine) (string, error) {
	b, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	return crypto.Seal(dek, teamID, deployLogContext(deploymentID), b)
}

// OpenDeployLog opens a stored deployment-log line.
func OpenDeployLog(dek []byte, teamID string, row db.DeploymentLog) (LogLine, error) {
	pt, err := crypto.Open(dek, teamID, deployLogContext(row.DeploymentID), row.LineSealed)
	if err != nil {
		return LogLine{}, err
	}
	var l LogLine
	if err := json.Unmarshal(pt, &l); err != nil {
		return LogLine{}, err
	}
	l.Seq = row.Seq
	return l, nil
}

// AppendDeployLog writes the next line of a deployment's log in the caller's transaction. The seq
// comes from the deployment row (UPDATE … RETURNING under its row lock), so an agent's events and
// the pipeline's own lines interleave in one order. opID/opSeq identify an agent's line, so its
// replay after a reconnect is refused by the unique index instead of landing twice.
func AppendDeployLog(ctx context.Context, q *db.Queries, dek []byte, teamID, serverID, deploymentID string, opID *string, opSeq *int64, l LogLine) error {
	row, err := q.NextDeploymentLogSeq(ctx, deploymentID)
	if err != nil {
		return err
	}
	l.Seq = row.LastSeq
	if l.At.IsZero() {
		l.At = time.Now().UTC()
	}
	sealed, err := SealDeployLog(dek, teamID, deploymentID, l)
	if err != nil {
		return err
	}
	return q.InsertDeploymentLog(ctx, db.InsertDeploymentLogParams{TeamID: teamID, ServerID: serverID, AppID: row.AppID,
		DeploymentID: deploymentID, OperationID: opID, OpSeq: opSeq, Seq: l.Seq, Stream: streamOf(l), LineSealed: sealed})
}

// Operation log lines are sealed with the team's DEK, bound to the team and to the operation, so a
// line copied onto another operation or team does not open.
func logContext(opID string) string { return "oplog:" + opID }

// SealLog seals one line for storage.
func SealLog(dek []byte, teamID, opID string, l LogLine) (string, error) {
	b, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	return crypto.Seal(dek, teamID, logContext(opID), b)
}

// OpenLog opens a stored line.
func OpenLog(dek []byte, teamID string, row db.OperationLog) (LogLine, error) {
	pt, err := crypto.Open(dek, teamID, logContext(row.OperationID), row.LineSealed)
	if err != nil {
		return LogLine{}, err
	}
	var l LogLine
	if err := json.Unmarshal(pt, &l); err != nil {
		return LogLine{}, err
	}
	l.Seq = row.Seq
	return l, nil
}

func lineFromEvent(ev agentproto.Event) LogLine {
	l := LogLine{Seq: ev.Seq, Kind: ev.Kind, Stream: ev.Stream, Line: ev.Line, Step: ev.Step, Total: ev.Total,
		Status: ev.Status, ExitCode: ev.ExitCode, Error: ev.Error, At: ev.At}
	if l.At.IsZero() {
		l.At = time.Now().UTC()
	}
	return l
}

// TeamDEK unwraps a team's data key inside the caller's (team-scoped) transaction.
func (s *Service) TeamDEK(ctx context.Context, q *db.Queries, teamID string) ([]byte, error) {
	t, err := q.GetTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	return s.a.Keys.UnwrapDEK(teamID, t.DekWrapped)
}

// appendLog writes the next line of an operation in the caller's transaction: seq is one past the
// highest stored. Used for lines the CONTROL PLANE writes (provisioning, timeouts, refusals); an
// agent's lines carry the seq the agent assigned.
func appendLog(ctx context.Context, q *db.Queries, dek []byte, op db.Operation, l LogLine) error {
	last, err := q.MaxOperationLogSeq(ctx, db.MaxOperationLogSeqParams{OperationID: op.ID, ServerID: op.ServerID})
	if err != nil {
		return err
	}
	l.Seq = last + 1
	if l.At.IsZero() {
		l.At = time.Now().UTC()
	}
	sealed, err := SealLog(dek, op.TeamID, op.ID, l)
	if err != nil {
		return err
	}
	if _, err := q.InsertOperationLog(ctx, db.InsertOperationLogParams{TeamID: op.TeamID, ServerID: op.ServerID,
		OperationID: op.ID, Seq: l.Seq, Stream: streamOf(l), LineSealed: sealed}); err != nil {
		return err
	}
	return q.AdvanceOperationSeq(ctx, db.AdvanceOperationSeqParams{Seq: l.Seq, ID: op.ID, ServerID: op.ServerID})
}

func streamOf(l LogLine) string {
	if l.Stream != "" {
		return l.Stream
	}
	return l.Kind
}

// systemDEK loads a server's team DEK in the system scope (workers that see every team).
func (s *Service) systemDEK(ctx context.Context, q *db.Queries, serverID string) (teamID string, dek []byte, err error) {
	k, err := q.ServerTeamKey(ctx, serverID)
	if err != nil {
		return "", nil, err
	}
	dek, err = s.a.Keys.UnwrapDEK(k.TeamID, k.DekWrapped)
	return k.TeamID, dek, err
}

// controlPlaneLog appends a line to a control-plane operation from a worker (system scope).
func (s *Service) controlPlaneLog(ctx context.Context, op db.Operation, stream, line string) {
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		_, dek, err := s.systemDEK(ctx, q, op.ServerID)
		if err != nil {
			return err
		}
		return appendLog(ctx, q, dek, op, LogLine{Kind: agentproto.EventLog, Stream: stream, Line: line})
	})
	if err != nil {
		s.log.Warn("could not store a control-plane log line", "op", op.ID, "err", err)
	}
	s.broker.Publish(OpTopic(op.ID), ServerTopic(op.ServerID))
}

func statusLine(status string, code *int, errText string) string {
	out := "finished: " + status
	if code != nil {
		out += fmt.Sprintf(" (exit %d)", *code)
	}
	if errText != "" {
		out += " — " + errText
	}
	return out
}
