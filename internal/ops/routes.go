package ops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// routesFrame handles a "routes" frame (MH-1331): the agent re-pointed a route because a restart
// moved a container's published port, reports a route down, or (at the start of a session) says
// which port each app's route uses. The live release's host_port follows, so the next route.set
// (a domain added, SyncRoutes) does not send Caddy back to the old port.
//
// A report only moves a release whose web (or realtime) container is the one the agent names: an
// agent can only re-point an app to that app's own running container, never pick another port.
func (s *Service) routesFrame(ctx context.Context, ac *agentConn, f agentproto.Frame) {
	rs := f.Routes
	if len(rs) > agentproto.MaxRouteReports {
		rs = rs[:agentproto.MaxRouteReports]
	}
	for _, r := range rs {
		if r.Level == agentproto.RouteReportWarn {
			s.log.Warn("agent route report", "server", ac.serverID, "app", r.AppID, "proxy", r.ProxyID, "msg", clip(r.Message, 500))
		}
		if r.AppID == "" || !agentproto.ValidID(r.AppID) {
			continue
		}
		rr := r
		// Releases are written by the system scope only; the team id lets the activity entry in.
		err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: ac.teamID}, func(q *db.Queries, _ *sql.Tx) error {
			return applyRouteReport(ctx, q, ac.teamID, ac.serverID, rr)
		})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			s.log.Warn("route report not applied", "server", ac.serverID, "app", r.AppID, "err", err)
		}
	}
}

// applyRouteReport moves the live release's host ports to what the agent routes now, and records
// a move or a warning in the team's activity log.
func applyRouteReport(ctx context.Context, q *db.Queries, teamID, serverID string, r agentproto.RouteReport) error {
	app, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: r.AppID, TeamID: teamID})
	if err != nil {
		return err
	}
	if app.ServerID != serverID {
		return nil // another server's app: not this agent's to report on
	}
	changes := map[string]any{"server_id": serverID}
	if r.Level == agentproto.RouteReportWarn {
		changes["message"] = clip(r.Message, 500)
		if r.Previous != "" {
			changes["upstream"] = r.Previous
		}
		if err := tenancy.Record(ctx, q, teamID, "agent:"+serverID, "app.route.unreachable", "app", app.ID, changes); err != nil {
			return err
		}
	}
	rel, err := q.LiveReleaseOfApp(ctx, app.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	hostPort, containers, moved := movedRelease(rel, r)
	if len(moved) == 0 {
		return nil
	}
	if err := q.SetReleaseContainers(ctx, db.SetReleaseContainersParams{HostPort: hostPort, Containers: containers, ID: rel.ID}); err != nil {
		return err
	}
	if r.Previous == "" && r.RealtimePrevious == "" {
		return nil // a session snapshot catching the record up: nothing moved just now
	}
	changes["moved"] = moved
	if r.Level != agentproto.RouteReportWarn {
		changes["message"] = clip(r.Message, 500)
	}
	return tenancy.Record(ctx, q, teamID, "agent:"+serverID, "app.route.reconciled", "app", app.ID, changes)
}

// movedRelease is the release's host_port and containers record with the report applied, and
// what moved ("web"/"realtime" -> "old -> new"; empty when nothing does). A port is only taken
// from a report naming the container the release recorded for that role.
func movedRelease(rel db.Release, r agentproto.RouteReport) (*int64, *string, map[string]string) {
	moved := map[string]string{}
	hostPort, containers := rel.HostPort, rel.Containers
	if rel.Containers == nil {
		return hostPort, containers, moved
	}
	var rec map[string]json.RawMessage
	if json.Unmarshal([]byte(*rel.Containers), &rec) != nil {
		return hostPort, containers, moved
	}
	var web string
	_ = json.Unmarshal(rec["web"], &web)
	if p := upstreamPort(r.Upstream); p != 0 && web != "" && r.Container == web && (rel.HostPort == nil || *rel.HostPort != p) {
		old := "none"
		if rel.HostPort != nil {
			old = strconv.FormatInt(*rel.HostPort, 10)
		}
		moved["web"] = old + " -> " + strconv.FormatInt(p, 10)
		hostPort = &p
	}
	if raw, ok := rec["realtime"]; ok && r.RealtimeContainer != "" {
		var rt map[string]any
		if json.Unmarshal(raw, &rt) == nil && rt != nil {
			p := upstreamPort(r.RealtimeUpstream)
			name, _ := rt["container"].(string)
			old, _ := rt["host_port"].(float64)
			if p != 0 && name == r.RealtimeContainer && int64(old) != p {
				moved["realtime"] = fmt.Sprintf("%d -> %d", int64(old), p)
				rt["host_port"] = p
				if b, err := json.Marshal(rt); err == nil {
					rec["realtime"] = b
				}
			}
		}
	}
	if _, ok := moved["realtime"]; ok {
		if b, err := json.Marshal(rec); err == nil {
			s := string(b)
			containers = &s
		}
	}
	return hostPort, containers, moved
}

// upstreamPort is <port> of "127.0.0.1:<port>", or 0.
func upstreamPort(u string) int64 {
	rest, ok := strings.CutPrefix(u, "127.0.0.1:")
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(rest, 10, 32)
	if err != nil || n < 1 || n > 65535 {
		return 0
	}
	return n
}
