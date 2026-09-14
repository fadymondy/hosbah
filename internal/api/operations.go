package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/cloudy/internal/agentproto"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/ops"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

// OperationOut is one queued/run operation.
type OperationOut struct {
	ID         string          `json:"id"`
	ServerID   string          `json:"server_id"`
	Kind       string          `json:"kind"`
	Executor   string          `json:"executor" enum:"agent,control_plane"`
	Status     string          `json:"status" enum:"pending,sent,running,ok,failed,timeout"`
	Params     json.RawMessage `json:"params"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      *string         `json:"error"`
	ExitCode   *int64          `json:"exit_code"`
	Attempts   int64           `json:"attempts"`
	LastSeq    int64           `json:"last_seq"`
	CreatedAt  time.Time       `json:"created_at"`
	SentAt     *time.Time      `json:"sent_at"`
	StartedAt  *time.Time      `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at"`
}

func toOperationOut(o db.Operation) OperationOut {
	out := OperationOut{ID: o.ID, ServerID: o.ServerID, Kind: o.Kind, Executor: o.Executor, Status: o.Status,
		Params: json.RawMessage(o.Params), Error: o.Error, ExitCode: o.ExitCode, Attempts: o.Attempts, LastSeq: o.LastSeq,
		CreatedAt: o.CreatedAt, SentAt: o.SentAt, StartedAt: o.StartedAt, FinishedAt: o.FinishedAt}
	if o.Result != nil && json.Valid([]byte(*o.Result)) {
		out.Result = json.RawMessage(*o.Result)
	}
	return out
}

// OpPath addresses one operation of one server.
type OpPath struct {
	ServerPath
	Op string `path:"op" maxLength:"64"`
}

func terminal(status string) bool { return status == "ok" || status == "failed" || status == "timeout" }

// abilityFor: read-only kinds for anyone who operates servers; changes need server management.
func abilityFor(kind string) string {
	switch kind {
	case agentproto.KindSystemInfo, agentproto.KindDockerCheck:
		return tenancy.AbilityOperate
	}
	return tenancy.AbilityManageServers
}

func (d *deps) registerOperations(api huma.API) {
	type opsOut struct {
		Body struct {
			Operations []OperationOut `json:"operations"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-server-operations", Method: http.MethodGet, Path: "/api/teams/{team}/servers/{server}/operations",
		Summary: "A server's operations, newest first", Tags: []string{"Operations"},
	}, func(ctx context.Context, in *struct {
		ServerPath
		Limit int `query:"limit" minimum:"1" maximum:"200" default:"50"`
	}) (*opsOut, error) {
		out := &opsOut{}
		out.Body.Operations = []OperationOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			rows, err := q.ListOperationsOfServer(ctx, db.ListOperationsOfServerParams{ServerID: s.ID, TeamID: m.TeamID, Limit: int32(in.Limit)})
			if err != nil {
				return huma.Error500InternalServerError("could not list operations")
			}
			for _, r := range rows {
				out.Body.Operations = append(out.Body.Operations, toOperationOut(r))
			}
			return nil
		})
		return out, err
	})

	type opCreatedOut struct {
		Status int
		Body   OperationOut
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-server-operation", Method: http.MethodPost, Path: "/api/teams/{team}/servers/{server}/operations",
		DefaultStatus: http.StatusCreated, Tags: []string{"Operations"},
		Summary: "Queue a typed operation on the server's agent. system.info and docker.check: owner, admin or deployer; " +
			"provision.step and agent.update: owner or admin. Idempotent by op_id (200 with the existing operation).",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			Kind   string `json:"kind" enum:"system.info,docker.check,provision.step,agent.update"`
			Step   string `json:"step,omitempty" enum:"packages.base,docker.install,swap.ensure" doc:"provision.step: which step."`
			SwapMB int    `json:"swap_mb,omitempty" minimum:"256" maximum:"16384" doc:"provision.step swap.ensure: size (default 1024)."`
			OpID   string `json:"op_id,omitempty" pattern:"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$" doc:"Optional idempotency key (a lowercase uuid)."`
		}
	}) (*opCreatedOut, error) {
		out := &opCreatedOut{Status: http.StatusCreated}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if !tenancy.Can(ctx, m, abilityFor(in.Body.Kind)) {
				return errForbidden
			}
			if s.Status != "enrolled" {
				return huma.Error409Conflict("this server's agent has not enrolled yet")
			}
			params := json.RawMessage(`{}`)
			switch in.Body.Kind {
			case agentproto.KindProvisionStep:
				if in.Body.Step == "" {
					return huma.Error422UnprocessableEntity("provision.step needs a step")
				}
				p := map[string]any{"step": in.Body.Step}
				if in.Body.Step == agentproto.StepSwap && in.Body.SwapMB > 0 {
					p["swap_mb"] = in.Body.SwapMB
				}
				params, _ = json.Marshal(p)
			case agentproto.KindAgentUpdate:
				params, _, err = d.ops.UpdateParams(deref(s.Arch))
				if err != nil {
					return huma.Error409Conflict(err.Error())
				}
			}
			op, created, err := d.ops.Enqueue(ctx, q, ops.EnqueueSpec{OpID: in.Body.OpID, TeamID: m.TeamID, ServerID: s.ID,
				Kind: in.Body.Kind, Params: params, CreatedBy: id.ID})
			switch {
			case errors.Is(err, ops.ErrOpIDTaken):
				return huma.Error409Conflict(err.Error())
			case err != nil:
				return huma.Error500InternalServerError("could not queue the operation")
			}
			out.Body = toOperationOut(op)
			if !created {
				out.Status = http.StatusOK
				return nil
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "operation.queued", "operation", op.ID,
				map[string]any{"server_id": s.ID, "server": s.Name, "kind": op.Kind, "params": json.RawMessage(op.Params)})
		})
		return out, err
	})

	type opDetailOut struct {
		Body struct {
			Operation OperationOut  `json:"operation"`
			Logs      []ops.LogLine `json:"logs"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-server-operation", Method: http.MethodGet, Path: "/api/teams/{team}/servers/{server}/operations/{op}",
		Summary: "One operation with its log so far", Tags: []string{"Operations"},
	}, func(ctx context.Context, in *OpPath) (*opDetailOut, error) {
		out := &opDetailOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			op, lines, err := d.opWithLogs(ctx, q, m, in.Server, in.Op, 0, 5000)
			if err != nil {
				return err
			}
			out.Body.Operation, out.Body.Logs = toOperationOut(op), lines
			return nil
		})
		return out, err
	})

	// Live log: Server-Sent Events. The membership and the operation are checked BEFORE a byte is
	// streamed (a stranger gets the usual 404), and again on every read, through the team-scoped
	// transaction — so a stream shows only what row-level security lets this team see, and a
	// member removed mid-stream stops receiving lines. Never togo's global /events.
	huma.Register(api, huma.Operation{
		OperationID: "stream-server-operation", Method: http.MethodGet, Path: "/api/teams/{team}/servers/{server}/operations/{op}/stream",
		Summary: "Live log of one operation (text/event-stream: events line, status, done)", Tags: []string{"Operations"},
	}, func(ctx context.Context, in *struct {
		OpPath
		LastEventID string `header:"Last-Event-ID" maxLength:"20"`
		After       int64  `query:"after" minimum:"0" doc:"Resume after this seq (Last-Event-ID wins when larger)."`
	}) (*huma.StreamResponse, error) {
		if err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			_, _, err := d.opWithLogs(ctx, q, m, in.Server, in.Op, 0, 0)
			return err
		}); err != nil {
			return nil, err
		}
		after := in.After
		if n, err := strconv.ParseInt(in.LastEventID, 10, 64); err == nil && n > after {
			after = n
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			sse := newSSE(hctx)
			wake, unsub := d.ops.Broker().Subscribe(ops.OpTopic(in.Op))
			defer unsub()
			lastStatus := ""
			for {
				var op db.Operation
				var lines []ops.LogLine
				err := d.inTeam(sse.ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
					var err error
					op, lines, err = d.opWithLogs(sse.ctx, q, m, in.Server, in.Op, after, 500)
					return err
				})
				if err != nil {
					sse.event("gone", "", map[string]string{"error": "not found"})
					return
				}
				for _, l := range lines {
					sse.event("line", strconv.FormatInt(l.Seq, 10), l)
					after = l.Seq
				}
				if op.Status != lastStatus {
					sse.event("status", "", toOperationOut(op))
					lastStatus = op.Status
				}
				if terminal(op.Status) && len(lines) < 500 {
					sse.event("done", "", map[string]string{"status": op.Status})
					return
				}
				if !sse.wait(wake, 3*time.Second) {
					return
				}
			}
		}}, nil
	})

	// The server page's live view: the server (status, online, metrics) and its latest operations,
	// re-sent whenever either changes (heartbeats included), checked like the operation stream.
	huma.Register(api, huma.Operation{
		OperationID: "stream-server", Method: http.MethodGet, Path: "/api/teams/{team}/servers/{server}/stream",
		Summary: "Live server status, metrics and operations (text/event-stream: events server, operations)", Tags: []string{"Servers"},
	}, func(ctx context.Context, in *ServerPath) (*huma.StreamResponse, error) {
		if err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			_, err := serverIn(ctx, q, m, in.Server)
			return err
		}); err != nil {
			return nil, err
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			sse := newSSE(hctx)
			wake, unsub := d.ops.Broker().Subscribe(ops.ServerTopic(in.Server))
			defer unsub()
			var lastServer, lastOps []byte
			for {
				var srv ServerOut
				opsList := []OperationOut{}
				err := d.inTeam(sse.ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
					s, err := serverIn(sse.ctx, q, m, in.Server)
					if err != nil {
						return err
					}
					srv = d.serverDetail(sse.ctx, q, m, s)
					rows, err := q.ListOperationsOfServer(sse.ctx, db.ListOperationsOfServerParams{ServerID: s.ID, TeamID: m.TeamID, Limit: 25})
					if err != nil {
						return err
					}
					for _, r := range rows {
						opsList = append(opsList, toOperationOut(r))
					}
					return nil
				})
				if err != nil {
					sse.event("gone", "", map[string]string{"error": "not found"})
					return
				}
				if b, _ := json.Marshal(srv); string(b) != string(lastServer) {
					sse.raw("server", "", b)
					lastServer = b
				}
				if b, _ := json.Marshal(opsList); string(b) != string(lastOps) {
					sse.raw("operations", "", b)
					lastOps = b
				}
				if !sse.wait(wake, 5*time.Second) {
					return
				}
			}
		}}, nil
	})
}

// opWithLogs loads an operation of the team's server and (limit > 0) its decrypted lines after a seq.
func (d *deps) opWithLogs(ctx context.Context, q *db.Queries, m *tenancy.Membership, serverID, opID string, after int64, limit int32) (db.Operation, []ops.LogLine, error) {
	op, err := q.OperationInTeam(ctx, db.OperationInTeamParams{ID: opID, TeamID: m.TeamID})
	if err != nil || op.ServerID != serverID {
		return db.Operation{}, nil, errNotFound
	}
	lines := []ops.LogLine{}
	if limit <= 0 {
		return op, lines, nil
	}
	rows, err := q.OperationLogsAfter(ctx, db.OperationLogsAfterParams{OperationID: op.ID, TeamID: m.TeamID, Seq: after, Limit: limit})
	if err != nil {
		return op, nil, huma.Error500InternalServerError("could not read the log")
	}
	if len(rows) == 0 {
		return op, lines, nil
	}
	dek, err := d.ops.TeamDEK(ctx, q, m.TeamID)
	if err != nil {
		return op, nil, huma.Error500InternalServerError("team key unavailable")
	}
	for _, r := range rows {
		l, err := ops.OpenLog(dek, m.TeamID, r)
		if err != nil {
			l = ops.LogLine{Seq: r.Seq, Kind: "log", Stream: "error", Line: "(this line could not be decrypted)", At: r.CreatedAt}
		}
		lines = append(lines, l)
	}
	return op, lines, nil
}

// ---- SSE ---------------------------------------------------------------------------------------

type sseWriter struct {
	ctx   context.Context
	w     http.ResponseWriter
	rc    *http.ResponseController
	keep  *time.Ticker
	fails bool
}

func newSSE(hctx huma.Context) *sseWriter {
	hctx.SetHeader("Content-Type", "text/event-stream")
	// no-transform: a compressing proxy (Next's dev server proxying /api, the `compression`
	// middleware, some CDNs) otherwise gzips — and therefore buffers — the whole stream.
	hctx.SetHeader("Cache-Control", "no-cache, no-store, no-transform")
	hctx.SetHeader("X-Accel-Buffering", "no") // no proxy buffering (nginx/NPM)
	w, _ := hctx.BodyWriter().(http.ResponseWriter)
	s := &sseWriter{ctx: hctx.Context(), w: w, rc: http.NewResponseController(w), keep: time.NewTicker(15 * time.Second)}
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	s.flush()
	return s
}

func (s *sseWriter) flush() {
	if err := s.rc.Flush(); err != nil {
		s.fails = true
	}
}

func (s *sseWriter) event(name, id string, v any) {
	b, _ := json.Marshal(v)
	s.raw(name, id, b)
}

func (s *sseWriter) raw(name, id string, data []byte) {
	if id != "" {
		_, _ = fmt.Fprintf(s.w, "id: %s\n", id)
	}
	_, _ = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data)
	s.flush()
}

// wait blocks until a wake-up, the poll interval, or the client leaving (false). It sends a
// keep-alive comment every 15 s so proxies (Cloudflare, NPM) do not close an idle stream.
func (s *sseWriter) wait(wake <-chan struct{}, poll time.Duration) bool {
	t := time.NewTimer(poll)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return false
		case <-wake:
			return true
		case <-t.C:
			return true
		case <-s.keep.C:
			_, _ = fmt.Fprint(s.w, ": keep-alive\n\n")
			s.flush()
		}
	}
}
