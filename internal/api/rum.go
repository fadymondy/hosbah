package api

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/rum"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Real-user monitoring (MH-575 H5): a per-app toggle and rotatable, non-secret ingest key (settings),
and the Core Web Vitals the app's own visitors' browsers reported (summary, paths, timeseries).
Reading is any member; enabling, disabling and rotating the key are owner/admin (AbilityManageApps),
like every other app-configuration change.
*/

func (d *deps) rum() *rum.Service {
	if d.a == nil {
		return nil
	}
	s, _ := d.a.RUM.(*rum.Service)
	return s
}

// RumSettingsOut is the current toggle state. Key and Snippet are set only right after a call that
// (re)creates the key (enabling for the first time, or rotating) — the plaintext key is never
// stored, so it cannot be shown again afterwards, the same rule as an MCP token or a deploy hook.
type RumSettingsOut struct {
	Enabled    bool    `json:"enabled"`
	HasKey     bool    `json:"has_key"`
	Key        *string `json:"key,omitempty" doc:"The ingest key, shown once. Paste it into the snippet, or use the snippet field as-is."`
	SnippetURL *string `json:"snippet_url,omitempty"`
	Snippet    *string `json:"snippet,omitempty" doc:"<script> tag to paste into the app's pages."`
}

type rumSettingsIn struct {
	Enabled bool `json:"enabled"`
}

type RumSummaryOut struct{ Body rum.Summary }
type RumPathsOut struct {
	Body struct {
		Window rum.Window          `json:"window"`
		Paths  []rum.PathBreakdown `json:"paths"`
	}
}
type RumTimeseriesOut struct {
	Body struct {
		Window rum.Window     `json:"window"`
		Points []rum.RumPoint `json:"points"`
	}
}

type rumWindowIn struct {
	AppPath
	Window string `query:"window" enum:"24h,7d,30d" default:"24h"`
}

func (d *deps) registerRum(api huma.API) {
	tags := []string{"Real-user monitoring"}
	base := "/api/teams/{team}/apps/{app}/rum"

	settingsOut := func(row db.AppRumSetting) RumSettingsOut {
		return RumSettingsOut{Enabled: row.Enabled, HasKey: row.KeyHash != nil}
	}

	huma.Register(api, huma.Operation{OperationID: "get-app-rum-settings", Method: http.MethodGet, Path: base + "/settings", Tags: tags,
		Summary: "Whether real-user monitoring is on for the app, and whether an ingest key exists. Any member.",
	}, func(ctx context.Context, in *AppPath) (*struct{ Body RumSettingsOut }, error) {
		out := &struct{ Body RumSettingsOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			row, err := q.AppRumSettingOfApp(ctx, db.AppRumSettingOfAppParams{TeamID: m.TeamID, AppID: a.ID})
			if isNoRows(err) {
				out.Body = RumSettingsOut{}
				return nil
			}
			if err != nil {
				return huma.Error500InternalServerError("could not read the RUM settings")
			}
			out.Body = settingsOut(row)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-app-rum-settings", Method: http.MethodPut, Path: base + "/settings", Tags: tags,
		Summary: "Turn real-user monitoring on or off. The first time it is turned on, a fresh ingest key is minted and its snippet " +
			"returned once — save it, it is not shown again (rotate to get a new one). Turning it off again and back on reuses the " +
			"existing key. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body rumSettingsIn
	}) (*struct{ Body RumSettingsOut }, error) {
		out := &struct{ Body RumSettingsOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			existing, err := q.AppRumSettingOfApp(ctx, db.AppRumSettingOfAppParams{TeamID: m.TeamID, AppID: a.ID})
			hasKey := err == nil && existing.KeyHash != nil
			if err != nil && !isNoRows(err) {
				return huma.Error500InternalServerError("could not read the RUM settings")
			}

			if in.Body.Enabled && !hasKey {
				plain, hash := rum.NewKey()
				row, err := q.UpsertAppRumSettingKey(ctx, db.UpsertAppRumSettingKeyParams{AppID: a.ID, TeamID: m.TeamID, KeyHash: &hash, CreatedBy: id.ID})
				if err != nil {
					return huma.Error500InternalServerError("could not enable RUM")
				}
				out.Body = settingsOut(row)
				url := d.rumURL()
				snippet := rum.Snippet(url, plain)
				out.Body.Key, out.Body.SnippetURL, out.Body.Snippet = &plain, &url, &snippet
			} else {
				row, err := q.SetAppRumSettingEnabled(ctx, db.SetAppRumSettingEnabledParams{Enabled: in.Body.Enabled, TeamID: m.TeamID, AppID: a.ID})
				if isNoRows(err) {
					// Turning off (or on with no key yet turned on before) with no row yet: nothing to flip.
					out.Body = RumSettingsOut{Enabled: false}
					return nil
				}
				if err != nil {
					return huma.Error500InternalServerError("could not update the RUM settings")
				}
				out.Body = settingsOut(row)
			}
			return tenancy.Record(ctx, q, m.TeamID, webActor(id).UserID, "app.rum_settings_updated", "app", a.ID,
				map[string]any{"enabled": in.Body.Enabled})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "rotate-app-rum-key", Method: http.MethodPost, Path: base + "/settings/rotate", Tags: tags,
		Summary: "Replace the app's ingest key. The old key stops working at once (a beacon already loaded with it just gets dropped, " +
			"never an error the visitor sees). Turns RUM on if it was off. Owner or admin. Audited.",
	}, func(ctx context.Context, in *AppPath) (*struct{ Body RumSettingsOut }, error) {
		out := &struct{ Body RumSettingsOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			plain, hash := rum.NewKey()
			row, err := q.UpsertAppRumSettingKey(ctx, db.UpsertAppRumSettingKeyParams{AppID: a.ID, TeamID: m.TeamID, KeyHash: &hash, CreatedBy: id.ID})
			if err != nil {
				return huma.Error500InternalServerError("could not rotate the RUM key")
			}
			out.Body = settingsOut(row)
			url := d.rumURL()
			snippet := rum.Snippet(url, plain)
			out.Body.Key, out.Body.SnippetURL, out.Body.Snippet = &plain, &url, &snippet
			return tenancy.Record(ctx, q, m.TeamID, webActor(id).UserID, "app.rum_key_rotated", "app", a.ID, nil)
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "app-rum-summary", Method: http.MethodGet, Path: base + "/summary", Tags: tags,
		Summary: "Core Web Vitals p75 (LCP, CLS, INP, FCP, TTFB) over the window, each rated good / needs-improvement / poor per " +
			"Google's published thresholds, with its own sample count. Any member.",
	}, func(ctx context.Context, in *rumWindowIn) (*RumSummaryOut, error) {
		out := &RumSummaryOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			w := rum.ParseWindow(in.Window)
			row, err := q.RumSummary(ctx, db.RumSummaryParams{TeamID: m.TeamID, AppID: a.ID, Since: w.Since(d.rum().Now())})
			if err != nil {
				return huma.Error500InternalServerError("could not summarise RUM data")
			}
			out.Body = rum.SummaryFrom(w, row)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "app-rum-paths", Method: http.MethodGet, Path: base + "/paths", Tags: tags,
		Summary: "The 20 busiest paths over the window, each with its own Core Web Vitals p75. Any member.",
	}, func(ctx context.Context, in *rumWindowIn) (*RumPathsOut, error) {
		out := &RumPathsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			w := rum.ParseWindow(in.Window)
			rows, err := q.RumTopPaths(ctx, db.RumTopPathsParams{TeamID: m.TeamID, AppID: a.ID, Since: w.Since(d.rum().Now())})
			if err != nil {
				return huma.Error500InternalServerError("could not list the app's paths")
			}
			out.Body.Window = w
			out.Body.Paths = rum.PathsFrom(rows)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "app-rum-timeseries", Method: http.MethodGet, Path: base + "/timeseries", Tags: tags,
		Summary: "p75 per metric over time (hourly buckets for the 24h window, daily otherwise). Any member.",
	}, func(ctx context.Context, in *rumWindowIn) (*RumTimeseriesOut, error) {
		out := &RumTimeseriesOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			w := rum.ParseWindow(in.Window)
			rows, err := q.RumTimeseries(ctx, db.RumTimeseriesParams{Bucket: w.Bucket(), TeamID: m.TeamID, AppID: a.ID, Since: w.Since(d.rum().Now())})
			if err != nil {
				return huma.Error500InternalServerError("could not build the RUM timeseries")
			}
			out.Body.Window = w
			out.Body.Points = rum.TimeseriesFrom(rows)
			return nil
		})
		return out, err
	})
}

// rumURL is the base URL /rum.js and /api/rum/{key} are served from.
func (d *deps) rumURL() string {
	if s := d.rum(); s != nil {
		return s.PublicURL()
	}
	return ""
}
