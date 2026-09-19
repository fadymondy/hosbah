package ops

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// handleWS is GET /agent/ws: challenge, verify, then heartbeats and events in, signed ops out.
func (s *Service) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer c.CloseNow() //nolint:errcheck
	c.SetReadLimit(4 << 20)
	ctx := r.Context()

	ac, err := s.authenticate(ctx, c, remoteIP(r))
	if err != nil {
		s.log.Warn("agent refused", "remote", remoteIP(r), "err", err)
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = wsjson.Write(wctx, c, agentproto.Frame{Type: agentproto.TypeError, Error: "authentication failed"})
		cancel()
		_ = c.Close(websocket.StatusPolicyViolation, "authentication failed")
		return
	}
	s.hub.add(ac)
	defer func() {
		s.hub.remove(ac)
		s.flushMetrics(ac)
		s.broker.Publish(ServerTopic(ac.serverID))
		s.log.Info("agent disconnected", "server", ac.serverID)
	}()
	s.log.Info("agent connected", "server", ac.serverID, "version", ac.version, "remote", ac.remoteIP)

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { // writer: the only goroutine that writes to the socket after the handshake
		for {
			select {
			case <-sctx.Done():
				return
			case f := <-ac.send:
				wctx, wcancel := context.WithTimeout(sctx, 10*time.Second)
				err := wsjson.Write(wctx, c, f)
				wcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Resume: every operation this server's agent was sent (or had started) and has not finished,
	// with the last seq stored for it. The agent replays what is missing or says "unknown".
	resume, err := s.resumeList(sctx, ac.serverID)
	if err != nil {
		s.log.Warn("resume list failed", "server", ac.serverID, "err", err)
	}
	ac.send <- agentproto.Frame{Type: agentproto.TypeResume, Resume: resume}
	s.broker.Publish(ServerTopic(ac.serverID))
	s.Wake()

	for {
		var f agentproto.Frame
		if err := wsjson.Read(sctx, c, &f); err != nil {
			return
		}
		switch f.Type {
		case agentproto.TypeHeartbeat:
			s.heartbeat(sctx, ac, f)
		case agentproto.TypeMetrics:
			s.liveMetricsFrame(sctx, ac, f)
		case agentproto.TypeEvent:
			if f.Event != nil {
				s.recordEvent(sctx, ac, *f.Event)
			}
		}
	}
}

// authenticate runs the challenge. The server row is found in the system scope (the agent is not
// yet anyone); only after the signature verifies against the key the agent enrolled with does the
// connection get its ServerID scope.
func (s *Service) authenticate(ctx context.Context, c *websocket.Conn, ip string) (*agentConn, error) {
	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	nb := make([]byte, 32)
	_, _ = rand.Read(nb)
	nonce := base64.RawURLEncoding.EncodeToString(nb)
	if err := wsjson.Write(hctx, c, agentproto.Frame{Type: agentproto.TypeChallenge, Nonce: nonce, Protocol: agentproto.ProtocolVersion}); err != nil {
		return nil, err
	}
	var auth agentproto.Frame
	if err := wsjson.Read(hctx, c, &auth); err != nil {
		return nil, err
	}
	if auth.Type != agentproto.TypeAuth || auth.ServerID == "" || len(auth.ServerID) > 64 {
		return nil, errors.New("no auth frame")
	}
	var key db.ServerTeamKeyRow
	var status string
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		k, err := q.ServerTeamKey(ctx, auth.ServerID)
		if err != nil {
			return err
		}
		srv, err := q.ServerByID(ctx, auth.ServerID)
		if err != nil {
			return err
		}
		key, status = k, srv.Status
		return nil
	})
	if err != nil || key.AgentPublicKey == nil {
		return nil, errors.New("unknown or un-enrolled server")
	}
	if !agentproto.VerifyAuth(*key.AgentPublicKey, nonce, auth.ServerID, auth.Signature) {
		return nil, errors.New("challenge signature does not verify")
	}
	if status != "enrolled" {
		return nil, errors.New("server is not enrolled")
	}
	dek, err := s.a.Keys.UnwrapDEK(key.TeamID, key.DekWrapped)
	if err != nil {
		return nil, err
	}
	ac := &agentConn{serverID: auth.ServerID, teamID: key.TeamID, dek: dek, remoteIP: ip, version: auth.AgentVersion,
		ws: c, send: make(chan agentproto.Frame, 256)}
	if err := wsjson.Write(hctx, c, agentproto.Frame{Type: agentproto.TypeWelcome, ServerID: auth.ServerID, Protocol: agentproto.ProtocolVersion}); err != nil {
		return nil, err
	}
	_ = s.agentTx(ctx, ac, func(q *db.Queries) error {
		return q.ServerSeen(ctx, db.ServerSeenParams{IpAddress: optional(ip), AgentVersion: optional(auth.AgentVersion), ID: ac.serverID})
	})
	return ac, nil
}

// agentTx runs fn scoped to the agent's own server.
func (s *Service) agentTx(ctx context.Context, ac *agentConn, fn func(q *db.Queries) error) error {
	return tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{ServerID: ac.serverID}, func(q *db.Queries, _ *sql.Tx) error { return fn(q) })
}

func (s *Service) resumeList(ctx context.Context, serverID string) ([]agentproto.ResumeEntry, error) {
	var out []agentproto.ResumeEntry
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{ServerID: serverID}, func(q *db.Queries, _ *sql.Tx) error {
		ops, err := q.InFlightOperationsForServer(ctx, serverID)
		if err != nil {
			return err
		}
		for _, op := range ops {
			last, err := q.MaxOperationLogSeq(ctx, db.MaxOperationLogSeqParams{OperationID: op.ID, ServerID: serverID})
			if err != nil {
				return err
			}
			out = append(out, agentproto.ResumeEntry{OpID: op.ID, LastSeq: last})
		}
		return nil
	})
	return out, err
}

func (s *Service) heartbeat(ctx context.Context, ac *agentConn, f agentproto.Frame) {
	if f.Metrics == nil {
		return
	}
	b, _ := json.Marshal(f.Metrics)
	err := s.agentTx(ctx, ac, func(q *db.Queries) error {
		if err := q.ServerHeartbeat(ctx, db.ServerHeartbeatParams{Metrics: string(b), ID: ac.serverID}); err != nil {
			return err
		}
		// A heartbeat carries a fresh Docker reading only on its slower cadence; merge it into
		// facts.docker so the server card stops showing enrolment's stale "not installed".
		if f.Metrics.Docker != nil {
			db2, _ := json.Marshal(f.Metrics.Docker)
			return q.RefreshServerDocker(ctx, db.RefreshServerDockerParams{Docker: string(db2), ID: ac.serverID})
		}
		return nil
	})
	if err != nil {
		s.log.Warn("heartbeat not stored", "server", ac.serverID, "err", err)
	}
	s.recordMetrics(ctx, ac, *f.Metrics)
	s.broker.Publish(ServerTopic(ac.serverID))
}

// recordEvent stores one agent event. Everything is filtered by the connection's server id AND
// runs in its ServerID scope: an agent naming another server's op_id finds nothing.
func (s *Service) recordEvent(ctx context.Context, ac *agentConn, ev agentproto.Event) {
	requeued := false
	deployment := ""
	var importFacts []byte // a completed system.info: sync the server's imported workloads after the commit
	err := s.agentTx(ctx, ac, func(q *db.Queries) error {
		op, err := q.OperationForServer(ctx, db.OperationForServerParams{ID: ev.OpID, ServerID: ac.serverID})
		if errors.Is(err, sql.ErrNoRows) {
			return nil // not this server's operation (or not an operation at all): ignore
		}
		if err != nil {
			return err
		}
		inFlight := op.Status == "pending" || op.Status == "sent" || op.Status == "running"

		switch {
		case ev.Kind == agentproto.EventStatus && ev.Status == agentproto.StatusUnknown:
			// The agent never got it (or restarted and forgot): back to the queue.
			q.ReturnOperationToQueue(ctx, db.ReturnOperationToQueueParams{ID: op.ID, ServerID: ac.serverID}) //nolint:errcheck
			requeued = true
			return nil
		case ev.Kind == agentproto.EventStatus && ev.Status == agentproto.StatusRejected:
			if !inFlight {
				return nil
			}
			msg := "the agent refused this operation: " + ev.Error
			if err := appendLog(ctx, q, ac.dek, op, LogLine{Kind: agentproto.EventStatus, Status: agentproto.StatusFailed,
				Stream: "error", Error: msg, Line: statusLine(agentproto.StatusFailed, nil, msg)}); err != nil {
				return err
			}
			_, err := q.FinishOperation(ctx, db.FinishOperationParams{Status: agentproto.StatusFailed, Error: &msg, ID: op.ID, ServerID: ac.serverID})
			return err
		case ev.Seq < 1:
			return nil
		}

		ev.Data = cleanEventData(ev.Data)
		l := lineFromEvent(ev)
		if ev.Kind == agentproto.EventStatus && ev.Status != agentproto.StatusRunning {
			l.Line = statusLine(ev.Status, ev.ExitCode, ev.Error)
		}
		sealed, err := SealLog(ac.dek, ac.teamID, op.ID, l)
		if err != nil {
			return err
		}
		n, err := q.InsertOperationLog(ctx, db.InsertOperationLogParams{TeamID: ac.teamID, ServerID: ac.serverID,
			OperationID: op.ID, Seq: ev.Seq, Stream: streamOf(l), LineSealed: sealed})
		if err != nil {
			return err
		}
		// A deployment's operation: the line joins the deployment's log too — only when it is new
		// here (n == 1), so a replay after a reconnect never duplicates it there either.
		if n == 1 && op.DeploymentID != nil {
			deployment = *op.DeploymentID
			dl := l
			dl.Op = op.Kind
			if err := AppendDeployLog(ctx, q, ac.dek, ac.teamID, ac.serverID, deployment, &op.ID, ptr(ev.Seq), dl); err != nil {
				return err
			}
		}
		if err := q.AdvanceOperationSeq(ctx, db.AdvanceOperationSeqParams{Seq: ev.Seq, ID: op.ID, ServerID: ac.serverID}); err != nil {
			return err
		}
		// github.* results (and a device login's one-time code) keep the server's GitHub state current.
		if n == 1 {
			if patch := githubPatch(op.Kind, op.ID, ev, time.Now()); patch != nil {
				if err := q.MergeServerGitHub(ctx, db.MergeServerGitHubParams{Patch: string(patch), ID: ac.serverID}); err != nil {
					return err
				}
			}
		}
		if ev.Kind != agentproto.EventStatus {
			return nil
		}
		switch ev.Status {
		case agentproto.StatusRunning:
			_, err = q.MarkOperationRunning(ctx, db.MarkOperationRunningParams{ID: op.ID, ServerID: ac.serverID})
		case agentproto.StatusOK, agentproto.StatusFailed:
			var res, errText *string
			if len(ev.Result) > 0 && json.Valid(ev.Result) {
				res = ptr(string(ev.Result))
			}
			if ev.Error != "" {
				errText = ptr(ev.Error)
			}
			var code *int64
			if ev.ExitCode != nil {
				code = ptr(int64(*ev.ExitCode))
			}
			_, err = q.FinishOperation(ctx, db.FinishOperationParams{Status: ev.Status, Result: res, ExitCode: code,
				Error: errText, ID: op.ID, ServerID: ac.serverID})
			// A fresh system.info refreshes what Hosbah shows for the server (its discovered services and
			// guests, which become its sites), not only the operation's log.
			if err == nil && op.Kind == agentproto.KindSystemInfo && ev.Status == agentproto.StatusOK && res != nil {
				var r struct {
					Facts json.RawMessage `json:"facts"`
				}
				if json.Unmarshal([]byte(*res), &r) == nil && len(r.Facts) > 2 {
					err = q.RefreshServerFacts(ctx, db.RefreshServerFactsParams{Facts: string(r.Facts), ID: ac.serverID})
					if err == nil {
						importFacts = r.Facts
					}
				}
			}
		}
		return err
	})
	if err != nil {
		s.log.Warn("agent event not stored", "server", ac.serverID, "op", ev.OpID, "seq", ev.Seq, "err", err)
	}
	if err == nil && importFacts != nil {
		// Discovered workloads become imported apps and data services (team scope; never deletes).
		if _, ierr := importer.SyncFacts(ctx, s.a.SQLDB, ac.teamID, ac.serverID, importFacts); ierr != nil {
			s.log.Warn("imported workloads not synced", "server", ac.serverID, "err", ierr)
		}
	}
	if requeued {
		s.Wake()
	}
	s.broker.Publish(OpTopic(ev.OpID), ServerTopic(ac.serverID))
	if deployment != "" {
		s.broker.Publish(DeployTopic(deployment))
	}
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
