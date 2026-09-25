package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/services"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// SharedServiceTools: the team's directory of Proxmox guests classified as shared infra, and what
// an app's env variables were detected pointing at. Same actions as REST (internal/api/shared_services.go).
func SharedServiceTools() []Tool {
	svcArg := str("Shared service name or id")

	findSharedService := func(ctx context.Context, env *Env, args Args) (db.SharedService, error) {
		ref := args.Str("service")
		if ref == "" {
			return db.SharedService{}, errors.New("service is required")
		}
		if row, err := env.Q.SharedServiceInTeam(ctx, db.SharedServiceInTeamParams{ID: ref, TeamID: env.Caller.TeamID}); err == nil {
			return row, nil
		}
		rows, err := env.Q.ListSharedServicesOfTeam(ctx, env.Caller.TeamID)
		if err != nil {
			return db.SharedService{}, errors.New("not found")
		}
		for _, r := range rows {
			if strings.EqualFold(r.Name, ref) {
				return r, nil
			}
		}
		return db.SharedService{}, errors.New("not found")
	}

	return []Tool{
		{
			Name: "shared_services_list", Scope: scopes.Read,
			Description: "The team's shared services: Proxmox guests classified as shared infra (databases, caches, search, a browser box…), and the node/CT that hosts each.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				rows, err := env.Q.ListSharedServicesOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list shared services")
				}
				if len(rows) == 0 {
					return "No shared services yet. shared_services_scan looks for them on the team's Proxmox nodes.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d shared service(s):\n", len(rows))
				for _, r := range rows {
					endpoint := "—"
					if r.Host != nil {
						endpoint = *r.Host
						if r.Port != nil {
							endpoint = fmt.Sprintf("%s:%d", endpoint, *r.Port)
						}
					}
					hidden := ""
					if r.Hidden {
						hidden = " (hidden)"
					}
					fmt.Fprintf(&b, "  %-16s %-12s CT %-6s %-22s (id %s)%s\n", r.Kind, r.Name, r.GuestVmid, endpoint, r.ID, hidden)
				}
				return b.String(), nil
			},
		},
		{
			Name: "shared_services_scan", Scope: scopes.Write,
			Description: "Rescan every Proxmox node server's guests for shared services. Admin or owner.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				nodes, err := env.Q.ListServersOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list servers")
				}
				n := 0
				for _, s := range nodes {
					if s.Facts == nil {
						continue
					}
					rows, err := services.ScanFromFacts(ctx, env.Q, env.Caller.TeamID, s.ID, []byte(*s.Facts))
					if err != nil {
						return "", errors.New("could not scan " + s.Name)
					}
					n += len(rows)
				}
				return fmt.Sprintf("Scanned. %d shared service(s) found or refreshed.", n), nil
			},
		},
		{
			Name: "shared_service_update", Scope: scopes.Write,
			Description: "Rename, recategorise or hide a shared service. Admin or owner.",
			InputSchema: schema([]string{"service"}, map[string]any{"service": svcArg,
				"name": str("New name"), "kind": str("New kind (postgres, mysql, redis, nats, minio, searxng, firecrawl, browser, meilisearch, rabbitmq, elasticsearch, opensearch, clickhouse, mongodb, other)"),
				"hidden": map[string]any{"type": "boolean", "description": "Hide (or unhide) it from the list"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				svc, err := findSharedService(ctx, env, args)
				if err != nil {
					return "", err
				}
				var name, kind *string
				var hidden sql.NullBool
				if v, ok := args["name"].(string); ok && v != "" {
					name = &v
				}
				if v, ok := args["kind"].(string); ok && v != "" {
					kind = &v
				}
				if v, ok := args["hidden"].(bool); ok {
					hidden = sql.NullBool{Bool: v, Valid: true}
				}
				row, err := env.Q.UpdateSharedService(ctx, db.UpdateSharedServiceParams{ID: svc.ID, TeamID: env.Caller.TeamID, Name: name, Kind: kind, Hidden: hidden})
				if err != nil {
					return "", errors.New("could not update the shared service")
				}
				return fmt.Sprintf("Updated %s (%s).", row.Name, row.Kind), nil
			},
		},
		{
			Name: "app_service_links", Scope: scopes.Read,
			Description: "What an app's env variables were detected pointing at: a shared service or a data service.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": str("App name or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				rows, err := env.Q.ListAppServiceLinks(ctx, db.ListAppServiceLinksParams{AppID: a.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("could not list the app's service links")
				}
				if len(rows) == 0 {
					return "No service links detected. app_service_links_detect re-scans the app's env.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					fmt.Fprintf(&b, "  %-24s -> %s service %s (%s)\n", r.Variable, r.ServiceKind, r.ServiceID, r.Source)
				}
				return b.String(), nil
			},
		},
		{
			Name: "app_service_links_detect", Scope: scopes.Deploy,
			Description: "Re-parse the app's env variables for connection endpoints and re-match them to shared and data services. Deployer, admin or owner.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": str("App name or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				o, err := opsOf(env)
				if err != nil {
					return "", err
				}
				links, err := services.Detect(ctx, env.Q, o, env.Caller.TeamID, a)
				if err != nil {
					return "", errors.New("could not detect service links")
				}
				if len(links) == 0 {
					return fmt.Sprintf("%s: no connection variables matched a known service.", a.Name), nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%s: %d link(s) detected.\n", a.Name, len(links))
				for _, l := range links {
					fmt.Fprintf(&b, "  %-24s -> %s service %s\n", l.Variable, l.ServiceKind, l.ServiceID)
				}
				return b.String(), nil
			},
		},
	}
}
