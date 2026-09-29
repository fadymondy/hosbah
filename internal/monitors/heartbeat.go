package monitors

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

// MountPublic adds the heartbeat ping route, outside /api, authenticated by its own token (only
// its hash is stored) exactly like internal/deploy's deploy hook:
//
//	GET|POST /hooks/heartbeat/{token}
func (s *Service) MountPublic(r chi.Router) {
	r.Get("/hooks/heartbeat/{token}", s.handleHeartbeatPing)
	r.Post("/hooks/heartbeat/{token}", s.handleHeartbeatPing)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handleHeartbeatPing records one check-in. A ping always answers 200 whatever the monitor's
// previous state was (there is nothing for a legitimate caller to retry); an unknown or malformed
// token answers 404, indistinguishable from "no such monitor".
func (s *Service) handleHeartbeatPing(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if !tokens.HasPrefix(token, tokens.Ping) || len(token) > 100 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	hash := tokens.Hash(token)
	now := s.Now()
	ctx := r.Context()

	status := http.StatusNotFound
	out := any(map[string]string{"error": "not found"})
	var ev *Event

	err := tenancy.Tx(ctx, s.db, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		m, err := q.MonitorByPingToken(ctx, &hash)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := tenancy.SetTeam(ctx, tx, m.TeamID); err != nil {
			return err
		}
		cur, err := q.LockMonitor(ctx, db.LockMonitorParams{ID: m.ID, TeamID: m.TeamID})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !cur.Enabled {
			status, out = http.StatusOK, map[string]any{"ok": true, "enabled": false}
			return nil
		}
		open, err := q.OpenIncidentOfMonitor(ctx, db.OpenIncidentOfMonitorParams{MonitorID: cur.ID, TeamID: cur.TeamID})
		hasOpen := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		up, err := q.RecordHeartbeatPing(ctx, db.RecordHeartbeatPingParams{ID: cur.ID, TeamID: cur.TeamID, Now: now})
		if err != nil {
			return err
		}
		if hasOpen {
			inc, err := q.ResolveMonitorIncident(ctx, db.ResolveMonitorIncidentParams{ID: open.ID, TeamID: cur.TeamID, ResolvedAt: now, LastValue: "ping received"})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				base := Event{Event: "resolved", Monitor: MonitorRef{ID: up.ID, Name: up.Name, Kind: up.Kind}, App: EventApp{ID: up.AppID},
					Reason: inc.Reason, Value: "ping received", At: now.UTC(), teamID: cur.TeamID, incidentID: inc.ID,
					webhook: up.NotifyWebhook, emails: up.NotifyEmails, channelIDs: up.NotifyChannelIds}
				if a, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: up.AppID, TeamID: cur.TeamID}); err == nil {
					base.App.Name = a.Name
				}
				ev = &base
			}
		}
		status, out = http.StatusOK, map[string]any{"ok": true, "state": up.State}
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not record the check-in"})
		return
	}
	// The team's own notification feed: notifications allows only a system INSERT, never a
	// ping-token-scoped one (see 20260914145012's notifications_system_insert), so this runs as its
	// own transaction, System AND team — same as Service.apply's — after the ping-token transaction
	// above that found and updated the monitor by its token.
	if ev != nil {
		body := ev.text()
		if nerr := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true, TeamID: ev.teamID}, func(q *db.Queries, _ *sql.Tx) error {
			return q.InsertNotification(ctx, db.InsertNotificationParams{TeamID: ev.teamID, Kind: "monitor.resolved", Title: ev.title(),
				Body: &body, SubjectType: ptr("app"), SubjectID: &ev.App.ID})
		}); nerr != nil {
			s.log.Warn("heartbeat ping: could not write the team notification", "monitor", ev.Monitor.ID, "err", nerr)
		} else {
			s.publishTeam(ev.teamID)
		}
		s.announce(ctx, *ev)
	}
	writeJSON(w, status, out)
}

// sweepHeartbeats opens an incident for every heartbeat monitor that has gone
// heartbeat_interval_seconds + heartbeat_grace_seconds without a ping. Called once per Tick,
// system scope for the claim (ListDueHeartbeats), system+team for applying each result — the same
// split run() uses for http | cpu | memory | disk monitors.
func (s *Service) sweepHeartbeats(ctx context.Context) {
	now := s.Now()
	var due []db.Monitor
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var e error
		due, e = q.ListDueHeartbeats(ctx, db.ListDueHeartbeatsParams{Now: now, Lim: claimBatch})
		return e
	})
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("heartbeat sweep: could not list due heartbeats", "err", err)
		}
		return
	}
	for _, m := range due {
		var tg target
		err := tenancy.Tx(ctx, s.db, tenancy.Scope{System: true, TeamID: m.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
			a, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: m.AppID, TeamID: m.TeamID})
			tg.App = a
			return err
		})
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("heartbeat sweep: could not read the app", "monitor", m.ID, "err", err)
			}
			continue
		}
		res := result{State: StateFailing, Reason: fmt.Sprintf("no ping arrived within %d s of the expected one",
			m.HeartbeatIntervalSeconds+m.HeartbeatGraceSeconds)}
		_, ev, err := s.apply(ctx, m, tg, res)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("heartbeat sweep: could not apply", "monitor", m.ID, "err", err)
			}
			continue
		}
		if ev != nil {
			s.announce(ctx, *ev)
		}
	}
}

// ---- uptime -------------------------------------------------------------------------------------

// Uptime is a monitor's uptime over three windows, each 0..100.
type Uptime struct {
	Last24h float64 `json:"last_24h"`
	Last7d  float64 `json:"last_7d"`
	Last30d float64 `json:"last_30d"`
}

func uptimePercent(ctx context.Context, q *db.Queries, teamID, monitorID string, now time.Time, since time.Time) (float64, error) {
	failing, err := q.MonitorUptimeSince(ctx, db.MonitorUptimeSinceParams{MonitorID: monitorID, TeamID: teamID, Now: now, Since: since})
	if err != nil {
		return 0, err
	}
	window := now.Sub(since).Seconds()
	if window <= 0 {
		return 100, nil
	}
	pct := 100 * (1 - failing/window)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return math_Round1(pct), nil
}

// MonitorUptime computes a monitor's uptime over 24h, 7d and 30d. Team scope.
func MonitorUptime(ctx context.Context, q *db.Queries, teamID, monitorID string, now time.Time) (Uptime, error) {
	var out Uptime
	var err error
	if out.Last24h, err = uptimePercent(ctx, q, teamID, monitorID, now, now.Add(-24*time.Hour)); err != nil {
		return Uptime{}, err
	}
	if out.Last7d, err = uptimePercent(ctx, q, teamID, monitorID, now, now.Add(-7*24*time.Hour)); err != nil {
		return Uptime{}, err
	}
	if out.Last30d, err = uptimePercent(ctx, q, teamID, monitorID, now, now.Add(-30*24*time.Hour)); err != nil {
		return Uptime{}, err
	}
	return out, nil
}

func math_Round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}
