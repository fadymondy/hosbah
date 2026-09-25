package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/apm"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
APM: traces and exceptions (MH-575 H7): a per-app toggle and rotatable, secret ingest key
(settings), slow-endpoint summaries and trace waterfalls built from the reduced spans the app's own
OTel SDK exported (endpoints, traces), and grouped exceptions (issues). Reading is any member;
enabling, disabling, rotating the key and changing an issue's status are owner/admin
(AbilityManageApps), the same tier as every other app-configuration change.
*/

func (d *deps) apm() *apm.Service {
	if d.a == nil {
		return nil
	}
	s, _ := d.a.Apm.(*apm.Service)
	return s
}

// ApmSettingsOut mirrors RumSettingsOut's shape: Key/Snippet are set only right after a call that
// (re)creates the key — never shown again afterwards.
type ApmSettingsOut struct {
	Enabled    bool    `json:"enabled"`
	HasKey     bool    `json:"has_key"`
	Key        *string `json:"key,omitempty" doc:"The ingest key, shown once. A real secret — unlike the RUM key, never put it in client-side code."`
	Snippet    *string `json:"snippet,omitempty" doc:"Env vars to paste into the app's own environment. Auto-injected on the next deploy once enabled, so this is informational."`
	SnippetURL *string `json:"snippet_url,omitempty"`
}

type apmSettingsIn struct {
	Enabled bool `json:"enabled"`
}

type ApmEndpointOut struct {
	Method  string  `json:"method"`
	Route   string  `json:"route"`
	Samples int64   `json:"samples"`
	P50Ms   float64 `json:"p50_ms"`
	P95Ms   float64 `json:"p95_ms"`
	P99Ms   float64 `json:"p99_ms"`
	Errors  int64   `json:"errors"`
}

type ApmEndpointsOut struct {
	Body struct {
		Window    string           `json:"window"`
		Endpoints []ApmEndpointOut `json:"endpoints"`
	}
}

type apmWindowIn struct {
	AppPath
	Window string `query:"window" enum:"1h,24h,7d" default:"24h"`
}

type apmRouteIn struct {
	AppPath
	Method string `query:"method"`
	Route  string `query:"route"`
}

type ApmTraceListOut struct {
	Body struct {
		Traces []ApmTraceSummary `json:"traces"`
	}
}
type ApmTraceSummary struct {
	TraceID    string  `json:"trace_id"`
	DurationMs float32 `json:"duration_ms"`
	Error      bool    `json:"error"`
	HTTPStatus *int32  `json:"http_status,omitempty"`
	StartTime  string  `json:"start_time"`
}

type apmTraceIn struct {
	AppPath
	TraceID string `path:"trace_id"`
}

type ApmSpanOut struct {
	SpanID       string  `json:"span_id"`
	ParentSpanID *string `json:"parent_span_id,omitempty"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	Service      string  `json:"service"`
	HTTPMethod   *string `json:"http_method,omitempty"`
	HTTPRoute    *string `json:"http_route,omitempty"`
	HTTPStatus   *int32  `json:"http_status,omitempty"`
	DurationMs   float32 `json:"duration_ms"`
	Error        bool    `json:"error"`
	StartTime    string  `json:"start_time"`
}
type ApmTraceOut struct {
	Body struct {
		Spans []ApmSpanOut `json:"spans"`
	}
}

type ApmIssueOut struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Message   string `json:"message"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
	Count     int32  `json:"count"`
	Status    string `json:"status"`
}
type ApmIssuesOut struct {
	Body struct {
		Issues []ApmIssueOut `json:"issues"`
	}
}
type ApmIssueDetailOut struct {
	Body struct {
		ApmIssueOut
		LatestStacktrace string `json:"latest_stacktrace,omitempty"`
	}
}
type apmIssueIn struct {
	AppPath
	Issue string `path:"issue"`
}
type apmIssueStatusIn struct {
	AppPath
	Issue string `path:"issue"`
	Body  struct {
		Status string `json:"status" enum:"open,resolved,ignored"`
	}
}

func issueOut(row db.ApmIssue) ApmIssueOut {
	return ApmIssueOut{ID: row.ID, Type: row.Type, Message: row.Message, FirstSeen: row.FirstSeen.Format(time.RFC3339),
		LastSeen: row.LastSeen.Format(time.RFC3339), Count: row.Count, Status: row.Status}
}

func windowSince(now time.Time, w string) time.Time {
	switch w {
	case "1h":
		return now.Add(-1 * time.Hour)
	case "7d":
		return now.Add(-7 * 24 * time.Hour)
	default:
		return now.Add(-24 * time.Hour)
	}
}

func (d *deps) registerApm(api huma.API) {
	tags := []string{"APM"}
	base := "/api/teams/{team}/apps/{app}/apm"

	settingsOut := func(row db.AppApmSetting) ApmSettingsOut {
		return ApmSettingsOut{Enabled: row.Enabled, HasKey: row.KeyHash != nil}
	}

	huma.Register(api, huma.Operation{OperationID: "get-app-apm-settings", Method: http.MethodGet, Path: base + "/settings", Tags: tags,
		Summary: "Whether APM (traces and exceptions) is on for the app, and whether an ingest key exists. Any member.",
	}, func(ctx context.Context, in *AppPath) (*struct{ Body ApmSettingsOut }, error) {
		out := &struct{ Body ApmSettingsOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			row, err := q.AppApmSettingOfApp(ctx, db.AppApmSettingOfAppParams{TeamID: m.TeamID, AppID: a.ID})
			if isNoRows(err) {
				out.Body = ApmSettingsOut{}
				return nil
			}
			if err != nil {
				return huma.Error500InternalServerError("could not read the APM settings")
			}
			out.Body = settingsOut(row)
			return nil
		})
		return out, err
	})

	mint := func(ctx context.Context, q *db.Queries, id *auth.Identity, m *tenancy.Membership, appID, appName string) (ApmSettingsOut, error) {
		plain, hash := apm.NewKey()
		team, err := q.GetTeam(ctx, m.TeamID)
		if err != nil {
			return ApmSettingsOut{}, huma.Error500InternalServerError("could not read the team")
		}
		dek, err := d.a.Keys.UnwrapDEK(m.TeamID, team.DekWrapped)
		if err != nil {
			return ApmSettingsOut{}, huma.Error500InternalServerError("team key unavailable")
		}
		sealed, err := crypto.Seal(dek, m.TeamID, apm.EnvSealContext(appID), []byte(plain))
		if err != nil {
			return ApmSettingsOut{}, huma.Error500InternalServerError("could not seal the APM key")
		}
		row, err := q.UpsertAppApmSettingKey(ctx, db.UpsertAppApmSettingKeyParams{AppID: appID, TeamID: m.TeamID, KeyHash: &hash, KeySealed: &sealed, CreatedBy: id.ID})
		if err != nil {
			return ApmSettingsOut{}, huma.Error500InternalServerError("could not enable APM")
		}
		out := settingsOut(row)
		url := d.apmURL()
		snippet := apm.Snippet(url, plain, appName)
		out.Key, out.Snippet, out.SnippetURL = &plain, &snippet, &url
		return out, nil
	}

	huma.Register(api, huma.Operation{OperationID: "update-app-apm-settings", Method: http.MethodPut, Path: base + "/settings", Tags: tags,
		Summary: "Turn APM on or off. The first time it is turned on, a fresh secret ingest key is minted and its env-var snippet " +
			"returned once — save it (rotate to get a new one). The key is also auto-injected into the app's own env on its next " +
			"deploy, so most teams never need the snippet. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body apmSettingsIn
	}) (*struct{ Body ApmSettingsOut }, error) {
		out := &struct{ Body ApmSettingsOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			existing, err := q.AppApmSettingOfApp(ctx, db.AppApmSettingOfAppParams{TeamID: m.TeamID, AppID: a.ID})
			hasKey := err == nil && existing.KeyHash != nil
			if err != nil && !isNoRows(err) {
				return huma.Error500InternalServerError("could not read the APM settings")
			}
			if in.Body.Enabled && !hasKey {
				body, err := mint(ctx, q, id, m, a.ID, a.Name)
				if err != nil {
					return err
				}
				out.Body = body
			} else {
				row, err := q.SetAppApmSettingEnabled(ctx, db.SetAppApmSettingEnabledParams{Enabled: in.Body.Enabled, TeamID: m.TeamID, AppID: a.ID})
				if isNoRows(err) {
					out.Body = ApmSettingsOut{Enabled: false}
					return nil
				}
				if err != nil {
					return huma.Error500InternalServerError("could not update the APM settings")
				}
				out.Body = settingsOut(row)
			}
			return tenancy.Record(ctx, q, m.TeamID, webActor(id).UserID, "app.apm_settings_updated", "app", a.ID,
				map[string]any{"enabled": in.Body.Enabled})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "rotate-app-apm-key", Method: http.MethodPost, Path: base + "/settings/rotate", Tags: tags,
		Summary: "Replace the app's APM ingest key. The old key stops accepting traces/logs at once. Turns APM on if it was off. " +
			"Owner or admin. Audited.",
	}, func(ctx context.Context, in *AppPath) (*struct{ Body ApmSettingsOut }, error) {
		out := &struct{ Body ApmSettingsOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			body, err := mint(ctx, q, id, m, a.ID, a.Name)
			if err != nil {
				return err
			}
			out.Body = body
			return tenancy.Record(ctx, q, m.TeamID, webActor(id).UserID, "app.apm_key_rotated", "app", a.ID, nil)
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "app-apm-endpoints", Method: http.MethodGet, Path: base + "/endpoints", Tags: tags,
		Summary: "Per-(method, route) request counts and duration percentiles (p50/p95/p99) plus error counts, over the window " +
			"(1h, 24h or 7d, default 24h). Any member.",
	}, func(ctx context.Context, in *apmWindowIn) (*ApmEndpointsOut, error) {
		out := &ApmEndpointsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			since := windowSince(d.apmNow(), in.Window)
			rows, err := q.ApmEndpointsSummary(ctx, db.ApmEndpointsSummaryParams{TeamID: m.TeamID, AppID: a.ID, Since: since})
			if err != nil {
				return huma.Error500InternalServerError("could not summarise the app's endpoints")
			}
			out.Body.Window = in.Window
			for _, r := range rows {
				out.Body.Endpoints = append(out.Body.Endpoints, ApmEndpointOut{
					Method: r.HttpMethod, Route: r.HttpRoute, Samples: r.Samples, P50Ms: r.P50Ms, P95Ms: r.P95Ms, P99Ms: r.P99Ms, Errors: r.Errors,
				})
			}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "app-apm-traces", Method: http.MethodGet, Path: base + "/traces", Tags: tags,
		Summary: "Up to 50 recent traces for one (method, route) — both query params required, or the list is empty. Any member.",
	}, func(ctx context.Context, in *apmRouteIn) (*ApmTraceListOut, error) {
		out := &ApmTraceListOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if in.Method == "" || in.Route == "" {
				return nil
			}
			rows, err := q.ApmRecentTracesForRoute(ctx, db.ApmRecentTracesForRouteParams{TeamID: m.TeamID, AppID: a.ID, HttpMethod: &in.Method, HttpRoute: &in.Route})
			if err != nil {
				return huma.Error500InternalServerError("could not list traces")
			}
			for _, r := range rows {
				t := ApmTraceSummary{TraceID: r.TraceID, DurationMs: r.DurationMs, Error: r.Error, StartTime: r.StartTime.Format(time.RFC3339)}
				if r.HttpStatus.Valid {
					v := r.HttpStatus.Int32
					t.HTTPStatus = &v
				}
				out.Body.Traces = append(out.Body.Traces, t)
			}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "app-apm-trace", Method: http.MethodGet, Path: base + "/traces/{trace_id}", Tags: tags,
		Summary: "One trace's stored spans (waterfall) — only the root/error/slow subset kept at ingest, not necessarily the full " +
			"original trace. Any member.",
	}, func(ctx context.Context, in *apmTraceIn) (*ApmTraceOut, error) {
		out := &ApmTraceOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			rows, err := q.ApmTraceSpans(ctx, db.ApmTraceSpansParams{TeamID: m.TeamID, AppID: a.ID, TraceID: in.TraceID})
			if err != nil {
				return huma.Error500InternalServerError("could not read the trace")
			}
			for _, r := range rows {
				sp := ApmSpanOut{SpanID: r.SpanID, ParentSpanID: r.ParentSpanID, Name: r.Name, Kind: r.Kind, Service: r.Service,
					HTTPMethod: r.HttpMethod, HTTPRoute: r.HttpRoute, DurationMs: r.DurationMs, Error: r.Error, StartTime: r.StartTime.Format(time.RFC3339)}
				if r.HttpStatus.Valid {
					v := r.HttpStatus.Int32
					sp.HTTPStatus = &v
				}
				out.Body.Spans = append(out.Body.Spans, sp)
			}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "app-apm-issues", Method: http.MethodGet, Path: base + "/issues", Tags: tags,
		Summary: "Grouped exceptions (issues), seen in the last 30 days, newest first. Any member.",
	}, func(ctx context.Context, in *AppPath) (*ApmIssuesOut, error) {
		out := &ApmIssuesOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			since := d.apmNow().Add(-apm.ExceptionRetention)
			rows, err := q.ApmIssuesOfApp(ctx, db.ApmIssuesOfAppParams{TeamID: m.TeamID, AppID: a.ID, Since: since})
			if err != nil {
				return huma.Error500InternalServerError("could not list issues")
			}
			for _, r := range rows {
				out.Body.Issues = append(out.Body.Issues, issueOut(r))
			}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "app-apm-issue", Method: http.MethodGet, Path: base + "/issues/{issue}", Tags: tags,
		Summary: "One issue's detail with its latest occurrence's stacktrace. Any member.",
	}, func(ctx context.Context, in *apmIssueIn) (*ApmIssueDetailOut, error) {
		out := &ApmIssueDetailOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			row, err := q.ApmIssueOfApp(ctx, db.ApmIssueOfAppParams{TeamID: m.TeamID, AppID: a.ID, ID: in.Issue})
			if isNoRows(err) {
				return huma.Error404NotFound("issue not found")
			}
			if err != nil {
				return huma.Error500InternalServerError("could not read the issue")
			}
			out.Body.ApmIssueOut = issueOut(row)
			ev, err := q.ApmLatestExceptionEventOfIssue(ctx, db.ApmLatestExceptionEventOfIssueParams{TeamID: m.TeamID, AppID: a.ID, IssueID: row.ID})
			if err == nil {
				out.Body.LatestStacktrace = ev.Stacktrace
			}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-app-apm-issue-status", Method: http.MethodPut, Path: base + "/issues/{issue}/status", Tags: tags,
		Summary: "Mark an issue open, resolved, or ignored. A resolved issue reopens automatically the next time it recurs; an " +
			"ignored one does not. Owner or admin. Audited.",
	}, func(ctx context.Context, in *apmIssueStatusIn) (*struct{ Body ApmIssueOut }, error) {
		out := &struct{ Body ApmIssueOut }{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			row, err := q.SetApmIssueStatus(ctx, db.SetApmIssueStatusParams{Status: in.Body.Status, TeamID: m.TeamID, AppID: a.ID, ID: in.Issue})
			if isNoRows(err) {
				return huma.Error404NotFound("issue not found")
			}
			if err != nil {
				return huma.Error500InternalServerError("could not update the issue")
			}
			out.Body = issueOut(row)
			return tenancy.Record(ctx, q, m.TeamID, webActor(id).UserID, "app.apm_issue_status_updated", "app", a.ID,
				map[string]any{"issue": in.Issue, "status": in.Body.Status})
		})
		return out, err
	})
}

// apmURL is the base URL /api/otlp is served from.
func (d *deps) apmURL() string {
	if s := d.apm(); s != nil {
		return s.PublicURL()
	}
	return ""
}

func (d *deps) apmNow() time.Time {
	if s := d.apm(); s != nil {
		return s.Now()
	}
	return time.Now()
}
