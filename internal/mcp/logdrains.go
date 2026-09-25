package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// LogDrainTools: H8 team log drains (syslog, http, loki) — ship the team's app containers' logs to
// an outside collector, scoped to one app or every app. Write and delete need owner or admin
// (AbilityManageServers); delete_log_drain is destructive (confirm = the drain's name).
func LogDrainTools() []Tool {
	labels := func(extra map[string]any) map[string]any { return extra }
	writeProps := labels(map[string]any{
		"name":             str("Up to 80 characters"),
		"app":              str("Scope to one app (name or id); omit or \"\" for every app of the team"),
		"internal":         map[string]any{"type": "boolean", "description": "Allow a private, loopback or link-local http/loki address"},
		"enabled":          map[string]any{"type": "boolean", "description": "Default true"},
		"syslog_transport": map[string]any{"type": "string", "enum": []string{"tcp+tls", "udp"}, "description": "syslog only"},
		"syslog_host":      str("syslog only"),
		"syslog_port":      map[string]any{"type": "number", "description": "syslog only"},
		"url":              str("http or loki: the collector's URL (https unless internal)"),
		"auth_header":      str("http or loki: the header name the auth value is sent as, e.g. Authorization"),
		"auth_value":       str("The auth header's plaintext value, sealed at rest; omit to leave unchanged, \"\" to clear it"),
		"loki_labels":      map[string]any{"type": "object", "description": "loki only: extra labels attached to every stream"},
	})
	buildInput := func(ctx context.Context, env *Env, kind string, args Args) (deploy.LogDrainInput, error) {
		in := deploy.LogDrainInput{Kind: kind}
		if v, ok := args["name"].(string); ok {
			in.Name = v
		}
		if v, ok := args["app"].(string); ok {
			v = strings.TrimSpace(v)
			in.AppIDSet = true
			if v != "" {
				a, err := findApp(ctx, env, Args{"app": v})
				if err != nil {
					return deploy.LogDrainInput{}, err
				}
				in.AppID = a.ID
			}
		}
		if v, ok := args["internal"].(bool); ok {
			in.Internal = v
		}
		if v, ok := args["enabled"].(bool); ok {
			in.Enabled = &v
		}
		if v, ok := args["auth_value"].(string); ok {
			in.AuthValue = &v
		}
		if _, ok := args["syslog_host"]; ok || kind == agentproto.DrainKindSyslog {
			port := args.Int("syslog_port", 0, 0, 65535)
			in.Syslog = &agentproto.SyslogSink{Transport: args.Str("syslog_transport"), Host: args.Str("syslog_host"), Port: port}
		}
		if _, ok := args["url"]; ok && kind == agentproto.DrainKindHTTP {
			in.HTTP = &deploy.LogDrainHTTPConfig{URL: args.Str("url"), AuthHeader: args.Str("auth_header")}
		}
		if _, ok := args["url"]; ok && kind == agentproto.DrainKindLoki {
			l := &deploy.LogDrainLokiConfig{URL: args.Str("url"), AuthHeader: args.Str("auth_header")}
			if raw, ok := args["loki_labels"].(map[string]any); ok {
				l.Labels = map[string]string{}
				for k, v := range raw {
					if s, ok := v.(string); ok {
						l.Labels[k] = s
					}
				}
			}
			in.Loki = l
		}
		return in, nil
	}
	find := func(ctx context.Context, env *Env, args Args) (db.LogDrain, error) {
		s, err := deploys(env)
		if err != nil {
			return db.LogDrain{}, err
		}
		return s.LogDrainByID(ctx, env.Q, env.Caller.TeamID, strings.TrimSpace(args.Str("drain")))
	}

	return []Tool{
		{
			Name: "log_drains_list", Scope: scopes.Read,
			Description: "The team's log drains: kind, scope (app or every app), enabled, ship/drop counters, last success and error.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				rows, err := s.ListLogDrains(ctx, env.Q, env.Caller.TeamID)
				if err != nil {
					return "", err
				}
				if len(rows) == 0 {
					return "No log drains yet. log_drain_create adds one.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					b.WriteString(logDrainLine(r))
				}
				return b.String(), nil
			},
		},
		{
			Name: "log_drain_create", Scope: scopes.Write,
			Description: "Add a log drain: kind syslog (tcp+tls or udp), http (NDJSON batches) or loki (its push API). An http or loki " +
				"url must be https unless internal is set. Owner or admin.",
			InputSchema: schema([]string{"kind"}, mergeProps(map[string]any{
				"kind": map[string]any{"type": "string", "enum": []string{"syslog", "http", "loki"}},
			}, writeProps)),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				kind := args.Str("kind")
				in, err := buildInput(ctx, env, kind, args)
				if err != nil {
					return "", err
				}
				row, err := s.CreateLogDrain(ctx, env.Q, env.Caller.TeamID, mcpActor(env), in)
				if err != nil {
					return "", err
				}
				return "Created.\n" + logDrainLine(row), nil
			},
		},
		{
			Name: "log_drain_update", Scope: scopes.Write,
			Description: "Change a log drain's settings (never its kind); re-applied at once on every server it covers. Only the fields " +
				"given change. Owner or admin.",
			InputSchema: schema([]string{"drain"}, mergeProps(map[string]any{"drain": str("Log drain id")}, writeProps)),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				row, err := find(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				in, err := buildInput(ctx, env, row.Kind, args)
				if err != nil {
					return "", err
				}
				updated, err := s.UpdateLogDrain(ctx, env.Q, env.Caller.TeamID, mcpActor(env), row, in)
				if err != nil {
					return "", err
				}
				return "Updated.\n" + logDrainLine(updated), nil
			},
		},
		{
			Name: "log_drain_delete", Scope: scopes.Destructive, Destructive: true, ResourceType: "log_drain",
			Description: "Remove a log drain everywhere it runs. confirm must equal the drain's name.",
			InputSchema: schema([]string{"drain", "confirm"}, map[string]any{"drain": str("Log drain id"), "confirm": str("The drain's name, exactly")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				row, err := find(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, row.Name); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				if err := s.DeleteLogDrain(ctx, env.Q, env.Caller.TeamID, mcpActor(env), row); err != nil {
					return "", err
				}
				return "Deleted " + row.Name + ".", nil
			},
		},
		{
			Name: "log_drain_test", Scope: scopes.Write,
			Description: "Ship one synthetic line through the drain now and report whether the sink accepted it (waits up to 30s). Owner or admin.",
			InputSchema: schema([]string{"drain"}, map[string]any{"drain": str("Log drain id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				row, err := find(ctx, env, args)
				if err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				res, err := s.TestLogDrain(ctx, env.Q, env.Caller.TeamID, row)
				if err != nil {
					return "", err
				}
				if res.OK {
					return "OK: " + res.Detail, nil
				}
				return "", fmt.Errorf("the sink refused the test line: %s", res.Detail)
			},
		},
	}
}

func mergeProps(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func logDrainLine(r db.LogDrain) string {
	scope := "every app"
	if r.AppID != "" {
		scope = "app " + r.AppID
	}
	state := "enabled"
	if !r.Enabled {
		state = "disabled"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  %s  %-20s %-7s %-8s %-10s shipped=%d dropped=%d", r.ID, r.Name, r.Kind, state, scope, r.Shipped, r.Dropped)
	if r.LastError != "" {
		fmt.Fprintf(&b, "  last error: %s", r.LastError)
	}
	b.WriteString("\n")
	return b.String()
}
