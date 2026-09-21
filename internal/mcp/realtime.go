package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/realtime"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// RealtimeTools: the "Realtime" section of an app — its websocket server as a worker of the release,
// and the path Caddy sends to it. Same actions as the REST API (internal/realtime).
func RealtimeTools() []Tool {
	appArg := str("App name or id")

	describe := func(st realtime.RealtimeStatus) string {
		var b strings.Builder
		if c := st.Config; c == nil {
			b.WriteString("No realtime configuration. set_app_realtime saves one.\n")
		} else {
			state := "enabled"
			if !c.Enabled {
				state = "disabled"
			}
			routed := "NOT routed yet — deploy or restart the app"
			if st.Routed {
				routed = "routed"
			} else if !c.Enabled {
				routed = "not routed"
			}
			fmt.Fprintf(&b, "%s (%s): worker:%s, container port %d, path %s — %s.\n", c.Kind, state, c.Process, c.Port, c.Path, routed)
			if st.ProcessRunning {
				b.WriteString("The live release started that process (its record; not a live probe).\n")
			}
			for _, h := range st.Hostnames {
				b.WriteString("  " + h + "\n")
			}
		}
		if s := st.Suggestion; s.Kind != "" {
			fmt.Fprintf(&b, "Suggestion (%s): kind %s, process %q, port %d, path %s\n", s.Confidence, s.Kind, s.Process, s.Port, s.Path)
		} else {
			b.WriteString("Suggestion: none (unknown)\n")
		}
		for _, e := range st.Suggestion.Evidence {
			b.WriteString("  - " + e + "\n")
		}
		if len(st.DeclaredProcesses) > 0 {
			b.WriteString("Workers of the live release: " + strings.Join(st.DeclaredProcesses, ", ") + "\n")
		}
		b.WriteString("Connections: unknown (" + st.ConnectionsReason + ").\n" + st.Note)
		return b.String()
	}

	return []Tool{
		{
			Name: "get_app_realtime", Scope: scopes.Read,
			Description: "An app's realtime (websocket) section: the saved configuration (kind, worker process, container port, path), whether " +
				"the live release is routed, the wss:// URLs clients connect to, and a suggestion detected from the app's processes and the " +
				"names of its environment variables, with its confidence and evidence. The number of connections is not known for any kind.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				st, err := realtime.Get(ctx, env.Q, env.Caller.TeamID, a)
				if err != nil {
					return "", err
				}
				return describe(st), nil
			},
		},
		{
			Name: "set_app_realtime", Scope: scopes.Write,
			Description: "Save an app's realtime configuration: which worker of cloudy.toml is the websocket server, the port it listens on inside " +
				"its container, and the path of the app's hostnames that Caddy sends to it. Applies from the next deploy or restart — or at once " +
				"when the live release already publishes that process's port (a changed path, enabling again). Refused for a process the live " +
				"release does not declare, and for imported apps. Owner or admin.",
			InputSchema: schema([]string{"app", "kind", "process", "port"}, map[string]any{"app": appArg,
				"kind":       map[string]any{"type": "string", "enum": realtime.Kinds, "description": "What the process is; it only chooses the default path"},
				"process":    str(`The worker's bare name: reverb for cloudy.toml's "worker:reverb"`),
				"port":       map[string]any{"type": "integer", "minimum": 1, "maximum": 65535, "description": "The port the process listens on inside its container"},
				"path":       str("Default /app (reverb), /socket.io (socketio), /ws (others)"),
				"enabled":    map[string]any{"type": "boolean", "description": "Default true; false keeps the configuration and removes the route at once"},
				"whole_site": map[string]any{"type": "boolean", "description": "Required, with kind custom, for the path /"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
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
				in := realtime.Input{Kind: args.Str("kind"), Process: args.Str("process"), Path: args.Str("path"), WholeSite: args.Bool("whole_site")}
				if v, ok := args["port"].(float64); ok && v == float64(int(v)) {
					in.Port = int(v) // not clamped: a port out of range is refused, not corrected
				}
				if v, ok := args["enabled"].(bool); ok {
					in.Enabled = &v
				}
				res, err := realtime.Set(ctx, env.Q, o, env.Caller.TeamID, mcpActor(env), a, in)
				if err != nil {
					return "", err
				}
				env.Then = func(context.Context) (string, error) { o.Wake(); return "", nil }
				head := "Saved. It applies from " + a.Name + "'s next deploy or restart.\n"
				if res.Applies == realtime.AppliesNow {
					head = "Saved; applied at once.\n"
					if res.Operation != nil {
						head = "Saved; the route is being changed now (operation " + *res.Operation + ").\n"
					}
				}
				return head + describe(res.RealtimeStatus), nil
			},
		},
		{
			Name: "remove_app_realtime", Scope: scopes.Write,
			Description: "Remove an app's realtime configuration. A route the live release has is removed at once; the process keeps running, " +
				"reachable on loopback only, until the next deploy or restart. Owner or admin.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
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
				res, err := realtime.Remove(ctx, env.Q, o, env.Caller.TeamID, mcpActor(env), a)
				if err != nil {
					return "", err
				}
				env.Then = func(context.Context) (string, error) { o.Wake(); return "", nil }
				out := "Removed " + a.Name + "'s realtime configuration."
				if res.Operation != nil {
					out += " Its route is being removed now (operation " + *res.Operation + ")."
				}
				return out, nil
			},
		},
	}
}
