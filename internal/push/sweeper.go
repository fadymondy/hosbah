package push

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The server sweeper: the two alerts nothing else raised.

  - server.offline: an enrolled server whose agent was heartbeating has been silent for OfflineAfter
    (longer than ops.OnlineWindow, so an agent restart or a short network blip is not an alert).
  - resource.high: CPU, memory or disk at or above HighPercent on two sweeps in a row, at most
    once per Cooldown per server and resource.

State is kept in memory, so the first sweep after a restart only learns where every server stands
and alerts nothing: a deploy of Hosbah itself never pages anyone about servers that were already
down. Hosbah runs one control-plane process; a second one would alert twice.
*/

const (
	SweepEvery   = time.Minute
	OfflineAfter = 3 * time.Minute
	HighPercent  = 90.0
	// ClearPercent is where a resource counts as back to normal (hysteresis, no flapping).
	ClearPercent = 85.0
	Cooldown     = time.Hour
)

type serverState struct {
	online  bool
	over    map[string]int
	alerted map[string]time.Time
}

type sweeper struct {
	s     *Service
	mu    sync.Mutex
	state map[string]*serverState
	first bool
	stop  chan struct{}
	done  chan struct{}
}

// StartSweeper runs the server sweeper every SweepEvery until StopSweeper.
func (s *Service) StartSweeper() {
	if s == nil || s.sweeper != nil {
		return
	}
	sw := s.newSweeper()
	sw.stop, sw.done = make(chan struct{}), make(chan struct{})
	s.sweeper = sw
	go func() {
		defer close(sw.done)
		t := time.NewTicker(SweepEvery)
		defer t.Stop()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			sw.tick(ctx, time.Now())
			cancel()
			select {
			case <-sw.stop:
				return
			case <-t.C:
			}
		}
	}()
}

// StopSweeper stops the sweeper and waits for deliveries in flight.
func (s *Service) StopSweeper() {
	if s == nil {
		return
	}
	if s.sweeper != nil && s.sweeper.stop != nil {
		close(s.sweeper.stop)
		<-s.sweeper.done
		s.sweeper = nil
	}
	s.Wait()
}

func (s *Service) newSweeper() *sweeper {
	return &sweeper{s: s, state: map[string]*serverState{}, first: true}
}

type hostMetrics struct {
	CPUPercent     float64 `json:"cpu_percent"`
	MemUsedBytes   float64 `json:"mem_used_bytes"`
	MemTotalBytes  float64 `json:"mem_total_bytes"`
	DiskUsedBytes  float64 `json:"disk_used_bytes"`
	DiskTotalBytes float64 `json:"disk_total_bytes"`
}

// percents is the server's last reading per resource; a resource it did not report is absent.
func percents(raw *string) map[string]float64 {
	out := map[string]float64{}
	if raw == nil || *raw == "" {
		return out
	}
	var m hostMetrics
	if json.Unmarshal([]byte(*raw), &m) != nil {
		return out
	}
	out["cpu"] = m.CPUPercent
	if m.MemTotalBytes > 0 {
		out["memory"] = m.MemUsedBytes * 100 / m.MemTotalBytes
	}
	if m.DiskTotalBytes > 0 {
		out["disk"] = m.DiskUsedBytes * 100 / m.DiskTotalBytes
	}
	return out
}

var resourceAR = map[string]string{"cpu": "المعالج", "memory": "الذاكرة", "disk": "القرص"}
var resourceEN = map[string]string{"cpu": "CPU", "memory": "Memory", "disk": "Disk"}

func (sw *sweeper) tick(ctx context.Context, now time.Time) {
	var rows []db.AlertableServersRow
	err := tenancy.Tx(ctx, sw.s.db, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		rows, err = q.AlertableServers(ctx)
		return err
	})
	if err != nil {
		sw.s.log.Warn("push sweeper: could not read servers", "err", err)
		return
	}
	for _, ev := range sw.evaluate(rows, now) {
		sw.s.Record(ctx, ev)
		if ev.Kind == EventServerOffline {
			sw.s.Channels(ctx, ev)
		}
	}
}

// evaluate advances the per-server state to rows (read at now) and returns the alerts to raise.
func (sw *sweeper) evaluate(rows []db.AlertableServersRow, now time.Time) []Event {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	first := sw.first
	sw.first = false
	seen := map[string]bool{}
	var out []Event
	for _, r := range rows {
		seen[r.ID] = true
		st := sw.state[r.ID]
		if st == nil {
			st = &serverState{over: map[string]int{}, alerted: map[string]time.Time{}}
			sw.state[r.ID] = st
		}
		online := r.LastSeenAt != nil && now.Sub(*r.LastSeenAt) < OfflineAfter
		if st.online && !online && !first {
			out = append(out, Event{
				TeamID: r.TeamID, Kind: EventServerOffline, SubjectType: "server", SubjectID: r.ID, Link: "servers/" + r.ID,
				Title:   fmt.Sprintf("%s is offline", r.Name),
				Body:    fmt.Sprintf("Its agent has not checked in since %s.", r.LastSeenAt.UTC().Format("15:04 UTC")),
				TitleAR: fmt.Sprintf("الخادم %s غير متصل", r.Name),
				BodyAR:  fmt.Sprintf("لم يتواصل وكيله منذ %s.", r.LastSeenAt.UTC().Format("15:04 UTC")),
			})
		}
		st.online = online
		if !online {
			st.over = map[string]int{}
			continue
		}
		for res, pct := range percents(r.Metrics) {
			switch {
			case pct >= HighPercent:
				st.over[res]++
			case pct < ClearPercent:
				st.over[res] = 0
			}
			if first || st.over[res] < 2 || now.Sub(st.alerted[res]) < Cooldown {
				continue
			}
			st.alerted[res] = now
			p := math.Round(pct)
			out = append(out, Event{
				TeamID: r.TeamID, Kind: EventResourceHigh, SubjectType: "server", SubjectID: r.ID, Link: "servers/" + r.ID,
				Tag:     "resource:" + r.ID + ":" + res,
				Title:   fmt.Sprintf("%s: %s at %.0f%%", r.Name, resourceEN[res], p),
				Body:    fmt.Sprintf("%s has stayed above %.0f%% for the last two checks.", resourceEN[res], HighPercent),
				TitleAR: fmt.Sprintf("%s: %s عند %.0f٪", r.Name, resourceAR[res], p),
				BodyAR:  fmt.Sprintf("%s فوق %.0f٪ في آخر فحصين.", resourceAR[res], HighPercent),
			})
		}
	}
	for id := range sw.state {
		if !seen[id] {
			delete(sw.state, id) // deleted or unenrolled
		}
	}
	return out
}
