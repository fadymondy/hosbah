package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/scopes"
)

// SiteTools: a server's sites — the apps Hosbah deploys on it, and what it found already running
// there (a Proxmox node's guests, a Docker host's containers), each with its details.
func SiteTools() []Tool {
	return []Tool{{
		Name: "list_sites", Scope: scopes.Read,
		Description: "A server's sites: Hosbah apps deployed on it, plus imported workloads it already runs — Proxmox guests " +
			"(vmid, type, address, vCPU, memory, disk, status) and Docker containers. Run run_server_step kind=system.info to refresh.",
		InputSchema: schema([]string{"server"}, map[string]any{"server": str("Server name or id")}),
		Run: func(ctx context.Context, env *Env, args Args) (string, error) {
			srv, err := findServer(ctx, env, args)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			apps, err := env.Q.ListAppsOfTeam(ctx, env.Caller.TeamID)
			if err != nil {
				return "", errors.New("could not list apps")
			}
			for _, a := range apps {
				if a.ServerID == srv.ID {
					fmt.Fprintf(&b, "  app        %-24s %-10s %s\n", a.Name, a.Status, a.RepoURL)
				}
			}
			if srv.Facts != nil {
				var f agentproto.Facts
				if json.Unmarshal([]byte(*srv.Facts), &f) == nil {
					for _, s := range f.Services {
						switch s.Kind {
						case "guest":
							fmt.Fprintf(&b, "  %-10s %-24s %-10s %-15s %d vCPU · %s RAM · %s disk\n", s.Type+" "+s.ID, s.Name, s.Status,
								orDash(&s.Address), s.CPUs, bytesOf(s.MemBytes), bytesOf(s.DiskBytes))
						case "container":
							fmt.Fprintf(&b, "  container  %-24s %-10s %s %s\n", s.Name, s.Status, s.Image, strings.Join(s.Ports, ", "))
						}
					}
				}
			}
			if b.Len() == 0 {
				return srv.Name + " has no sites yet.", nil
			}
			return fmt.Sprintf("%s:\n%s", srv.Name, b.String()), nil
		},
	}}
}
