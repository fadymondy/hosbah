package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/pgtext"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// statusPageSlugRe mirrors internal/api/status_pages.go's slugRe (unexported there).
var statusPageSlugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// StatusPageTools: a team's public status pages (MH-575 H3) — a published, read-only page at
// /status/{slug} showing a chosen set of the team's monitors. Owner or admin write; any member
// reads. Mirrors internal/api/status_pages.go.
func StatusPageTools() []Tool {
	pageArg := str("Status page id or slug")

	line := func(p db.StatusPage) string {
		return fmt.Sprintf("  %s  /%s  %q  published=%v  monitors=%d\n", p.ID, p.Slug, p.Title, p.Published, len(p.MonitorIds))
	}

	return []Tool{
		{
			Name: "status_pages_list", Scope: scopes.Read,
			Description: "The team's status pages: slug, title, published state, monitors shown. Any member.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				rows, err := env.Q.ListStatusPagesOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", err
				}
				if len(rows) == 0 {
					return "No status pages yet. status_page_create adds one.", nil
				}
				var b strings.Builder
				for _, p := range rows {
					b.WriteString(line(p))
				}
				return b.String(), nil
			},
		},
		{
			Name: "status_page_create", Scope: scopes.Write,
			Description: "Add a status page. slug: lowercase letters, digits, hyphens, unique across every team. " +
				"monitor_ids: which of the team's monitors it shows. Owner or admin. Audited.",
			InputSchema: schema([]string{"slug", "title"}, map[string]any{
				"slug":  str("Lowercase letters, digits and hyphens, 1 to 63 characters"),
				"title": str("1 to 120 characters"),
				"monitor_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 50,
					"description": "Ids of the team's monitors to show"},
				"published": map[string]any{"type": "boolean", "description": "Default false"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageTeam); err != nil {
					return "", err
				}
				slug, err := validateSlugMCP(args.Str("slug"))
				if err != nil {
					return "", err
				}
				title, err := validateTitleMCP(args.Str("title"))
				if err != nil {
					return "", err
				}
				ids := strList(args["monitor_ids"])
				if err := monitorIDsExistMCP(ctx, env, ids); err != nil {
					return "", err
				}
				if n, err := env.Q.StatusPageSlugTaken(ctx, slug); err != nil {
					return "", errors.New("could not check the slug")
				} else if n > 0 {
					return "", errors.New("that slug is already taken")
				}
				p, err := env.Q.InsertStatusPage(ctx, db.InsertStatusPageParams{TeamID: env.Caller.TeamID, Slug: slug, Title: title,
					MonitorIds: pgtext.Array(ids), Published: args.Bool("published")})
				if err != nil {
					return "", errors.New("could not create the status page")
				}
				return "Created.\n" + line(p), nil
			},
		},
		{
			Name: "status_page_update", Scope: scopes.Write,
			Description: "Change a status page's title, monitors or publication state; only the fields given change. Owner or admin. Audited.",
			InputSchema: schema([]string{"status_page"}, map[string]any{
				"status_page": pageArg,
				"title":       str("1 to 120 characters"),
				"monitor_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 50},
				"published":   map[string]any{"type": "boolean"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageTeam); err != nil {
					return "", err
				}
				p, err := statusPageByRefMCP(ctx, env, args.Str("status_page"))
				if err != nil {
					return "", err
				}
				title := p.Title
				if _, ok := args["title"]; ok {
					if title, err = validateTitleMCP(args.Str("title")); err != nil {
						return "", err
					}
				}
				ids := p.MonitorIds
				if _, ok := args["monitor_ids"]; ok {
					ids = strList(args["monitor_ids"])
					if err := monitorIDsExistMCP(ctx, env, ids); err != nil {
						return "", err
					}
				}
				published := p.Published
				if _, ok := args["published"]; ok {
					published = args.Bool("published")
				}
				up, err := env.Q.UpdateStatusPageInTeam(ctx, db.UpdateStatusPageInTeamParams{ID: p.ID, TeamID: env.Caller.TeamID, Title: title,
					MonitorIds: pgtext.Array(ids), Published: published})
				if err != nil {
					return "", errors.New("could not update the status page")
				}
				return "Updated.\n" + line(up), nil
			},
		},
		{
			Name: "status_page_delete", Scope: scopes.Destructive, Destructive: true, ResourceType: "team",
			Description: "Remove a status page. confirm must equal the page's slug. Owner or admin. Audited.",
			InputSchema: schema([]string{"status_page", "confirm"}, map[string]any{"status_page": pageArg, "confirm": str("The page's slug, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageTeam); err != nil {
					return "", err
				}
				p, err := statusPageByRefMCP(ctx, env, args.Str("status_page"))
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, p.Slug); err != nil {
					return "", err
				}
				n, err := env.Q.DeleteStatusPageInTeam(ctx, db.DeleteStatusPageInTeamParams{ID: p.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("could not delete the status page")
				}
				if n == 0 {
					return "", errors.New("status page not found")
				}
				return "Deleted " + p.Slug + ".", nil
			},
		},
	}
}

func validateSlugMCP(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !statusPageSlugRe.MatchString(s) {
		return "", errors.New("slug must be lowercase letters, digits and hyphens, 1 to 63 characters, not starting or ending with a hyphen")
	}
	return s, nil
}

func validateTitleMCP(s string) (string, error) {
	s = strings.TrimSpace(s)
	if n := len([]rune(s)); n < 1 || n > 120 {
		return "", errors.New("title must be 1 to 120 characters")
	}
	return s, nil
}

func monitorIDsExistMCP(ctx context.Context, env *Env, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := env.Q.MonitorsByIDsInTeam(ctx, db.MonitorsByIDsInTeamParams{TeamID: env.Caller.TeamID, Ids: strings.Join(ids, ",")})
	if err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return errors.New("monitor_ids: one or more of those monitors do not exist")
	}
	return nil
}

func statusPageByRefMCP(ctx context.Context, env *Env, ref string) (db.StatusPage, error) {
	p, err := env.Q.StatusPageInTeam(ctx, db.StatusPageInTeamParams{TeamID: env.Caller.TeamID, Ref: strings.TrimSpace(ref)})
	if err != nil {
		return db.StatusPage{}, errors.New("status page not found")
	}
	return p, nil
}
