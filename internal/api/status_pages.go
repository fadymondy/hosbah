package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/monitors"
	"github.com/fadymondy/hosbah/internal/pgtext"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Team status pages (MH-575 H3): a published, read-only page (GET /api/public/status/{slug} —
internal/api/public.go — and the web app's /status/{slug}) showing a chosen set of the team's
monitors. Slugs are unique across every team, like app and proxy-host hostnames. Owner or admin
manage a team's status pages; any member reads.
*/

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// StatusPagePath addresses one status page of a team (id or slug).
type StatusPagePath struct {
	TeamPath
	StatusPage string `path:"status_page" maxLength:"64" doc:"Status page id or slug."`
}

// StatusPageOut is a status page as the API shows it.
type StatusPageOut struct {
	ID         string    `json:"id"`
	Slug       string    `json:"slug"`
	Title      string    `json:"title"`
	MonitorIDs []string  `json:"monitor_ids"`
	Published  bool      `json:"published"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func statusPageView(p db.StatusPage) StatusPageOut {
	return StatusPageOut{ID: p.ID, Slug: p.Slug, Title: p.Title, MonitorIDs: append([]string{}, p.MonitorIds...),
		Published: p.Published, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

// StatusPageCreateIn is a new status page.
type StatusPageCreateIn struct {
	Slug       string   `json:"slug" minLength:"1" maxLength:"63" doc:"Lowercase letters, digits and hyphens; unique across every team."`
	Title      string   `json:"title" minLength:"1" maxLength:"120"`
	MonitorIDs []string `json:"monitor_ids,omitempty" maxItems:"50"`
	Published  *bool    `json:"published,omitempty" doc:"Default false."`
}

// StatusPagePatchIn changes a status page. Omit a field to leave it. The slug never changes.
type StatusPagePatchIn struct {
	Title      *string  `json:"title,omitempty" minLength:"1" maxLength:"120"`
	MonitorIDs []string `json:"monitor_ids,omitempty" maxItems:"50"`
	Published  *bool    `json:"published,omitempty"`
}

func validateSlug(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !slugRe.MatchString(s) {
		return "", huma.Error422UnprocessableEntity("slug must be lowercase letters, digits and hyphens, 1 to 63 characters, not starting or ending with a hyphen")
	}
	return s, nil
}

func validateTitle(s string) (string, error) {
	s = strings.TrimSpace(s)
	if n := utf8.RuneCountInString(s); n < 1 || n > 120 {
		return "", huma.Error422UnprocessableEntity("title must be 1 to 120 characters")
	}
	return s, nil
}

func monitorIDsExist(ctx context.Context, q *db.Queries, teamID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.MonitorsByIDsInTeam(ctx, db.MonitorsByIDsInTeamParams{TeamID: teamID, Ids: strings.Join(ids, ",")})
	if err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return huma.Error422UnprocessableEntity("monitor_ids: one or more of those monitors do not exist")
	}
	return nil
}

func statusPageByRef(ctx context.Context, q *db.Queries, teamID, ref string) (db.StatusPage, error) {
	p, err := q.StatusPageInTeam(ctx, db.StatusPageInTeamParams{TeamID: teamID, Ref: strings.TrimSpace(ref)})
	if errors.Is(err, sql.ErrNoRows) {
		return db.StatusPage{}, errNotFound
	}
	return p, err
}

func (d *deps) registerStatusPages(api huma.API) {
	tags := []string{"Status pages"}
	base := "/api/teams/{team}/status-pages"

	type statusPagesBody struct {
		StatusPages []StatusPageOut `json:"status_pages"`
	}
	type listOut struct{ Body statusPagesBody }
	huma.Register(api, huma.Operation{OperationID: "list-status-pages", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The team's status pages. Any member.",
	}, func(ctx context.Context, in *TeamPath) (*listOut, error) {
		out := &listOut{Body: statusPagesBody{StatusPages: []StatusPageOut{}}}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListStatusPagesOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list the status pages")
			}
			for _, p := range rows {
				out.Body.StatusPages = append(out.Body.StatusPages, statusPageView(p))
			}
			return nil
		})
		return out, err
	})

	type pageOut struct{ Body StatusPageOut }
	huma.Register(api, huma.Operation{OperationID: "create-status-page", Method: http.MethodPost, Path: base,
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a status page. 409: the slug is already taken by any team. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body StatusPageCreateIn
	}) (*pageOut, error) {
		out := &pageOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			slug, err := validateSlug(in.Body.Slug)
			if err != nil {
				return err
			}
			title, err := validateTitle(in.Body.Title)
			if err != nil {
				return err
			}
			if err := monitorIDsExist(ctx, q, m.TeamID, in.Body.MonitorIDs); err != nil {
				return err
			}
			if n, err := q.StatusPageSlugTaken(ctx, slug); err != nil {
				return huma.Error500InternalServerError("could not check the slug")
			} else if n > 0 {
				return huma.Error409Conflict("that slug is already taken")
			}
			published := false
			if in.Body.Published != nil {
				published = *in.Body.Published
			}
			p, err := q.InsertStatusPage(ctx, db.InsertStatusPageParams{TeamID: m.TeamID, Slug: slug, Title: title,
				MonitorIds: pgtext.Array(in.Body.MonitorIDs), Published: published})
			if err != nil {
				return huma.Error500InternalServerError("could not create the status page")
			}
			out.Body = statusPageView(p)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "status_page.created", "team", m.TeamID,
				map[string]any{"status_page": p.ID, "slug": p.Slug})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-status-page", Method: http.MethodPatch, Path: base + "/{status_page}", Tags: tags,
		Summary: "Change a status page's title, monitors or publication state. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		StatusPagePath
		Body StatusPagePatchIn
	}) (*pageOut, error) {
		out := &pageOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := statusPageByRef(ctx, q, m.TeamID, in.StatusPage)
			if err != nil {
				return err
			}
			title := p.Title
			if in.Body.Title != nil {
				if title, err = validateTitle(*in.Body.Title); err != nil {
					return err
				}
			}
			ids := p.MonitorIds
			if in.Body.MonitorIDs != nil {
				if err := monitorIDsExist(ctx, q, m.TeamID, in.Body.MonitorIDs); err != nil {
					return err
				}
				ids = in.Body.MonitorIDs
			}
			published := p.Published
			if in.Body.Published != nil {
				published = *in.Body.Published
			}
			up, err := q.UpdateStatusPageInTeam(ctx, db.UpdateStatusPageInTeamParams{ID: p.ID, TeamID: m.TeamID, Title: title,
				MonitorIds: pgtext.Array(ids), Published: published})
			if errors.Is(err, sql.ErrNoRows) {
				return errNotFound
			}
			if err != nil {
				return huma.Error500InternalServerError("could not update the status page")
			}
			out.Body = statusPageView(up)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "status_page.updated", "team", m.TeamID,
				map[string]any{"status_page": p.ID, "published": up.Published})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-status-page", Method: http.MethodDelete, Path: base + "/{status_page}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Remove a status page. Owner or admin. Audited.",
	}, func(ctx context.Context, in *StatusPagePath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageTeam, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			p, err := statusPageByRef(ctx, q, m.TeamID, in.StatusPage)
			if err != nil {
				return err
			}
			n, err := q.DeleteStatusPageInTeam(ctx, db.DeleteStatusPageInTeamParams{ID: p.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not delete the status page")
			}
			if n == 0 {
				return errNotFound
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "status_page.deleted", "team", m.TeamID,
				map[string]any{"status_page": p.ID, "slug": p.Slug})
		})
		return nil, err
	})

	type uptimeOut struct{ Body monitors.Uptime }
	huma.Register(api, huma.Operation{OperationID: "monitor-uptime", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/monitors/{monitor}/uptime", Tags: []string{"Alerts & monitors"},
		Summary: "A monitor's uptime over the last 24h, 7d and 30d (percent). Any member.",
	}, func(ctx context.Context, in *monitorPathForUptime) (*uptimeOut, error) {
		out := &uptimeOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			mon, err := monitors.Find(ctx, q, m.TeamID, a, in.Monitor)
			if err != nil {
				return errNotFound
			}
			now := time.Now()
			if svc, ok := d.a.Monitors.(*monitors.Service); ok {
				now = svc.Now()
			}
			out.Body, err = monitors.MonitorUptime(ctx, q, m.TeamID, mon.ID, now)
			return actionErr(err, "could not compute uptime")
		})
		return out, err
	})
}

// monitorPathForUptime avoids importing MonitorPath's other fields; it is the same shape.
type monitorPathForUptime = MonitorPath
