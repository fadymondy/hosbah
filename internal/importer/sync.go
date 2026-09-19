package importer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Actor is who the importer's rows and audit entries name.
const Actor = "system:import"

// Result counts what one sync did.
type Result struct {
	Created, Updated, Missing, Ignored, Removed int
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
func Sync(ctx context.Context, sqlDB *sql.DB, teamID, serverID string, services []agentproto.Service) (Result, error) {
	var res Result
	items := Classify(services)
	err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		res = Result{}
		srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: serverID, TeamID: teamID})
		if err != nil {
			return nil // the server is gone (or not this team's): nothing to import into
		}
		ignoredKeys, err := q.ImportIgnoresOfServer(ctx, db.ImportIgnoresOfServerParams{ServerID: serverID, TeamID: teamID})
		if err != nil {
			return err
		}
		ignored := map[string]bool{}
		for _, k := range ignoredKeys {
			ignored[k] = true
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
			if ignored[it.OriginKey] {
				res.Ignored++
				continue
			}
			seen[it.OriginKey] = true
			origin, _ := json.Marshal(it.Origin)
			status := statusOf(it.State)
			host, port := endpoint(it, serverIP)
			switch a, isApp := appByKey[it.OriginKey]; {
			case isApp:
				if err := q.RefreshImportedApp(ctx, db.RefreshImportedAppParams{OriginKind: &it.OriginKind, Origin: string(origin),
					ImportedState: &it.State, Status: status, ID: a.ID, TeamID: teamID}); err != nil {
					return err
				}
				res.Updated++
				continue
			}
			if d, ok := dsByKey[it.OriginKey]; ok {
				version := it.Version
				if version == "" {
					version = d.Version
				}
				if err := q.RefreshImportedDataService(ctx, db.RefreshImportedDataServiceParams{Version: version, Host: host, Port: port,
					Origin: string(origin), ImportedState: &it.State, Status: status, ID: d.ID, TeamID: teamID}); err != nil {
					return err
				}
				res.Updated++
				continue
			}
			if it.Target == TargetDataService {
				name := UniqueName(it.Name, it.Kind, dsNames)
				var container *string
				if it.OriginKind == "container" {
					container = &it.OriginKey
				}
				if _, err := q.InsertImportedDataService(ctx, db.InsertImportedDataServiceParams{ID: newUUID(), TeamID: teamID, ServerID: serverID,
					Name: name, Kind: it.Kind, Version: it.Version, Status: status, Container: container, Host: host, Port: port,
					OriginKind: &it.OriginKind, OriginKey: &it.OriginKey, Origin: string(origin), ImportedState: &it.State, CreatedBy: Actor}); err != nil {
					return err
				}
			} else {
				name := UniqueName(it.Name, "site", appNames)
				appPort := int64(8080)
				if _, p := FirstPublished(it.Origin.Ports); p > 0 {
					appPort = int64(p)
				}
				if _, err := q.InsertImportedApp(ctx, db.InsertImportedAppParams{ID: newUUID(), TeamID: teamID, ServerID: serverID, Name: name,
					Port: appPort, Status: status, OriginKind: &it.OriginKind, OriginKey: &it.OriginKey, Origin: string(origin),
					ImportedState: &it.State, CreatedBy: Actor}); err != nil {
					return err
				}
			}
			res.Created++
		}
		for k, a := range appByKey {
			if !seen[k] && !ignored[k] {
				if err := q.MarkImportedAppMissing(ctx, db.MarkImportedAppMissingParams{ID: a.ID, TeamID: teamID}); err != nil {
					return err
				}
				if a.ImportedState == nil || *a.ImportedState != StateMissing {
					res.Missing++
				}
			}
		}
		for k, d := range dsByKey {
			if !seen[k] && !ignored[k] {
				if err := q.MarkImportedDataServiceMissing(ctx, db.MarkImportedDataServiceMissingParams{ID: d.ID, TeamID: teamID}); err != nil {
					return err
				}
				if d.ImportedState == nil || *d.ImportedState != StateMissing {
					res.Missing++
				}
			}
		}
		if res.Created > 0 || res.Missing > 0 || res.Removed > 0 {
			return tenancy.Record(ctx, q, teamID, Actor, "server.workloads_imported", "server", serverID, map[string]any{
				"created": res.Created, "missing": res.Missing, "updated": res.Updated, "removed": res.Removed})
		}
		return nil
	})
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
