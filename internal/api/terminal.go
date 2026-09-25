package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// A web terminal for a server (and, cheaply, for an app's web container): GET
// /api/teams/{team}/servers/{server}/terminal/ws upgrades to a websocket and relays term_data /
// term_resize / term_close frames (internal/agentproto) between the browser and the server's
// agent, through internal/ops's in-memory session registry. Never through Huma: a websocket
// upgrade is not a JSON request/response, so this is a raw route, mounted directly on the
// kernel's router (see MountTerminal) — it still inherits the normal session-cookie
// authentication and the "member" authorization tier from internal/server/authorization.go
// (the whole /api/teams prefix), then does its own team-membership + ability check below, the
// same as every handler in this package.
//
// No terminal output is ever written to the database — only two audit entries, at open and at
// close (who, server, target, duration).

// MountTerminal adds the terminal websocket route to r (called from internal/server once, next to
// api.Register).
func MountTerminal(r chi.Router, a *app.App, svc *ops.Service) {
	d := &deps{a: a, ops: svc}
	r.Get("/api/teams/{team}/servers/{server}/terminal/ws", d.handleTerminalWS)
}

func (d *deps) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := current(ctx)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	team := chi.URLParam(r, "team")
	serverRef := chi.URLParam(r, "server")

	target := r.URL.Query().Get("target")
	if target == "" {
		target = agentproto.TerminalTargetHost
	}
	deploymentID := r.URL.Query().Get("deployment_id")
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	// Host: owner/admin only (AbilityManageServers, the same as connecting a server or writing a
	// file). An app container: deployer, admin or owner — the same as app.exec (AbilityDeploy).
	ability := tenancy.AbilityManageServers
	if target != agentproto.TerminalTargetHost {
		ability = tenancy.AbilityDeploy
	}

	var (
		srv    db.Server
		teamID string
	)
	err = d.inTeam(ctx, team, ability, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
		row, err := serverIn(ctx, q, m, serverRef)
		if err != nil {
			return err
		}
		srv, teamID = row, m.TeamID
		return nil
	})
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, errNotFound):
			status = http.StatusNotFound
		case errors.Is(err, errForbidden):
			status = http.StatusForbidden
		}
		http.Error(w, "not found", status)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled,
		OriginPatterns: terminalOrigins(d.ops.PublicURL())})
	if err != nil {
		return
	}
	defer c.CloseNow() //nolint:errcheck
	c.SetReadLimit(1 << 20)

	sess, err := d.ops.OpenTerminal(teamID, srv.ID, id.ID, target, deploymentID, cols, rows)
	if err != nil {
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = wsjson.Write(wctx, c, agentproto.Frame{Type: agentproto.TypeTermClose, TermReason: err.Error()})
		cancel()
		_ = c.Close(websocket.StatusPolicyViolation, "could not open a terminal session")
		return
	}
	opened := time.Now()
	d.auditTerminal(ctx, team, id.ID, srv.ID, "server.terminal_opened", map[string]any{"target": target})

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 2)

	go func() { // agent -> browser
		for {
			select {
			case <-sctx.Done():
				errc <- sctx.Err()
				return
			case f, ok := <-sess.Out:
				if !ok {
					errc <- nil
					return
				}
				wctx, wcancel := context.WithTimeout(sctx, 10*time.Second)
				werr := wsjson.Write(wctx, c, f)
				wcancel()
				if werr != nil {
					errc <- werr
					return
				}
				if f.Type == agentproto.TypeTermClose {
					errc <- nil
					return
				}
			}
		}
	}()
	go func() { // browser -> agent
		for {
			var f agentproto.Frame
			if err := wsjson.Read(sctx, c, &f); err != nil {
				errc <- err
				return
			}
			switch f.Type {
			case agentproto.TypeTermData:
				if !agentproto.ValidTermData(f.TermData) {
					continue
				}
				d.ops.SendTerminalInput(sess, agentproto.Frame{Type: agentproto.TypeTermData, TermData: f.TermData})
			case agentproto.TypeTermResize:
				if !agentproto.ValidTermSize(f.TermCols, f.TermRows) {
					continue
				}
				d.ops.SendTerminalInput(sess, agentproto.Frame{Type: agentproto.TypeTermResize, TermCols: f.TermCols, TermRows: f.TermRows})
			case agentproto.TypeTermClose:
				errc <- nil
				return
			}
		}
	}()
	<-errc
	cancel()
	d.ops.CloseTerminal(sess)
	d.auditTerminal(ctx, team, id.ID, srv.ID, "server.terminal_closed",
		map[string]any{"target": target, "duration_seconds": time.Since(opened).Round(time.Second).Seconds()})
	_ = c.Close(websocket.StatusNormalClosure, "")
}

// auditTerminal writes one audit entry in its own transaction (a terminal session outlives the
// request that opened it, and its close happens after the browser's socket has already ended).
// The ability is not re-checked: only that the caller is still a member of the team.
func (d *deps) auditTerminal(ctx context.Context, teamRef, userID, serverID, action string, changes map[string]any) {
	_ = d.inTeam(ctx, teamRef, "", func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
		return tenancy.Record(ctx, q, m.TeamID, userID, action, "server", serverID, changes)
	})
}

// terminalOrigins: the session cookie authenticates this socket, so a page on another site must not
// be able to open one (the library's default already refuses an Origin that differs from Host).
// Behind the web app's /api proxy Host is the API's own address, so the public app origin is
// allowed by name as well.
func terminalOrigins(publicURL string) []string {
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" {
		return nil
	}
	return []string{u.Host}
}
