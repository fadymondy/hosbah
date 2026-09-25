package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/processes"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ProcessTools: the "Workers" section of an app — status, restart, stop and start of one or every
// process of its live release, through the agent's app.processes operation (internal/processes).
func ProcessTools() []Tool {
	appArg := str("App name or id")
	processArg := str("web or worker:<name>, as get_app_storage or the app's deploy config shows it")

	run := func(ctx context.Context, env *Env, args Args, then func(context.Context, *processes.Service, processes.InTeam, db.App) (string, error)) (string, error) {
		if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
			return "", err
		}
		a, err := findApp(ctx, env, args)
		if err != nil {
			return "", err
		}
		svc := processes.From(env.App)
		if svc == nil {
			return "", errors.New("operations are not available on this control plane")
		}
		rounds := func(ctx context.Context, fn processes.Round) error {
			return tenancy.Tx(ctx, env.App.SQLDB, env.Scope, func(q *db.Queries, _ *sql.Tx) error {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return err
				}
				cur, err := deploy.AppByRef(ctx, q, env.Caller.TeamID, a.ID)
				if err != nil {
					return err
				}
				return fn(q, env.Caller.TeamID, mcpActor(env), cur)
			})
		}
		env.Then = func(ctx context.Context) (string, error) { return then(ctx, svc, rounds, a) }
		return "", nil
	}

	render := func(a db.App, res processes.Result) string {
		if len(res.Processes.Processes) == 0 {
			return a.Name + " has no processes running (operation " + res.Operation + ").\n"
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s (operation %s):\n", a.Name, res.Operation)
		for _, p := range res.Processes.Processes {
			fmt.Fprintf(&b, "  %-16s %-12s %-9s restarts %d", p.Process, p.Container, p.State, p.RestartCount)
			if p.State == "running" {
				fmt.Fprintf(&b, "  cpu %.1f%%  mem %s/%s", p.CPUPercent, humanBytes(p.MemBytes), humanBytes(p.MemLimitBytes))
			}
			if p.StartedAt != "" {
				fmt.Fprintf(&b, "  started %s", p.StartedAt)
			}
			b.WriteString("\n")
		}
		return b.String()
	}

	return []Tool{
		{
			Name: "app_processes", Scope: scopes.Read,
			Description: "An app's processes — web and every worker of its live release: each container's state, uptime, restart count and, " +
				"while running, live CPU and memory use. Owner, admin or deployer.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return run(ctx, env, args, func(ctx context.Context, svc *processes.Service, in processes.InTeam, a db.App) (string, error) {
					res, err := svc.Status(ctx, in, "")
					if err != nil {
						return "", err
					}
					return render(a, res), nil
				})
			},
		},
		{
			Name: "app_process_action", Scope: scopes.Write,
			Description: "Restart, stop or start one process of an app — or, for restart alone, every process at once (process left empty). " +
				"Stop and start are refused for web: stopping it takes the site down; only a worker process may be stopped or started here. " +
				"stop does not remove the container, so start reuses it. Owner, admin or deployer. Audited.",
			InputSchema: schema([]string{"app", "action"}, map[string]any{"app": appArg, "process": processArg,
				"action": map[string]any{"type": "string", "enum": []string{"restart", "stop", "start"}}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return run(ctx, env, args, func(ctx context.Context, svc *processes.Service, in processes.InTeam, a db.App) (string, error) {
					res, err := svc.Act(ctx, in, args.Str("action"), args.Str("process"))
					if err != nil {
						return "", err
					}
					return render(a, res), nil
				})
			},
		},
	}
}

func humanBytes(n int64) string {
	if n <= 0 {
		return "0B"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for n2 := n / unit; n2 >= unit; n2 /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
