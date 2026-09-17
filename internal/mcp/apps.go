package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/cloudy/internal/app"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/deploy"
	"github.com/fadymondy/cloudy/internal/ops"
	"github.com/fadymondy/cloudy/internal/scopes"
	"github.com/fadymondy/cloudy/internal/tenancy"
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
				fmt.Fprintf(&b, "%s  (id %s)\nstatus: %s\nrepository: %s @ %s\nruntime: %s\nport: %d · health check GET %s within %ds\nauto-deploy: %s\n",
					a.Name, a.ID, a.Status, a.RepoURL, a.Branch, orDash(a.Runtime), a.Port, a.HealthcheckPath, a.HealthcheckTimeout,
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
			Description: "An app's deployments, newest first: number, kind, status, step, commit, error.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg, "limit": map[string]any{"type": "integer", "default": 20, "maximum": 100}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
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
					fmt.Fprintf(&b, "  %s  tls %s  %s  (TXT _cloudy-verify.%s = %s)\n", d.Hostname, d.Tls,
						map[bool]string{true: "verified", false: "not verified"}[d.VerifiedAt != nil], d.Hostname, d.VerifyToken)
				}
				return b.String(), nil
			},
		},

		// ---- deploy ----
		{
			Name: "deploy_app", Scope: scopes.Deploy,
			Description: "Deploy a ref (default: the app's branch): build, release, health-gate, switch, drain. With wait=true, returns when it has finished (up to wait_seconds).",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg, "ref": str("Branch, tag or commit (optional)"),
				"wait": map[string]any{"type": "boolean", "default": false}, "wait_seconds": map[string]any{"type": "integer", "default": 600, "maximum": 1800}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return queueAndMaybeWait(ctx, env, args, func(s *deploy.Service, a db.App) (db.Deployment, error) {
					return s.QueueDeployment(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, "deploy", args.Str("ref"), nil)
				})
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

		// ---- write ----
		{
			Name: "create_app", Scope: scopes.Write,
			Description: "Create an app from a public git URL on one of the team's servers. Returns its deploy-hook URL once. deploy=true queues the first deploy.",
			InputSchema: schema([]string{"name", "server", "repo_url"}, map[string]any{
				"name": str("Lowercase letters, digits, dashes"), "server": str("Server name or id"), "repo_url": str("Public https/http/git URL"),
				"branch": str("Branch (default main)"), "port": map[string]any{"type": "integer", "description": "Port the app listens on (default 8080)"},
				"healthcheck_path": str("Health-check path (default /)"), "deploy": map[string]any{"type": "boolean", "default": false}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				a, hook, err := s.CreateApp(ctx, env.Q, env.Caller.TeamID, mcpActor(env), deploy.CreateAppInput{Name: args.Str("name"), Server: args.Str("server"),
					RepoURL: args.Str("repo_url"), Branch: args.Str("branch"), Port: args.Int("port", 0, 0, 65535), HealthPath: args.Str("healthcheck_path")})
				if err != nil {
					return "", err
				}
				out := fmt.Sprintf("Created %s (id %s) on server %s from %s @ %s.\nDeploy hook (shown once — POST to it to deploy): %s\n", a.Name, a.ID, args.Str("server"), a.RepoURL, a.Branch, hook)
				if args.Bool("deploy") {
					d, err := s.QueueDeployment(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, "deploy", "", nil)
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
				return fmt.Sprintf("Removed %s from %s.", args.Str("name"), a.Name), nil
			},
		},
		{
			Name: "add_domain", Scope: scopes.Write,
			Description: "Route a hostname to the app (re-routed at once if a release is live). tls: auto (public certificate once verified), internal (Caddy's CA) or off (plain HTTP).",
			InputSchema: schema([]string{"app", "hostname"}, map[string]any{"app": appArg, "hostname": str("e.g. app.example.com"),
				"tls": map[string]any{"type": "string", "enum": []string{"auto", "internal", "off"}, "default": "auto"}}),
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
				d, err := s.AddDomain(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, args.Str("hostname"), args.Str("tls"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%s now routes to %s (tls %s). To verify it, add a TXT record _cloudy-verify.%s = %s and call verify_domain.",
					d.Hostname, a.Name, d.Tls, d.Hostname, d.VerifyToken), nil
			},
		},
		{
			Name: "verify_domain", Scope: scopes.Write,
			Description: "Check a domain's _cloudy-verify TXT record. Verification lets Caddy issue a public certificate for it.",
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
			Name: "delete_app", Scope: scopes.Destructive, Destructive: true,
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
	if !args.Bool("wait") {
		return out + "Follow it with get_deployment / deploy_logs.", nil
	}
	limit := time.Duration(args.Int("wait_seconds", 600, 10, 1800)) * time.Second
	env.Then = func(ctx context.Context) (string, error) {
		return waitDeployment(ctx, env, s, d, limit)
	}
	return out, nil
}

func waitDeployment(ctx context.Context, env *Env, s *deploy.Service, d db.Deployment, limit time.Duration) (string, error) {
	wake, unsub := s.Ops().Broker().Subscribe(ops.DeployTopic(d.ID))
	defer unsub()
	deadline := time.Now().Add(limit)
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
		if time.Now().After(deadline) {
			return fmt.Sprintf("Still %s (step %s) after %s — follow it with get_deployment.\n", cur.Status, orDash(cur.Step), limit), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-wake:
		case <-time.After(3 * time.Second):
		}
	}
}
