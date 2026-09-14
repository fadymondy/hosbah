package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/cloudy/internal/agentproto"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/ops"
	"github.com/fadymondy/cloudy/internal/scopes"
)

// ServerTools are the P1 read tools. All `read`: they never return secrets, key material, sealed
// values or operation logs — only what the servers page shows.
func ServerTools() []Tool {
	serverArg := map[string]any{"server": str("Server id or name")}
	return []Tool{
		{
			Name:        "list_servers",
			Description: "The team's servers: status, online, address, OS, agent version, last heartbeat.",
			Scope:       scopes.Read,
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				rows, err := env.Q.ListServersOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", errors.New("could not list servers")
				}
				if len(rows) == 0 {
					return "No servers yet.", nil
				}
				now := time.Now()
				var b strings.Builder
				fmt.Fprintf(&b, "%d server(s):\n", len(rows))
				for _, s := range rows {
					fmt.Fprintf(&b, "  %-8s %-24s %-8s %-15s %-18s agent %-10s last seen %s  (id %s)\n",
						presence(s, now), s.Name, s.Provider, orDash(s.IpAddress), osOf(s), orDash(s.AgentVersion), ago(s.LastSeenAt, now), s.ID)
				}
				return b.String(), nil
			},
		},
		{
			Name:        "get_server",
			Description: "One server: lifecycle status, online, facts (OS, kernel, CPU, memory, Docker) and its enrolment checks.",
			Scope:       scopes.Read,
			InputSchema: schema([]string{"server"}, serverArg),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				s, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				now := time.Now()
				var b strings.Builder
				fmt.Fprintf(&b, "%s  (id %s)\nprovider: %s (%s)\nstatus: %s, %s, last heartbeat %s\n", s.Name, s.ID, s.Provider, s.Kind,
					s.Status, presence(s, now), ago(s.LastSeenAt, now))
				fmt.Fprintf(&b, "hostname: %s\naddress: %s\nos: %s\nkernel: %s\narch: %s\nagent: %s\n",
					orDash(s.Hostname), orDash(s.IpAddress), osOf(s), orDash(s.Kernel), orDash(s.Arch), orDash(s.AgentVersion))
				if s.Facts != nil {
					var f agentproto.Facts
					if json.Unmarshal([]byte(*s.Facts), &f) == nil {
						fmt.Fprintf(&b, "cpus: %d\nmemory: %s\nsystemd: %v\ndocker: %s\n", f.CPUs, bytesOf(f.MemTotalBytes), f.Systemd, dockerOf(f.Docker))
					}
				}
				if s.Error != nil {
					fmt.Fprintf(&b, "error: %s\n", *s.Error)
				}
				if s.Checks != nil {
					var checks []agentproto.Check
					if json.Unmarshal([]byte(*s.Checks), &checks) == nil && len(checks) > 0 {
						b.WriteString("enrolment checks:\n")
						for _, c := range checks {
							fmt.Fprintf(&b, "  %-4s %-8s %s\n", c.Status, c.Name, c.Detail)
						}
					}
				}
				return b.String(), nil
			},
		},
		{
			Name:        "server_metrics",
			Description: "The latest heartbeat metrics of a server: CPU, memory, root disk, load, uptime.",
			Scope:       scopes.Read,
			InputSchema: schema([]string{"server"}, serverArg),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				s, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				if s.Metrics == nil {
					return s.Name + " has not sent a heartbeat yet.", nil
				}
				var m agentproto.Metrics
				if err := json.Unmarshal([]byte(*s.Metrics), &m); err != nil {
					return "", errors.New("the stored metrics could not be read")
				}
				now := time.Now()
				return fmt.Sprintf("%s (%s, heartbeat %s)\ncpu: %.1f%%\nmemory: %s of %s\ndisk (/): %s of %s\nload (1 min): %.2f\nuptime: %s\n",
					s.Name, presence(s, now), ago(s.LastSeenAt, now), m.CPUPercent, bytesOf(m.MemUsedBytes), bytesOf(m.MemTotalBytes),
					bytesOf(m.DiskUsedBytes), bytesOf(m.DiskTotalBytes), m.Load1, (time.Duration(m.UptimeSeconds) * time.Second).String()), nil
			},
		},
		{
			Name:        "list_operations",
			Description: "Recent operations (system.info, docker.check, provisioning…) for one server or the whole team, with status and exit code. No log contents.",
			Scope:       scopes.Read,
			InputSchema: schema(nil, map[string]any{
				"server": str("Optional: server id or name"),
				"limit":  map[string]any{"type": "integer", "default": 20, "maximum": 100},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				limit := int32(args.Int("limit", 20, 1, 100))
				var b strings.Builder
				if args.Str("server") != "" {
					s, err := findServer(ctx, env, args)
					if err != nil {
						return "", err
					}
					rows, err := env.Q.ListOperationsOfServer(ctx, db.ListOperationsOfServerParams{ServerID: s.ID, TeamID: env.Caller.TeamID, Limit: limit})
					if err != nil {
						return "", errors.New("could not list operations")
					}
					for _, o := range rows {
						fmt.Fprintf(&b, "  %s  %-8s %-15s %-8s exit %-4s %s  (id %s)\n", o.CreatedAt.UTC().Format(time.RFC3339), s.Name, o.Kind, o.Status, exitOf(o.ExitCode), orDash(o.Error), o.ID)
					}
				} else {
					rows, err := env.Q.ListOperationsOfTeam(ctx, db.ListOperationsOfTeamParams{TeamID: env.Caller.TeamID, Limit: limit})
					if err != nil {
						return "", errors.New("could not list operations")
					}
					for _, o := range rows {
						fmt.Fprintf(&b, "  %s  %-8s %-15s %-8s exit %-4s %s  (id %s)\n", o.CreatedAt.UTC().Format(time.RFC3339), o.ServerName, o.Kind, o.Status, exitOf(o.ExitCode), orDash(o.Error), o.ID)
					}
				}
				if b.Len() == 0 {
					return "No operations yet.", nil
				}
				return b.String(), nil
			},
		},
	}
}

// findServer resolves the `server` argument (id or name) within the token's team only.
func findServer(ctx context.Context, env *Env, args Args) (db.Server, error) {
	ref := args.Str("server")
	if ref == "" {
		return db.Server{}, errors.New("server is required (an id or a name)")
	}
	s, err := env.Q.ServerInTeamByRef(ctx, db.ServerInTeamByRefParams{TeamID: env.Caller.TeamID, Ref: ref})
	if err != nil {
		return db.Server{}, errors.New("server not found")
	}
	return s, nil
}

func presence(s db.Server, now time.Time) string {
	switch {
	case s.Status != "enrolled":
		return s.Status
	case ops.Online(s.LastSeenAt, now):
		return "online"
	default:
		return "offline"
	}
}

func orDash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

func osOf(s db.Server) string {
	if s.Os == nil {
		return "—"
	}
	return strings.TrimSpace(*s.Os + " " + orDash(s.OsVersion))
}

func ago(t *time.Time, now time.Time) string {
	if t == nil {
		return "never"
	}
	return now.Sub(*t).Round(time.Second).String() + " ago"
}

func exitOf(c *int64) string {
	if c == nil {
		return "—"
	}
	return fmt.Sprint(*c)
}

func bytesOf(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func dockerOf(d agentproto.DockerFacts) string {
	switch {
	case !d.Installed:
		return "not installed"
	case !d.Running:
		return "installed, not running"
	default:
		return d.Version + " (" + d.StorageDriver + ")"
	}
}
