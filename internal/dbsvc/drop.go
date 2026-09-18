package dbsvc

import (
	"context"
	"strings"

	"github.com/fadymondy/cloudy/internal/agentproto"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/tenancy"
)

// DropDatabase drops one database (a MinIO bucket) of a service and the users created for it. The
// rows go at once, in the caller's transaction; db.drop removes them on the server in the background.
// An app attached with one of those users keeps its variable until it is detached or redeployed.
func (s *Service) DropDatabase(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, ref string) (db.Database, error) {
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
	users, err := q.ListDatabaseUsersOfService(ctx, db.ListDatabaseUsersOfServiceParams{DataServiceID: svc.ID, TeamID: teamID})
	if err != nil {
		return db.Database{}, err
	}
	var names []string
	for _, u := range users {
		if u.DatabaseID != nil && *u.DatabaseID == target.ID {
			names = append(names, u.Name)
			if _, err := q.DeleteDatabaseUserInTeam(ctx, db.DeleteDatabaseUserInTeamParams{ID: u.ID, TeamID: teamID}); err != nil {
				return db.Database{}, err
			}
		}
	}
	if n, err := q.DeleteDatabaseInTeam(ctx, db.DeleteDatabaseInTeamParams{ID: target.ID, TeamID: teamID}); err != nil || n == 0 {
		return db.Database{}, notFound("database")
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "database.drop", "data_service", svc.ID,
		auditExtra(actor, map[string]any{"database": target.Name, "users": names})); err != nil {
		return db.Database{}, err
	}
	dropped := *target
	s.spawn(func(bg context.Context) {
		_ = s.withServer(bg, teamID, svc.ServerID, func(agentKey string, dek []byte) (any, string, error) {
			rootBox, err := s.sealRoot(agentKey, dek, teamID, svc)
			if err != nil {
				return nil, "", err
			}
			p := agentproto.DBDropParams{ServiceID: svc.ID, Kind: svc.Kind, Container: contOf(svc), Database: dropped.Name, Users: names, RootSecret: rootBox}
			return p, opID(dropped.ID, "drop"), nil
		}, agentproto.KindDBDrop, nil)
	})
	return dropped, nil
}
