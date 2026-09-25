package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/monitors"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// MonitorTools: the "Alerts & monitors" section of an app — HTTP checks of its own hostnames, CPU /
// memory / disk rules over its server's metrics, and the incidents they opened.
func MonitorTools() []Tool {
	appArg := str("App name or id")
	num := func(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
	settings := func(extra map[string]any) map[string]any {
		props := map[string]any{
			"app":              appArg,
			"target":           str("http: an http(s):// URL on one of the app's own hostnames (anything else is refused)"),
			"expected_status":  num("http: the status a check must answer (default 200); 0 = any 2xx or 3xx"),
			"threshold":        num("http: the slowest acceptable response in ms, 0 = no limit. cpu | memory | disk: percent, 1 to 100"),
			"window_minutes":   num("cpu | memory | disk: minutes the average is taken over, 1 to 60 (default 5)"),
			"interval_seconds": num("How often it is checked, 30 to 3600 (default 60)"),
			"enabled":          map[string]any{"type": "boolean", "description": "Switch the monitor on or off (default on)"},
			"notify_emails": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": monitors.MaxEmails,
				"description": "Who is emailed when an incident opens or resolves"},
			"notify_webhook": str("An https:// URL that is POSTed {event, monitor, app, reason, value, at}; \"\" = none"),
			"notify_channel_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": monitors.MaxChannels,
				"description": "Team notification channel ids (notification_channels_list) this monitor's incidents also reach"},
			"heartbeat_interval_seconds": num(fmt.Sprintf("kind heartbeat: how often a ping is expected, %d to %d seconds (default 300)",
				monitors.MinHeartbeatInterval, monitors.MaxHeartbeatInterval)),
			"heartbeat_grace_seconds": num(fmt.Sprintf("kind heartbeat: how late a ping may be before an incident opens, %d to %d seconds (default 120)",
				monitors.MinHeartbeatGrace, monitors.MaxHeartbeatGrace)),
		}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}
	input := func(args Args) (monitors.Input, error) {
		in := monitors.Input{Kind: args.Str("kind")}
		if _, ok := args["name"]; ok {
			in.Name = ptrTo(args.Str("name"))
		}
		for k, dst := range map[string]**string{"target": &in.Target, "notify_webhook": &in.NotifyWebhook} {
			if v, ok := args[k].(string); ok {
				*dst = ptrTo(v)
			}
		}
		for k, dst := range map[string]**int{"expected_status": &in.ExpectedStatus, "window_minutes": &in.WindowMinutes, "interval_seconds": &in.IntervalSeconds,
			"heartbeat_interval_seconds": &in.HeartbeatIntervalSeconds, "heartbeat_grace_seconds": &in.HeartbeatGraceSeconds} {
			if v, ok := args[k].(float64); ok {
				if v != float64(int(v)) {
					return in, fmt.Errorf("%s must be a whole number", k)
				}
				*dst = ptrTo(int(v))
			}
		}
		if v, ok := args["threshold"].(float64); ok {
			in.Threshold = &v
		}
		if v, ok := args["enabled"].(bool); ok {
			in.Enabled = &v
		}
		if raw, ok := args["notify_emails"]; ok {
			list, isList := raw.([]any)
			if !isList {
				return in, errors.New("notify_emails must be a list of email addresses")
			}
			emails := []string{}
			for _, e := range list {
				s, isStr := e.(string)
				if !isStr {
					return in, errors.New("notify_emails must be a list of email addresses")
				}
				emails = append(emails, s)
			}
			in.NotifyEmails = &emails
		}
		if raw, ok := args["notify_channel_ids"]; ok {
			list, isList := raw.([]any)
			if !isList {
				return in, errors.New("notify_channel_ids must be a list of channel ids")
			}
			ids := []string{}
			for _, e := range list {
				s, isStr := e.(string)
				if !isStr {
					return in, errors.New("notify_channel_ids must be a list of channel ids")
				}
				ids = append(ids, s)
			}
			in.NotifyChannelIDs = &ids
		}
		return in, nil
	}
	clock := func(env *Env) time.Time {
		if env.App != nil {
			if s, ok := env.App.Monitors.(*monitors.Service); ok && s != nil {
				return s.Now()
			}
		}
		return time.Now()
	}

	return []Tool{
		{
			Name: "list_app_monitors", Scope: scopes.Read,
			Description: "An app's monitors: kind (http, cpu, memory, disk), rule, state (unknown, ok, failing), the last error and the open incident.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				list, err := monitors.List(ctx, env.Q, env.Caller.TeamID, a)
				if err != nil {
					return "", err
				}
				if len(list) == 0 {
					return a.Name + " has no monitors. create_app_monitor adds one.", nil
				}
				var b strings.Builder
				for _, m := range list {
					b.WriteString(monitorLine(m))
				}
				return b.String(), nil
			},
		},
		{
			Name: "list_app_incidents", Scope: scopes.Read,
			Description: "The incidents of an app's monitors, newest first: when each opened and resolved, why, and the latest reading.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg,
				"limit":  num("How many (1 to 100, default 20)"),
				"before": str("next cursor of the previous page")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				f, err := monitors.Incidents(ctx, env.Q, env.Caller.TeamID, a, args.Int("limit", 20, 1, 100), args.Str("before"))
				if err != nil {
					return "", err
				}
				if len(f.Incidents) == 0 {
					return "No incidents.", nil
				}
				var b strings.Builder
				for _, i := range f.Incidents {
					status := "OPEN"
					if i.ResolvedAt != nil {
						status = "resolved " + i.ResolvedAt.UTC().Format(time.RFC3339)
					}
					fmt.Fprintf(&b, "  %s  %s (%s)  opened %s  %s  %s", i.ID, i.Monitor.Name, i.Monitor.Kind, i.OpenedAt.UTC().Format(time.RFC3339), status, i.Reason)
					if i.LastValue != "" {
						fmt.Fprintf(&b, "  [%s]", i.LastValue)
					}
					b.WriteString("\n")
				}
				if f.NextCursor != nil {
					fmt.Fprintf(&b, "next page: before=%s\n", *f.NextCursor)
				}
				return b.String(), nil
			},
		},
		{
			Name: "create_app_monitor", Scope: scopes.Write,
			Description: "Add a monitor to an app. kind http: GET target (a URL on one of the app's OWN hostnames) every interval_seconds, " +
				"expecting expected_status within threshold ms; an incident opens on the second failure in a row. kind cpu | memory | disk: " +
				"the app's server's average over window_minutes against threshold percent. kind heartbeat: mints a unique, unguessable ping " +
				"URL (shown once, in this response, and never again) that something else must call at least every heartbeat_interval_seconds " +
				"(+ heartbeat_grace_seconds of grace) or an incident opens; a ping resolves it. Incidents are announced to the team, to " +
				"notify_emails, to notify_webhook and to notify_channel_ids. Owner or admin.",
			InputSchema: schema([]string{"app", "name", "kind"}, settings(map[string]any{
				"name": str("Unique in the app, up to 80 characters"),
				"kind": map[string]any{"type": "string", "enum": []string{"http", "cpu", "memory", "disk", "heartbeat"}}})),
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
				if in.Name == nil {
					in.Name = ptrTo("")
				}
				m, err := monitors.Create(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, in)
				if err != nil {
					return "", err
				}
				out := "Created.\n" + monitorLine(m)
				if m.PingURLPath != "" {
					out += fmt.Sprintf("\nPing URL (shown once, never again), on the API origin: %s\ncurl example: curl -fsS <api origin>%s\n", m.PingURLPath, m.PingURLPath)
				}
				return out, nil
			},
		},
		{
			Name: "update_app_monitor", Scope: scopes.Write,
			Description: "Change a monitor's settings (never its kind); only the fields given change. Changing the rule, or switching it " +
				"off, forgets its state and closes its open incident quietly. Owner or admin.",
			InputSchema: schema([]string{"app", "monitor"}, settings(map[string]any{
				"monitor": str("Monitor name or id"), "name": str("A new name")})),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				cur, err := monitors.Find(ctx, env.Q, env.Caller.TeamID, a, args.Str("monitor"))
				if err != nil {
					return "", err
				}
				in, err := input(args)
				if err != nil {
					return "", err
				}
				m, err := monitors.Update(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, cur, in, clock(env))
				if err != nil {
					return "", err
				}
				return "Updated.\n" + monitorLine(m), nil
			},
		},
		{
			Name: "delete_app_monitor", Scope: scopes.Write,
			Description: "Remove a monitor of an app, with its incidents. Owner or admin.",
			InputSchema: schema([]string{"app", "monitor"}, map[string]any{"app": appArg, "monitor": str("Monitor name or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				cur, err := monitors.Find(ctx, env.Q, env.Caller.TeamID, a, args.Str("monitor"))
				if err != nil {
					return "", err
				}
				if err := monitors.Delete(ctx, env.Q, env.Caller.TeamID, mcpActor(env), a, cur); err != nil {
					return "", err
				}
				return "Deleted " + cur.Name + ".", nil
			},
		},
		{
			Name: "monitor_uptime", Scope: scopes.Read,
			Description: "A monitor's uptime over the last 24h, 7d and 30d, as a percentage. Any member.",
			InputSchema: schema([]string{"app", "monitor"}, map[string]any{"app": appArg, "monitor": str("Monitor name or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				cur, err := monitors.Find(ctx, env.Q, env.Caller.TeamID, a, args.Str("monitor"))
				if err != nil {
					return "", err
				}
				u, err := monitors.MonitorUptime(ctx, env.Q, env.Caller.TeamID, cur.ID, clock(env))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("%s uptime — 24h: %g%%  7d: %g%%  30d: %g%%\n", cur.Name, u.Last24h, u.Last7d, u.Last30d), nil
			},
		},
	}
}

func ptrTo[T any](v T) *T { return &v }

func monitorLine(m monitors.Monitor) string {
	var b strings.Builder
	rule := ""
	if m.Kind == monitors.KindHTTP {
		rule = m.Target
		if m.ExpectedStatus == 0 {
			rule += "  expects 2xx/3xx"
		} else {
			rule += fmt.Sprintf("  expects %d", m.ExpectedStatus)
		}
		if m.Threshold > 0 {
			rule += fmt.Sprintf(" within %g ms", m.Threshold)
		}
	} else if m.Kind == monitors.KindHeartbeat {
		rule = fmt.Sprintf("expects a ping every %d s (%d s grace)", m.HeartbeatIntervalSeconds, m.HeartbeatGraceSeconds)
	} else {
		rule = fmt.Sprintf("above %g%% over %d min", m.Threshold, m.WindowMinutes)
	}
	state := m.State
	if !m.Enabled {
		state = "disabled"
	}
	fmt.Fprintf(&b, "  %s  %-24s %-10s %-8s %s  every %d s", m.ID, m.Name, m.Kind, state, rule, m.IntervalSeconds)
	if m.Kind == monitors.KindHeartbeat && m.LastPingAt != nil {
		fmt.Fprintf(&b, "  last ping %s", m.LastPingAt.UTC().Format(time.RFC3339))
	}
	if m.LastError != "" {
		fmt.Fprintf(&b, "  last error: %s", m.LastError)
	}
	if m.OpenIncident != nil {
		fmt.Fprintf(&b, "  INCIDENT since %s: %s", m.OpenIncident.OpenedAt.UTC().Format(time.RFC3339), m.OpenIncident.Reason)
	}
	b.WriteString("\n")
	return b.String()
}
