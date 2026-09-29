package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/provisioning"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Connect guest: an imported Proxmox guest app (origin_kind "guest") lives on a server that is
// itself the hypervisor's node — the app's own "deploy" is meaningless there (the node is not the
// guest, and building on the hypervisor is both wrong and unsafe). Instead the team can have Hosbah
// put its own agent INSIDE the guest: a new byo server row is created (nested under the node, like
// an auto-linked guest already would be), and the node's agent is asked, over the already-typed and
// signed agent protocol, to run the new server's one-time install command inside the guest with
// `pct exec`. Once that agent enrols, the importer picks its own containers up on its own — nothing
// else here waits for that.

// ConnectGuest is idempotent: an app already linked (ConnectedServerID set) returns that server
// again, with a nil install (there is nothing new to run).
func (s *Service) ConnectGuest(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App) (db.Server, *ops.Install, error) {
	if !IsImported(a) || a.OriginKind == nil || *a.OriginKind != "guest" {
		return db.Server{}, nil, coded(422, CodeUnsupportedOrigin, "connect-guest is for an imported Proxmox guest")
	}
	if a.ConnectedServerID != nil {
		linked, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: *a.ConnectedServerID, TeamID: teamID})
		if err != nil {
			return db.Server{}, nil, notFound("the connected server")
		}
		return linked, nil, nil
	}

	node, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil {
		return db.Server{}, nil, notFound("server")
	}
	if node.Status != "enrolled" || node.AgentPublicKey == nil {
		return db.Server{}, nil, coded(409, CodeServerOffline, "the node's agent is not connected")
	}

	vmid := guestVMID(a)
	if vmid == "" {
		return db.Server{}, nil, coded(422, CodeUnsupportedOrigin, "this app has no Proxmox guest id on record")
	}

	name := a.Name
	row, install, err := provisioning.AddServer(ctx, s.ops, q, teamID, actor.UserID, provisioning.AddInput{Name: name, Provider: "byo"})
	if err != nil {
		if ue, ok := err.(*provisioning.UserError); ok && ue.Conflict {
			// The app's own name is taken (most likely by a server another connect-guest, or the
			// team, already created) — try once more with a "-lxc" suffix before giving up.
			row, install, err = provisioning.AddServer(ctx, s.ops, q, teamID, actor.UserID,
				provisioning.AddInput{Name: name + "-lxc", Provider: "byo"})
		}
		if err != nil {
			return db.Server{}, nil, err
		}
	}

	guestType := "lxc"
	if a.Origin != nil {
		var o importer.Origin
		if json.Unmarshal([]byte(*a.Origin), &o) == nil && o.GuestType != "" {
			guestType = o.GuestType
		}
	}
	if linked, err := q.SetServerParent(ctx, db.SetServerParentParams{ParentServerID: &node.ID, GuestVmid: nullVMID(vmid),
		GuestType: &guestType, Manual: true, ID: row.ID, TeamID: teamID}); err == nil {
		row = linked
	}

	commandBox, err := agentproto.SealBox(*node.AgentPublicKey, agentproto.GuestEnrolContext(vmid), []byte(install.CommandRoot))
	if err != nil {
		return db.Server{}, nil, invalid("could not seal the install command to the node's agent")
	}
	params, _ := json.Marshal(agentproto.GuestEnrolParams{VMID: vmid, CommandBox: commandBox})
	if _, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: node.ID, Kind: agentproto.KindGuestEnrol,
		Params: params, CreatedBy: actor.UserID}); err != nil {
		return db.Server{}, nil, err
	}

	if _, err := q.SetAppConnectedServer(ctx, db.SetAppConnectedServerParams{ID: a.ID, TeamID: teamID, ConnectedServerID: row.ID}); err != nil {
		return db.Server{}, nil, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "app.guest_connected", "app", a.ID, actor.audit(map[string]any{
		"server": row.Name, "node": node.Name, "vmid": vmid})); err != nil {
		return db.Server{}, nil, err
	}
	return row, install, nil
}

// guestVMID reads the Proxmox guest id off an imported app: its origin JSON when present, else the
// "guest:<vmid>" origin key the importer always sets.
func guestVMID(a db.App) string {
	if a.Origin != nil {
		var o importer.Origin
		if json.Unmarshal([]byte(*a.Origin), &o) == nil && o.VMID != "" {
			return o.VMID
		}
	}
	if a.OriginKey != nil {
		if _, id, ok := strings.Cut(*a.OriginKey, "guest:"); ok {
			return id
		}
	}
	return ""
}

func nullVMID(s string) sql.NullInt32 {
	if s == "" {
		return sql.NullInt32{}
	}
	var n int32
	for _, c := range s {
		if c < '0' || c > '9' {
			return sql.NullInt32{}
		}
		n = n*10 + int32(c-'0')
	}
	return sql.NullInt32{Int32: n, Valid: true}
}
