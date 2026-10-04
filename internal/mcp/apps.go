package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/services"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/traffic"
)

// AppTools are the P2 app tools. They call the same deploy actions as the REST API, so the rules
// (validation, roles, audit, secrets never in a result) are one implementation. Scopes:
// read — look; deploy — deploy, cancel, roll back, restart, pause; write — create and configure;
// secrets:read — reveal_env (audited); destructive — delete_app (confirm = the app's name).
func AppTools() []Tool {
	appArg := str("App name or id")
	return []Tool{
		// ---- read ----
		{
			Name: "list_apps", Scope: scopes.Read,
			Description: "The team's apps: status, server, detected runtime, live release, domains, last deployment.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				rows, err := env.Q.ListAppsOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list apps")
				}
				if len(rows) == 0 {
					return "No apps yet. create_app adds one from a public git URL.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d app(s):\n", len(rows))
				for _, r := range rows {
					if r.Source == importer.SourceImported {
						fmt.Fprintf(&b, "  %-20s %-9s server %-16s %s  (id %s)\n", r.Name, r.Status, r.ServerName,
							importedLine(r.Name, r.OriginKind, r.Origin, r.ImportedState, ""), r.ID)
						continue
					}
					live := "—"
					if rel, err := env.Q.LiveReleaseOfApp(ctx, r.ID); err == nil {
						live = fmt.Sprintf("v%d", rel.Version)
					}
					last := "never deployed"
					if ds, err := env.Q.ListDeploymentsOfApp(ctx, db.ListDeploymentsOfAppParams{AppID: r.ID, TeamID: env.Caller.TeamID, Limit: 1}); err == nil && len(ds) > 0 {
						last = fmt.Sprintf("#%d %s %s", ds[0].Number, ds[0].Kind, ds[0].Status)
					}
					fmt.Fprintf(&b, "  %-20s %-9s server %-16s runtime %-18s live %-4s last %s  (id %s)\n",
						r.Name, r.Status, r.ServerName, orDash(r.Runtime), live, last, r.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "get_app", Scope: scopes.Read,
			Description: "One app: repository, branch, port and health check, live and previous release, domains, processes, recent deployments.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				var b strings.Builder
				if a.Source == importer.SourceImported {
					fmt.Fprintf(&b, "%s  (id %s)\nimported: Hosbah shows it but does not deploy or change it\n%s\nlast seen: %s\n", a.Name, a.ID,
						importedLine(a.Name, a.OriginKind, a.Origin, a.ImportedState, ""), lastSeen(a.LastSeenAt))
					return b.String(), nil
				}
				fmt.Fprintf(&b, "%s  (id %s)\nstatus: %s\nrepository: %s @ %s\nruntime: %s\nport: %d · health check GET %s within %ds\nauto-deploy: %s\n",
					a.Name, a.ID, a.Status, orDash(a.RepoURL), orDash(a.Branch), orDash(a.Runtime), a.Port, a.HealthcheckPath, a.HealthcheckTimeout,
					map[bool]string{true: "paused", false: "on"}[a.AutodeployPaused])
				if s, err := env.Q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: env.Caller.TeamID}); err == nil {
					fmt.Fprintf(&b, "server: %s (%s)\n", s.Name, presence(s, time.Now()))
				}
				if r, err := env.Q.LiveReleaseOfApp(ctx, a.ID); err == nil {
					fmt.Fprintf(&b, "live: v%d  %s  commit %s\n", r.Version, r.Image, short(r.CommitSha))
				} else {
					b.WriteString("live: nothing yet\n")
				}
				if r, err := env.Q.PreviousReleaseOfApp(ctx, a.ID); err == nil {
					fmt.Fprintf(&b, "rollback target: v%d  commit %s\n", r.Version, short(r.CommitSha))
				}
				if ds, err := env.Q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: env.Caller.TeamID}); err == nil {
					for _, d := range ds {
						fmt.Fprintf(&b, "domain: %s  tls %s  %s\n", d.Hostname, d.Tls, map[bool]string{true: "verified", false: "not verified"}[d.VerifiedAt != nil])
					}
				}
				if ps, err := env.Q.ListProcessTypesOfApp(ctx, db.ListProcessTypesOfAppParams{AppID: a.ID, TeamID: env.Caller.TeamID}); err == nil {
					for _, p := range ps {
						fmt.Fprintf(&b, "process: %s  %s\n", p.Name, orDash(p.Command))
					}
				}
				if ds, err := env.Q.ListDeploymentsOfApp(ctx, db.ListDeploymentsOfAppParams{AppID: a.ID, TeamID: env.Caller.TeamID, Limit: 5}); err == nil {
					for _, d := range ds {
						b.WriteString("deployment: " + deploymentLine(d) + "\n")
					}
				}
				return b.String(), nil
			},
		},
		{
			Name: "list_deployments", Scope: scopes.Read,
			Description: "Deployments, newest first. With app: that app's deployments (number, kind, status, step, commit, error). " +
				"Without app: the team's latest deployments across every app, as JSON {deployments:[{id, number, app:{id,name}, server:{id,name}, status, kind, branch, " +
				"commit_sha, commit_message, trigger, triggered_by:{email}|null, created_at, finished_at}], next_cursor} — pass next_cursor as before for the next page.",
			InputSchema: schema(nil, map[string]any{"app": appArg, "limit": map[string]any{"type": "integer", "default": 20, "maximum": 100},
				"before": str("Team feed only: next_cursor of the previous page")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if args.Str("app") == "" {
					f, err := deploy.TeamFeed(ctx, env.Q, env.Caller.TeamID, args.Int("limit", 20, 1, 100), args.Str("before"))
					if err != nil {
						if errors.Is(err, deploy.ErrBadCursor) {
							return "", err
						}
						return "", errors.New("could not list deployments")
					}
					b, _ := json.Marshal(f)
					return string(b), nil
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				ds, err := env.Q.ListDeploymentsOfApp(ctx, db.ListDeploymentsOfAppParams{AppID: a.ID, TeamID: env.Caller.TeamID, Limit: int32(args.Int("limit", 20, 1, 100))})
				if err != nil {
					return "", errors.New("could not list deployments")
				}
				if len(ds) == 0 {
					return a.Name + " has not been deployed. deploy_app starts one.", nil
				}
				var b strings.Builder
				for _, d := range ds {
					b.WriteString("  " + deploymentLine(d) + "\n")
				}
				return b.String(), nil
			},
		},
		{
			Name: "get_deployment", Scope: scopes.Read,
			Description: "One deployment (number or id): status, step, commit, image, timings, error.",
			InputSchema: schema([]string{"app", "deployment"}, map[string]any{"app": appArg, "deployment": str("Deployment number (e.g. 12) or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, d, err := findDeployment(ctx, env, args)
				if err != nil {
					return "", err
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%s #%d  (id %s)\nkind: %s · trigger: %s\nstatus: %s", a.Name, d.Number, d.ID, d.Kind, d.Trigger, d.Status)
				if d.Step != nil {
					fmt.Fprintf(&b, " (step %s)", *d.Step)
				}
				fmt.Fprintf(&b, "\nref: %s · commit %s %s\nimage: %s · runtime: %s\n", d.Ref, short(d.CommitSha), orDash(d.CommitMessage), orDash(d.Image), orDash(d.Runtime))
				fmt.Fprintf(&b, "queued %s", d.CreatedAt.UTC().Format(time.RFC3339))
				if d.StartedAt != nil && d.FinishedAt != nil {
					fmt.Fprintf(&b, " · ran %s", d.FinishedAt.Sub(*d.StartedAt).Round(time.Second))
				}
				b.WriteString("\n")
				if d.Error != nil {
					fmt.Fprintf(&b, "error: %s\n", *d.Error)
				}
				return b.String(), nil
			},
		},
		{
			Name: "deploy_logs", Scope: scopes.Read,
			Description: "A deployment's build and deploy log (decrypted for this team), from a line number on. Bounded to 500 lines per call.",
			InputSchema: schema([]string{"app", "deployment"}, map[string]any{"app": appArg, "deployment": str("Deployment number or id"),
				"after": map[string]any{"type": "integer", "default": 0, "description": "Only lines after this seq"},
				"limit": map[string]any{"type": "integer", "default": 200, "maximum": 500}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				_, d, err := findDeployment(ctx, env, args)
				if err != nil {
					return "", err
				}
				lines, err := deployLines(ctx, env.App, env.Q, env.Caller.TeamID, d.ID, int64(args.Int("after", 0, 0, 1<<30)), int32(args.Int("limit", 200, 1, 500)))
				if err != nil {
					return "", err
				}
				if len(lines) == 0 {
					return fmt.Sprintf("No log lines (yet) — #%d is %s.", d.Number, d.Status), nil
				}
				var b strings.Builder
				for _, l := range lines {
					b.WriteString(formatLine(l) + "\n")
				}
				fmt.Fprintf(&b, "(#%d is %s; last seq %d — pass after=%d for more)\n", d.Number, d.Status, lines[len(lines)-1].Seq, lines[len(lines)-1].Seq)
				return b.String(), nil
			},
		},
		{
			Name: "app_logs_search", Scope: scopes.Read,
			Description: "Search the app's current containers' logs: a plain substring or an RE2 regex (regex=true), optionally " +
				"case-insensitive, with an error/warn level filter and a time window up to 7 days (since/until: 15m/1h/6h/24h/7d or " +
				"RFC 3339; default the last hour). The agent filters the lines itself; at most limit matches are returned.",
			InputSchema: schema([]string{"app"}, map[string]any{
				"app": appArg, "q": str("A plain substring, or an RE2 pattern when regex=true"),
				"regex":            map[string]any{"type": "boolean", "description": "Treat q as an RE2 regular expression"},
				"case_insensitive": map[string]any{"type": "boolean"},
				"process":          str("web (default: every process) or worker:<name>"),
				"level":            map[string]any{"type": "string", "enum": []string{"", "error", "warn"}},
				"since":            str("15m, 1h, 6h, 24h, 7d, or an RFC 3339 time; default one hour before until"),
				"until":            str("An RFC 3339 time; default now"),
				"limit":            map[string]any{"type": "integer", "default": 200, "maximum": agentproto.MaxLogSearchLines},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				op, err := s.LogsSearch(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, agentproto.LogsSearchParams{
					Process: args.Str("process"), Query: args.Str("q"), Regex: args.Bool("regex"), CaseInsensitive: args.Bool("case_insensitive"),
					Level: args.Str("level"), Since: args.Str("since"), Until: args.Str("until"), Limit: args.Int("limit", 200, 0, agentproto.MaxLogSearchLines),
				})
				if err != nil {
					return "", err
				}
				env.Then = func(ctx context.Context) (string, error) {
					var res agentproto.LogsSearchResult
					if _, err := s.AwaitResult(ctx, op, 30*time.Second, &res); err != nil {
						if errors.Is(err, deploy.ErrStillRunning) {
							return fmt.Sprintf("The agent has not answered yet (operation %s).", op.ID), nil
						}
						return "", err
					}
					if len(res.Lines) == 0 {
						return "No matching lines.", nil
					}
					var b strings.Builder
					for _, l := range res.Lines {
						lvl := l.Level
						if lvl == "" {
							lvl = "-"
						}
						fmt.Fprintf(&b, "%s  %-12s %-6s %s\n", l.At.UTC().Format(time.RFC3339), l.Process, lvl, l.Line)
					}
					if res.Truncated {
						b.WriteString("(truncated — narrow the query or the time window for more)\n")
					}
					return b.String(), nil
				}
				return "", nil
			},
		},
		{
			Name: "list_env", Scope: scopes.Read,
			Description: "An app's environment variable NAMES (values are never listed; reveal_env needs secrets:read).",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				rows, err := env.Q.ListEnvVarsOfApp(ctx, db.ListEnvVarsOfAppParams{AppID: a.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("could not list variables")
				}
				if len(rows) == 0 {
					return a.Name + " has no environment variables.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					fmt.Fprintf(&b, "  %s  (updated %s)\n", r.Name, r.UpdatedAt.UTC().Format(time.RFC3339))
				}
				return b.String(), nil
			},
		},
		{
			Name: "list_team_domains", Scope: scopes.Read,
			Description: "Every app hostname of the team: hostname → app, TLS mode, verified or not. Proxy hosts are in list_proxy_hosts.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				rows, err := env.Q.TeamDomains(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list domains")
				}
				if len(rows) == 0 {
					return "No app has a domain. add_domain routes one.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					fmt.Fprintf(&b, "  %s → %s  tls %s  %s\n", r.Domain.Hostname, r.AppName, r.Domain.Tls,
						map[bool]string{true: "verified", false: "not verified"}[r.Domain.VerifiedAt != nil])
				}
				return b.String(), nil
			},
		},
		{
			Name: "list_domains", Scope: scopes.Read,
			Description: "An app's hostnames, TLS mode and verification record.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				rows, err := env.Q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("could not list domains")
				}
				if len(rows) == 0 {
					return a.Name + " has no domains. add_domain routes one.", nil
				}
				var b strings.Builder
				for _, d := range rows {
					fmt.Fprintf(&b, "  %s  tls %s  %s  (TXT %s = %s)\n", d.Hostname, d.Tls,
						map[bool]string{true: "verified", false: "not verified"}[d.VerifiedAt != nil], deploy.VerifyName(d.Hostname), deploy.VerifyValue(d.VerifyToken))
				}
				return b.String(), nil
			},
		},
		{
			Name: "app_analytics", Scope: scopes.Read,
			Description: "An app's traffic history from Caddy's access log: requests over time, status breakdown, top paths and referrers, unique visitors, p50/p95 latency.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg,
				"range": map[string]any{"type": "string", "enum": []string{"1h", "24h", "7d", "30d"}, "default": "1h"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				r, ok := traffic.Ranges[args.Str("range")]
				if !ok {
					r = traffic.Ranges["1h"]
				}
				s, err := traffic.AppSeries(ctx, env.Q, env.Caller.TeamID, a.ID, r, time.Now())
				if err != nil {
					return "", errors.New("could not read analytics")
				}
				if s.Totals.Requests == 0 {
					return fmt.Sprintf("%s has no traffic in the last %s.", a.Name, r.Key), nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%s — last %s\nrequests: %d · visitors: %d · error rate: %.1f%% · p95: %.0fms\n",
					a.Name, r.Key, s.Totals.Requests, s.Totals.Visitors, s.Totals.ErrorRate*100, s.Totals.P95Ms)
				if len(s.TopPaths) > 0 {
					b.WriteString("top paths:\n")
					for _, p := range s.TopPaths {
						fmt.Fprintf(&b, "  %-30s %d\n", p.Key, p.Count)
					}
				}
				if len(s.TopReferrers) > 0 {
					b.WriteString("top referrers:\n")
					for _, ref := range s.TopReferrers {
						fmt.Fprintf(&b, "  %-30s %d\n", ref.Key, ref.Count)
					}
				}
				return b.String(), nil
			},
		},

		// ---- deploy ----
		{
			Name: "deploy_app", Scope: scopes.Deploy,
			Description: "Deploy a ref (default: the app's branch): build, release, health-gate, switch, drain. With wait=true, returns when it has finished (up to wait_seconds).",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg, "ref": str("Branch, tag or full 40-character commit sha, e.g. one from list_app_commits (optional)"),
				"track": map[string]any{"type": "string", "enum": []string{"auto", "fast", "full"}, "default": "auto",
					"description": "Override the app's deploy configuration for this run only: auto defers to it, fast and full are stored as asked."},
				"wait": map[string]any{"type": "boolean", "default": false}, "wait_seconds": map[string]any{"type": "integer", "default": 600, "maximum": 1800}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueAndMaybeWait(ctx, env, args, func(s *deploy.Service, a db.App) (db.Deployment, error) {
					return s.QueueDeployment(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, "deploy", args.Str("ref"), nil, args.Str("track"))
				})
			},
		},
		{
			Name: "wait_deployment", Scope: scopes.Read,
			Description: "Follow a deployment until it finishes, fails or `until` passes, holding at most ~25 seconds per call. " +
				"While it still runs, the reply's last line is `continue: wait_deployment {…}`: call it again with those arguments.",
			InputSchema: schema([]string{"app", "deployment"}, map[string]any{"app": appArg, "deployment": str("Deployment number or id"),
				"until":        str("RFC 3339 time to stop following (from a continue: line); default wait_seconds from now"),
				"wait_seconds": map[string]any{"type": "integer", "default": 600, "maximum": 1800}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, d, err := findDeployment(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				until := time.Now().Add(time.Duration(args.Int("wait_seconds", 600, 10, 1800)) * time.Second)
				if v := args.Str("until"); v != "" {
					t, err := time.Parse(time.RFC3339, v)
					if err != nil {
						return "", errors.New("until must be an RFC 3339 time, e.g. 2026-09-25T12:00:00Z")
					}
					// A caller resumes a wait; it cannot stretch one past the longest a wait may run.
					if limit := time.Now().Add(1800 * time.Second); t.After(limit) {
						t = limit
					}
					until = t
				}
				env.Then = func(ctx context.Context) (string, error) {
					return waitDeployment(ctx, env, s, a, d, until)
				}
				return "", nil
			},
		},
		{
			Name: "cancel_deployment", Scope: scopes.Deploy,
			Description: "Cancel a waiting deployment, or stop a running one before it switches traffic.",
			InputSchema: schema([]string{"app", "deployment"}, map[string]any{"app": appArg, "deployment": str("Deployment number or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return "", err
				}
				_, d, err := findDeployment(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				return s.CancelDeployment(ctx, env.Q, env.Caller.TeamID, mcpActor(env), d)
			},
		},
		{
			Name: "rollback_app", Scope: scopes.Deploy,
			Description: "Re-point the app at a kept release (default: the previous one) without a build — seconds. With wait=true, returns when done.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg, "release": str("Release version (3 or v3) or id; default the previous release"),
				"wait": map[string]any{"type": "boolean", "default": false}, "wait_seconds": map[string]any{"type": "integer", "default": 300, "maximum": 1800}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueAndMaybeWait(ctx, env, args, func(s *deploy.Service, a db.App) (db.Deployment, error) {
					d, _, err := s.Rollback(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, args.Str("release"))
					return d, err
				})
			},
		},
		{
			Name: "restart_app", Scope: scopes.Deploy,
			Description: "Start the live release in fresh containers (new env values apply), health-gated like a deploy.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg, "wait": map[string]any{"type": "boolean", "default": false},
				"wait_seconds": map[string]any{"type": "integer", "default": 300, "maximum": 1800}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueAndMaybeWait(ctx, env, args, func(s *deploy.Service, a db.App) (db.Deployment, error) {
					return s.Restart(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a)
				})
			},
		},
		{
			Name: "pause_autodeploy", Scope: scopes.Deploy,
			Description: "Pause (or resume, paused=false) deploys from the app's deploy hook and the GitHub App. Manual deploys still work.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg, "paused": map[string]any{"type": "boolean", "default": true}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				paused := true
				if v, ok := args["paused"].(bool); ok {
					paused = v
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				if err := s.SetPaused(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, paused); err != nil {
					return "", err
				}
				if paused {
					return "Auto-deploy of " + a.Name + " is paused: the deploy hook answers 409 until it is resumed.", nil
				}
				return "Auto-deploy of " + a.Name + " is on.", nil
			},
		},

		{
			Name: "adopt_app_proposal", Scope: scopes.Read,
			Description: "Guessed fields to pre-fill adopt_app for an imported app: a repository, branch, build type and port, each with a confidence " +
				"(high/medium/low) and where it came from (a container label, a connected GitHub repo, a published port…) and a list of what could not be guessed.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				prop, err := s.Propose(ctx, env.Q, env.Caller.TeamID, a)
				if err != nil {
					return "", err
				}
				raw, _ := json.MarshalIndent(prop, "", "  ")
				return string(raw), nil
			},
		},
		{
			Name: "adopt_app", Scope: scopes.Deploy,
			Description: "Turn an imported app into a Hosbah-built git app in place — no rebuild is required to take it over. The running workload keeps " +
				"serving anything routed to it directly, even after the first real deploy (build → health gate → switch): move that traffic, then " +
				"confirm_adopted_workload_stopped stops it. Hosbah never stops it on its own.",
			InputSchema: schema([]string{"app", "repo_url"}, map[string]any{"app": appArg, "repo_url": str("github.com/owner/repo, or a full URL"),
				"branch": str("Default: main"), "build": map[string]any{"type": "string", "enum": []string{"auto", "dockerfile", "railpack"}, "default": "auto"},
				"port": map[string]any{"type": "integer", "description": "Default: the app's current port"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				updated, rel, err := s.Adopt(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, deploy.AdoptInput{
					RepoURL: args.Str("repo_url"), Branch: args.Str("branch"), Build: args.Str("build"), Port: args.Int("port", 0, 0, 65535)})
				if err != nil {
					return "", err
				}
				if rel != nil {
					return fmt.Sprintf("%s is now a Hosbah git app (release v0 points at its running image). Deploy it to build from git; the old container "+
						"keeps running until you move its traffic and call confirm_adopted_workload_stopped.", updated.Name), nil
				}
				return fmt.Sprintf("%s is now a Hosbah git app. Deploy it to build from git; once that deploy is live, confirm_adopted_workload_stopped "+
					"can stop the old workload.", updated.Name), nil
			},
		},
		{
			Name: "confirm_adopted_workload_stopped", Scope: scopes.Deploy,
			Description: "Stop the old workload of an adopted app, once its traffic has moved. Hosbah never stops one on its own — a container is " +
				"stopped and removed here (volumes kept), a guest powered off, and a unit cannot be controlled so this only clears the record. " +
				"Requires the first real deploy to already be live.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				updated, err := s.ConfirmAdoptedWorkloadStopped(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a)
				if err != nil {
					return "", err
				}
				return updated.Name + "'s old workload is confirmed stopped.", nil
			},
		},

		{
			Name: "connect_guest", Scope: scopes.Write,
			Description: "Put a Hosbah agent inside an imported Proxmox guest, instead of creating a git app on the hypervisor node (which would fail). " +
				"Creates a server for the guest nested under its node and asks the node to install the agent inside the guest with pct exec. Idempotent — " +
				"calling it again on an already-connected app returns the same server. Once that server's agent enrols, its own containers import on their own.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				row, inst, err := s.ConnectGuest(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a)
				if err != nil {
					return "", err
				}
				if inst == nil {
					return fmt.Sprintf("%s is already connected as server %s (%s).", a.Name, row.Name, row.ID), nil
				}
				return fmt.Sprintf("Connecting %s: server %s (%s) created, nested under its Proxmox node; the install command is running inside the "+
					"guest now (guest.enrol). It shows up once its own agent enrols.", a.Name, row.Name, row.ID), nil
			},
		},

		// ---- write ----
		{
			Name: "create_app", Scope: scopes.Write,
			Description: "Create an app from a public git URL on one of the team's servers. Returns its deploy-hook URL once. deploy=true queues the first deploy.",
			InputSchema: schema([]string{"name", "server", "repo_url"}, map[string]any{
				"name": str("Lowercase letters, digits, dashes"), "server": str("Server name or id"), "repo_url": str("Public https/http/git URL"),
				"branch": str("Branch (default main)"), "port": map[string]any{"type": "integer", "description": "Port the app listens on (default 8080)"},
				"healthcheck_path": str("Health-check path (default /)"), "deploy": map[string]any{"type": "boolean", "default": false},
				"allow_duplicate": map[string]any{"type": "boolean", "default": false,
					"description": "Build this anyway, even if the team already has an imported app by this name or repo (otherwise refused — connect_guest or adopt_app instead)"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				a, hook, err := s.CreateApp(ctx, env.Q, env.Caller.TeamID, mcpActor(env), deploy.CreateAppInput{Name: args.Str("name"), Server: args.Str("server"),
					RepoURL: args.Str("repo_url"), Branch: args.Str("branch"), Port: args.Int("port", 0, 0, 65535), HealthPath: args.Str("healthcheck_path"),
					AllowDuplicate: args.Bool("allow_duplicate")})
				if err != nil {
					return "", err
				}
				out := fmt.Sprintf("Created %s (id %s) on server %s from %s @ %s.\nDeploy hook (shown once — POST to it to deploy): %s\n", a.Name, a.ID, args.Str("server"), orDash(a.RepoURL), orDash(a.Branch), hook)
				if args.Bool("deploy") {
					d, err := s.QueueDeployment(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, "deploy", "", nil, "")
					if err != nil {
						return "", err
					}
					out += fmt.Sprintf("Deployment #%d queued (id %s).\n", d.Number, d.ID)
				}
				return out, nil
			},
		},
		{
			Name: "set_env", Scope: scopes.Write,
			Description: "Set an environment variable (sealed with the team key). Applies from the next deploy or restart.",
			InputSchema: schema([]string{"app", "name", "value"}, map[string]any{"app": appArg, "name": str("Variable name"), "value": str("Value (never echoed back)")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				value, _ := args["value"].(string) // not trimmed: whitespace can be meaningful
				if err := s.SetEnv(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, args.Str("name"), value); err != nil {
					return "", err
				}
				if o, oerr := opsOf(env); oerr == nil {
					_, _ = services.Detect(ctx, env.Q, o, env.Caller.TeamID, a)
				}
				return fmt.Sprintf("Set %s on %s. It applies from the next deploy or restart_app.", args.Str("name"), a.Name), nil
			},
		},
		{
			Name: "unset_env", Scope: scopes.Write,
			Description: "Remove an environment variable. Applies from the next deploy or restart.",
			InputSchema: schema([]string{"app", "name"}, map[string]any{"app": appArg, "name": str("Variable name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				if err := s.UnsetEnv(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, args.Str("name")); err != nil {
					return "", err
				}
				if o, oerr := opsOf(env); oerr == nil {
					_, _ = services.Detect(ctx, env.Q, o, env.Caller.TeamID, a)
				}
				return fmt.Sprintf("Removed %s from %s.", args.Str("name"), a.Name), nil
			},
		},
		{
			Name: "add_domain", Scope: scopes.Write,
			Description: "Route a hostname to the app (re-routed at once if a release is live). tls: auto (public certificate once verified), internal (Caddy's CA) or off (plain HTTP). A wildcard (*.example.com) routes every subdomain without a domain of its own, over tls off or internal only; its DNS is yours to point.",
			InputSchema: schema([]string{"app", "hostname"}, map[string]any{"app": appArg, "hostname": str("e.g. app.example.com, or *.example.com for every subdomain"),
				"tls":  map[string]any{"type": "string", "enum": []string{"auto", "internal", "off"}, "default": "auto"},
				"port": map[string]any{"type": "integer", "minimum": 0, "maximum": 65535, "description": "Imported apps: a container's published port (default the first) or the port a Proxmox guest's site listens on (required)"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				d, err := s.AddDomain(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, args.Str("hostname"), args.Str("tls"), args.Int("port", 0, 0, 65535))
				if err != nil {
					return "", err
				}
				if d.VerifiedAt != nil {
					return fmt.Sprintf("%s now routes to %s (tls %s), verified through the team's Cloudflare (DNS created).", d.Hostname, a.Name, d.Tls), nil
				}
				if agentproto.IsWildcardHost(d.Hostname) {
					return fmt.Sprintf("%s now routes every subdomain without a domain of its own to %s (tls %s). Its DNS and public TLS are yours: point the wildcard record at the server, or at the proxy in front of it.", d.Hostname, a.Name, d.Tls), nil
				}
				return fmt.Sprintf("%s now routes to %s (tls %s). To verify it, add a TXT record %s = %s and call verify_domain.",
					d.Hostname, a.Name, d.Tls, deploy.VerifyName(d.Hostname), deploy.VerifyValue(d.VerifyToken)), nil
			},
		},
		{
			Name: "verify_domain", Scope: scopes.Write,
			Description: "Check a domain's _hosbah-verify TXT record (a pre-rename _cloudy-verify record also counts). Verification lets Caddy issue a public certificate for it.",
			InputSchema: schema([]string{"app", "domain"}, map[string]any{"app": appArg, "domain": str("Hostname or domain id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				d, err := deploy.DomainByRef(ctx, env.Q, env.Caller.TeamID, a, args.Str("domain"))
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				_, msg, err := s.VerifyDomain(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, d)
				return msg, err
			},
		},

		// ---- secrets:read ----
		{
			Name: "reveal_env", Scope: scopes.SecretsRead,
			Description: "Reveal ONE environment variable's value. Audited with this token's id.",
			InputSchema: schema([]string{"app", "name"}, map[string]any{"app": appArg, "name": str("Variable name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityRevealSecrets); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				v, err := s.RevealEnv(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, args.Str("name"))
				if err != nil {
					return "", err
				}
				return args.Str("name") + "=" + v, nil
			},
		},

		// ---- destructive ----
		{
			Name: "delete_app", Scope: scopes.Destructive, Destructive: true, ResourceType: "app",
			Description: "Delete an app: its route, containers and images on the server, its env, domains, deployments and releases. confirm must equal the app's name.",
			InputSchema: schema([]string{"app", "confirm"}, map[string]any{"app": appArg, "confirm": str("The app's name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, a.Name); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				if err := s.DeleteApp(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a); err != nil {
					return "", err
				}
				return "Deleted " + a.Name + ". Its containers, route and images are removed from the server as soon as its agent is connected.", nil
			},
		},
		{
			Name: "remove_domain", Scope: scopes.Destructive, Destructive: true, ResourceType: "domain",
			Description: "Stop routing a hostname to an app. The app keeps running; only the route goes. confirm must equal the hostname.",
			InputSchema: schema([]string{"app", "domain", "confirm"}, map[string]any{"app": appArg,
				"domain":  str("Hostname or domain id"),
				"confirm": str("The hostname, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				d, err := deploy.DomainByRef(ctx, env.Q, env.Caller.TeamID, a, args.Str("domain"))
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, d.Hostname); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				if err := s.RemoveDomain(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, d); err != nil {
					return "", err
				}
				return fmt.Sprintf("%s no longer routes to %s. Caddy is updated as soon as the server's agent is connected.", d.Hostname, a.Name), nil
			},
		},
	}
}

// ---- helpers -----------------------------------------------------------------------------------------

func deploys(env *Env) (*deploy.Service, error) {
	if env.App == nil {
		return nil, errors.New("the deploy service is not running")
	}
	s, ok := env.App.Deploy.(*deploy.Service)
	if !ok || s == nil {
		return nil, errors.New("the deploy service is not running")
	}
	return s, nil
}

func mcpActor(env *Env) deploy.Actor {
	return deploy.Actor{UserID: env.Caller.UserID, Via: "mcp", TokenID: env.Caller.TokenID}
}

func findApp(ctx context.Context, env *Env, args Args) (db.App, error) {
	ref := args.Str("app")
	if ref == "" {
		return db.App{}, errors.New("app is required (a name or an id)")
	}
	return deploy.AppByRef(ctx, env.Q, env.Caller.TeamID, ref)
}

func findDeployment(ctx context.Context, env *Env, args Args) (db.App, db.Deployment, error) {
	a, err := findApp(ctx, env, args)
	if err != nil {
		return db.App{}, db.Deployment{}, err
	}
	d, err := deploy.DeploymentByRef(ctx, env.Q, env.Caller.TeamID, a, args.Str("deployment"))
	return a, d, err
}

func short(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	if len(*s) > 10 {
		return (*s)[:10]
	}
	return *s
}

func deploymentLine(d db.Deployment) string {
	line := fmt.Sprintf("#%-4d %-8s %-9s %-10s ref %-12s commit %s", d.Number, d.Kind, d.Status, orDash(d.Step), d.Ref, short(d.CommitSha))
	if d.Track != nil {
		line += fmt.Sprintf("  track %s (%s)", *d.Track, orDash(d.TrackReason))
	}
	if d.Error != nil {
		line += "  — " + *d.Error
	}
	return line
}

func formatLine(l ops.LogLine) string {
	at := l.At.UTC().Format("15:04:05")
	switch l.Kind {
	case "progress":
		return fmt.Sprintf("%s [%s] step %d/%d", at, l.Op, l.Step, l.Total)
	case "status":
		if l.Status == "running" {
			return fmt.Sprintf("%s [%s] started", at, l.Op)
		}
		return fmt.Sprintf("%s [%s] %s", at, l.Op, l.Line)
	}
	return fmt.Sprintf("%s [%s] %s", at, l.Op, l.Line)
}

// deployLines decrypts a deployment's log lines for the team.
func deployLines(ctx context.Context, a *app.App, q *db.Queries, teamID, deploymentID string, after int64, limit int32) ([]ops.LogLine, error) {
	rows, err := q.DeploymentLogsAfter(ctx, db.DeploymentLogsAfterParams{DeploymentID: deploymentID, TeamID: teamID, Seq: after, Limit: limit})
	if err != nil {
		return nil, errors.New("could not read the log")
	}
	if len(rows) == 0 {
		return nil, nil
	}
	t, err := q.GetTeam(ctx, teamID)
	if err != nil {
		return nil, errors.New("team not found")
	}
	dek, err := a.Keys.UnwrapDEK(teamID, t.DekWrapped)
	if err != nil {
		return nil, errors.New("team key unavailable")
	}
	out := make([]ops.LogLine, 0, len(rows))
	for _, r := range rows {
		l, err := ops.OpenDeployLog(dek, teamID, r)
		if err != nil {
			l = ops.LogLine{Seq: r.Seq, Kind: "log", Line: "(this line could not be decrypted)", At: r.CreatedAt}
		}
		out = append(out, l)
	}
	return out, nil
}

// lastDeployLines is the tail of a deployment's log (for a failure report).
func lastDeployLines(ctx context.Context, s *deploy.Service, q *db.Queries, teamID, deploymentID string, n int64) ([]ops.LogLine, error) {
	d, err := q.DeploymentInTeam(ctx, db.DeploymentInTeamParams{ID: deploymentID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	after := d.LastSeq - n
	if after < 0 {
		after = 0
	}
	return deployLines(ctx, s.App(), q, teamID, deploymentID, after, int32(n))
}

// queueAndMaybeWait queues through fn inside the tool's transaction; with wait, a Then hook follows
// the deployment after the commit (outside any transaction) and reports how it ended.
func queueAndMaybeWait(ctx context.Context, env *Env, args Args, fn func(*deploy.Service, db.App) (db.Deployment, error)) (string, error) {
	if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
		return "", err
	}
	a, err := findApp(ctx, env, args)
	if err != nil {
		return "", err
	}
	s, err := deploys(env)
	if err != nil {
		return "", err
	}
	d, err := fn(s, a)
	if err != nil {
		return "", err
	}
	out := fmt.Sprintf("%s #%d of %s queued (id %s, ref %s).\n", strings.ToUpper(d.Kind[:1])+d.Kind[1:], d.Number, a.Name, d.ID, d.Ref)
	if d.Track != nil {
		out += fmt.Sprintf("Track: %s (%s).\n", *d.Track, orDash(d.TrackReason))
	}
	if !args.Bool("wait") {
		return out + "Follow it with get_deployment / deploy_logs.", nil
	}
	until := time.Now().Add(time.Duration(args.Int("wait_seconds", 600, 10, 1800)) * time.Second)
	env.Then = func(ctx context.Context) (string, error) {
		return waitDeployment(ctx, env, s, a, d, until)
	}
	return out, nil
}

// WaitHold is the longest one MCP request follows a deployment. Every hop in front of the API
// (the web app's proxy at 30s, NPM at 60s, Cloudflare at 100s) drops a request that stays silent
// longer, and the caller then sees a bare HTTP 500 while the deployment runs on. Past the hold the
// reply names the wait_deployment call that picks up where it stopped.
var WaitHold = 25 * time.Second

// ContinuePrefix starts the last line of a reply that is not finished yet: the tool and the JSON
// arguments to call next. The SDKs and the CLI follow it on their own; a model reads it.
const ContinuePrefix = "continue: "

func waitDeployment(ctx context.Context, env *Env, s *deploy.Service, a db.App, d db.Deployment, until time.Time) (string, error) {
	wake, unsub := s.Ops().Broker().Subscribe(ops.DeployTopic(d.ID))
	defer unsub()
	hold := time.Now().Add(WaitHold)
	started := time.Now()
	for {
		var cur db.Deployment
		var tail []ops.LogLine
		err := tenancy.Tx(ctx, env.App.SQLDB, env.Scope, func(q *db.Queries, _ *sql.Tx) error {
			var err error
			if cur, err = q.DeploymentInTeam(ctx, db.DeploymentInTeamParams{ID: d.ID, TeamID: env.Caller.TeamID}); err != nil {
				return err
			}
			if cur.Status == "failed" {
				tail, _ = lastDeployLines(ctx, s, q, env.Caller.TeamID, d.ID, 25)
			}
			return nil
		})
		if err != nil {
			return "", errors.New("the deployment disappeared")
		}
		switch cur.Status {
		case "succeeded":
			return fmt.Sprintf("✓ #%d succeeded in %s.\n", cur.Number, time.Since(started).Round(time.Second)), nil
		case "cancelled":
			return fmt.Sprintf("#%d was cancelled.\n", cur.Number), nil
		case "failed":
			var b strings.Builder
			fmt.Fprintf(&b, "✗ #%d failed: %s\nLast log lines:\n", cur.Number, orDash(cur.Error))
			for _, l := range tail {
				b.WriteString("  " + formatLine(l) + "\n")
			}
			return "", errors.New(b.String())
		}
		now := time.Now()
		if now.After(until) {
			return fmt.Sprintf("Still %s (step %s) and the wait ran out: follow it with get_deployment.\n", cur.Status, orDash(cur.Step)), nil
		}
		if now.After(hold) {
			next, _ := json.Marshal(map[string]any{"app": a.Name, "deployment": fmt.Sprint(cur.Number), "until": until.UTC().Format(time.RFC3339)})
			return fmt.Sprintf("#%d is %s (step %s).\n%swait_deployment %s\n", cur.Number, cur.Status, orDash(cur.Step), ContinuePrefix, next), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-wake:
		case <-time.After(3 * time.Second):
		}
	}
}
