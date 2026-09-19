package dbsvc

import (
	"context"
	"database/sql"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// DropDatabase drops one database (a MinIO bucket) of a service and the users created for it.
//
// The rows are only marked `dropping` here; they are deleted once the agent reports that db.drop
// succeeded. If the drop fails (an old agent, an unreachable server, a timeout) they come back as
// `failed` with the error, so the control plane never forgets a database that still exists on the
// server, and its name stays taken until it really is gone. Dropping again retries.
func (s *Service) DropDatabase(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, ref string) (db.Database, error) {
	if err := usable(svc); err != nil {
		return db.Database{}, err
	}
	if svc.Kind == "redis" {
		return db.Database{}, invalid("Redis services have no databases")
	}
	if svc.Status != "running" {
		return db.Database{}, conflict("the service is not running")
	}
	dbs, err := q.ListDatabasesOfService(ctx, db.ListDatabasesOfServiceParams{DataServiceID: svc.ID, TeamID: teamID})
	if err != nil {
		return db.Database{}, err
	}
	ref = strings.TrimSpace(ref)
	var target *db.Database
	for i := range dbs {
		if dbs[i].ID == ref || dbs[i].Name == strings.ToLower(ref) {
			target = &dbs[i]
		}
	}
	if target == nil {
		return db.Database{}, notFound("database")
	}
	if target.Status == "dropping" {
		return db.Database{}, conflict(target.Name + " is already being dropped")
	}
	users, err := q.ListDatabaseUsersOfService(ctx, db.ListDatabaseUsersOfServiceParams{DataServiceID: svc.ID, TeamID: teamID})
	if err != nil {
		return db.Database{}, err
	}
	var names, userIDs []string
	for _, u := range users {
		if u.DatabaseID != nil && *u.DatabaseID == target.ID {
			names, userIDs = append(names, u.Name), append(userIDs, u.ID)
			if err := q.SetDatabaseUserStatus(ctx, db.SetDatabaseUserStatusParams{ID: u.ID, TeamID: teamID, Status: "dropping"}); err != nil {
				return db.Database{}, err
			}
		}
	}
	if err := q.SetDatabaseStatus(ctx, db.SetDatabaseStatusParams{ID: target.ID, TeamID: teamID, Status: "dropping"}); err != nil {
		return db.Database{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "database.drop", "data_service", svc.ID,
		auditExtra(actor, map[string]any{"database": target.Name, "users": names})); err != nil {
		return db.Database{}, err
	}
	dropped := *target
	dropped.Status = "dropping"
	attempt := newUUID() // a retry after a failed drop must be a new operation, not the old failed one
	s.spawn(func(bg context.Context) { s.dropFrom(bg, teamID, svc, dropped, names, userIDs, attempt) })
	return dropped, nil
}

// dropFrom runs db.drop and settles the rows: deleted on success, `failed` with the error otherwise.
func (s *Service) dropFrom(ctx context.Context, teamID string, svc db.DataService, d db.Database, names, userIDs []string, attempt string) {
	err := s.withServer(ctx, teamID, svc.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		rootBox, err := s.sealRoot(agentKey, dek, teamID, svc)
		if err != nil {
			return nil, "", err
		}
		p := agentproto.DBDropParams{ServiceID: svc.ID, Kind: svc.Kind, Container: contOf(svc), Database: d.Name, Users: names, RootSecret: rootBox}
		return p, opID(d.ID, "drop:"+attempt), nil
	}, agentproto.KindDBDrop, nil)
	// Team scope, not system: RLS gives the system workers no DELETE on these tables.
	_ = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		if err != nil {
			msg := "drop failed: " + err.Error()
			_ = q.SetDatabaseStatus(ctx, db.SetDatabaseStatusParams{ID: d.ID, TeamID: teamID, Status: "failed", Error: &msg})
			for _, id := range userIDs {
				_ = q.SetDatabaseUserStatus(ctx, db.SetDatabaseUserStatusParams{ID: id, TeamID: teamID, Status: "failed", Error: &msg})
			}
			return nil
		}
		for _, id := range userIDs {
			_, _ = q.DeleteDatabaseUserInTeam(ctx, db.DeleteDatabaseUserInTeamParams{ID: id, TeamID: teamID})
		}
		_, _ = q.DeleteDatabaseInTeam(ctx, db.DeleteDatabaseInTeamParams{ID: d.ID, TeamID: teamID})
		return nil
	})
}
