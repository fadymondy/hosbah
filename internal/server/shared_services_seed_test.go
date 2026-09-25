package server

import (
	"context"
	"database/sql"
	"testing"

	db2 "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// seedSharedService inserts a shared service directly (as if a scan of a Proxmox node had found
// it), so the isolation sweep has a real row of team A's to try to leak or edit as team B.
func seedSharedService(t *testing.T, teamID, serverID, name string) string {
	t.Helper()
	db := testDB(t)
	var id string
	err := tenancy.Tx(context.Background(), db, tenancy.Scope{System: true, TeamID: teamID}, func(_ *db2.Queries, tx *sql.Tx) error {
		return tx.QueryRow(`INSERT INTO shared_services (team_id, server_id, guest_vmid, guest_type, kind, name, host, port)
			VALUES ($1, $2, '102', 'lxc', 'postgres', $3, '10.10.10.102', 5432) RETURNING id`,
			teamID, serverID, name).Scan(&id)
	})
	if err != nil {
		t.Fatalf("seed a shared service: %v", err)
	}
	return id
}

// seedAppServiceLink inserts an app-to-service link directly, as Detect would have found it.
func seedAppServiceLink(t *testing.T, teamID, appID, sharedServiceID, variable string) string {
	t.Helper()
	db := testDB(t)
	var id string
	err := tenancy.Tx(context.Background(), db, tenancy.Scope{System: true, TeamID: teamID}, func(_ *db2.Queries, tx *sql.Tx) error {
		return tx.QueryRow(`INSERT INTO app_service_links (team_id, app_id, service_kind, service_id, variable, source)
			VALUES ($1, $2, 'shared', $3, $4, 'auto') RETURNING id`,
			teamID, appID, sharedServiceID, variable).Scan(&id)
	})
	if err != nil {
		t.Fatalf("seed an app service link: %v", err)
	}
	return id
}
