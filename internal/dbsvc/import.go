package dbsvc

import (
	"context"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Import (db.import): pg_dump the source and pg_restore it into a database Hosbah already manages
// on a postgres service, both running inside the managed service's own container (which carries
// client binaries of its own major version — the source must be the same major version or older).
// The source connection URL travels sealed to the target agent's own key, exactly like a backup
// repo's or a root credential's secret (internal/agentproto.DBImportParams): never in plaintext
// params, never logged, never on an argv. Runs in the background like Backup/RestoreTest: the
// request returns once queued and audited; the database's status row (ready/failed) is how the web
// UI and the MCP tool poll for the outcome.

// ImportInput is one db.import call.
type ImportInput struct {
	SourceURL string // postgres://user:pass@host:port/db — a secret, never logged or returned
	Replace   bool   // drop and recreate the target database (owned by its current owner) first
}

// DatabaseInService resolves a database by id or name within svc — the same "id, then name" shape
// as ServiceByRef, scoped to one service so a name collision across services never crosses a
// boundary.
func DatabaseInService(ctx context.Context, q *db.Queries, teamID string, svc db.DataService, ref string) (db.Database, error) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" {
		return db.Database{}, invalid("a database name or id is required")
	}
	if agentproto.ValidID(ref) {
		if d, err := q.DatabaseInTeam(ctx, db.DatabaseInTeamParams{ID: ref, TeamID: teamID}); err == nil && d.DataServiceID == svc.ID {
			return d, nil
		}
	}
	d, err := q.DatabaseByNameOfService(ctx, db.DatabaseByNameOfServiceParams{DataServiceID: svc.ID, TeamID: teamID, Name: ref})
	if err != nil {
		return db.Database{}, notFound("database")
	}
	return d, nil
}

// databaseOwner is the name of the user Hosbah created this database with (CreateDatabaseAndUser
// ties a DatabaseUser to a Database's id); if none is found (an older row, or one made through
// db.user), the database's own name is the fallback, matching CreateDatabaseAndUser's own default of
// naming the user after the database.
func (s *Service) databaseOwner(ctx context.Context, q *db.Queries, teamID string, svc db.DataService, dbRow db.Database) (string, error) {
	users, err := q.ListDatabaseUsersOfService(ctx, db.ListDatabaseUsersOfServiceParams{DataServiceID: svc.ID, TeamID: teamID})
	if err != nil {
		return "", err
	}
	for _, u := range users {
		if u.DatabaseID != nil && *u.DatabaseID == dbRow.ID {
			return u.Name, nil
		}
	}
	return dbRow.Name, nil
}

// ImportDatabase validates the request, audits it, and runs the pg_dump | pg_restore in the
// background — mirroring Backup/RestoreTest, which also return before the agent op completes.
func (s *Service) ImportDatabase(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, dbRow db.Database, in ImportInput) error {
	if err := usable(svc); err != nil {
		return err
	}
	if svc.Kind != "postgres" {
		return invalid("importing an existing database is supported for postgres services only")
	}
	if svc.Status != "running" {
		return conflict("the service is not running")
	}
	in.SourceURL = strings.TrimSpace(in.SourceURL)
	if !agentproto.ValidDBSourceURL(in.SourceURL) {
		return invalid("source_url must be a postgres:// or postgresql:// connection URL with a host")
	}
	owner, err := s.databaseOwner(ctx, q, teamID, svc, dbRow)
	if err != nil {
		return err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "database.import", "data_service", svc.ID,
		auditExtra(actor, map[string]any{"database": dbRow.Name, "replace": in.Replace})); err != nil {
		return err
	}
	runID := newUUID()
	// The op's own timeout (ops.Timeout(agentproto.KindDBImport)) is 2h; this background context must
	// outlive it so withServer's Await is never cut short by spawnFor instead of the op's real result.
	s.spawnFor(2*time.Hour+10*time.Minute, func(bg context.Context) { s.runImport(bg, teamID, svc, dbRow, owner, runID, in) })
	return nil
}

func (s *Service) runImport(ctx context.Context, teamID string, svc db.DataService, dbRow db.Database, owner, runID string, in ImportInput) {
	var res agentproto.DBImportResult
	err := s.withServer(ctx, teamID, svc.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		rootBox, err := s.sealRoot(agentKey, dek, teamID, svc)
		if err != nil {
			return nil, "", err
		}
		srcBox, err := agentproto.SealBox(agentKey, agentproto.DBSecretContext(svc.ID, "import:"+dbRow.Name), mustJSON(map[string]string{"source_url": in.SourceURL}))
		if err != nil {
			return nil, "", err
		}
		target, vmid, guestType, container := execTarget(svc)
		p := agentproto.DBImportParams{
			ServiceID: svc.ID, Target: target, VMID: vmid, GuestType: guestType, Container: container,
			Database: dbRow.Name, Owner: owner, Replace: in.Replace, SourceSecret: srcBox, RootSecret: rootBox,
		}
		return p, opID(runID, "import"), nil
	}, agentproto.KindDBImport, &res)
	status, errStr := "ready", (*string)(nil)
	if err != nil {
		status, errStr = "failed", strptr(err.Error())
		s.log.Error("database import failed", "service", svc.ID, "database", dbRow.Name, "error", err)
	} else {
		s.log.Info("database import finished", "service", svc.ID, "database", dbRow.Name,
			"tables", res.TablesRestored, "bytes", res.BytesRestored, "warnings", res.WarningCount)
	}
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		return q.SetDatabaseStatus(ctx, db.SetDatabaseStatusParams{ID: dbRow.ID, TeamID: teamID, Status: status, Error: errStr})
	})
}
