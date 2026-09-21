package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/queues"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// QueueTools: the "Queues" section of an app — its job queues and failed jobs, read live inside its
// live release through the framework's own CLI (a fixed argv; see internal/queues).
func QueueTools() []Tool {
	appArg := str("App name or id")
	jobArg := str("The failed job's id, as list_app_failed_jobs shows it")

	// run checks the role and resolves the app in the tool's own transaction (so a stranger's "not
	// found" and a viewer's refusal come first), then does the work after it: every round of
	// internal/queues is a transaction of its own, because a queued command is only sent once the
	// transaction that queued it has committed.
	run := func(ctx context.Context, env *Env, args Args, ability string, then func(context.Context, *queues.Service, queues.InTeam, db.App) (string, error)) (string, error) {
		if err := needs(ctx, env, ability); err != nil {
			return "", err
		}
		a, err := findApp(ctx, env, args)
		if err != nil {
			return "", err
		}
		svc := queues.From(env.App)
		if svc == nil {
			return "", errors.New("operations are not available on this control plane")
		}
		rounds := func(ctx context.Context, fn queues.Round) error {
			return tenancy.Tx(ctx, env.App.SQLDB, env.Scope, func(q *db.Queries, _ *sql.Tx) error {
				if err := needs(ctx, env, ability); err != nil {
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
	action := func(name, act, desc string, withJob bool) Tool {
		props, required := map[string]any{"app": appArg}, []string{"app"}
		if withJob {
			props["job"], required = jobArg, append(required, "job")
		}
		return Tool{Name: name, Scope: scopes.Write, Description: desc + " Laravel apps only; runs inside the live release and waits for it. Owner or admin. Audited.",
			InputSchema: schema(required, props),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return run(ctx, env, args, tenancy.AbilityManageApps, func(ctx context.Context, svc *queues.Service, in queues.InTeam, _ db.App) (string, error) {
					res, err := svc.Act(ctx, in, act, args.Str("job"))
					if err != nil {
						return "", err
					}
					return fmt.Sprintf("Done (operation %s).\n%s\n", res.Operation, res.OutputTail), nil
				})
			}}
	}

	return []Tool{
		{
			Name: "get_app_queues", Scope: scopes.Read,
			Description: "An app's job queues, read live inside its live release: the detected driver (laravel, celery, bullmq, asynq or none) " +
				"with how sure that is and why, and per queue what is pending, running and failed. Counts that cannot be read dependably " +
				"are shown as ?, never guessed; bullmq and asynq are detected only. Owner, admin or deployer.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return run(ctx, env, args, tenancy.AbilityDeploy, func(ctx context.Context, svc *queues.Service, in queues.InTeam, a db.App) (string, error) {
					ov, err := svc.Overview(ctx, in)
					if err != nil {
						return "", err
					}
					var b strings.Builder
					fmt.Fprintf(&b, "%s: driver %s (%s)\n", a.Name, ov.Driver, ov.Confidence)
					for _, e := range ov.Evidence {
						fmt.Fprintf(&b, "  because %s\n", e)
					}
					if !ov.Readable {
						fmt.Fprintf(&b, "not readable: %s\n", orDash(ov.Reason))
						return b.String(), nil
					}
					if ov.Horizon != nil {
						if ov.Horizon.Installed {
							fmt.Fprintf(&b, "horizon: %s\n", orDash(ov.Horizon.Status))
						} else {
							b.WriteString("horizon: not installed\n")
						}
					}
					for _, q := range ov.Queues {
						fmt.Fprintf(&b, "  %-30s pending %s  running %s  failed %s\n", q.Name, count(q.Pending), count(q.Running), count(q.Failed))
					}
					fmt.Fprintf(&b, "failed jobs in total: %s\n", count(ov.FailedTotal))
					return b.String(), nil
				})
			},
		},
		{
			Name: "list_app_failed_jobs", Scope: scopes.Read,
			Description: "An app's failed jobs (Laravel): id, connection and queue, job name, when it failed and the first line of the " +
				"exception. Never a job's payload. Owner, admin or deployer.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg,
				"limit": map[string]any{"type": "integer", "default": queues.DefaultFailedLimit, "maximum": queues.MaxFailedLimit}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return run(ctx, env, args, tenancy.AbilityDeploy, func(ctx context.Context, svc *queues.Service, in queues.InTeam, a db.App) (string, error) {
					page, err := svc.Failed(ctx, in, args.Int("limit", queues.DefaultFailedLimit, 1, queues.MaxFailedLimit))
					if err != nil {
						return "", err
					}
					if len(page.Jobs) == 0 {
						return a.Name + " has no failed jobs.", nil
					}
					var b strings.Builder
					for _, j := range page.Jobs {
						fmt.Fprintf(&b, "  %s  %s  %s@%s  %s", j.ID, j.FailedAt, j.Connection, j.Queue, j.Name)
						if j.Exception != nil {
							fmt.Fprintf(&b, "  — %s", *j.Exception)
						}
						b.WriteString("\n")
					}
					if page.Truncated {
						b.WriteString("  (more exist)\n")
					}
					return b.String(), nil
				})
			},
		},
		action("retry_app_failed_job", queues.ActionRetry, "Put one failed job of an app back on its queue (php artisan queue:retry <id>).", true),
		action("forget_app_failed_job", queues.ActionForget, "Delete one failed job of an app (php artisan queue:forget <id>).", true),
		action("retry_all_app_failed_jobs", queues.ActionRetryAll, "Put every failed job of an app back on its queue (php artisan queue:retry all).", false),
		action("flush_app_failed_jobs", queues.ActionFlush, "Delete every failed job of an app (php artisan queue:flush); they cannot be retried afterwards.", false),
	}
}

func count(n *int64) string {
	if n == nil {
		return "?"
	}
	return fmt.Sprintf("%d", *n)
}
