package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/catalog"
	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// InstallTools: "Apps & stacks" — the built-in catalog of recipes and the recipes installed next to
// an app. Same actions as the REST API (internal/dbsvc/installs.go). Never a secret.
func InstallTools() []Tool {
	appArg := str("App name or id")

	return []Tool{
		{
			Name: "list_catalog", Scope: scopes.Read,
			Description: "The built-in catalog of recipes: apps (one pinned image installed next to a site with install_recipe) and stacks " +
				"(a template for a new app: repository, branch, variables, hosbah.toml). \"not yet verified\" means Hosbah has not run that " +
				"recipe end to end.",
			InputSchema: schema(nil, map[string]any{"kind": str("app or stack (default: both)"), "q": str("Matches id, name and summary")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				kind := args.Str("kind")
				if kind != "" && kind != catalog.KindApp && kind != catalog.KindStack {
					return "", errors.New("kind is app or stack")
				}
				c, err := catalog.Builtin()
				if err != nil {
					return "", errors.New("the catalog did not load")
				}
				recipes := c.List(kind, args.Str("q"))
				if len(recipes) == 0 {
					return "No recipe matches.\n", nil
				}
				var b strings.Builder
				for _, r := range recipes {
					fmt.Fprintf(&b, "%s  %s  %s %s  %s  %s", r.ID, r.Kind, r.Name, r.Version, r.Licence, verifiedText(r))
					if r.Needs.Postgres {
						b.WriteString("  needs postgres")
					}
					if r.Needs.Redis {
						b.WriteString("  needs redis")
					}
					if r.HasWebUI {
						b.WriteString("  web UI")
					}
					fmt.Fprintf(&b, "\n    %s\n", r.Summary)
				}
				return b.String(), nil
			},
		},
		{
			Name: "get_recipe", Scope: scopes.Read,
			Description: "One recipe of the catalog, whole: image, port, limits, capabilities, volumes, the variables it takes and the ones it " +
				"sets on the site; for a stack the repository, branch, variables and hosbah.toml it suggests. There are no secrets in a recipe.",
			InputSchema: schema([]string{"recipe"}, map[string]any{"recipe": str("Recipe id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				c, err := catalog.Builtin()
				if err != nil {
					return "", errors.New("the catalog did not load")
				}
				r, ok := c.Get(args.Str("recipe"))
				if !ok {
					return "", errors.New("recipe not found")
				}
				return recipeText(r), nil
			},
		},
		{
			Name: "list_app_installs", Scope: scopes.Read,
			Description: "The recipes installed next to an app: name, recipe and version, hostname, the variables each set on the app, status, " +
				"and — when the server answers — whether it runs, its restarts and memory. Never a secret.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				installs, err := dbsvc.ListInstalls(ctx, env.Q, env.Caller.TeamID, a)
				if err != nil {
					return "", err
				}
				text := func() string {
					if len(installs) == 0 {
						return "No installs. list_catalog shows what install_recipe can add.\n"
					}
					var b strings.Builder
					for _, k := range installs {
						fmt.Fprintf(&b, "install %s  %s %s  %s", k.Name, k.Recipe, k.RecipeVersion, k.Status)
						if k.Hostname != nil {
							fmt.Fprintf(&b, "  https://%s", *k.Hostname)
						}
						if k.HostPort != nil {
							fmt.Fprintf(&b, "  127.0.0.1:%d on the server", *k.HostPort)
						}
						if len(k.EnvInjected) > 0 {
							fmt.Fprintf(&b, "  sets %s", strings.Join(k.EnvInjected, ", "))
						}
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
				teamID := env.Caller.TeamID
				op := s.QueueInstallStatus(ctx, env.Q, teamID, mcpActor(env), a, installs)
				if op.ID == "" {
					return text(), nil
				}
				env.Then = func(ctx context.Context) (string, error) { // after the commit; best effort
					var res agentproto.RecipeStatusResult
					if _, err := dp.AwaitResult(ctx, op, dbsvc.InstallStatusWait, &res); err == nil {
						dbsvc.WithInstallStatus(installs, res)
						s.ReconcileInstallPorts(ctx, teamID, a, installs, res)
					}
					return text(), nil
				}
				return "", nil
			},
		},
		{
			Name: "install_recipe", Scope: scopes.Write,
			Description: "Install an app recipe next to an app, on the app's server: one container of the recipe's pinned image with its " +
				"limits, reachable on the server's 127.0.0.1 only. hostname (optional) puts a normal proxy host in front of its web UI and is " +
				"where the site reaches it. Generated secrets are sealed and never shown; the variables it sets on the app are never " +
				"overwritten (refused when one exists). confirm must equal the install's name. Owner or admin.",
			InputSchema: schema([]string{"app", "recipe", "name", "confirm"}, map[string]any{"app": appArg,
				"recipe":   str("Recipe id from list_catalog (kind app)"),
				"name":     str("Lowercase letter, then up to 30 lowercase letters, digits and dashes; unique in the app"),
				"postgres": str("A managed Postgres service on the app's server — only for a recipe that needs one"),
				"redis":    str("A managed Redis service on the app's server — only for a recipe that needs one"),
				"hostname": str("Optional, for example search.example.com: a proxy host to the install's web UI"),
				"confirm":  str("The install's name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				if args.Str("hostname") != "" {
					if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
						return "", err
					}
				}
				if err := RequireConfirm(args, strings.TrimSpace(args.Str("name"))); err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				actor, teamID := mcpActor(env), env.Caller.TeamID
				created, err := s.CreateInstall(ctx, env.Q, teamID, actor, a, dbsvc.InstallSpec{Recipe: args.Str("recipe"), Name: args.Str("name"),
					Postgres: args.Str("postgres"), Redis: args.Str("redis"), Hostname: args.Str("hostname")})
				if err != nil {
					return "", err
				}
				env.Then = func(context.Context) (string, error) { // after the commit
					s.FollowInstall(teamID, actor, a, created.Install.ID, created.Operation)
					return "", nil
				}
				msg := fmt.Sprintf("Installing %s as %s (operation %s; a first image pull takes minutes). list_app_installs shows when it is ready.",
					created.Install.RecipeName, created.Install.Name, created.Operation.ID)
				if len(created.Install.EnvInjected) > 0 {
					msg += fmt.Sprintf(" %s gets %s on its next deploy or restart.", a.Name, strings.Join(created.Install.EnvInjected, ", "))
				}
				if len(created.Skipped) > 0 {
					msg += " Not set, because they need the install's address and no hostname was given: " + strings.Join(created.Skipped, ", ") + "."
				}
				return msg, nil
			},
		},
		{
			Name: "remove_install", Scope: scopes.Write,
			Description: "Remove an install from an app: its container is stopped and removed, the proxy host it created is deleted and " +
				"exactly the variables it set are unset. Its volumes are kept unless delete_data is true. confirm must equal the install's " +
				"name. Owner or admin.",
			InputSchema: schema([]string{"app", "install", "confirm"}, map[string]any{"app": appArg,
				"install":     str("Install name or id"),
				"delete_data": map[string]any{"type": "boolean", "description": "Also remove the install's volumes (irreversible)"},
				"confirm":     str("The install's name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := dbsvc.RefuseImportedInstalls(a); err != nil {
					return "", err
				}
				row, err := dbsvc.FindInstall(ctx, env.Q, env.Caller.TeamID, a, args.Str("install"))
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, row.Name); err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				deleteData, _ := args["delete_data"].(bool)
				actor, teamID := mcpActor(env), env.Caller.TeamID
				rm, err := s.BeginInstallRemoval(ctx, env.Q, teamID, actor, a, row, row.Name, deleteData)
				if err != nil {
					return "", err
				}
				env.Then = func(ctx context.Context) (string, error) { // after the commit
					failure := s.AwaitInstallRemoval(ctx, rm)
					fin := context.WithoutCancel(ctx)
					if err := tenancy.Tx(fin, env.App.SQLDB, env.Scope, func(q *db.Queries, _ *sql.Tx) error {
						return s.FinishInstallRemoval(fin, q, teamID, actor, a, rm, failure)
					}); err != nil {
						return "", err
					}
					if failure != "" {
						return "", dbsvc.InstallRemovalRefused(failure)
					}
					msg := "Removed install " + row.Name + " from " + a.Name + ": its container is gone"
					if deleteData {
						msg += ", its volumes too"
					}
					if len(row.EnvInjected) > 0 {
						msg += ", and " + strings.Join(row.EnvInjected, ", ") + " are unset (next deploy or restart)"
					}
					return msg + ".", nil
				}
				return "", nil
			},
		},
	}
}

func verifiedText(r catalog.Recipe) string {
	if r.Verified {
		return "verified"
	}
	return "not yet verified"
}

// recipeText is a whole recipe as text.
func recipeText(r catalog.Recipe) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s) — %s recipe %s, %s, %s\n%s\n\n%s\n", r.Name, r.ID, r.Kind, r.Version, r.Licence, verifiedText(r), r.Homepage, r.Description)
	if r.Kind == catalog.KindStack && r.Stack != nil {
		st := r.Stack
		fmt.Fprintf(&b, "\nrepository: %s (branch %s), runtime %s\n", st.Repo, st.Branch, st.Runtime)
		if len(st.Env) > 0 {
			fmt.Fprintf(&b, "variables to set: %s\n", strings.Join(st.Env, ", "))
		}
		for name, cmd := range st.Processes {
			fmt.Fprintf(&b, "process %s: %s\n", name, cmd)
		}
		if st.HosbahTOML != "" {
			fmt.Fprintf(&b, "hosbah.toml:\n%s\n", st.HosbahTOML)
		}
		if st.Notes != "" {
			fmt.Fprintf(&b, "notes: %s\n", st.Notes)
		}
		return b.String()
	}
	fmt.Fprintf(&b, "\nimage: %s\nlimits: %d MiB, %g CPU\n", r.Image, r.Limits.MemoryMB, r.Limits.CPUs)
	if r.Port > 0 {
		fmt.Fprintf(&b, "web port: %d (health %s)\n", r.Port, r.Healthcheck)
	}
	if len(r.Caps) > 0 {
		fmt.Fprintf(&b, "capabilities: %s\n", strings.Join(r.Caps, ", "))
	}
	for _, v := range r.Volumes {
		fmt.Fprintf(&b, "volume %s at %s\n", v.Name, v.MountPath)
	}
	if r.Needs.Postgres || r.Needs.Redis {
		fmt.Fprintf(&b, "needs: postgres=%t redis=%t (an existing managed service on the app's server)\n", r.Needs.Postgres, r.Needs.Redis)
	}
	for _, e := range r.Env {
		switch {
		case e.Generate != nil:
			fmt.Fprintf(&b, "env %s: generated (%d %s), sealed", e.Name, e.Generate.Length, e.Generate.Charset)
		case e.From != "":
			fmt.Fprintf(&b, "env %s: from %s, sealed", e.Name, e.From)
		default:
			fmt.Fprintf(&b, "env %s = %s", e.Name, *e.Value)
		}
		if e.InjectAs != "" {
			fmt.Fprintf(&b, "  (also set on the site as %s)", e.InjectAs)
		}
		b.WriteString("\n")
	}
	for _, x := range r.Exposes {
		fmt.Fprintf(&b, "sets %s = %s on the site\n", x.Env, x.Template)
	}
	return b.String()
}
