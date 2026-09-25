package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/sitetemplate"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Site templates (MH-576 B11): a declarative YAML document that scaffolds a ready-to-deploy app in
// one step — the stack driver, its repository, env defaults (literal, generated or referencing a
// data service the caller already has running) and the data services it needs. A built-in template
// ships in the binary (internal/sitetemplate, read-only, one per driver); a team may also save its
// own (site_templates, RLS). Reading the gallery is any member; saving, deleting and applying a
// template is owner or admin (tenancy.AbilityManageApps — the same tier app creation itself uses).
// Applying reuses deploy.Service.CreateApp and deploy.Service.SetEnv, never a parallel code path.

// TemplatePath addresses one team-saved template (id or name). A built-in is never reached through
// this path — GET …/site-templates/{template} tries a built-in name first.
type TemplatePath struct {
	TeamPath
	Template string `path:"template" maxLength:"80"`
}

// SiteTemplateOut is a team's saved template.
type SiteTemplateOut struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Driver      string `json:"driver"`
	SourceYAML  string `json:"source_yaml"`
	Builtin     bool   `json:"builtin"`
	CreatedBy   string `json:"created_by,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

func toSiteTemplateOut(s db.SiteTemplate) SiteTemplateOut {
	return SiteTemplateOut{ID: s.ID, Name: s.Name, Description: s.Description, Driver: s.Driver, SourceYAML: s.SourceYaml,
		CreatedBy: s.CreatedBy, CreatedAt: s.CreatedAt.Format(timeLayout), UpdatedAt: s.UpdatedAt.Format(timeLayout)}
}

func builtinTemplateOut(b sitetemplate.Builtin) SiteTemplateOut {
	return SiteTemplateOut{Name: b.Template.Name, Description: b.Template.Description, Driver: b.Template.Driver,
		SourceYAML: b.Source, Builtin: true}
}

// SiteTemplateIn saves (creates or replaces) a team template from its YAML source.
type SiteTemplateIn struct {
	SourceYAML string `json:"source_yaml" minLength:"1" maxLength:"65536" doc:"The whole template document. name/description/driver are read from it."`
}

// RenderedEnvOut is one variable a render (preview or apply) computed. A secret's value is never
// echoed back in a preview — only its name and that it is secret.
type RenderedEnvOut struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Secret bool   `json:"secret"`
}

// RenderedOut is what applying (or previewing) a template computes.
type RenderedOut struct {
	Driver     sitetemplate.Driver `json:"driver"`
	RepoURL    string              `json:"repo_url"`
	Branch     string              `json:"branch"`
	Port       int                 `json:"port"`
	HealthPath string              `json:"health_path"`
	Env        []RenderedEnvOut    `json:"env"`
	HosbahTOML string              `json:"hosbah_toml,omitempty" doc:"A suggested hosbah.toml for the repository; never applied automatically."`
	Notes      string              `json:"notes,omitempty"`
}

func toRenderedOut(r sitetemplate.Rendered, revealSecrets bool) RenderedOut {
	out := RenderedOut{Driver: r.Driver, RepoURL: r.RepoURL, Branch: r.Branch, Port: r.Port, HealthPath: r.HealthPath,
		HosbahTOML: r.HosbahTOML, Notes: r.Notes, Env: make([]RenderedEnvOut, 0, len(r.Env))}
	for _, e := range r.Env {
		v := e.Value
		if e.Secret && !revealSecrets {
			v = ""
		}
		out.Env = append(out.Env, RenderedEnvOut{Name: e.Name, Value: v, Secret: e.Secret})
	}
	return out
}

// serviceRefsIn is the caller's choice of an existing, already-running data service per kind a
// template's needs ask for. A template never provisions one itself (mirrors the app catalog).
type serviceRefsIn struct {
	PostgresService string `json:"postgres_service,omitempty" doc:"Id or name of an existing, running postgres service. Required if the template needs.postgres."`
	MySQLService    string `json:"mysql_service,omitempty" doc:"Id or name of an existing, running mysql service. Required if the template needs.mysql."`
	RedisService    string `json:"redis_service,omitempty" doc:"Id or name of an existing, running redis service. Required if the template needs.redis."`
}

// resolveServiceURLs looks up and reveals the connection string of every data service a template's
// needs ask for. A missing ref, a service that is not running, or a service of the wrong kind is a
// 422 — the caller must pick an existing, healthy service, never have one implicitly created.
func resolveServiceURLs(ctx context.Context, q *db.Queries, dbs *dbsvc.Service, m *tenancy.Membership, actor deploy.Actor,
	needs sitetemplate.Needs, refs serviceRefsIn) (sitetemplate.ServiceURLs, error) {
	var out sitetemplate.ServiceURLs
	pick := func(kind, ref string) (string, error) {
		if ref == "" {
			return "", huma.Error422UnprocessableEntity("this template needs a " + kind + " service — pass " + kind + "_service")
		}
		svc, err := serviceIn(ctx, q, m, ref)
		if err != nil {
			return "", huma.Error422UnprocessableEntity("the " + kind + " service was not found")
		}
		if svc.Kind != kind {
			return "", huma.Error422UnprocessableEntity(kind + "_service is not a " + kind + " service")
		}
		if svc.Status != "running" || svc.Host == nil || svc.Port == nil {
			return "", huma.Error422UnprocessableEntity("the " + kind + " service is not running yet")
		}
		url, err := dbs.RevealURL(ctx, q, m.TeamID, dbsvc.Actor(actor), svc, "")
		if err != nil {
			return "", dbErr(err, "could not resolve the "+kind+" service")
		}
		return url, nil
	}
	var err error
	if needs.Postgres {
		if out.Postgres, err = pick("postgres", refs.PostgresService); err != nil {
			return sitetemplate.ServiceURLs{}, err
		}
	}
	if needs.MySQL {
		if out.MySQL, err = pick("mysql", refs.MySQLService); err != nil {
			return sitetemplate.ServiceURLs{}, err
		}
	}
	if needs.Redis {
		if out.Redis, err = pick("redis", refs.RedisService); err != nil {
			return sitetemplate.ServiceURLs{}, err
		}
	}
	return out, nil
}

// findTemplate resolves {template} against the built-in registry first, then the team's own saved
// templates — a team cannot shadow a built-in's name (save/update reject it).
func findTemplate(ctx context.Context, q *db.Queries, m *tenancy.Membership, ref string) (sitetemplate.Template, bool, db.SiteTemplate, error) {
	if b, ok := sitetemplate.BuiltinByName(ref); ok {
		return b.Template, true, db.SiteTemplate{}, nil
	}
	row, err := q.GetTeamSiteTemplate(ctx, db.GetTeamSiteTemplateParams{TeamID: m.TeamID, ID: ref})
	if err != nil {
		row, err = q.GetTeamSiteTemplateByName(ctx, db.GetTeamSiteTemplateByNameParams{TeamID: m.TeamID, Name: ref})
		if err != nil {
			return sitetemplate.Template{}, false, db.SiteTemplate{}, errNotFound
		}
	}
	t, err := sitetemplate.Parse([]byte(row.SourceYaml))
	if err != nil {
		return sitetemplate.Template{}, false, db.SiteTemplate{}, huma.Error500InternalServerError("a saved template failed to re-parse")
	}
	return t, false, row, nil
}

// siteTemplateConflictErr: the site_templates_team_id_name_key unique violation is this section's
// 409; anything else falls through.
func siteTemplateConflictErr(err error, fallback string) error {
	if err == nil {
		return nil
	}
	if containsAny(err.Error(), "site_templates_team_id_name_key") {
		return huma.Error409Conflict("a template with that name already exists")
	}
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	return huma.Error500InternalServerError(fallback)
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) && (func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})() {
			return true
		}
	}
	return false
}

func (d *deps) registerSiteTemplates(api huma.API) {
	tags := []string{"Site templates"}
	base := "/api/teams/{team}/site-templates"

	// ---- drivers + gallery -----------------------------------------------------------------

	type driversOut struct {
		Body struct {
			Drivers []sitetemplate.Driver `json:"drivers"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-site-template-drivers", Method: http.MethodGet, Path: base + "/drivers", Tags: tags,
		Summary: "The small typed registry of stack drivers a template may reference. Any member.",
	}, func(ctx context.Context, in *TeamPath) (*driversOut, error) {
		out := &driversOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(*db.Queries, *sql.Tx, *auth.Identity, *tenancy.Membership) error {
			out.Body.Drivers = sitetemplate.Drivers
			return nil
		})
		return out, err
	})

	type templatesListOut struct {
		Body struct {
			Templates []SiteTemplateOut `json:"templates"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-site-templates", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "Built-in templates plus the team's own saved ones. Any member.",
	}, func(ctx context.Context, in *TeamPath) (*templatesListOut, error) {
		out := &templatesListOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			for _, b := range sitetemplate.Builtins() {
				out.Body.Templates = append(out.Body.Templates, builtinTemplateOut(b))
			}
			rows, err := q.ListTeamSiteTemplates(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list the templates")
			}
			for _, r := range rows {
				out.Body.Templates = append(out.Body.Templates, toSiteTemplateOut(r))
			}
			if out.Body.Templates == nil {
				out.Body.Templates = []SiteTemplateOut{}
			}
			return nil
		})
		return out, err
	})

	type templateOut struct{ Body SiteTemplateOut }
	huma.Register(api, huma.Operation{OperationID: "get-site-template", Method: http.MethodGet, Path: base + "/{template}", Tags: tags,
		Summary: "One template, built-in or the team's own, by id or name. Any member.",
	}, func(ctx context.Context, in *TemplatePath) (*templateOut, error) {
		out := &templateOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			t, builtin, row, err := findTemplate(ctx, q, m, in.Template)
			if err != nil {
				return err
			}
			if builtin {
				b, _ := sitetemplate.BuiltinByName(t.Name)
				out.Body = builtinTemplateOut(b)
				return nil
			}
			out.Body = toSiteTemplateOut(row)
			return nil
		})
		return out, err
	})

	// ---- save / update / delete -------------------------------------------------------------

	huma.Register(api, huma.Operation{OperationID: "save-site-template", Method: http.MethodPost, Path: base, DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Save a new team template from its YAML source. Strictly validated: unknown keys rejected, size-bounded, " +
			"no secret may carry a literal value. 409: the name is taken (a built-in's name too). Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body SiteTemplateIn
	}) (*templateOut, error) {
		out := &templateOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			t, err := sitetemplate.Parse([]byte(in.Body.SourceYAML))
			if err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			if _, ok := sitetemplate.BuiltinByName(t.Name); ok {
				return huma.Error409Conflict("that name is a built-in template")
			}
			row, err := q.CreateTeamSiteTemplate(ctx, db.CreateTeamSiteTemplateParams{TeamID: m.TeamID, Name: t.Name,
				Description: t.Description, Driver: t.Driver, SourceYaml: in.Body.SourceYAML, CreatedBy: id.ID})
			if err != nil {
				return siteTemplateConflictErr(err, "could not save the template")
			}
			out.Body = toSiteTemplateOut(row)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "site_template.create", "site_template", row.ID, map[string]any{"name": row.Name, "via": "web"})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-site-template", Method: http.MethodPut, Path: base + "/{template}", Tags: tags,
		Summary: "Replace a team template's YAML source. 404: a built-in — it cannot be edited. 409: the new name is taken. " +
			"Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		TemplatePath
		Body SiteTemplateIn
	}) (*templateOut, error) {
		out := &templateOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			if _, ok := sitetemplate.BuiltinByName(in.Template); ok {
				return errNotFound
			}
			existing, err := q.GetTeamSiteTemplate(ctx, db.GetTeamSiteTemplateParams{TeamID: m.TeamID, ID: in.Template})
			if err != nil {
				existing, err = q.GetTeamSiteTemplateByName(ctx, db.GetTeamSiteTemplateByNameParams{TeamID: m.TeamID, Name: in.Template})
				if err != nil {
					return errNotFound
				}
			}
			t, err := sitetemplate.Parse([]byte(in.Body.SourceYAML))
			if err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			if _, ok := sitetemplate.BuiltinByName(t.Name); ok {
				return huma.Error409Conflict("that name is a built-in template")
			}
			row, err := q.UpdateTeamSiteTemplate(ctx, db.UpdateTeamSiteTemplateParams{TeamID: m.TeamID, ID: existing.ID, Name: t.Name,
				Description: t.Description, Driver: t.Driver, SourceYaml: in.Body.SourceYAML})
			if err != nil {
				return siteTemplateConflictErr(err, "could not update the template")
			}
			out.Body = toSiteTemplateOut(row)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "site_template.update", "site_template", row.ID, map[string]any{"name": row.Name, "via": "web"})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-site-template", Method: http.MethodDelete, Path: base + "/{template}", DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Remove a team template. 404: a built-in — it cannot be deleted. Owner or admin. Audited.",
	}, func(ctx context.Context, in *TemplatePath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			if _, ok := sitetemplate.BuiltinByName(in.Template); ok {
				return errNotFound
			}
			existing, err := q.GetTeamSiteTemplate(ctx, db.GetTeamSiteTemplateParams{TeamID: m.TeamID, ID: in.Template})
			if err != nil {
				existing, err = q.GetTeamSiteTemplateByName(ctx, db.GetTeamSiteTemplateByNameParams{TeamID: m.TeamID, Name: in.Template})
				if err != nil {
					return errNotFound
				}
			}
			if err := q.DeleteTeamSiteTemplate(ctx, db.DeleteTeamSiteTemplateParams{TeamID: m.TeamID, ID: existing.ID}); err != nil {
				return huma.Error500InternalServerError("could not delete the template")
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "site_template.delete", "site_template", existing.ID, map[string]any{"name": existing.Name, "via": "web"})
		})
		return nil, err
	})

	// ---- preview + apply ---------------------------------------------------------------------

	type previewOut struct{ Body RenderedOut }
	huma.Register(api, huma.Operation{OperationID: "preview-site-template", Method: http.MethodPost, Path: base + "/{template}/preview", Tags: tags,
		Summary: "Render a template without creating anything: fills generated secrets and, if a data-service ref is given, its " +
			"resolved URL. Secret values are not echoed back (only their name). 422: needs a service ref not given, or the given " +
			"service is missing, not running, or the wrong kind. Any member.",
	}, func(ctx context.Context, in *struct {
		TemplatePath
		Body serviceRefsIn
	}) (*previewOut, error) {
		out := &previewOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			t, _, _, err := findTemplate(ctx, q, m, in.Template)
			if err != nil {
				return err
			}
			urls, err := resolveServiceURLs(ctx, q, d.dbs(), m, webActor(id), t.Needs, in.Body)
			if err != nil {
				return err
			}
			r, err := t.Render(urls)
			if err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			out.Body = toRenderedOut(r, false)
			return nil
		})
		return out, err
	})

	type applyOut struct {
		Body struct {
			App           AppOut         `json:"app"`
			DeployHookURL string         `json:"deploy_hook_url" doc:"POST to it to deploy. Shown once; only its hash is stored."`
			Rendered      RenderedOut    `json:"rendered"`
			Deployment    *DeploymentOut `json:"deployment,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "create-app-from-site-template", Method: http.MethodPost, Path: base + "/{template}/apply",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Create an app from a template on a chosen server: reuses the same app-creation and env-setting code paths as " +
			"the New app form. 422: needs a service ref not given, or the given service is missing, not running, or the wrong " +
			"kind. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		TemplatePath
		Body struct {
			serviceRefsIn
			Name   string `json:"name" minLength:"1" maxLength:"40" doc:"Lowercase letters, digits and dashes; unique in the team."`
			Server string `json:"server" minLength:"1" maxLength:"64" doc:"Server id or name."`
			Branch string `json:"branch,omitempty" maxLength:"200" doc:"Overrides the template's branch."`
			Deploy bool   `json:"deploy,omitempty" doc:"Queue the first deploy right away."`
		}
	}) (*applyOut, error) {
		out := &applyOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			t, _, _, err := findTemplate(ctx, q, m, in.Template)
			if err != nil {
				return err
			}
			actor := webActor(id)
			urls, err := resolveServiceURLs(ctx, q, d.dbs(), m, actor, t.Needs, in.Body.serviceRefsIn)
			if err != nil {
				return err
			}
			r, err := t.Render(urls)
			if err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			branch := in.Body.Branch
			if branch == "" {
				branch = r.Branch
			}
			s := d.deploys()
			a, hook, err := s.CreateApp(ctx, q, m.TeamID, actor, deploy.CreateAppInput{Name: in.Body.Name, Server: in.Body.Server,
				RepoURL: r.RepoURL, Branch: branch, Port: r.Port, HealthPath: r.HealthPath})
			if err != nil {
				return actionErr(err, "could not create the app")
			}
			for _, e := range r.Env {
				if err := s.SetEnv(ctx, q, m.TeamID, actor, a, e.Name, e.Value); err != nil {
					return actionErr(err, "could not set "+e.Name)
				}
			}
			out.Body.App, out.Body.DeployHookURL = toAppOut(a, ""), hook
			out.Body.Rendered = toRenderedOut(r, true)
			if in.Body.Deploy {
				dep, err := s.QueueDeployment(ctx, q, m.TeamID, actor, a, "deploy", "", nil, "")
				if err != nil {
					return actionErr(err, "could not queue the deploy")
				}
				do := toDeploymentOut(dep)
				out.Body.Deployment = &do
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "site_template.apply", "app", a.ID,
				map[string]any{"template": t.Name, "driver": t.Driver, "via": "web"})
		})
		return out, err
	})

	// ---- export an existing app as a template ------------------------------------------------

	type exportOut struct {
		Body struct {
			SourceYAML string `json:"source_yaml"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "export-app-template", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/export-template", Tags: tags,
		Summary: "Best-effort export of an app's current repo, branch, port, health path and driver as a template YAML — env " +
			"values are never included (a template never stores a secret in plain text; literal env is left for the team to " +
			"add by hand). Owner or admin.",
	}, func(ctx context.Context, in *AppPath) (*exportOut, error) {
		out := &exportOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if deploy.IsImported(a) {
				return huma.Error422UnprocessableEntity("an imported app has no repository to export")
			}
			driverName := driverForRuntime(deref(a.Runtime))
			t := sitetemplate.Template{
				Name:        a.Name,
				Description: "Exported from app " + a.Name,
				Driver:      driverName,
				RepoURL:     deref(a.RepoURL),
				Branch:      deref(a.Branch),
				Port:        int(a.Port),
				HealthPath:  a.HealthcheckPath,
				Notes:       "Exported by export_app_template; env values were not included on purpose.",
			}
			raw, err := t.ToYAML()
			if err != nil {
				return huma.Error500InternalServerError("could not render the template")
			}
			out.Body.SourceYAML = string(raw)
			return nil
		})
		return out, err
	})
}

// driverForRuntime maps an app's detected runtime to the closest stack driver for export. Best
// effort: the team can always change it before saving.
func driverForRuntime(runtime string) string {
	switch runtime {
	case "php":
		return "laravel"
	case "node":
		return "nextjs"
	case "python":
		return "django"
	case "static":
		return "static-site"
	default:
		return "node-api"
	}
}
