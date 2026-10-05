package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
MH-575 H3: the public status page. No session, no team header — a stranger's browser hits this
directly (the web app's own /status/{slug} calls it server-side). Everything here runs in pure
tenancy.Scope{System: true}, which only ever satisfies the explicit *_system_* RLS policies (see
db/atlas/migrations/20260925061853_..._row_level_security.sql), and every query is itself filtered
(PublicStatusPageBySlug: published only; PublicMonitorsByIDs / PublicOpenIncidentsByMonitorIDs:
no target, no notify_*, no ping token, no team_id) so an unpublished page or another team's data
can never reach the response, whatever the slug.

Mounted outside /api (see internal/server/authorization.go's isPublic) so it needs no entry in the
member/admin tiers, exactly like internal/deploy's own MountPublic routes.
*/

// PublicPathPrefix is where MountPublic adds its route; internal/server/authorization.go treats
// everything under it as public.
const PublicPathPrefix = "/api/public/status/"

// PublicMonitor is one monitor on a published status page — the safe subset of internal/monitors.
type PublicMonitor struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	State         string     `json:"state"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	// OpenIncident carries only when it opened: an incident's reason can name internal hosts or IPs.
	OpenIncident *struct {
		OpenedAt time.Time `json:"opened_at"`
	} `json:"open_incident,omitempty"`
}

// PublicStatusPageOut is what a stranger sees at /status/{slug}.
type PublicStatusPageOut struct {
	Title    string          `json:"title"`
	Slug     string          `json:"slug"`
	Monitors []PublicMonitor `json:"monitors"`
}

// MountPublic adds the public status-page route, outside /api's default-deny tiers:
//
//	GET /api/public/status/{slug}
//
// a and svc may be nil (OpenAPI export only), matching Register.
func MountPublic(r chi.Router, a *app.App, svc *ops.Service) {
	d := &deps{a: a, ops: svc}
	r.Get(PublicPathPrefix+"{slug}", d.handlePublicStatusPage)
	d.mountPanel(r)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// handlePublicStatusPage answers a published status page by its slug, or 404 — the same answer
// for "no such slug" and "that page exists but is not published", so publication state is never
// leaked either.
func (d *deps) handlePublicStatusPage(w http.ResponseWriter, r *http.Request) {
	slug := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "slug")))
	if slug == "" || len(slug) > 63 {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	ctx := r.Context()
	var out PublicStatusPageOut
	found := false
	err := tenancy.Tx(ctx, d.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		p, err := q.PublicStatusPageBySlug(ctx, slug)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out.Title, out.Slug = p.Title, p.Slug
		ids := []string(p.MonitorIds)
		if len(ids) == 0 {
			found = true
			return nil
		}
		rows, err := q.PublicMonitorsByIDs(ctx, db.PublicMonitorsByIDsParams{TeamID: p.TeamID, Ids: strings.Join(ids, ",")})
		if err != nil {
			return err
		}
		open, err := q.PublicOpenIncidentsByMonitorIDs(ctx, db.PublicOpenIncidentsByMonitorIDsParams{TeamID: p.TeamID, Ids: strings.Join(ids, ",")})
		if err != nil {
			return err
		}
		byMonitor := make(map[string]db.PublicOpenIncidentsByMonitorIDsRow, len(open))
		for _, inc := range open {
			byMonitor[inc.MonitorID] = inc
		}
		out.Monitors = make([]PublicMonitor, 0, len(rows))
		for _, m := range rows {
			pm := PublicMonitor{ID: m.ID, Name: m.Name, Kind: m.Kind, State: m.State, LastCheckedAt: m.LastCheckedAt}
			if inc, ok := byMonitor[m.ID]; ok {
				pm.OpenIncident = &struct {
					OpenedAt time.Time `json:"opened_at"`
				}{OpenedAt: inc.OpenedAt}
			}
			out.Monitors = append(out.Monitors, pm)
		}
		found = true
		return nil
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load the status page")
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}
