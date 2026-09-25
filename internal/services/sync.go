package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ScanFactsTx is ScanFromFacts wrapped in its own team-scoped transaction, for callers (the ws
// heartbeat) that only have a *sql.DB. Errors are the caller's to log; never fatal to the caller.
func ScanFactsTx(ctx context.Context, sqlDB *sql.DB, teamID, nodeServerID string, facts []byte) (int, error) {
	if len(facts) == 0 {
		return 0, nil
	}
	n := 0
	err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		rows, err := ScanFromFacts(ctx, q, teamID, nodeServerID, facts)
		n = len(rows)
		return err
	})
	return n, err
}

/*
Scan classifies a Proxmox node's discovered guests as shared services and upserts them
(shared_services, keyed on server_id+guest_vmid). A guest already recorded keeps its name/kind when
a member set them by hand (name_manual/kind_manual — see UpsertSharedService's ON CONFLICT); a guest
that no longer classifies as a known kind, or is no longer discovered, is left alone — Scan only
ever adds or refreshes, it never deletes (a member hides one with Hide instead). Hosbah's own guests
are skipped. Team scope; the caller has already checked the node server. Facts carry a guest's name
and address only (no tags or hostname today), so Classify runs on the name alone here.
*/
func Scan(ctx context.Context, q *db.Queries, teamID, nodeServerID string, guests []agentproto.Service) ([]db.SharedService, error) {
	var out []db.SharedService
	for _, g := range guests {
		if g.Kind != "guest" || g.ID == "" {
			continue
		}
		if importer.IsHosbahOwned(g.Name) {
			continue
		}
		kind := Classify(g.Name, "", "")
		if kind == "" {
			continue
		}
		var host *string
		if g.Address != "" {
			host = &g.Address
		}
		var port *int64
		if p, ok := DefaultPort[kind]; ok {
			p64 := int64(p)
			port = &p64
		}
		guestType := g.Type
		if guestType != "lxc" && guestType != "qemu" {
			guestType = "lxc"
		}
		row, err := q.UpsertSharedService(ctx, db.UpsertSharedServiceParams{
			ID: sharedServiceID(nodeServerID, g.ID), TeamID: teamID, ServerID: nodeServerID,
			GuestVmid: g.ID, GuestType: guestType, Kind: kind, Name: g.Name, Host: host, Port: port,
		})
		if err != nil {
			return out, err
		}
		out = append(out, row)
	}
	return out, nil
}

// sharedServiceID is deterministic per (server, guest) so re-scans of the same guest never race to
// insert two rows before the unique index would catch it, and the id is stable across scans.
func sharedServiceID(serverID, vmid string) string {
	return "svc-" + shortHash(serverID+":"+vmid)
}

// ScanFromFacts pulls the guests out of a node's raw facts JSON (as stored on servers.facts) and
// scans them, for callers that only have the JSON (e.g. the ws heartbeat).
func ScanFromFacts(ctx context.Context, q *db.Queries, teamID, nodeServerID string, facts []byte) ([]db.SharedService, error) {
	if len(facts) == 0 {
		return nil, nil
	}
	var f struct {
		Services []agentproto.Service `json:"services"`
	}
	if err := json.Unmarshal(facts, &f); err != nil {
		return nil, nil
	}
	return Scan(ctx, q, teamID, nodeServerID, f.Services)
}

// shortHash is a short, filesystem/UUID-friendly deterministic id, not a security boundary (the
// row is reachable through team scope only regardless of its id shape).
func shortHash(s string) string {
	// fnv-1a: fine, this is a stable local id and not exported outside this package
	var x uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		x ^= uint64(s[i])
		x *= 1099511628211
	}
	return strconv.FormatUint(x, 36)
}
