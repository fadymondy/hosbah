package mcp

import (
	"context"
	"fmt"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/quota"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ResourceTools: memory/CPU limits for apps and data services (MH-574 E13), and a team's quota
// usage. Same actions as the REST API (internal/api/resources_handler.go, teams.go).
func ResourceTools() []Tool {
	appArg := str("App name or id")
	serviceArg := str("Data service name or id")
	memArg := map[string]any{"type": "integer", "minimum": 0, "maximum": 1048576, "description": "MiB, 32-1048576 (1 TiB); 0 or omitted: no limit"}
	cpusArg := map[string]any{"type": "number", "minimum": 0, "maximum": 256, "description": "Fractional CPUs, 0.05-256; 0 or omitted: no limit"}

	cpusOf := func(args Args) float64 {
		v, _ := args["cpus"].(float64)
		return v
	}

	limitsOf := func(r agentproto.ResourceLimits) string {
		mem, cpus := "unlimited", "unlimited"
		if r.MemoryMB > 0 {
			mem = fmt.Sprintf("%d MiB", r.MemoryMB)
		}
		if r.CPUs > 0 {
			cpus = fmt.Sprintf("%g", r.CPUs)
		}
		return fmt.Sprintf("memory %s, cpus %s", mem, cpus)
	}

	return []Tool{
		{
			Name: "app_resources_get", Scope: scopes.Read,
			Description: "An app's memory and CPU limits: a base limit and any per-process overrides.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				r := deploy.Resources(a)
				out := a.Name + ": " + limitsOf(r.Base)
				for name, o := range r.Processes {
					out += fmt.Sprintf("\n  %s: %s", name, limitsOf(o))
				}
				return out, nil
			},
		},
		{
			Name: "app_resources_set", Scope: scopes.Write,
			Description: "Set an app's memory and CPU limits. Applies live to the running containers (no redeploy needed) and to the " +
				"next deploy or restart. Owner, admin or deployer.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg, "memory_mb": memArg, "cpus": cpusArg,
				"process": str("Optional: set an override for one process (\"web\" or \"worker:<name>\") instead of the base limit")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				r := deploy.Resources(a)
				limit := agentproto.ResourceLimits{MemoryMB: args.Int("memory_mb", 0, 0, agentproto.MaxMemoryMB), CPUs: cpusOf(args)}
				if process := args.Str("process"); process != "" {
					if r.Processes == nil {
						r.Processes = map[string]agentproto.ResourceLimits{}
					}
					r.Processes[process] = limit
				} else {
					r.Base = limit
				}
				updated, err := s.SetResources(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, r)
				if err != nil {
					return "", err
				}
				return "Saved " + a.Name + "'s resource limits: " + limitsOf(deploy.Resources(updated).Base), nil
			},
		},
		{
			Name: "data_service_resources_get", Scope: scopes.Read,
			Description: "A data service's memory and CPU limits.",
			InputSchema: schema([]string{"database"}, map[string]any{"database": serviceArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				svc, err := findService(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				return svc.Name + ": " + limitsOf(s.Resources(svc)), nil
			},
		},
		{
			Name: "data_service_resources_set", Scope: scopes.Write,
			Description: "Set a Hosbah-managed data service's memory and CPU limits. Applies live (no restart needed). Refused for an " +
				"imported service. Owner or admin.",
			InputSchema: schema([]string{"database"}, map[string]any{"database": serviceArg, "memory_mb": memArg, "cpus": cpusArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				svc, err := findService(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := dbsOf(env)
				if err != nil {
					return "", err
				}
				limit := agentproto.ResourceLimits{MemoryMB: args.Int("memory_mb", 0, 0, agentproto.MaxMemoryMB), CPUs: cpusOf(args)}
				updated, err := s.SetResources(ctx, env.Q, env.Caller.TeamID, mcpActor(env), svc, limit)
				if err != nil {
					return "", err
				}
				return "Saved " + svc.Name + "'s resource limits: " + limitsOf(s.Resources(updated)), nil
			},
		},
		{
			Name: "team_quotas_get", Scope: scopes.Read,
			Description: "This team's quota (max servers/apps/data services; null = unlimited) and its current usage against it.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				u, err := quota.GetUsage(ctx, env.Q, env.Caller.TeamID)
				if err != nil {
					return "", err
				}
				line := func(what string, have int64, max *int64) string {
					if max == nil {
						return fmt.Sprintf("%s: %d (unlimited)", what, have)
					}
					return fmt.Sprintf("%s: %d / %d", what, have, *max)
				}
				return line("servers", u.Servers, u.MaxServers) + "\n" + line("apps", u.Apps, u.MaxApps) + "\n" +
					line("data services", u.DataServices, u.MaxDataServices), nil
			},
		},
	}
}
