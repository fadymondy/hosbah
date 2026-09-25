package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/sitetemplate"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// SiteTemplateTools are MH-576 B11: a site template is a declarative YAML document that scaffolds a
// ready-to-deploy app in one step. Reading the gallery (built-in templates plus the team's own) is
// scopes.Read; saving, deleting and applying a template is scopes.Write, gated the same as app
// creation (tenancy.AbilityManageApps, owner/admin); create_app_from_template also changes the state
// of a real server, so — like create_app itself — it is not behind a destructive confirm (creating is
// not destructive), but delete_site_template is (confirm must equal the template's name).
func SiteTemplateTools() []Tool {
	return []Tool{
		{
			Name: "list_site_templates", Scope: scopes.Read,
			Description: "Built-in site templates (laravel, nextjs, node-api, static-site, django, wordpress-docker) plus the team's own saved ones.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				var b strings.Builder
				b.WriteString("Built-in:\n")
				for _, t := range sitetemplate.Builtins() {
					fmt.Fprintf(&b, "  %-18s driver %-16s %s\n", t.Template.Name, t.Template.Driver, t.Template.Description)
				}
				rows, err := env.Q.ListTeamSiteTemplates(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list the team's templates")
				}
				if len(rows) == 0 {
					b.WriteString("No team templates yet. save_site_template adds one.\n")
					return b.String(), nil
				}
				b.WriteString("Team:\n")
				for _, s := range rows {
					fmt.Fprintf(&b, "  %-18s driver %-16s %s  (id %s)\n", s.Name, s.Driver, s.Description, s.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "get_site_template", Scope: scopes.Read,
			Description: "One template (built-in or the team's own) by name or id, with its full YAML source.",
			InputSchema: schema([]string{"template"}, map[string]any{"template": str("Template name or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				t, builtin, source, err := findSiteTemplate(ctx, env, args.Str("template"))
				if err != nil {
					return "", err
				}
				kind := "team"
				if builtin {
					kind = "built-in"
				}
				return fmt.Sprintf("%s (%s, driver %s)\n%s\n\n--- source ---\n%s\n", t.Name, kind, t.Driver, t.Description, source), nil
			},
		},
		{
			Name: "list_site_template_drivers", Scope: scopes.Read,
			Description: "The small typed registry of stack drivers a template may reference (laravel, nextjs, node-api, static-site, django, wordpress-docker).",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				var b strings.Builder
				for _, d := range sitetemplate.Drivers {
					fmt.Fprintf(&b, "  %-18s build %-10s preset %-10s default port %d, health %s\n    %s\n",
						d.Name, d.Build, d.Preset, d.DefaultPort, d.DefaultHealth, d.Summary)
				}
				return b.String(), nil
			},
		},
		{
			Name: "save_site_template", Scope: scopes.Write,
			Description: "Create or replace a team site template from its full YAML source (strictly validated: unknown keys rejected, " +
				"size-bounded, no secret may carry a literal value — use generate or from). Omit template to create; give it (id or name) " +
				"to replace an existing one. A team template may not reuse a built-in's name.",
			InputSchema: schema([]string{"source_yaml"}, map[string]any{
				"template":    str("Existing team template id or name, to replace it (optional)"),
				"source_yaml": str("The whole template document"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				raw := args.Str("source_yaml")
				t, err := sitetemplate.Parse([]byte(raw))
				if err != nil {
					return "", err
				}
				if _, ok := sitetemplate.BuiltinByName(t.Name); ok {
					return "", errors.New("that name is a built-in template")
				}
				if ref := args.Str("template"); ref != "" {
					existing, err := findTeamSiteTemplate(ctx, env, ref)
					if err != nil {
						return "", err
					}
					s, err := env.Q.UpdateTeamSiteTemplate(ctx, db.UpdateTeamSiteTemplateParams{TeamID: env.Caller.TeamID, ID: existing.ID,
						Name: t.Name, Description: t.Description, Driver: t.Driver, SourceYaml: raw})
					if err != nil {
						return "", siteTemplateSaveErr(err)
					}
					return "Updated " + s.Name + ".", nil
				}
				s, err := env.Q.CreateTeamSiteTemplate(ctx, db.CreateTeamSiteTemplateParams{TeamID: env.Caller.TeamID, Name: t.Name,
					Description: t.Description, Driver: t.Driver, SourceYaml: raw, CreatedBy: env.Caller.UserID})
				if err != nil {
					return "", siteTemplateSaveErr(err)
				}
				return "Created " + s.Name + " (id " + s.ID + ").", nil
			},
		},
		{
			Name: "delete_site_template", Scope: scopes.Destructive, Destructive: true, ResourceType: "site_template",
			Description: "Remove a team site template. Built-in templates cannot be removed. confirm must equal the template's name.",
			InputSchema: schema([]string{"template", "confirm"}, map[string]any{"template": str("Team template id or name"), "confirm": str("The template's name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				s, err := findTeamSiteTemplate(ctx, env, args.Str("template"))
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, s.Name); err != nil {
					return "", err
				}
				if err := env.Q.DeleteTeamSiteTemplate(ctx, db.DeleteTeamSiteTemplateParams{TeamID: env.Caller.TeamID, ID: s.ID}); err != nil {
					return "", errors.New("could not delete the template")
				}
				return "Deleted " + s.Name + ".", nil
			},
		},
		{
			Name: "preview_site_template", Scope: scopes.Read,
			Description: "Render a template without creating anything: fills generated secrets and, if a data-service ref is given, its " +
				"resolved URL. Secret values are not shown, only their name.",
			InputSchema: schema([]string{"template"}, map[string]any{
				"template":         str("Template name or id"),
				"postgres_service": str("Existing, running postgres service id or name (required if the template needs.postgres)"),
				"mysql_service":    str("Existing, running mysql service id or name (required if the template needs.mysql)"),
				"redis_service":    str("Existing, running redis service id or name (required if the template needs.redis)"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				t, _, _, err := findSiteTemplate(ctx, env, args.Str("template"))
				if err != nil {
					return "", err
				}
				urls, err := mcpResolveServiceURLs(ctx, env, t.Needs, args)
				if err != nil {
					return "", err
				}
				r, err := t.Render(urls)
				if err != nil {
					return "", err
				}
				return renderRendered(r, false), nil
			},
		},
		{
			Name: "create_app_from_template", Scope: scopes.Write,
			Description: "Create an app from a template on a chosen server: reuses the same app-creation and env-setting code paths as " +
				"create_app.",
			InputSchema: schema([]string{"template", "name", "server"}, map[string]any{
				"template":         str("Template name or id"),
				"name":             str("The new app's name: lowercase letters, digits and dashes, unique in the team"),
				"server":           str("Server id or name"),
				"branch":           str("Overrides the template's branch (optional)"),
				"postgres_service": str("Existing, running postgres service id or name (required if the template needs.postgres)"),
				"mysql_service":    str("Existing, running mysql service id or name (required if the template needs.mysql)"),
				"redis_service":    str("Existing, running redis service id or name (required if the template needs.redis)"),
				"deploy":           map[string]any{"type": "boolean", "default": false, "description": "Queue the first deploy right away"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				t, _, _, err := findSiteTemplate(ctx, env, args.Str("template"))
				if err != nil {
					return "", err
				}
				urls, err := mcpResolveServiceURLs(ctx, env, t.Needs, args)
				if err != nil {
					return "", err
				}
				r, err := t.Render(urls)
				if err != nil {
					return "", err
				}
				branch := args.Str("branch")
				if branch == "" {
					branch = r.Branch
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				actor := mcpActor(env)
				a, _, err := s.CreateApp(ctx, env.Q, env.Caller.TeamID, actor, deploy.CreateAppInput{Name: args.Str("name"),
					Server: args.Str("server"), RepoURL: r.RepoURL, Branch: branch, Port: r.Port, HealthPath: r.HealthPath})
				if err != nil {
					return "", err
				}
				for _, e := range r.Env {
					if err := s.SetEnv(ctx, env.Q, env.Caller.TeamID, actor, a, e.Name, e.Value); err != nil {
						return "", fmt.Errorf("could not set %s: %w", e.Name, err)
					}
				}
				out := fmt.Sprintf("Created %s (id %s) from template %s.\n%s", a.Name, a.ID, t.Name, renderRendered(r, true))
				if args.Bool("deploy") {
					if _, err := s.QueueDeployment(ctx, env.Q, env.Caller.TeamID, actor, a, "deploy", "", nil, ""); err != nil {
						return "", fmt.Errorf("app created, but could not queue the deploy: %w", err)
					}
					out += "\nDeploy queued.\n"
				}
				return out, nil
			},
		},
		{
			Name: "export_app_template", Scope: scopes.Read,
			Description: "Best-effort export of an app's current repo, branch, port, health path and driver as a template YAML. Env " +
				"values are never included (a template never stores a secret in plain text).",
			InputSchema: schema([]string{"app"}, map[string]any{"app": str("App id or name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				if deploy.IsImported(a) {
					return "", errors.New("an imported app has no repository to export")
				}
				t := sitetemplate.Template{
					Name:        a.Name,
					Description: "Exported from app " + a.Name,
					Driver:      driverForRuntime(derefStr(a.Runtime)),
					RepoURL:     derefStr(a.RepoURL),
					Branch:      derefStr(a.Branch),
					Port:        int(a.Port),
					HealthPath:  a.HealthcheckPath,
					Notes:       "Exported by export_app_template; env values were not included on purpose.",
				}
				raw, err := t.ToYAML()
				if err != nil {
					return "", errors.New("could not render the template")
				}
				return string(raw), nil
			},
		},
	}
}

// ---- helpers -------------------------------------------------------------------------------------

func findSiteTemplate(ctx context.Context, env *Env, ref string) (sitetemplate.Template, bool, string, error) {
	if ref == "" {
		return sitetemplate.Template{}, false, "", errors.New("template is required (a name or an id)")
	}
	if b, ok := sitetemplate.BuiltinByName(ref); ok {
		return b.Template, true, b.Source, nil
	}
	row, err := findTeamSiteTemplate(ctx, env, ref)
	if err != nil {
		return sitetemplate.Template{}, false, "", err
	}
	t, err := sitetemplate.Parse([]byte(row.SourceYaml))
	if err != nil {
		return sitetemplate.Template{}, false, "", errors.New("this saved template failed to re-parse")
	}
	return t, false, row.SourceYaml, nil
}

func findTeamSiteTemplate(ctx context.Context, env *Env, ref string) (db.SiteTemplate, error) {
	if ref == "" {
		return db.SiteTemplate{}, errors.New("template is required (a name or an id)")
	}
	s, err := env.Q.GetTeamSiteTemplate(ctx, db.GetTeamSiteTemplateParams{TeamID: env.Caller.TeamID, ID: ref})
	if err == nil {
		return s, nil
	}
	s, err = env.Q.GetTeamSiteTemplateByName(ctx, db.GetTeamSiteTemplateByNameParams{TeamID: env.Caller.TeamID, Name: ref})
	if err != nil {
		return db.SiteTemplate{}, errors.New("template not found")
	}
	return s, nil
}

func siteTemplateSaveErr(err error) error {
	if strings.Contains(err.Error(), "site_templates_team_id_name_key") {
		return errors.New("a template with that name already exists")
	}
	return errors.New("could not save the template")
}

// mcpResolveServiceURLs looks up and reveals the connection string of every data service a
// template's needs ask for, from the caller's postgres_service/mysql_service/redis_service args. A
// template never provisions one itself — the caller must name an existing, running service.
func mcpResolveServiceURLs(ctx context.Context, env *Env, needsSvc sitetemplate.Needs, args Args) (sitetemplate.ServiceURLs, error) {
	dbs, err := dbsOf(env)
	if err != nil {
		return sitetemplate.ServiceURLs{}, err
	}
	var out sitetemplate.ServiceURLs
	pick := func(kind, ref string) (string, error) {
		if ref == "" {
			return "", fmt.Errorf("this template needs a %s service — pass %s_service", kind, kind)
		}
		svc, err := dbsvc.ServiceByRef(ctx, env.Q, env.Caller.TeamID, ref)
		if err != nil {
			return "", errors.New("the " + kind + " service was not found")
		}
		if svc.Kind != kind {
			return "", fmt.Errorf("%s_service is not a %s service", kind, kind)
		}
		if svc.Status != "running" || svc.Host == nil || svc.Port == nil {
			return "", fmt.Errorf("the %s service is not running yet", kind)
		}
		return dbs.RevealURL(ctx, env.Q, env.Caller.TeamID, dbsvc.Actor(mcpActor(env)), svc, "")
	}
	if needsSvc.Postgres {
		if out.Postgres, err = pick("postgres", args.Str("postgres_service")); err != nil {
			return sitetemplate.ServiceURLs{}, err
		}
	}
	if needsSvc.MySQL {
		if out.MySQL, err = pick("mysql", args.Str("mysql_service")); err != nil {
			return sitetemplate.ServiceURLs{}, err
		}
	}
	if needsSvc.Redis {
		if out.Redis, err = pick("redis", args.Str("redis_service")); err != nil {
			return sitetemplate.ServiceURLs{}, err
		}
	}
	return out, nil
}

func renderRendered(r sitetemplate.Rendered, revealSecrets bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "driver %s, repo %s@%s, port %d, health %s\n", r.Driver.Name, r.RepoURL, r.Branch, r.Port, r.HealthPath)
	if len(r.Env) > 0 {
		b.WriteString("env:\n")
		for _, e := range r.Env {
			v := e.Value
			if e.Secret && !revealSecrets {
				v = "(secret, hidden)"
			}
			fmt.Fprintf(&b, "  %s=%s\n", e.Name, v)
		}
	}
	if r.Notes != "" {
		fmt.Fprintf(&b, "notes: %s\n", r.Notes)
	}
	return b.String()
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

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
