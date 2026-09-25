package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/apm"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ApmTools: H7 traces and exceptions — slow-endpoint summaries and grouped exceptions built from
// the spans and exception events an app's own OpenTelemetry SDK exported over OTLP/HTTP JSON
// (internal/apm). app_apm_settings enables/disables it and (re)mints the secret ingest key, whose
// plaintext is returned once in the tool's own output, the same rule the team REST API follows.
func ApmTools() []Tool {
	apmSvc := func(env *Env) *apm.Service {
		s, _ := env.App.Apm.(*apm.Service)
		return s
	}

	return []Tool{
		{
			Name: "app_apm_settings", Scope: scopes.Write,
			Description: "Turn APM (traces and exceptions) on or off for an app. Enabling it for the first time (or rotate=true) " +
				"mints a fresh secret ingest key and returns the OTEL_* env snippet — the key is shown once, save it now. The key " +
				"is also auto-injected into the app's own env on its next deploy. Owner or admin.",
			InputSchema: schema([]string{"app"}, map[string]any{
				"app":     str("App id or name"),
				"enabled": map[string]any{"type": "boolean", "description": "Default true"},
				"rotate":  map[string]any{"type": "boolean", "description": "Replace the ingest key even if one already exists"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				enabled := true
				if v, ok := args["enabled"]; ok {
					if b, ok := v.(bool); ok {
						enabled = b
					}
				}
				rotate := args.Bool("rotate")
				existing, eerr := env.Q.AppApmSettingOfApp(ctx, db.AppApmSettingOfAppParams{TeamID: env.Caller.TeamID, AppID: a.ID})
				hasKey := eerr == nil && existing.KeyHash != nil

				s := apmSvc(env)
				if enabled && (!hasKey || rotate) {
					plain, hash := apm.NewKey()
					team, err := env.Q.GetTeam(ctx, env.Caller.TeamID)
					if err != nil {
						return "", errors.New("could not read the team")
					}
					dek, err := env.App.Keys.UnwrapDEK(env.Caller.TeamID, team.DekWrapped)
					if err != nil {
						return "", errors.New("team key unavailable")
					}
					sealed, err := crypto.Seal(dek, env.Caller.TeamID, apm.EnvSealContext(a.ID), []byte(plain))
					if err != nil {
						return "", errors.New("could not seal the APM key")
					}
					_, err = env.Q.UpsertAppApmSettingKey(ctx, db.UpsertAppApmSettingKeyParams{
						AppID: a.ID, TeamID: env.Caller.TeamID, KeyHash: &hash, KeySealed: &sealed, CreatedBy: env.Caller.UserID,
					})
					if err != nil {
						return "", errors.New("could not enable APM")
					}
					if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "app.apm_key_rotated", "app", a.ID, auditChanges(env, nil)); err != nil {
						return "", err
					}
					url := ""
					if s != nil {
						url = s.PublicURL()
					}
					return fmt.Sprintf("APM enabled. The key is shown once — save it (auto-injected into the app's own env on its next deploy):\n%s", apm.Snippet(url, plain, a.Name)), nil
				}
				row, err := env.Q.SetAppApmSettingEnabled(ctx, db.SetAppApmSettingEnabledParams{Enabled: enabled, TeamID: env.Caller.TeamID, AppID: a.ID})
				if err != nil {
					return "", errors.New("APM is not set up for this app yet — call with enabled=true first")
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "app.apm_settings_updated", "app", a.ID,
					auditChanges(env, map[string]any{"enabled": enabled})); err != nil {
					return "", err
				}
				state := "disabled"
				if row.Enabled {
					state = "enabled"
				}
				return "APM is now " + state + ".", nil
			},
		},
		{
			Name: "app_endpoints", Scope: scopes.Read,
			Description: "Slow-endpoint summary for an app over a window (1h, 24h or 7d, default 24h): request counts, p50/p95/p99 " +
				"duration and error counts per (method, route).",
			InputSchema: schema([]string{"app"}, map[string]any{
				"app":    str("App id or name"),
				"window": map[string]any{"type": "string", "enum": []string{"1h", "24h", "7d"}, "description": "Default 24h"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s := apmSvc(env)
				if s == nil {
					return "", errors.New("the APM service is not running")
				}
				setting, serr := env.Q.AppApmSettingOfApp(ctx, db.AppApmSettingOfAppParams{TeamID: env.Caller.TeamID, AppID: a.ID})
				if serr != nil || !setting.Enabled {
					return "APM is not enabled for this app. app_apm_settings turns it on.", nil
				}
				w := args.Str("window")
				since := apmWindowSince(s.Now(), w)
				rows, err := env.Q.ApmEndpointsSummary(ctx, db.ApmEndpointsSummaryParams{TeamID: env.Caller.TeamID, AppID: a.ID, Since: since})
				if err != nil {
					return "", errors.New("could not summarise the app's endpoints")
				}
				if len(rows) == 0 {
					return "No endpoint traffic recorded in this window.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d endpoint(s):\n", len(rows))
				for _, r := range rows {
					fmt.Fprintf(&b, "  %-6s %-40s n=%-6d p50=%.0fms p95=%.0fms p99=%.0fms errors=%d\n",
						r.HttpMethod, r.HttpRoute, r.Samples, r.P50Ms, r.P95Ms, r.P99Ms, r.Errors)
				}
				return b.String(), nil
			},
		},
		{
			Name: "app_traces", Scope: scopes.Read,
			Description: "Recent traces for one (method, route), or one trace's full waterfall by trace_id.",
			InputSchema: schema([]string{"app"}, map[string]any{
				"app":      str("App id or name"),
				"method":   str("HTTP method (with route, lists recent traces for that endpoint)"),
				"route":    str("HTTP route (with method, lists recent traces for that endpoint)"),
				"trace_id": str("Trace id (fetches that one trace's waterfall)"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				if id := args.Str("trace_id"); id != "" {
					rows, err := env.Q.ApmTraceSpans(ctx, db.ApmTraceSpansParams{TeamID: env.Caller.TeamID, AppID: a.ID, TraceID: id})
					if err != nil || len(rows) == 0 {
						return "No spans stored for that trace id.", nil
					}
					var b strings.Builder
					fmt.Fprintf(&b, "Trace %s, %d span(s):\n", id, len(rows))
					for _, r := range rows {
						errFlag := ""
						if r.Error {
							errFlag = " ERROR"
						}
						fmt.Fprintf(&b, "  %-8s %-30s kind=%-8s %.0fms%s\n", r.SpanID[:min8(len(r.SpanID))], r.Name, r.Kind, r.DurationMs, errFlag)
					}
					return b.String(), nil
				}
				method, route := args.Str("method"), args.Str("route")
				if method == "" || route == "" {
					return "", errors.New("pass either trace_id, or both method and route")
				}
				rows, err := env.Q.ApmRecentTracesForRoute(ctx, db.ApmRecentTracesForRouteParams{TeamID: env.Caller.TeamID, AppID: a.ID, HttpMethod: &method, HttpRoute: &route})
				if err != nil {
					return "", errors.New("could not list traces")
				}
				if len(rows) == 0 {
					return "No recent traces for that endpoint.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d recent trace(s) for %s %s:\n", len(rows), method, route)
				for _, r := range rows {
					errFlag := ""
					if r.Error {
						errFlag = " ERROR"
					}
					fmt.Fprintf(&b, "  %s  %.0fms%s  %s\n", r.TraceID, r.DurationMs, errFlag, r.StartTime.Format(time.RFC3339))
				}
				return b.String(), nil
			},
		},
		{
			Name: "app_errors", Scope: scopes.Read,
			Description: "List an app's grouped exceptions (issues), or, with issue_id, one issue's detail and its latest stacktrace.",
			InputSchema: schema([]string{"app"}, map[string]any{
				"app":      str("App id or name"),
				"issue_id": str("An issue id, to fetch its detail and latest stacktrace"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				if id := args.Str("issue_id"); id != "" {
					row, err := env.Q.ApmIssueOfApp(ctx, db.ApmIssueOfAppParams{TeamID: env.Caller.TeamID, AppID: a.ID, ID: id})
					if err != nil {
						return "", errors.New("issue not found")
					}
					var b strings.Builder
					fmt.Fprintf(&b, "%s (%s)\n%s\nfirst seen %s, last seen %s, %d occurrence(s)\n",
						row.Type, row.Status, row.Message, row.FirstSeen.Format(time.RFC3339), row.LastSeen.Format(time.RFC3339), row.Count)
					ev, err := env.Q.ApmLatestExceptionEventOfIssue(ctx, db.ApmLatestExceptionEventOfIssueParams{TeamID: env.Caller.TeamID, AppID: a.ID, IssueID: row.ID})
					if err == nil && ev.Stacktrace != "" {
						b.WriteString("\n" + ev.Stacktrace)
					}
					return b.String(), nil
				}
				since := time.Now().Add(-apm.ExceptionRetention)
				if s := (func() *apm.Service { s, _ := env.App.Apm.(*apm.Service); return s })(); s != nil {
					since = s.Now().Add(-apm.ExceptionRetention)
				}
				rows, err := env.Q.ApmIssuesOfApp(ctx, db.ApmIssuesOfAppParams{TeamID: env.Caller.TeamID, AppID: a.ID, Since: since})
				if err != nil {
					return "", errors.New("could not list issues")
				}
				if len(rows) == 0 {
					return "No exceptions recorded.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d issue(s):\n", len(rows))
				for _, r := range rows {
					fmt.Fprintf(&b, "  [%s] %-8s x%-4d %-30s last seen %s  (id=%s)\n", r.Status, r.Type, r.Count, truncate(r.Message, 30), r.LastSeen.Format(time.RFC3339), r.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "app_error_status", Scope: scopes.Write,
			Description: "Mark an issue open, resolved, or ignored. A resolved issue reopens automatically if it recurs; an ignored one does not. Owner or admin.",
			InputSchema: schema([]string{"app", "issue_id", "status"}, map[string]any{
				"app":      str("App id or name"),
				"issue_id": str("The issue id"),
				"status":   map[string]any{"type": "string", "enum": []string{"open", "resolved", "ignored"}},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				id, status := args.Str("issue_id"), args.Str("status")
				if id == "" || status == "" {
					return "", errors.New("issue_id and status are required")
				}
				row, err := env.Q.SetApmIssueStatus(ctx, db.SetApmIssueStatusParams{Status: status, TeamID: env.Caller.TeamID, AppID: a.ID, ID: id})
				if err != nil {
					return "", errors.New("issue not found")
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "app.apm_issue_status_updated", "app", a.ID,
					auditChanges(env, map[string]any{"issue": id, "status": status})); err != nil {
					return "", err
				}
				return fmt.Sprintf("Issue %s is now %s.", row.ID, row.Status), nil
			},
		},
	}
}

func apmWindowSince(now time.Time, w string) time.Time {
	switch w {
	case "1h":
		return now.Add(-1 * time.Hour)
	case "7d":
		return now.Add(-7 * 24 * time.Hour)
	default:
		return now.Add(-24 * time.Hour)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func min8(n int) int {
	if n < 8 {
		return n
	}
	return 8
}
