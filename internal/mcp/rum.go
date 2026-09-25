package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/rum"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// RumTools: H5 real-user monitoring — Core Web Vitals (LCP, CLS, INP, FCP, TTFB) reported by an
// app's own visitors' browsers through the public beacon (internal/rum). app_web_vitals reads the
// summary and top paths; app_rum_settings enables/disables it and (re)mints the ingest key, whose
// plaintext is returned once in the tool's own output, the same rule the team REST API follows.
func RumTools() []Tool {
	rumSvc := func(env *Env) *rum.Service {
		s, _ := env.App.RUM.(*rum.Service)
		return s
	}

	return []Tool{
		{
			Name: "app_web_vitals", Scope: scopes.Read,
			Description: "Core Web Vitals for an app over a window (24h, 7d or 30d, default 24h): p75 of LCP, CLS, INP, FCP and TTFB, " +
				"each rated good / needs-improvement / poor per Google's published thresholds, plus the busiest paths.",
			InputSchema: schema([]string{"app"}, map[string]any{
				"app":    str("App id or name"),
				"window": map[string]any{"type": "string", "enum": []string{"24h", "7d", "30d"}, "description": "Default 24h"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s := rumSvc(env)
				if s == nil {
					return "", errors.New("the RUM service is not running")
				}
				w := rum.ParseWindow(args.Str("window"))
				since := w.Since(s.Now())
				setting, serr := env.Q.AppRumSettingOfApp(ctx, db.AppRumSettingOfAppParams{TeamID: env.Caller.TeamID, AppID: a.ID})
				if serr != nil || !setting.Enabled {
					return "Real-user monitoring is not enabled for this app. app_rum_settings turns it on.", nil
				}
				row, err := env.Q.RumSummary(ctx, db.RumSummaryParams{TeamID: env.Caller.TeamID, AppID: a.ID, Since: since})
				if err != nil {
					return "", errors.New("could not summarise the app's Core Web Vitals")
				}
				sum := rum.SummaryFrom(w, row)
				var b strings.Builder
				fmt.Fprintf(&b, "%s window, %d sample(s):\n", w, sum.Samples)
				b.WriteString(metricsLines(sum.RumMetrics))
				paths, err := env.Q.RumTopPaths(ctx, db.RumTopPathsParams{TeamID: env.Caller.TeamID, AppID: a.ID, Since: since})
				if err == nil && len(paths) > 0 {
					b.WriteString("\nBusiest paths:\n")
					for _, p := range rum.PathsFrom(paths) {
						fmt.Fprintf(&b, "  %-40s samples=%-6d lcp=%s cls=%s inp=%s\n", p.Path, p.Samples,
							ratingCell(p.RumMetrics.LCP), ratingCell(p.RumMetrics.CLS), ratingCell(p.RumMetrics.INP))
					}
				}
				return b.String(), nil
			},
		},
		{
			Name: "app_rum_settings", Scope: scopes.Write,
			Description: "Turn real-user monitoring on or off for an app. Enabling it for the first time (or rotate=true) mints a fresh " +
				"ingest key and returns the <script> snippet to paste into the app's pages — the key is shown once and not stored in " +
				"the clear, so save it now. Owner or admin.",
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
				existing, eerr := env.Q.AppRumSettingOfApp(ctx, db.AppRumSettingOfAppParams{TeamID: env.Caller.TeamID, AppID: a.ID})
				hasKey := eerr == nil && existing.KeyHash != nil

				s := rumSvc(env)
				if enabled && (!hasKey || rotate) {
					plain, hash := rum.NewKey()
					row, err := env.Q.UpsertAppRumSettingKey(ctx, db.UpsertAppRumSettingKeyParams{AppID: a.ID, TeamID: env.Caller.TeamID, KeyHash: &hash, CreatedBy: env.Caller.UserID})
					if err != nil {
						return "", errors.New("could not enable real-user monitoring")
					}
					if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "app.rum_key_rotated", "app", a.ID, auditChanges(env, nil)); err != nil {
						return "", err
					}
					url := ""
					if s != nil {
						url = s.PublicURL()
					}
					_ = row
					return fmt.Sprintf("Real-user monitoring enabled. Paste this into the app's pages (the key is shown once):\n%s\n", rum.Snippet(url, plain)), nil
				}
				row, err := env.Q.SetAppRumSettingEnabled(ctx, db.SetAppRumSettingEnabledParams{Enabled: enabled, TeamID: env.Caller.TeamID, AppID: a.ID})
				if err != nil {
					return "", errors.New("real-user monitoring is not set up for this app yet — call with enabled=true first")
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "app.rum_settings_updated", "app", a.ID,
					auditChanges(env, map[string]any{"enabled": enabled})); err != nil {
					return "", err
				}
				state := "disabled"
				if row.Enabled {
					state = "enabled"
				}
				return "Real-user monitoring is now " + state + ".", nil
			},
		},
	}
}

func ratingCell(m rum.Metric) string {
	if m.Samples == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f(%s)", m.P75, m.Rating)
}

func metricsLines(m rum.RumMetrics) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  LCP  p75=%s ms\n", ratingCell(m.LCP))
	fmt.Fprintf(&b, "  CLS  p75=%s\n", ratingCell(m.CLS))
	fmt.Fprintf(&b, "  INP  p75=%s ms\n", ratingCell(m.INP))
	fmt.Fprintf(&b, "  FCP  p75=%s ms\n", ratingCell(m.FCP))
	fmt.Fprintf(&b, "  TTFB p75=%s ms\n", ratingCell(m.TTFB))
	return b.String()
}
