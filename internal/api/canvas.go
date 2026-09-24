package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
A site's overview canvas: where its boxes sit (app_canvas), and which data services its connection
variables point at, so the canvas draws those edges from what is true rather than from variable
names alone.

Read: any member. Save: whoever may manage the app (manage_apps) — a viewer can still pan, zoom and
drag on their own screen; nothing is stored. Git and imported apps alike. Not audited: it is where
boxes sit, saved on every drag. A team the caller is not in: 404.
*/

// MaxCanvasBytes caps a stored layout (the table's check is the backstop).
const MaxCanvasBytes = 64 << 10

const (
	maxCanvasNodes   = 500
	maxCanvasKey     = 300
	maxCanvasCoord   = 100_000
	maxCanvasViewPan = 1_000_000
)

// CanvasPoint is where one box sits, in canvas units.
type CanvasPoint struct {
	X float64 `json:"x" minimum:"-100000" maximum:"100000"`
	Y float64 `json:"y" minimum:"-100000" maximum:"100000"`
}

// CanvasViewport is the pan and zoom the canvas was left at.
type CanvasViewport struct {
	X    float64 `json:"x" minimum:"-1000000" maximum:"1000000"`
	Y    float64 `json:"y" minimum:"-1000000" maximum:"1000000"`
	Zoom float64 `json:"zoom" minimum:"0.05" maximum:"4"`
}

// CanvasLayout is app_canvas.layout.
type CanvasLayout struct {
	Nodes    map[string]CanvasPoint `json:"nodes" doc:"Box positions by stable node key (\"app\", \"domain:<hostname>\", \"db:<service id>\", …). At most 500 keys of at most 300 characters."`
	Viewport *CanvasViewport        `json:"viewport,omitempty" doc:"The pan and zoom; absent = fit the boxes to the view."`
}

// CanvasOut is GET …/canvas.
type CanvasOut struct {
	Layout      CanvasLayout `json:"layout"`
	UpdatedAt   *time.Time   `json:"updated_at" doc:"null: never saved — the web app lays the boxes out itself."`
	CanSave     bool         `json:"can_save" doc:"Whether the caller may save a layout (manage_apps)."`
	Connections []dbsvc.Link `json:"connections" doc:"Data services on the app's server that one of its connection variables points at."`
}

// CanvasSavedOut is PUT …/canvas.
type CanvasSavedOut struct {
	Layout    CanvasLayout `json:"layout"`
	UpdatedAt time.Time    `json:"updated_at"`
}

func (d *deps) registerCanvas(api huma.API) {
	tags := []string{"Apps"}
	path := "/api/teams/{team}/apps/{app}/canvas"

	type getOut struct{ Body CanvasOut }
	huma.Register(api, huma.Operation{OperationID: "get-app-canvas", Method: http.MethodGet, Path: path, Tags: tags,
		Summary: "The site's overview canvas: where its boxes were left (empty until saved once) and which data services on its server " +
			"its connection variables (DATABASE_URL, REDIS_URL, S3_URL) point at — compared by host and port on the server; no value is " +
			"returned. can_save says whether the caller may store a layout. Any member.",
	}, func(ctx context.Context, in *AppPath) (*getOut, error) {
		out := &getOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body = CanvasOut{Layout: CanvasLayout{Nodes: map[string]CanvasPoint{}}, CanSave: tenancy.Can(ctx, m, tenancy.AbilityManageApps), Connections: []dbsvc.Link{}}
			row, err := q.GetAppCanvas(ctx, db.GetAppCanvasParams{AppID: a.ID, TeamID: m.TeamID})
			switch {
			case err == nil:
				if json.Unmarshal([]byte(row.Layout), &out.Body.Layout) != nil || out.Body.Layout.Nodes == nil {
					out.Body.Layout = CanvasLayout{Nodes: map[string]CanvasPoint{}}
				}
				out.Body.UpdatedAt = &row.UpdatedAt
			case !errors.Is(err, sql.ErrNoRows):
				return huma.Error500InternalServerError("could not read the canvas")
			}
			if s := d.dbs(); s != nil {
				links, err := s.Links(ctx, q, m.TeamID, a)
				if err != nil {
					return huma.Error500InternalServerError("could not read the app's connections")
				}
				out.Body.Connections = links
			}
			return nil
		})
		return out, err
	})

	type putOut struct{ Body CanvasSavedOut }
	huma.Register(api, huma.Operation{OperationID: "put-app-canvas", Method: http.MethodPut, Path: path, Tags: tags,
		Summary: "Save where the site's overview canvas boxes sit, and its pan and zoom (replaces the whole layout; at most 64 KiB). " +
			"Owner or admin (manage_apps). Not audited.",
		MaxBodyBytes: MaxCanvasBytes + 1024,
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Layout CanvasLayout `json:"layout"`
		}
	}) (*putOut, error) {
		out := &putOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if err := need(ctx, m, tenancy.AbilityManageApps); err != nil {
				return err
			}
			l := in.Body.Layout
			if err := checkCanvas(l); err != nil {
				return err
			}
			if l.Nodes == nil {
				l.Nodes = map[string]CanvasPoint{}
			}
			raw, err := json.Marshal(l)
			if err != nil {
				return huma.Error422UnprocessableEntity("the layout could not be encoded")
			}
			if len(raw) > MaxCanvasBytes {
				return huma.Error422UnprocessableEntity("the layout is larger than 64 KiB")
			}
			row, err := q.UpsertAppCanvas(ctx, db.UpsertAppCanvasParams{AppID: a.ID, TeamID: m.TeamID, Layout: string(raw)})
			if err != nil {
				return huma.Error500InternalServerError("could not save the canvas")
			}
			out.Body = CanvasSavedOut{Layout: l, UpdatedAt: row.UpdatedAt}
			return nil
		})
		return out, err
	})
}

// checkCanvas refuses what the schema tags cannot say: how many keys, what a key may be.
func checkCanvas(l CanvasLayout) error {
	if len(l.Nodes) > maxCanvasNodes {
		return huma.Error422UnprocessableEntity("a canvas holds at most 500 boxes")
	}
	for k, p := range l.Nodes {
		if k == "" || len(k) > maxCanvasKey || !utf8.ValidString(k) || strings.ContainsFunc(k, unicode.IsControl) {
			return huma.Error422UnprocessableEntity("node keys are 1 to 300 characters of text")
		}
		// The schema checks these too; a map's values are checked here as well, in case it does not.
		if p.X < -maxCanvasCoord || p.X > maxCanvasCoord || p.Y < -maxCanvasCoord || p.Y > maxCanvasCoord {
			return huma.Error422UnprocessableEntity("box positions are within ±100000")
		}
	}
	if v := l.Viewport; v != nil {
		if v.Zoom < 0.05 || v.Zoom > 4 || v.X < -maxCanvasViewPan || v.X > maxCanvasViewPan || v.Y < -maxCanvasViewPan || v.Y > maxCanvasViewPan {
			return huma.Error422UnprocessableEntity("the viewport is out of range")
		}
	}
	return nil
}
