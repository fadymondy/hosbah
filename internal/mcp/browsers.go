package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// BrowserTools: the "Browsers" section of an app — headless Chromium instances the site drives over
// CDP/WebSocket. Same actions as the REST API (internal/dbsvc/browsers.go). Never a token.
func BrowserTools() []Tool {
	appArg := str("App name or id")
	num := func(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
	n64 := func(args Args, k string) int64 { f, _ := args[k].(float64); return int64(f) }

	return []Tool{
		{
			Name: "list_app_browsers", Scope: scopes.Read,
			Description: "An app's headless browsers: name, image, limits, the variables each set on the app, status, and — when the server " +
				"answers — whether it runs, its restarts and memory. Never a token.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				browsers, err := dbsvc.ListBrowsers(ctx, env.Q, env.Caller.TeamID, a)
				if err != nil {
					return "", err
				}
				text := func() string {
					if len(browsers) == 0 {
						return "No browsers. create_app_browser adds one.\n"
					}
					var b strings.Builder
					for _, k := range browsers {
						fmt.Fprintf(&b, "browser %s  %s  %d MiB, %g CPU, %d sessions, idle %d s  %s  sets %s", k.Name, k.Image, k.MemoryMB, k.CPUs,
							k.MaxSessions, k.IdleTimeoutSeconds, k.Status, strings.Join(k.Env, ", "))
						if k.Running != nil {
							fmt.Fprintf(&b, "  running=%t restarts=%d", *k.Running, *k.Restarts)
						}
						if k.MemoryBytes != nil {
							fmt.Fprintf(&b, " memory=%d bytes", *k.MemoryBytes)
						}
						if k.Error != nil {
							fmt.Fprintf(&b, "  error: %s", *k.Error)
						}
						b.WriteString("\n")
					}
					return b.String()
				}
				s, serr := dbsOf(env)
				dp, derr := deploys(env)
				if serr != nil || derr != nil {
					return text(), nil
				}
				op := s.QueueBrowserStatus(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, browsers)
				if op.ID == "" {
					return text(), nil
				}
				env.Then = func(ctx context.Context) (string, error) { // after the commit; best effort
					var res agentproto.BrowserStatusResult
					if _, err := dp.AwaitResult(ctx, op, dbsvc.BrowserStatusWait, &res); err == nil {
						dbsvc.WithBrowserStatus(browsers, res)
					}
					return text(), nil
				}
				return "", nil
			},
		},
		{
			Name: "create_app_browser", Scope: scopes.Write,
			Description: "Add a headless Chromium to an app, on the app's server, and set <PREFIX>_WS_ENDPOINT and <PREFIX>_TOKEN on the app as " +
				"sealed variables (next deploy or restart). The image is fixed by Hosbah; the browser is reachable from the app's containers " +
				"only, and only with the token. Refused when one of those names is already set, or the app has 3. The token is never shown. " +
				"Owner or admin.",
			InputSchema: schema([]string{"app", "name"}, map[string]any{"app": appArg,
				"name":                 str("Lowercase letter, then up to 30 lowercase letters, digits and dashes; unique in the app"),
				"memory_mb":            num("256 to 4096 (default 1024)"),
				"cpus":                 num("0.25 to 4 (default 1)"),
				"max_sessions":         num("Concurrent sessions, 1 to 20 (default 5)"),
				"idle_timeout_seconds": num("A session silent this long is closed, 30 to 3600 (default 300)"),
				"env_prefix":           str("Prefix of the variables (default BROWSER): an upper-case letter, then upper-case letters, digits and _")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				cpus, _ := args["cpus"].(float64)
				b, op, err := s.CreateBrowser(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, dbsvc.BrowserSpec{Name: args.Str("name"),
					MemoryMB: n64(args, "memory_mb"), CPUs: cpus, MaxSessions: n64(args, "max_sessions"),
					IdleTimeoutSeconds: n64(args, "idle_timeout_seconds"), EnvPrefix: args.Str("env_prefix")})
				if err != nil {
					return "", err
				}
				teamID := env.Caller.TeamID
				env.Then = func(context.Context) (string, error) { // after the commit
					s.FollowBrowser(teamID, b.ID, op)
					return "", nil
				}
				return fmt.Sprintf("Creating browser %s (operation %s; a first image pull takes minutes). %s gets %s on its next deploy or restart; list_app_browsers shows when it is ready.",
					b.Name, op.ID, a.Name, strings.Join(b.Env, ", ")), nil
			},
		},
		{
			Name: "remove_app_browser", Scope: scopes.Write,
			Description: "Remove a browser from an app: its container is stopped and removed and exactly the variables it set are unset. " +
				"confirm must equal the browser's name. Owner or admin.",
			InputSchema: schema([]string{"app", "browser", "confirm"}, map[string]any{"app": appArg,
				"browser": str("Browser name or id"),
				"confirm": str("The browser's name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := dbsvc.RefuseImportedBrowsers(a); err != nil {
					return "", err
				}
				b, err := dbsvc.FindBrowser(ctx, env.Q, env.Caller.TeamID, a, args.Str("browser"))
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, b.Name); err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				actor, teamID := mcpActor(env), env.Caller.TeamID
				rm, err := s.BeginBrowserRemoval(ctx, env.Q, teamID, actor, a, b, b.Name)
				if err != nil {
					return "", err
				}
				env.Then = func(ctx context.Context) (string, error) { // after the commit
					failure := s.AwaitBrowserRemoval(ctx, rm)
					fin := context.WithoutCancel(ctx)
					if err := tenancy.Tx(fin, env.App.SQLDB, env.Scope, func(q *db.Queries, _ *sql.Tx) error {
						return s.FinishBrowserRemoval(fin, q, teamID, actor, a, rm, failure)
					}); err != nil {
						return "", err
					}
					if failure != "" {
						return "", dbsvc.BrowserRemovalRefused(failure)
					}
					return "Removed browser " + b.Name + " from " + a.Name + ": its container is gone and " +
						strings.Join(dbsvc.BrowserEnvNames(b.EnvPrefix), ", ") + " are unset (next deploy or restart).", nil
				}
				return "", nil
			},
		},
	}
}
