package importer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Actor is who the importer's rows and audit entries name.
const Actor = "system:import"

// Decisions (import_ignores.decision; MH-574 A4): a member's sticky choice for one discovered
// workload. A later sync always honours it instead of reclassifying the item.
const (
	DecisionIgnore        = "ignore"
	DecisionApp           = "app"
	DecisionDatabase      = "database"
	DecisionSharedService = "shared_service"
)

// Item outcomes (import_items.outcome): what a sync did with one discovered workload.
const (
	OutcomeImportedApp           = "imported_app"
	OutcomeImportedDataService   = "imported_data_service"
	OutcomeImportedSharedService = "imported_shared_service"
	OutcomeLinkedGuest           = "linked_guest"
	OutcomeUpdated               = "updated"
	OutcomeMissing               = "missing"
	OutcomeRemoved               = "removed"
	OutcomeSkipped               = "skipped"
	OutcomeIgnored               = "ignored"
	OutcomeError                 = "error"
)

// applyDecision applies a member's decision (import_ignores.decision) to a classified item: ignore
// reports true (the caller keeps the item out); app and database force the item's target (app also
// clears any detected data-service kind — it is no longer meaningful); no decision (or an unknown
// one) leaves the item as Classify decided. DecisionSharedService is handled by the caller (Sync)
// instead, since it needs database access. Pure: no I/O, for unit tests.
func applyDecision(it Item, decision string) (Item, bool) {
	switch decision {
	case DecisionIgnore:
		return it, true
	case DecisionApp:
		it.Target, it.Kind = TargetApp, ""
	case DecisionDatabase:
		it.Target = TargetDataService
	}
	return it, false
}

// Result counts what one sync did.
type Result struct {
	Created, Updated, Missing, Ignored, Removed, SharedServices, Errors int
}

// itemOutcome is one row of import_items, built while syncing and inserted after the sync's own
// transaction commits (so a per-item error never rolls back the whole sync — see Sync).
type itemOutcome struct {
	OriginKind, OriginKey, Name string
	DetectedKind                *string
	Target                      string
	Outcome                     string
	Reason, Error, RecordID     *string
}

// SyncFacts parses a facts document (agentproto.Facts JSON) and syncs its services. A document
// without a services list is ignored (an older agent, or a report that did not look).
func SyncFacts(ctx context.Context, sqlDB *sql.DB, teamID, serverID string, facts []byte) (Result, error) {
	var f struct {
		Services *[]agentproto.Service `json:"services"`
	}
	if err := json.Unmarshal(facts, &f); err != nil || f.Services == nil {
		return Result{}, nil
	}
	return Sync(ctx, sqlDB, teamID, serverID, *f.Services)
}

// discoveredKeys are the origin keys of everything the server reported, importable or not.
func discoveredKeys(services []agentproto.Service) []string {
	var keys []string
	for _, s := range services {
		name := strings.TrimSpace(strings.TrimPrefix(s.Name, "/"))
		switch s.Kind {
		case "guest":
			if s.ID != "" {
				keys = append(keys, "guest:"+s.ID)
			}
		case "container":
			if name != "" {
				keys = append(keys, name)
			}
		case "systemd":
			if u := strings.TrimSuffix(name, ".service"); u != "" {
				keys = append(keys, u)
			}
		}
	}
	return keys
}

// Sync upserts the imported apps and data services of one server from its discovered services, in
// the server's team scope (RLS: the team's own rows only). Present → refreshed; no longer
// discovered → imported_state missing (never deleted); tombstoned (deleted by the user) → skipped.
//
// A member's decision (import_ignores.decision, set through the import REST/MCP endpoints) is
// consulted per item and always wins over classification: ignore keeps the item out (as before);
// app / database forces the target; shared_service upserts the guest into the team's shared
// services directory instead of an app or data service (only a Proxmox guest can become one — any
// other kind is recorded skipped).
//
// The outcome of every discovered item, and the run as a whole, is recorded (import_runs,
// import_items — MH-574 A4) so "import not working" has an answer: what was found, what it became,
// why something was skipped, and any error. A per-item failure is recorded on that item and does
// not stop the rest of the sync; a failure before syncing could even start (e.g. the server's own
// row could not be read) is recorded as the run's error.
func Sync(ctx context.Context, sqlDB *sql.DB, teamID, serverID string, services []agentproto.Service) (Result, error) {
	var res Result
	started := time.Now()
	items := Classify(services)
	var itemsOut []itemOutcome

	err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		res = Result{}
		itemsOut = nil
		srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: serverID, TeamID: teamID})
		if err != nil {
			return nil // the server is gone (or not this team's): nothing to import into
		}
		decisionRows, err := q.ImportDecisionsOfServer(ctx, db.ImportDecisionsOfServerParams{ServerID: serverID, TeamID: teamID})
		if err != nil {
			return err
		}
		decisions := map[string]string{}
		for _, d := range decisionRows {
			decisions[d.OriginKey] = d.Decision
		}
		apps, err := q.ImportedAppsOfServer(ctx, db.ImportedAppsOfServerParams{ServerID: serverID, TeamID: teamID})
		if err != nil {
			return err
		}
		dss, err := q.ImportedDataServicesOfServer(ctx, db.ImportedDataServicesOfServerParams{ServerID: serverID, TeamID: teamID})
		if err != nil {
			return err
		}
		appByKey, dsByKey := map[string]db.App{}, map[string]db.DataService{}
		for _, a := range apps {
			if a.OriginKey != nil {
				appByKey[*a.OriginKey] = a
			}
		}
		for _, d := range dss {
			if d.OriginKey != nil {
				dsByKey[*d.OriginKey] = d
			}
		}
		appNames, dsNames, err := usedNames(ctx, q, teamID)
		if err != nil {
			return err
		}
		serverIP := ""
		if srv.IpAddress != nil {
			serverIP = *srv.IpAddress
		}
		// A guest that is itself one of the team's servers (its address is a server's) is a server
		// Hosbah already manages, not a site on this one.
		servers, err := q.ListServersOfTeam(ctx, teamID)
		if err != nil {
			return err
		}
		serverAddrs := map[string]bool{}
		for _, other := range servers {
			if other.ID != serverID && other.IpAddress != nil && *other.IpAddress != "" {
				serverAddrs[*other.IpAddress] = true
			}
		}
		importable := map[string]bool{}
		kept := items[:0:0]
		for _, it := range items {
			if it.OriginKind == "guest" && it.Origin.Address != "" && serverAddrs[it.Origin.Address] {
				continue
			}
			importable[it.OriginKey] = true
			kept = append(kept, it)
		}
		items = kept
		// Records for workloads that are still reported but no longer importable (Hosbah's own, or
		// a guest that is a Hosbah server) are removed — the record only, never the workload.
		for _, k := range discoveredKeys(services) {
			if importable[k] {
				continue
			}
			if a, ok := appByKey[k]; ok {
				if _, err := q.DeleteAppInTeam(ctx, db.DeleteAppInTeamParams{ID: a.ID, TeamID: teamID}); err != nil {
					return err
				}
				delete(appByKey, k)
				res.Removed++
			}
			if d, ok := dsByKey[k]; ok {
				if _, err := q.DeleteDataServiceInTeam(ctx, db.DeleteDataServiceInTeamParams{ID: d.ID, TeamID: teamID}); err != nil {
					return err
				}
				delete(dsByKey, k)
				res.Removed++
			}
		}

		seen := map[string]bool{}
		for _, it := range items {
			decision := decisions[it.OriginKey]
			var detectedKind *string
			if it.Kind != "" {
				k := it.Kind
				detectedKind = &k
			}
			if decision == DecisionIgnore {
				res.Ignored++
				reason := "ignored by a team member"
				itemsOut = append(itemsOut, itemOutcome{OriginKind: it.OriginKind, OriginKey: it.OriginKey, Name: it.Name,
					DetectedKind: detectedKind, Target: it.Target, Outcome: OutcomeIgnored, Reason: &reason})
				continue
			}
			if decision == DecisionSharedService {
				seen[it.OriginKey] = true
				out := itemOutcome{OriginKind: it.OriginKind, OriginKey: it.OriginKey, Name: it.Name, DetectedKind: detectedKind, Target: it.Target}
				if it.OriginKind != "guest" || it.Origin.VMID == "" {
					reason := "only a Proxmox guest can become a shared service"
					out.Outcome, out.Reason = OutcomeSkipped, &reason
				} else {
					kind := it.Kind
					if kind == "" {
						kind = "other"
					}
					guestType := it.Origin.GuestType
					if guestType != "lxc" && guestType != "qemu" {
						guestType = "lxc"
					}
					var host *string
					if it.Origin.Address != "" {
						host = &it.Origin.Address
					}
					svc, err := q.UpsertSharedService(ctx, db.UpsertSharedServiceParams{
						ID: sharedServiceID(serverID, it.Origin.VMID), TeamID: teamID, ServerID: serverID,
						GuestVmid: it.Origin.VMID, GuestType: guestType, Kind: kind, Name: it.Name, Host: host, Port: nil,
					})
					if err != nil {
						msg := err.Error()
						out.Outcome, out.Error = OutcomeError, &msg
						res.Errors++
					} else {
						out.Outcome, out.RecordID = OutcomeImportedSharedService, &svc.ID
						res.SharedServices++
					}
				}
				itemsOut = append(itemsOut, out)
				continue
			}
			var ignore bool
			it, ignore = applyDecision(it, decision)
			if ignore {
				// unreachable (DecisionIgnore is handled above), kept so applyDecision stays the
				// single source of truth for what a decision does to an item
				continue
			}
			if decision == DecisionApp {
				detectedKind = nil
			}
			seen[it.OriginKey] = true
			origin, _ := json.Marshal(it.Origin)
			status := statusOf(it.State)
			host, port := endpoint(it, serverIP)
			out := itemOutcome{OriginKind: it.OriginKind, OriginKey: it.OriginKey, Name: it.Name, DetectedKind: detectedKind, Target: it.Target}
			switch a, isApp := appByKey[it.OriginKey]; {
			case isApp:
				if err := q.RefreshImportedApp(ctx, db.RefreshImportedAppParams{OriginKind: &it.OriginKind, Origin: string(origin),
					ImportedState: &it.State, Status: status, ID: a.ID, TeamID: teamID}); err != nil {
					msg := err.Error()
					out.Outcome, out.Error = OutcomeError, &msg
					res.Errors++
				} else {
					out.Outcome, out.RecordID = OutcomeUpdated, &a.ID
					res.Updated++
				}
				itemsOut = append(itemsOut, out)
				continue
			}
			if d, ok := dsByKey[it.OriginKey]; ok {
				version := it.Version
				if version == "" {
					version = d.Version
				}
				if err := q.RefreshImportedDataService(ctx, db.RefreshImportedDataServiceParams{Version: version, Host: host, Port: port,
					Origin: string(origin), ImportedState: &it.State, Status: status, ID: d.ID, TeamID: teamID}); err != nil {
					msg := err.Error()
					out.Outcome, out.Error = OutcomeError, &msg
					res.Errors++
				} else {
					out.Outcome, out.RecordID = OutcomeUpdated, &d.ID
					res.Updated++
				}
				itemsOut = append(itemsOut, out)
				continue
			}
			if it.Target == TargetDataService {
				if it.Kind == "" {
					reason := "could not tell what database engine this is"
					out.Outcome, out.Reason = OutcomeError, &reason
					res.Errors++
					itemsOut = append(itemsOut, out)
					continue
				}
				name := UniqueName(it.Name, it.Kind, dsNames)
				var container *string
				if it.OriginKind == "container" {
					container = &it.OriginKey
				}
				id := newUUID()
				if _, err := q.InsertImportedDataService(ctx, db.InsertImportedDataServiceParams{ID: id, TeamID: teamID, ServerID: serverID,
					Name: name, Kind: it.Kind, Version: it.Version, Status: status, Container: container, Host: host, Port: port,
					OriginKind: &it.OriginKind, OriginKey: &it.OriginKey, Origin: string(origin), ImportedState: &it.State, CreatedBy: Actor}); err != nil {
					msg := err.Error()
					out.Outcome, out.Error = OutcomeError, &msg
					res.Errors++
				} else {
					out.Outcome, out.RecordID = OutcomeImportedDataService, &id
					res.Created++
				}
			} else {
				name := UniqueName(it.Name, "site", appNames)
				appPort := int64(8080)
				if _, p := FirstPublished(it.Origin.Ports); p > 0 {
					appPort = int64(p)
				}
				id := newUUID()
				if _, err := q.InsertImportedApp(ctx, db.InsertImportedAppParams{ID: id, TeamID: teamID, ServerID: serverID, Name: name,
					Port: appPort, Status: status, OriginKind: &it.OriginKind, OriginKey: &it.OriginKey, Origin: string(origin),
					ImportedState: &it.State, CreatedBy: Actor}); err != nil {
					msg := err.Error()
					out.Outcome, out.Error = OutcomeError, &msg
					res.Errors++
				} else {
					out.Outcome, out.RecordID = OutcomeImportedApp, &id
					res.Created++
				}
			}
			itemsOut = append(itemsOut, out)
		}
		for k, a := range appByKey {
			if !seen[k] && decisions[k] != DecisionIgnore {
				if err := q.MarkImportedAppMissing(ctx, db.MarkImportedAppMissingParams{ID: a.ID, TeamID: teamID}); err != nil {
					return err
				}
				if a.ImportedState == nil || *a.ImportedState != StateMissing {
					res.Missing++
				}
			}
		}
		for k, d := range dsByKey {
			if !seen[k] && decisions[k] != DecisionIgnore {
				if err := q.MarkImportedDataServiceMissing(ctx, db.MarkImportedDataServiceMissingParams{ID: d.ID, TeamID: teamID}); err != nil {
					return err
				}
				if d.ImportedState == nil || *d.ImportedState != StateMissing {
					res.Missing++
				}
			}
		}
		// import_runs / import_items: the run's outcome, for the Import tab (MH-574 A4).
		finished := time.Now()
		run, err := q.UpsertImportRun(ctx, db.UpsertImportRunParams{
			TeamID: teamID, ServerID: serverID, Status: "ok", Error: nil,
			Discovered: int32(len(itemsOut)), Created: int32(res.Created), Updated: int32(res.Updated),
			Missing: int32(res.Missing), Removed: int32(res.Removed), Ignored: int32(res.Ignored),
			StartedAt: started, FinishedAt: &finished,
		})
		if err != nil {
			return err
		}
		if err := q.DeleteImportItemsOfServer(ctx, db.DeleteImportItemsOfServerParams{ServerID: serverID, TeamID: teamID}); err != nil {
			return err
		}
		for _, it := range itemsOut {
			if _, err := q.InsertImportItem(ctx, db.InsertImportItemParams{
				TeamID: teamID, ServerID: serverID, RunID: run.ID, OriginKind: it.OriginKind, OriginKey: it.OriginKey, Name: it.Name,
				DetectedKind: it.DetectedKind, Target: it.Target, Outcome: it.Outcome, Reason: it.Reason, Error: it.Error, RecordID: it.RecordID,
			}); err != nil {
				return err
			}
		}
		if res.Created > 0 || res.Missing > 0 || res.Removed > 0 || res.SharedServices > 0 {
			return tenancy.Record(ctx, q, teamID, Actor, "server.workloads_imported", "server", serverID, map[string]any{
				"created": res.Created, "missing": res.Missing, "updated": res.Updated, "removed": res.Removed, "shared_services": res.SharedServices})
		}
		return nil
	})
	if err != nil {
		// The sync itself failed before (or while) writing its run: record the failure so "import
		// not working" has an answer instead of nothing at all. Best effort — a second failure here
		// is only ever logged by the caller.
		finished := time.Now()
		msg := err.Error()
		_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
			_, e := q.UpsertImportRun(ctx, db.UpsertImportRunParams{
				TeamID: teamID, ServerID: serverID, Status: "error", Error: &msg,
				Discovered: 0, Created: 0, Updated: 0, Missing: 0, Removed: 0, Ignored: 0,
				StartedAt: started, FinishedAt: &finished,
			})
			return e
		})
	}
	return res, err
}

// Backfill syncs every server whose facts already list discovered services (run once at API
// boot, so servers connected before imports existed convert without waiting for a report).
func Backfill(ctx context.Context, sqlDB *sql.DB, log *slog.Logger) (int, error) {
	var rows []db.ServersWithDiscoveredServicesRow
	err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		rows, err = q.ServersWithDiscoveredServices(ctx)
		return err
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		if r.Facts == nil {
			continue
		}
		res, err := SyncFacts(ctx, sqlDB, r.TeamID, r.ID, []byte(*r.Facts))
		if err != nil {
			if log != nil {
				log.Warn("import backfill failed", "server", r.ID, "err", err)
			}
			continue
		}
		n++
		if log != nil && (res.Created > 0 || res.Missing > 0 || res.Removed > 0) {
			log.Info("imported workloads", "server", r.ID, "created", res.Created, "updated", res.Updated, "missing", res.Missing, "removed", res.Removed)
		}
	}
	return n, nil
}

func usedNames(ctx context.Context, q *db.Queries, teamID string) (apps, dss map[string]bool, err error) {
	apps, dss = map[string]bool{}, map[string]bool{}
	ar, err := q.ListAppsOfTeam(ctx, teamID)
	if err != nil {
		return nil, nil, err
	}
	for _, a := range ar {
		apps[a.Name] = true
	}
	dr, err := q.ListDataServicesOfTeam(ctx, teamID)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range dr {
		dss[d.Name] = true
	}
	return apps, dss, nil
}

// endpoint is a data service's host and port: a guest's address, or a container's published port
// (bound to a specific address, else the server's).
func endpoint(it Item, serverIP string) (*string, *int64) {
	if it.Target != TargetDataService && it.Kind == "" {
		return nil, nil
	}
	var host *string
	var port *int64
	h := it.Host
	if it.OriginKind == "container" && h == "" && it.Port > 0 {
		h = serverIP
	}
	if h != "" {
		host = &h
	}
	if it.Port > 0 {
		p := int64(it.Port)
		port = &p
	}
	return host, port
}

func statusOf(state string) string {
	if state == StateRunning {
		return "running"
	}
	return "stopped"
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// sharedServiceID is deterministic per (server, guest), matching internal/services.sharedServiceID,
// so a later shared_services_scan of the same guest upserts the same row rather than a duplicate.
func sharedServiceID(serverID, vmid string) string {
	return "svc-" + shortHash(serverID+":"+vmid)
}

func shortHash(s string) string {
	var x uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		x ^= uint64(s[i])
		x *= 1099511628211
	}
	return strconv.FormatUint(x, 36)
}
