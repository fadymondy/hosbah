package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/catalog"
	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/storage"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
"Apps & stacks": the built-in catalog of recipes (package catalog) and the recipes installed next to
a site (internal/dbsvc/installs.go).

The catalog is not tenant data — it is the same for every team — but its routes are team-scoped like
everything else a member reads, so a team you are not in is a 404 here too. Catalog reads: any
member. Installs: read any member, write whoever may manage the app (manage_apps), and a hostname
additionally needs what a proxy host needs (manage_servers). Imported apps: every write is 422. An
install's secrets are never in a response, and there is no reveal route.
*/

// RecipePath addresses one recipe of the catalog.
type RecipePath struct {
	TeamPath
	Recipe string `path:"recipe" maxLength:"64" doc:"Recipe id."`
}

// InstallPath addresses one install of an app (id or name).
type InstallPath struct {
	AppPath
	Install string `path:"install" maxLength:"64" doc:"Install id or name."`
}

// RecipeSummary is a recipe in the catalog's listing.
type RecipeSummary struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Summary  string              `json:"summary"`
	Kind     string              `json:"kind" enum:"app,stack"`
	Licence  string              `json:"licence" doc:"SPDX identifier."`
	Homepage string              `json:"homepage"`
	Version  string              `json:"version"`
	Verified bool                `json:"verified" doc:"false: written from the project's documentation but not yet run end to end by Hosbah — say so."`
	Needs    catalog.RecipeNeeds `json:"needs"`
	HasWebUI bool                `json:"has_web_ui" doc:"An app with an HTTP port a hostname can be put in front of."`
}

func recipeSummary(r catalog.Recipe) RecipeSummary {
	return RecipeSummary{ID: r.ID, Name: r.Name, Summary: r.Summary, Kind: r.Kind, Licence: r.Licence, Homepage: r.Homepage,
		Version: r.Version, Verified: r.Verified, Needs: r.Needs, HasWebUI: r.HasWebUI}
}

// RecipeOut is a whole recipe. There is nothing secret in a recipe.
type RecipeOut = catalog.Recipe

// InstallsOut is a site's installs.
type InstallsOut struct {
	Installs        []dbsvc.AppInstall `json:"installs"`
	StatusOperation *string            `json:"status_operation" doc:"The recipe.status operation that was asked; null when there are no installs or the server is not connected."`
}

// installErr maps what the install service and the proxy-host and env code under it refuse.
func installErr(err error, fallback string) error {
	var ae *deploy.ActionError
	if errors.As(err, &ae) {
		return actionErr(err, fallback)
	}
	return dbErr(err, fallback)
}

func (d *deps) registerInstalls(api huma.API) {
	tags := []string{"Apps & stacks"}

	type catalogOut struct {
		Body struct {
			Recipes []RecipeSummary `json:"recipes"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-catalog", Method: http.MethodGet, Path: "/api/teams/{team}/catalog", Tags: tags,
		Summary: "The built-in catalog of recipes: apps (one pinned image installed next to a site) and stacks (a template that prefills a new " +
			"app). verified false means Hosbah has not run that recipe end to end yet. Any member.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Kind string `query:"kind" maxLength:"10" doc:"app or stack; empty for both."`
		Q    string `query:"q" maxLength:"100" doc:"Matches id, name and summary."`
	}) (*catalogOut, error) {
		out := &catalogOut{}
		out.Body.Recipes = []RecipeSummary{}
		if in.Kind != "" && in.Kind != catalog.KindApp && in.Kind != catalog.KindStack {
			return nil, huma.Error422UnprocessableEntity("kind is app or stack")
		}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(*db.Queries, *sql.Tx, *auth.Identity, *tenancy.Membership) error {
			c, err := catalog.Builtin()
			if err != nil {
				return huma.Error500InternalServerError("the catalog did not load")
			}
			for _, r := range c.List(in.Kind, in.Q) {
				out.Body.Recipes = append(out.Body.Recipes, recipeSummary(r))
			}
			return nil
		})
		return out, err
	})

	type recipeOut struct{ Body RecipeOut }
	huma.Register(api, huma.Operation{OperationID: "get-recipe", Method: http.MethodGet, Path: "/api/teams/{team}/catalog/{recipe}", Tags: tags,
		Summary: "One recipe, whole: what it runs (image, port, limits, capabilities, volumes), the variables it takes and sets, and for a " +
			"stack the repository, branch and hosbah.toml it suggests. 404: no such recipe. Any member.",
	}, func(ctx context.Context, in *RecipePath) (*recipeOut, error) {
		out := &recipeOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(*db.Queries, *sql.Tx, *auth.Identity, *tenancy.Membership) error {
			c, err := catalog.Builtin()
			if err != nil {
				return huma.Error500InternalServerError("the catalog did not load")
			}
			r, ok := c.Get(in.Recipe)
			if !ok {
				return errNotFound
			}
			if r.Env == nil {
				r.Env = []catalog.RecipeEnv{}
			}
			if r.Volumes == nil {
				r.Volumes = []catalog.RecipeVolume{}
			}
			if r.Caps == nil {
				r.Caps = []string{}
			}
			if r.Exposes == nil {
				r.Exposes = []catalog.RecipeExpose{}
			}
			out.Body = r
			return nil
		})
		return out, err
	})

	base := "/api/teams/{team}/apps/{app}/installs"

	type installsOut struct{ Body InstallsOut }
	huma.Register(api, huma.Operation{OperationID: "list-app-installs", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The recipes installed next to the app. running, restarts and memory_bytes are best effort — the server is asked (up to 8 s), " +
			"and an offline or silent server leaves them null, still 200. Never a secret. Any member.",
	}, func(ctx context.Context, in *AppPath) (*installsOut, error) {
		out := &installsOut{Body: InstallsOut{Installs: []dbsvc.AppInstall{}}}
		var (
			op     db.Operation
			a      db.App
			teamID string
		)
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			var err error
			if a, err = appIn(ctx, q, m, in.App); err != nil {
				return err
			}
			teamID = m.TeamID
			if out.Body.Installs, err = dbsvc.ListInstalls(ctx, q, m.TeamID, a); err != nil {
				return huma.Error500InternalServerError("could not list the installs")
			}
			op = d.dbs().QueueInstallStatus(ctx, q, m.TeamID, webActor(id), a, out.Body.Installs)
			return nil
		})
		if err != nil {
			return nil, err
		}
		if op.ID != "" { // after the commit: the operation exists for the dispatcher
			out.Body.StatusOperation = &op.ID
			var res agentproto.RecipeStatusResult
			if _, err := d.deploys().AwaitResult(ctx, op, dbsvc.InstallStatusWait, &res); err == nil {
				dbsvc.WithInstallStatus(out.Body.Installs, res)
				d.dbs().ReconcileInstallPorts(ctx, teamID, a, out.Body.Installs, res)
			}
		}
		return out, nil
	})

	type installCreatedOut struct {
		Body struct {
			dbsvc.AppInstall
			Skipped   []string `json:"skipped" doc:"Variables the recipe would set on the site but could not: they need the install's address, and no hostname was given."`
			Applies   string   `json:"applies" enum:"next_deploy" doc:"The variables reach the app on its next deploy or restart."`
			Operation string   `json:"operation" doc:"The recipe.install operation; the install's status follows it (creating, then ready or failed)."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "create-app-install", Method: http.MethodPost, Path: base,
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Install an app recipe next to the app, on the app's server: one container of the recipe's pinned image with its limits, " +
			"reachable on the server's 127.0.0.1 only. hostname (optional) puts a normal proxy host in front of its web UI — the same " +
			"rules as any proxy host — and is where the site reaches it; without one, the variables that need its address are listed in " +
			"skipped. Generated secrets are sealed and never returned. 201 with status creating — it turns ready or failed when the server " +
			"has answered (a first image pull takes minutes). 404: no such recipe. 422: a stack, a bad name, a missing or wrong data " +
			"service, a hostname for a recipe without a web port, an imported app. 409: the name or hostname is taken, the app has 10, " +
			"its server has no agent, or a variable is already set on the app (nothing is ever overwritten). Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Recipe   string `json:"recipe" minLength:"2" maxLength:"41" doc:"Recipe id from the catalog (kind app)."`
			Name     string `json:"name" minLength:"1" maxLength:"31" doc:"^[a-z][a-z0-9-]{0,30}$, unique in the app."`
			MySQL    string `json:"mysql,omitempty" maxLength:"64" doc:"A managed MySQL/MariaDB service (name or id) on the app's server — only for a recipe that needs one."`
				Postgres string `json:"postgres,omitempty" maxLength:"64" doc:"A managed Postgres service (name or id) on the app's server — only for a recipe that needs one."`
			Redis    string `json:"redis,omitempty" maxLength:"64" doc:"A managed Redis service (name or id) on the app's server — only for a recipe that needs one."`
			Hostname string `json:"hostname,omitempty" maxLength:"253" doc:"Optional, for example search.example.com: creates a proxy host to the install's web UI."`
		}
	}) (*installCreatedOut, error) {
		out := &installCreatedOut{}
		var (
			created dbsvc.InstallCreated
			a       db.App
			actor   deploy.Actor
			teamID  string
		)
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			var err error
			if a, err = appIn(ctx, q, m, in.App); err != nil {
				return err
			}
			if in.Body.Hostname != "" && !tenancy.Can(ctx, m, tenancy.AbilityManageServers) {
				return huma.Error403Forbidden("a hostname creates a proxy host, which needs the manage_servers ability")
			}
			teamID, actor = m.TeamID, webActor(id)
			created, err = d.dbs().CreateInstall(ctx, q, m.TeamID, actor, a, dbsvc.InstallSpec{Recipe: in.Body.Recipe, Name: in.Body.Name,
				Postgres: in.Body.Postgres, MySQL: in.Body.MySQL, Redis: in.Body.Redis, Hostname: in.Body.Hostname})
			return installErr(err, "could not install the recipe")
		})
		if err != nil {
			return nil, err
		}
		d.dbs().FollowInstall(teamID, actor, a, created.Install.ID, created.Operation) // after the commit
		out.Body.AppInstall, out.Body.Skipped = created.Install, append([]string{}, created.Skipped...)
		out.Body.Applies, out.Body.Operation = storage.AppliesNextDeploy, created.Operation.ID
		return out, nil
	})

	type installRemovedOut struct {
		Body struct {
			Removed     bool     `json:"removed"`
			Unset       []string `json:"unset" doc:"The variables removed from the app."`
			DataDeleted bool     `json:"data_deleted" doc:"Whether the install's volumes were removed too."`
			Applies     string   `json:"applies" enum:"next_deploy" doc:"The app loses the variables on its next deploy or restart; the install is already gone."`
			Operation   *string  `json:"operation" doc:"The recipe.remove operation."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "remove-app-install", Method: http.MethodDelete, Path: base + "/{install}", Tags: tags,
		Summary: "Remove an install (confirm = its name): its container is stopped and removed, the proxy host it created is deleted and " +
			"exactly the variables it set are unset. Its volumes are kept unless delete_data is true. Waits up to 90 s for the server; 409 " +
			"(nothing changed): the server is offline or did not answer, or the install is still being created. 422: confirm is not the " +
			"name; an imported app. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		InstallPath
		Body struct {
			Confirm    string `json:"confirm" minLength:"1" maxLength:"64" doc:"The install's name, exactly."`
			DeleteData bool   `json:"delete_data,omitempty" doc:"Also remove the install's volumes. Irreversible."`
		}
	}) (*installRemovedOut, error) {
		out := &installRemovedOut{}
		var rm dbsvc.InstallRemoval
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if err := dbsvc.RefuseImportedInstalls(a); err != nil {
				return installErr(err, "could not remove the install")
			}
			row, err := dbsvc.FindInstall(ctx, q, m.TeamID, a, in.Install)
			if err != nil {
				return errNotFound
			}
			rm, err = d.dbs().BeginInstallRemoval(ctx, q, m.TeamID, webActor(id), a, row, in.Body.Confirm, in.Body.DeleteData)
			return installErr(err, "could not remove the install")
		})
		if err != nil {
			return nil, err
		}
		failure := d.dbs().AwaitInstallRemoval(ctx, rm) // after the commit
		fin := context.WithoutCancel(ctx)               // a caller who hung up must not leave the install 'removing'
		err = d.inTeam(fin, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(fin, q, m, in.App)
			if err != nil {
				return err
			}
			return installErr(d.dbs().FinishInstallRemoval(fin, q, m.TeamID, webActor(id), a, rm, failure), "could not remove the install")
		})
		if err != nil {
			return nil, err
		}
		if failure != "" {
			return nil, installErr(dbsvc.InstallRemovalRefused(failure), "could not remove the install")
		}
		out.Body.Removed, out.Body.Unset, out.Body.Applies = true, append([]string{}, rm.Install.EnvInjected...), storage.AppliesNextDeploy
		out.Body.DataDeleted = rm.DeleteData
		if rm.Operation.ID != "" {
			out.Body.Operation = &rm.Operation.ID
		}
		return out, nil
	})
}
