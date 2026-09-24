package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/provisioning"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
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
						fmt.Fprintf(&b, "platform: %s", f.Platform())
						if f.Proxmox.Version != "" {
							fmt.Fprintf(&b, " (Proxmox VE %s)", f.Proxmox.Version)
						}
						b.WriteString("\n")
						if len(f.Services) > 0 {
							b.WriteString("found on the server:\n")
							for _, sv := range f.Services {
								fmt.Fprintf(&b, "  %-9s %-28s %-10s %s %s\n", sv.Kind, sv.Name, sv.Status, sv.Image, strings.Join(sv.Ports, ", "))
							}
						}
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

		{
			Name:        "server_guests",
			Description: "A Proxmox node's discovered guests (from its own agent, /cluster/resources), enrolled or not: vmid, name, type, status, address, resources.",
			Scope:       scopes.Read,
			InputSchema: schema([]string{"server"}, serverArg),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				s, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				guests := serverGuests(s)
				if len(guests) == 0 {
					return s.Name + " has no discovered guests.", nil
				}
				rows, err := env.Q.ListServersOfTeam(ctx, env.Caller.TeamID)
				byVMID := map[string]string{}
				if err == nil {
					for _, r := range rows {
						if r.ParentServerID != nil && *r.ParentServerID == s.ID && r.GuestVmid.Valid {
							byVMID[fmt.Sprint(r.GuestVmid.Int32)] = r.Name
						}
					}
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%s guests:\n", s.Name)
				for _, g := range guests {
					enrolled := "not enrolled"
					if n, ok := byVMID[g.ID]; ok {
						enrolled = "enrolled as " + n
					}
					fmt.Fprintf(&b, "  CT/VM %-6s %-9s %-8s %-24s %-15s %s\n", g.ID, g.Type, g.Status, g.Name, orDash(strPtr(g.Address)), enrolled)
				}
				return b.String(), nil
			},
		},
		{
			Name: "server_set_parent", Scope: scopes.Write,
			Description: "Manually link (or unlink) a server as the guest of a Proxmox node in the same team (owner or admin). Pass parent to link, or an empty parent to unlink.",
			InputSchema: schema([]string{"server"}, map[string]any{
				"server":     str("Server id or name (the guest)"),
				"parent":     str("Node server id or name to be a guest of, or empty to unlink"),
				"guest_vmid": map[string]any{"type": "integer", "description": "Its Proxmox VMID on that node (optional)"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				var parentID *string
				var vmid sql.NullInt32
				var guestType *string
				if p := args.Str("parent"); p != "" {
					parent, err := env.Q.ServerInTeamByRef(ctx, db.ServerInTeamByRefParams{TeamID: env.Caller.TeamID, Ref: p})
					if err != nil {
						return "", errors.New("parent server not found")
					}
					if parent.ID == s.ID {
						return "", errors.New("a server cannot be its own parent")
					}
					parentID = &parent.ID
					guestType = s.GuestType
					if v, ok := args["guest_vmid"]; ok {
						vmid = sql.NullInt32{Int32: int32(args.Int("guest_vmid", 0, 0, 999999)), Valid: true}
						_ = v
					}
				}
				row, err := env.Q.SetServerParent(ctx, db.SetServerParentParams{ID: s.ID, TeamID: env.Caller.TeamID,
					ParentServerID: parentID, GuestVmid: vmid, GuestType: guestType, Manual: true})
				if err != nil {
					return "", errors.New("could not update the server's parent")
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "server.parent_set", "server", s.ID,
					auditChanges(env, map[string]any{"parent_server_id": strDeref(parentID)})); err != nil {
					return "", err
				}
				if parentID == nil {
					return fmt.Sprintf("%s unlinked from its parent.", row.Name), nil
				}
				return fmt.Sprintf("%s linked as a guest of %s.", row.Name, *parentID), nil
			},
		},

		{
			Name: "update_server", Scope: scopes.Write,
			Description: "Rename or restyle a server (owner or admin): name, color (#RRGGBB), icon (" + strings.Join(provisioning.ServerIcons, ", ") +
				") and logo_url (https, shown instead of the icon). Omit a field to leave it; an empty string clears color, icon or logo_url.",
			InputSchema: schema([]string{"server"}, map[string]any{
				"server":   str("Server id or name"),
				"name":     str("New name: letters, digits, dot, dash, underscore; unique in the team"),
				"color":    str("#RRGGBB, or empty to clear"),
				"icon":     map[string]any{"type": "string", "enum": append([]string{""}, provisioning.ServerIcons...), "description": "Icon key, or empty to clear"},
				"logo_url": str("https:// image URL (at most 500 characters), or empty to clear"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				opt := func(k string) *string {
					if _, ok := args[k]; !ok {
						return nil
					}
					v := args.Str(k)
					return &v
				}
				p := provisioning.ServerPatch{Name: opt("name"), Color: opt("color"), Icon: opt("icon"), LogoURL: opt("logo_url"),
					Audit: auditChanges(env, nil)}
				if p.Name == nil && p.Color == nil && p.Icon == nil && p.LogoURL == nil {
					return "", errors.New("nothing to change: pass name, color, icon or logo_url")
				}
				row, err := provisioning.UpdateServer(ctx, env.Q, env.Caller.TeamID, actor(env), s, p)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Updated %s (id %s): color %s · icon %s · logo %s", row.Name, row.ID, orDash(row.Color), orDash(row.Icon), orDash(row.LogoUrl)), nil
			},
		},

		// ---- destructive ----
		{
			Name: "delete_server", Scope: scopes.Destructive, Destructive: true, ResourceType: "server",
			Description: "Remove a server from the team. A Proxmox machine Hosbah created is destroyed first; a bring-your-own server is only forgotten (nothing on it is touched except that its agent stops being trusted). Apps must be deleted first. confirm must equal the server's name.",
			InputSchema: schema([]string{"server", "confirm"}, map[string]any{
				"server":  str("Server id or name"),
				"confirm": str("The server's name, exactly"),
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				if err := RequireConfirm(args, s.Name); err != nil {
					return "", err
				}
				// The same guard the dashboard has. Deleting a server out from under running apps
				// would leave their rows pointing at nothing, so it is refused, not cascaded —
				// a machine caller is exactly who would do this by accident.
				if n, err := env.Q.CountAppsOnServer(ctx, db.CountAppsOnServerParams{ServerID: s.ID, TeamID: env.Caller.TeamID}); err == nil && n > 0 {
					return "", fmt.Errorf("%d app(s) still run on %s — delete them first; nothing was changed", n, s.Name)
				}
				// Imported workloads are only records: forgetting the server forgets them.
				if err := env.Q.DeleteImportedAppsOnServer(ctx, db.DeleteImportedAppsOnServerParams{ServerID: s.ID, TeamID: env.Caller.TeamID}); err != nil {
					return "", errors.New("could not forget the server's imported apps")
				}
				if err := env.Q.DeleteImportedDataServicesOnServer(ctx, db.DeleteImportedDataServicesOnServerParams{ServerID: s.ID, TeamID: env.Caller.TeamID}); err != nil {
					return "", errors.New("could not forget the server's imported databases")
				}
				if s.Provider == "proxmox" && s.ProviderRef != nil && s.CredentialID != nil {
					if env.App == nil || env.App.Keys == nil {
						return "", errors.New("the provider driver is not available, so the machine was not deleted")
					}
					if err := provisioning.DeleteMachine(ctx, env.App.Keys, env.Q, s); err != nil {
						return "", fmt.Errorf("the machine could not be deleted on Proxmox, so the server was kept: %w", err)
					}
				}
				if n, err := env.Q.DeleteServerInTeam(ctx, db.DeleteServerInTeamParams{ID: s.ID, TeamID: env.Caller.TeamID}); err != nil || n == 0 {
					return "", errors.New("server not found")
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "server.deleted", "server", s.ID,
					auditChanges(env, map[string]any{"name": s.Name, "provider": s.Provider})); err != nil {
					return "", err
				}
				// Dropping the websocket is deliberately deferred: it must not happen if the
				// transaction below rolls back, or a still-registered agent would be disconnected
				// for a deletion that never committed.
				id := s.ID
				env.Then = func(ctx context.Context) (string, error) {
					if env.App != nil {
						if o, ok := env.App.Ops.(interface{ Disconnect(string) }); ok {
							o.Disconnect(id)
						}
					}
					return "", nil
				}
				verb := "forgotten"
				if s.Provider == "proxmox" {
					verb = "destroyed on Proxmox and removed"
				}
				return fmt.Sprintf("Server %s %s.", s.Name, verb), nil
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

// serverGuests reads the Proxmox guests out of a node's facts.
func serverGuests(s db.Server) []agentproto.Service {
	if s.Facts == nil {
		return nil
	}
	var f agentproto.Facts
	if json.Unmarshal([]byte(*s.Facts), &f) != nil {
		return nil
	}
	var out []agentproto.Service
	for _, sv := range f.Services {
		if sv.Kind == "guest" && sv.ID != "" {
			out = append(out, sv)
		}
	}
	return out
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
