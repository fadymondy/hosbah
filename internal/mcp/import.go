package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ImportTools: what the last import sync of a server found and what became of it, choosing what a
// discovered workload becomes, and running import now (MH-574 A4). Same actions as REST
// (internal/api/import.go).
func ImportTools() []Tool {
	return []Tool{
		{
			Name: "server_import_status", Scope: scopes.Read,
			Description: "The last import sync of a server: when it ran, its counts, and every discovered workload with what became of it (imported as an app or a database, marked a shared service, updated, skipped, ignored, or errored).",
			InputSchema: schema([]string{"server"}, map[string]any{"server": str("Server name or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				decisionRows, err := env.Q.ImportDecisionsOfServer(ctx, db.ImportDecisionsOfServerParams{ServerID: srv.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("could not list the server's import decisions")
				}
				decisions := map[string]string{}
				for _, r := range decisionRows {
					decisions[r.OriginKey] = r.Decision
				}
				var b strings.Builder
				if run, err := env.Q.ImportRunOfServer(ctx, db.ImportRunOfServerParams{ServerID: srv.ID, TeamID: env.Caller.TeamID}); err == nil {
					fmt.Fprintf(&b, "Last run: %s, started %s, discovered %d, created %d, updated %d, missing %d, removed %d, ignored %d",
						run.Status, run.StartedAt.Format("2006-01-02 15:04:05"), run.Discovered, run.Created, run.Updated, run.Missing, run.Removed, run.Ignored)
					if run.Error != nil {
						fmt.Fprintf(&b, ", error: %s", *run.Error)
					}
					b.WriteString("\n")
				} else {
					b.WriteString("No import has run yet. server_import_run queues one.\n")
				}
				items, err := env.Q.ImportItemsOfServer(ctx, db.ImportItemsOfServerParams{ServerID: srv.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", errors.New("could not list the server's discovered workloads")
				}
				if len(items) == 0 {
					return b.String(), nil
				}
				for _, it := range items {
					decision := decisions[it.OriginKey]
					if decision == "" {
						decision = "—"
					}
					kind := ""
					if it.DetectedKind != nil {
						kind = " (" + *it.DetectedKind + ")"
					}
					line := fmt.Sprintf("  %-24s %-10s%-14s outcome=%-24s decision=%s", it.Name, it.OriginKind, kind, it.Outcome, decision)
					if it.Reason != nil {
						line += " reason=" + *it.Reason
					}
					if it.Error != nil {
						line += " error=" + *it.Error
					}
					b.WriteString(line + "\n")
				}
				return b.String(), nil
			},
		},
		{
			Name: "server_import_run", Scope: scopes.Write,
			Description: "Run import now: queue a system.info refresh, which syncs the server's discovered workloads on completion. Owner, admin or deployer.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": str("Server name or id")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityOperate); err != nil {
					return "", err
				}
				svc, err := opsOf(env)
				if err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				if srv.Status != "enrolled" {
					return "", errors.New("that server's agent has not enrolled yet")
				}
				_, created, err := svc.Enqueue(ctx, env.Q, ops.EnqueueSpec{TeamID: env.Caller.TeamID, ServerID: srv.ID,
					Kind: agentproto.KindSystemInfo, Params: json.RawMessage(`{}`), CreatedBy: actor(env)})
				if err != nil {
					return "", errors.New("could not queue the import")
				}
				if !created {
					return "An import for " + srv.Name + " is already queued or running.", nil
				}
				return "Import queued for " + srv.Name + ". server_import_status shows the result once it completes.", nil
			},
		},
		{
			Name: "server_import_decide", Scope: scopes.Write,
			Description: "Choose what a discovered workload becomes: import as app, import as database, mark as a shared service, or ignore. Sticky — the next sync always honours it. Owner, admin or deployer.",
			InputSchema: schema([]string{"server", "item", "decision"}, map[string]any{"server": str("Server name or id"),
				"item":     str("The workload's origin key (container name, systemd unit, or guest:<vmid> — as listed by server_import_status)"),
				"decision": map[string]any{"type": "string", "enum": []string{"app", "database", "shared_service", "ignore"}}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityOperate); err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				item := args.Str("item")
				decision := args.Str("decision")
				if item == "" {
					return "", errors.New("item is required")
				}
				switch decision {
				case "app", "database", "shared_service", "ignore":
				default:
					return "", errors.New("decision must be app, database, shared_service or ignore")
				}
				kind, name := "app", item
				if rows, err := env.Q.ImportItemsOfServer(ctx, db.ImportItemsOfServerParams{ServerID: srv.ID, TeamID: env.Caller.TeamID}); err == nil {
					for _, row := range rows {
						if row.OriginKey == item {
							kind, name = row.Target, row.Name
							break
						}
					}
				}
				if _, err := env.Q.SetImportDecision(ctx, db.SetImportDecisionParams{TeamID: env.Caller.TeamID, ServerID: srv.ID, Kind: kind,
					OriginKey: item, Name: name, CreatedBy: actor(env), Decision: decision}); err != nil {
					return "", errors.New("could not save the decision")
				}
				return fmt.Sprintf("%s: %s will be %s.", srv.Name, name, decision), nil
			},
		},
	}
}
