package ops

import (
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// AppLoadPoints is how many readings the app load history keeps per app: an hour of heartbeats
// (one every ~15 s).
const AppLoadPoints = 240

// AppLoadPoint is one reading of an app's containers on its server.
type AppLoadPoint struct {
	At            time.Time `json:"at"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemUsedBytes  uint64    `json:"mem_used_bytes"`
	MemLimitBytes uint64    `json:"mem_limit_bytes"`
	Containers    int       `json:"containers"`
}

// appLoadHistory holds, per (server, app), the last hour of the agent's per-app container load.
// In memory on the instance holding the agent's connection, like the live server reading: it is
// for live charts, not records, and starts empty after a restart.
//
// It is keyed by the server the agent authenticated as, so an agent can only ever write load under
// its own server; readers look it up with the server id of an app they resolved in their team's
// scope, so a reading claimed for another team's app on another server is never shown.
type appLoadHistory struct {
	mu sync.Mutex
	by map[appLoadKey][]AppLoadPoint
}

type appLoadKey struct{ server, app string }

// recordAppLoads appends one heartbeat's per-app load. An app the beat no longer reports (stopped,
// or moved away) gets no point; its history ages out once the reader drops points older than an hour.
func (s *Service) recordAppLoads(serverID string, loads []agentproto.AppLoad) {
	if len(loads) == 0 {
		return
	}
	now := s.now()
	h := &s.apps
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.by == nil {
		h.by = map[appLoadKey][]AppLoadPoint{}
	}
	for _, l := range loads {
		k := appLoadKey{serverID, l.AppID}
		pts := append(h.by[k], AppLoadPoint{At: now, CPUPercent: l.CPUPercent, MemUsedBytes: l.MemUsedBytes, MemLimitBytes: l.MemLimitBytes, Containers: l.Containers})
		if len(pts) > AppLoadPoints {
			pts = append([]AppLoadPoint(nil), pts[len(pts)-AppLoadPoints:]...)
		}
		h.by[k] = pts
	}
	// Forget apps that have not reported for an hour, so the map does not grow forever.
	for k, pts := range h.by {
		if now.Sub(pts[len(pts)-1].At) > time.Hour {
			delete(h.by, k)
		}
	}
}

// AppLoad is the last hour of an app's container load, oldest first. Callers must have resolved
// the app, and so serverID, within the caller's team first.
func (s *Service) AppLoad(serverID, appID string) []AppLoadPoint {
	h := &s.apps
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := s.now().Add(-time.Hour)
	var out []AppLoadPoint
	for _, p := range h.by[appLoadKey{serverID, appID}] {
		if p.At.After(cutoff) {
			out = append(out, p)
		}
	}
	return out
}
