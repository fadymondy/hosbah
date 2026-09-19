package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/scopes"
)

// SiteTools: a server's sites — the apps Hosbah deploys on it, and the workloads it found already
// running there (imported apps and data services: Proxmox guests, Docker containers), each with its
// details. Imported ones are Hosbah records that Hosbah shows but never deploys or changes.
func SiteTools() []Tool {
	return []Tool{{
		Name: "list_sites", Scope: scopes.Read,
		Description: "A server's sites: Hosbah apps deployed on it, plus imported workloads it already runs (marked imported) — Proxmox guests " +
			"(vmid, type, address, vCPU, memory, disk, status), Docker containers and their databases. Hosbah never deploys or changes " +
			"an imported workload. Run run_server_step kind=system.info to refresh.",
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
				if a.ServerID != srv.ID {
					continue
				}
				if a.Source != importer.SourceImported {
					fmt.Fprintf(&b, "  app        %-24s %-10s %s\n", a.Name, a.Status, orDash(a.RepoURL))
					continue
				}
				b.WriteString("  " + importedLine(a.Name, a.OriginKind, a.Origin, a.ImportedState, "") + "\n")
			}
			dss, err := env.Q.ListDataServicesOfServer(ctx, db.ListDataServicesOfServerParams{ServerID: srv.ID, TeamID: env.Caller.TeamID})
			if err != nil {
				return "", errors.New("could not list data services")
			}
			for _, d := range dss {
				if d.Source != importer.SourceImported {
					fmt.Fprintf(&b, "  database   %-24s %-10s %s %s\n", d.Name, d.Status, d.Kind, d.Version)
					continue
				}
				b.WriteString("  " + importedLine(d.Name, d.OriginKind, d.Origin, d.ImportedState, d.Kind+" "+d.Version) + "\n")
			}
			if b.Len() == 0 {
				return srv.Name + " has no sites yet.", nil
			}
			return fmt.Sprintf("%s:\n%s", srv.Name, b.String()), nil
		},
	}}
}

// importedLine describes one imported workload: what it is on the host, its state and details.
func importedLine(name string, kind, rawOrigin, state *string, dbKind string) string {
	var o importer.Origin
	if rawOrigin != nil {
		_ = json.Unmarshal([]byte(*rawOrigin), &o)
	}
	what := "imported " + orDash(kind)
	if dbKind != "" {
		what = "imported database (" + strings.TrimSpace(dbKind) + ")"
	}
	st := orDash(state)
	switch deref(kind) {
	case "guest":
		return fmt.Sprintf("%-10s %-24s %-10s %-15s %d vCPU · %s RAM · %s disk  [%s]", o.GuestType+" "+o.VMID, name, st,
			orDash(&o.Address), o.CPUs, bytesOf(o.MemBytes), bytesOf(o.DiskBytes), what)
	case "container":
		return fmt.Sprintf("container  %-24s %-10s %s %s  [%s]", name, st, o.Image, strings.Join(o.Ports, ", "), what)
	}
	return fmt.Sprintf("%-10s %-24s %-10s %s  [%s]", deref(kind), name, st, strings.Join(o.Ports, ", "), what)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func lastSeen(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.UTC().Format(time.RFC3339)
}
