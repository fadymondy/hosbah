package dbsvc

import (
	"context"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Standalone database users (MH-574 F1): create, rotate, delete, grant and revoke a user independent of
CreateDatabaseAndUser's combined database+user flow. Every action is a synchronous db.user operation
(the caller waits for the agent, like Attach/RevealURL) so a create/rotate can return the plaintext
password once, and a grant/revoke/delete can report success or failure immediately rather than leaving
a row to poll. The row is only written once the agent has confirmed the action, so a failed one never
lingers half-done in the team's list.

Applies to postgres, mysql and minio (databases/buckets and their users); Redis and an imported
service without a stored root credential are refused by usable()/checked here. The engine never sees
free text: Database and User are validated identifiers (agentproto.ValidDBName), Grants is one of the
three presets db.create already uses (all/readwrite/readonly) plus "none" for a revoked user, and the
agent builds the SQL itself from those fixed values — nothing here is interpolated into a query string.
*/

func findDatabaseUser(ctx context.Context, q *db.Queries, teamID string, svc db.DataService, ref string) (db.DatabaseUser, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.DatabaseUser{}, invalid("a user name or id is required")
	}
	if agentproto.ValidID(ref) {
		if u, err := q.DatabaseUserInTeam(ctx, db.DatabaseUserInTeamParams{ID: ref, TeamID: teamID}); err == nil && u.DataServiceID == svc.ID {
			return u, nil
		}
	}
	u, err := q.DatabaseUserByNameOfService(ctx, db.DatabaseUserByNameOfServiceParams{DataServiceID: svc.ID, TeamID: teamID, Name: strings.ToLower(ref)})
	if err != nil {
		return db.DatabaseUser{}, notFound("database user")
	}
	return u, nil
}

func findDatabaseOfService(ctx context.Context, q *db.Queries, teamID string, svc db.DataService, ref string) (db.Database, error) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	rows, err := q.ListDatabasesOfService(ctx, db.ListDatabasesOfServiceParams{DataServiceID: svc.ID, TeamID: teamID})
	if err != nil {
		return db.Database{}, err
	}
	for _, r := range rows {
		if r.ID == ref || r.Name == ref {
			return r, nil
		}
	}
	return db.Database{}, notFound("database")
}

// preflightUser checks a service is a supported, running engine for standalone user management.
func preflightUser(svc db.DataService) error {
	if err := usable(svc); err != nil {
		return err
	}
	if svc.Kind != "postgres" && svc.Kind != "mysql" && svc.Kind != "minio" {
		return invalid("database users apply to postgres, mysql and minio (buckets)")
	}
	if svc.Status != "running" {
		return conflict("the service is not running")
	}
	return nil
}

// runUserOp seals the user's secret (when given) and runs db.user, synchronously. Every call is its
// own fresh operation id (a create/rotate/delete/grant/revoke is not meant to be resumed like a
// long-running provision — it either finishes now or the caller retries the whole action).
func (s *Service) runUserOp(ctx context.Context, q *db.Queries, teamID string, svc db.DataService, action, database, userName, grants, newPassword string) error {
	return s.withServer(ctx, teamID, svc.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		rootBox, err := s.sealRoot(agentKey, dek, teamID, svc)
		if err != nil {
			return nil, "", err
		}
		target, vmid, guestType, container := execTarget(svc)
		p := agentproto.DBUserParams{
			ServiceID: svc.ID, Kind: svc.Kind, Target: target, VMID: vmid, GuestType: guestType, Container: container, Action: action,
			Database: database, User: userName, Grants: grants, RootSecret: rootBox,
		}
		if newPassword != "" {
			userBox, err := agentproto.SealBox(agentKey, agentproto.DBSecretContext(svc.ID, "user:"+userName), mustJSON(map[string]string{"password": newPassword}))
			if err != nil {
				return nil, "", err
			}
			p.UserSecret = userBox
		}
		return p, opID(svc.ID, "user:"+action+":"+userName+":"+newUUID()), nil
	}, agentproto.KindDBUser, nil)
}

// CreateUser adds a user to an existing database/bucket on a running service and returns its
// plaintext password once (never stored — only the sealed form is). owner/admin only (AbilityRevealSecrets).
func (s *Service) CreateUser(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, databaseRef, userName, grants string) (db.DatabaseUser, string, error) {
	if err := preflightUser(svc); err != nil {
		return db.DatabaseUser{}, "", err
	}
	target, err := findDatabaseOfService(ctx, q, teamID, svc, databaseRef)
	if err != nil {
		return db.DatabaseUser{}, "", err
	}
	userName = strings.ToLower(strings.TrimSpace(userName))
	if !agentproto.ValidDBName(userName) {
		return db.DatabaseUser{}, "", invalid("a user name is lower snake_case")
	}
	if grants == "" {
		grants = "readwrite"
	}
	if !agentproto.ValidGrants(grants) {
		return db.DatabaseUser{}, "", invalid("grants are all, readwrite or readonly")
	}
	if _, err := q.DatabaseUserByNameOfService(ctx, db.DatabaseUserByNameOfServiceParams{DataServiceID: svc.ID, TeamID: teamID, Name: userName}); err == nil {
		return db.DatabaseUser{}, "", conflict("a user named " + userName + " already exists on this service")
	}
	pw := randPassword(24)
	if err := s.runUserOp(ctx, q, teamID, svc, "create", target.Name, userName, grants, pw); err != nil {
		return db.DatabaseUser{}, "", err
	}
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return db.DatabaseUser{}, "", err
	}
	sealed, err := crypto.Seal(dek, teamID, userContext(svc.ID, userName), []byte(pw))
	if err != nil {
		return db.DatabaseUser{}, "", err
	}
	row, err := q.InsertDatabaseUser(ctx, db.InsertDatabaseUserParams{
		ID: newUUID(), TeamID: teamID, DataServiceID: svc.ID, DatabaseID: &target.ID, Name: userName,
		Grants: grants, PasswordSealed: sealed, Status: "ready", CreatedBy: actor.UserID,
	})
	if err != nil {
		return db.DatabaseUser{}, "", conflict("a user named " + userName + " already exists on this service")
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "database_user.create", "data_service", svc.ID,
		auditExtra(actor, map[string]any{"database": target.Name, "user": userName, "grants": grants})); err != nil {
		return db.DatabaseUser{}, "", err
	}
	return row, pw, nil
}

// RotateUserPassword generates a new password, applies it on the service, and returns it once.
func (s *Service) RotateUserPassword(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, userRef string) (db.DatabaseUser, string, error) {
	if err := preflightUser(svc); err != nil {
		return db.DatabaseUser{}, "", err
	}
	u, err := findDatabaseUser(ctx, q, teamID, svc, userRef)
	if err != nil {
		return db.DatabaseUser{}, "", err
	}
	pw := randPassword(24)
	if err := s.runUserOp(ctx, q, teamID, svc, "rotate", "", u.Name, "", pw); err != nil {
		return db.DatabaseUser{}, "", err
	}
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return db.DatabaseUser{}, "", err
	}
	sealed, err := crypto.Seal(dek, teamID, userContext(svc.ID, u.Name), []byte(pw))
	if err != nil {
		return db.DatabaseUser{}, "", err
	}
	if err := q.SetDatabaseUserPasswordSealed(ctx, db.SetDatabaseUserPasswordSealedParams{ID: u.ID, TeamID: teamID, PasswordSealed: sealed, Status: "ready", Error: nil}); err != nil {
		return db.DatabaseUser{}, "", err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "database_user.rotate_password", "data_service", svc.ID,
		auditExtra(actor, map[string]any{"user": u.Name})); err != nil {
		return db.DatabaseUser{}, "", err
	}
	u.Status = "ready"
	return u, pw, nil
}

// DeleteUser drops the user on the service, then its row. The service's own root/superuser credential
// is never a database_users row (it lives sealed on data_services.root_sealed), so there is no
// built-in admin to accidentally refuse here — every row this can find is a user Hosbah itself created.
func (s *Service) DeleteUser(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, userRef, confirm string) error {
	if err := preflightUser(svc); err != nil {
		return err
	}
	u, err := findDatabaseUser(ctx, q, teamID, svc, userRef)
	if err != nil {
		return err
	}
	if confirm != u.Name {
		return invalid("confirm must be the user's name: " + u.Name)
	}
	if err := s.runUserOp(ctx, q, teamID, svc, "delete", "", u.Name, "", ""); err != nil {
		return err
	}
	if _, err := q.DeleteDatabaseUserInTeam(ctx, db.DeleteDatabaseUserInTeamParams{ID: u.ID, TeamID: teamID}); err != nil {
		return err
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "database_user.delete", "data_service", svc.ID, auditExtra(actor, map[string]any{"user": u.Name}))
}

// GrantUser applies a grants preset for a user on a database (also used to change an existing user's
// level: grant re-applies from scratch, so switching from readonly to readwrite is a single call).
func (s *Service) GrantUser(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, userRef, databaseRef, grants string) (db.DatabaseUser, error) {
	if err := preflightUser(svc); err != nil {
		return db.DatabaseUser{}, err
	}
	u, err := findDatabaseUser(ctx, q, teamID, svc, userRef)
	if err != nil {
		return db.DatabaseUser{}, err
	}
	if !agentproto.ValidGrants(grants) {
		return db.DatabaseUser{}, invalid("grants are all, readwrite or readonly")
	}
	target, err := resolveUserDatabase(ctx, q, teamID, svc, u, databaseRef)
	if err != nil {
		return db.DatabaseUser{}, err
	}
	if err := s.runUserOp(ctx, q, teamID, svc, "grant", target.Name, u.Name, grants, ""); err != nil {
		return db.DatabaseUser{}, err
	}
	if err := q.SetDatabaseUserGrants(ctx, db.SetDatabaseUserGrantsParams{ID: u.ID, TeamID: teamID, Grants: grants, Status: "ready", Error: nil}); err != nil {
		return db.DatabaseUser{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "database_user.grant", "data_service", svc.ID,
		auditExtra(actor, map[string]any{"user": u.Name, "database": target.Name, "grants": grants})); err != nil {
		return db.DatabaseUser{}, err
	}
	u.Grants, u.Status = grants, "ready"
	return u, nil
}

// RevokeUser removes every privilege a grant preset could have given the user on a database, leaving
// the login/role itself in place (grants becomes "none" — delete_user drops the login entirely).
func (s *Service) RevokeUser(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, userRef, databaseRef string) (db.DatabaseUser, error) {
	if err := preflightUser(svc); err != nil {
		return db.DatabaseUser{}, err
	}
	u, err := findDatabaseUser(ctx, q, teamID, svc, userRef)
	if err != nil {
		return db.DatabaseUser{}, err
	}
	target, err := resolveUserDatabase(ctx, q, teamID, svc, u, databaseRef)
	if err != nil {
		return db.DatabaseUser{}, err
	}
	if err := s.runUserOp(ctx, q, teamID, svc, "revoke", target.Name, u.Name, "", ""); err != nil {
		return db.DatabaseUser{}, err
	}
	if err := q.SetDatabaseUserGrants(ctx, db.SetDatabaseUserGrantsParams{ID: u.ID, TeamID: teamID, Grants: "none", Status: "ready", Error: nil}); err != nil {
		return db.DatabaseUser{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "database_user.revoke", "data_service", svc.ID,
		auditExtra(actor, map[string]any{"user": u.Name, "database": target.Name})); err != nil {
		return db.DatabaseUser{}, err
	}
	u.Grants, u.Status = "none", "ready"
	return u, nil
}

// resolveUserDatabase is the database a grant/revoke applies to: the caller's choice, else the one the
// user was created against.
func resolveUserDatabase(ctx context.Context, q *db.Queries, teamID string, svc db.DataService, u db.DatabaseUser, databaseRef string) (db.Database, error) {
	if strings.TrimSpace(databaseRef) != "" {
		return findDatabaseOfService(ctx, q, teamID, svc, databaseRef)
	}
	if u.DatabaseID == nil {
		return db.Database{}, invalid("this user has no database on record — give one explicitly")
	}
	d, err := q.DatabaseInTeam(ctx, db.DatabaseInTeamParams{ID: *u.DatabaseID, TeamID: teamID})
	if err != nil {
		return db.Database{}, notFound("database")
	}
	return d, nil
}
