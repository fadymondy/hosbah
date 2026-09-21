package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/schedules"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ScheduleTools: the "Schedules" section of an app — cron jobs that run inside its live release.
func ScheduleTools() []Tool {
	appArg := str("App name or id")
	schedArg := str("Schedule name or id")
	num := func(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
	settings := func(extra map[string]any) map[string]any {
		props := map[string]any{
			"app":             appArg,
			"cron":            str("5 fields: minute hour day-of-month month day-of-week, e.g. */5 * * * *"),
			"timezone":        str("IANA zone the expression is read in (default UTC)"),
			"command":         str("A shell line (1 to 1000 characters, one line). Runs as sh -lc inside the live release's web container"),
			"timeout_seconds": num("10 to 3600 (default 300); the command is killed after it"),
			"enabled":         map[string]any{"type": "boolean", "description": "Switch the schedule on or off (default on)"},
		}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}
	input := func(args Args) (schedules.Input, error) {
		var in schedules.Input
		for k, dst := range map[string]**string{"name": &in.Name, "cron": &in.Cron, "timezone": &in.Timezone, "command": &in.Command} {
			if v, ok := args[k].(string); ok {
				*dst = ptrTo(v)
			}
		}
		if v, ok := args["timeout_seconds"].(float64); ok {
			if v != float64(int(v)) {
				return in, errors.New("timeout_seconds must be a whole number")
			}
			in.TimeoutSeconds = ptrTo(int(v))
		}
		if v, ok := args["enabled"].(bool); ok {
			in.Enabled = &v
		}
		return in, nil
	}
	service := func(env *Env) *schedules.Service {
		if env.App == nil {
			return nil
		}
		s, _ := env.App.Schedules.(*schedules.Service)
		return s
	}
	clock := func(env *Env) time.Time {
		if s := service(env); s != nil {
			return s.Now()
		}
		return time.Now()
	}

	return []Tool{
		{
			Name: "list_app_schedules", Scope: scopes.Read,
			Description: "An app's schedules (cron jobs): expression, timezone, command, when each fires next and how its last run ended.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				list, err := schedules.List(ctx, env.Q, env.Caller.TeamID, a, clock(env))
				if err != nil {
					return "", err
				}
				if len(list) == 0 {
					return a.Name + " has no schedules. create_app_schedule adds one.", nil
				}
				var b strings.Builder
				for _, s := range list {
					b.WriteString(scheduleLine(s))
				}
				return b.String(), nil
			},
		},
		{
			Name: "list_schedule_runs", Scope: scopes.Read,
			Description: "The runs of one schedule, newest first: trigger, status (queued, running, succeeded, failed, timeout, skipped), " +
				"exit code, duration and error. What a run printed is not included.",
			InputSchema: schema([]string{"app", "schedule"}, map[string]any{"app": appArg, "schedule": schedArg,
				"limit":  num("How many (1 to 100, default 20)"),
				"before": str("next cursor of the previous page")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := schedules.Find(ctx, env.Q, env.Caller.TeamID, a, args.Str("schedule"))
				if err != nil {
					return "", err
				}
				f, err := schedules.Runs(ctx, env.Q, s, args.Int("limit", 20, 1, 100), args.Str("before"))
				if err != nil {
					return "", err
				}
				if len(f.Runs) == 0 {
					return s.Name + " has not run yet.", nil
				}
				var b strings.Builder
				for _, r := range f.Runs {
					b.WriteString(runLine(r))
				}
				if f.NextCursor != nil {
					fmt.Fprintf(&b, "next page: before=%s\n", *f.NextCursor)
				}
				return b.String(), nil
			},
		},
		{
			Name: "create_app_schedule", Scope: scopes.Write,
			Description: "Add a cron job to a git-deployed app: command runs as sh -lc inside the live release's web container (its " +
				"environment, no TTY) whenever cron fires in timezone, and is killed after timeout_seconds. A fire while the previous run " +
				"is still going is recorded as skipped. Imported apps are refused. Owner or admin.",
			InputSchema: schema([]string{"app", "name", "cron", "command"}, settings(map[string]any{"name": str("Unique in the app, up to 80 characters")})),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				in, err := input(args)
				if err != nil {
					return "", err
				}
				for _, p := range []**string{&in.Name, &in.Cron, &in.Command} {
					if *p == nil {
						*p = ptrTo("")
					}
				}
				s, err := schedules.Create(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, in, clock(env))
				if err != nil {
					return "", err
				}
				return "Created.\n" + scheduleLine(s), nil
			},
		},
		{
			Name: "update_app_schedule", Scope: scopes.Write,
			Description: "Change a schedule; only the fields given change. A new expression or timezone, or switching it back on, " +
				"recomputes when it fires next; switching it off clears it. Owner or admin.",
			InputSchema: schema([]string{"app", "schedule"}, settings(map[string]any{"schedule": schedArg, "name": str("A new name")})),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				cur, err := schedules.Find(ctx, env.Q, env.Caller.TeamID, a, args.Str("schedule"))
				if err != nil {
					return "", err
				}
				in, err := input(args)
				if err != nil {
					return "", err
				}
				s, err := schedules.Update(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, cur, in, clock(env))
				if err != nil {
					return "", err
				}
				return "Updated.\n" + scheduleLine(s), nil
			},
		},
		{
			Name: "delete_app_schedule", Scope: scopes.Write,
			Description: "Remove a schedule of an app, with its runs. Owner or admin.",
			InputSchema: schema([]string{"app", "schedule"}, map[string]any{"app": appArg, "schedule": schedArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				cur, err := schedules.Find(ctx, env.Q, env.Caller.TeamID, a, args.Str("schedule"))
				if err != nil {
					return "", err
				}
				if err := schedules.Delete(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, cur); err != nil {
					return "", err
				}
				return "Deleted " + cur.Name + ".", nil
			},
		},
		{
			Name: "run_app_schedule", Scope: scopes.Write,
			Description: "Run a schedule now (also while it is disabled), inside the app's live release. Returns the queued run; " +
				"list_schedule_runs shows how it ended. Refused while a run of it is still going, when nothing is live, and for " +
				"imported apps. Owner, admin or deployer.",
			InputSchema: schema([]string{"app", "schedule"}, map[string]any{"app": appArg, "schedule": schedArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return "", err
				}
				svc := service(env)
				if svc == nil {
					return "", errors.New("schedules are not available on this control plane")
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				cur, err := schedules.Find(ctx, env.Q, env.Caller.TeamID, a, args.Str("schedule"))
				if err != nil {
					return "", err
				}
				run, err := svc.RunNow(ctx, env.Q, mcpActor(env), a, cur)
				if err != nil {
					return "", err
				}
				// The tool's transaction commits after it returns; Follow waits for the run to appear.
				svc.Follow(run)
				return "Queued.\n" + runLine(schedules.RunView(run)), nil
			},
		},
	}
}

func scheduleLine(s schedules.Schedule) string {
	var b strings.Builder
	state := "disabled"
	if s.Enabled && s.NextRunAt != nil {
		state = "next " + s.NextRunAt.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "  %s  %-24s %-16s %-14s %s  timeout %d s  $ %s", s.ID, s.Name, s.Cron, s.Timezone, state, s.TimeoutSeconds, s.Command)
	if s.LastRun != nil {
		fmt.Fprintf(&b, "  last run %s: %s", s.LastRun.StartedAt.UTC().Format(time.RFC3339), s.LastRun.Status)
	}
	b.WriteString("\n")
	return b.String()
}

func runLine(r schedules.Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %s  %s  %-8s %-9s", r.ID, r.StartedAt.UTC().Format(time.RFC3339), r.Trigger, r.Status)
	if r.ExitCode != nil {
		fmt.Fprintf(&b, "  exit %d", *r.ExitCode)
	}
	if r.DurationMs != nil {
		fmt.Fprintf(&b, "  %d ms", *r.DurationMs)
	}
	if r.Error != "" {
		fmt.Fprintf(&b, "  %s", r.Error)
	}
	b.WriteString("\n")
	return b.String()
}
