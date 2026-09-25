package dbsvc

import (
	"context"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Table browser and SQL console (MH-574 F2): db.query, run synchronously like db.user — the caller
waits for the agent and gets back one page of rows or one statement's result. Postgres and MySQL only
(usable() plus a kind check refuses Redis and MinIO with a clear message). Every call reseals the
service's root credential to the target agent, exactly like F1; nothing here is ever interpolated
unescaped into SQL — identifiers go through agentproto's strict validation on both sides, and the only
free text that ever reaches the engine is the member's own statement, fed to the client over stdin.

Read (tables/columns/rows and a read-only "sql") needs AbilityRevealSecrets, the same tier F1 uses for
a user's password: table contents can hold secrets, so it is treated as reveal-grade and is available
to owner/admin only. write=true additionally requires the caller already be owner/admin (the same
ability check covers it — there is no broader "member" tier here to fall back to) and is audited with
the statement text, truncated to 2 KiB; read-only queries are not audited per statement (list/get
already are, and auditing every SELECT would just be noise).
*/

const auditSQLClip = 2048

// QueryInput is one db.query call. Table is required for columns/rows (optionally schema-qualified
// for Postgres — "schema.table"). SQL is required for mode "sql".
type QueryInput struct {
	Database string
	Mode     string // tables|columns|rows|sql
	Table    string
	Limit    int
	Offset   int
	SQL      string
	Write    bool
}

// preflightQuery checks the service is a supported, running engine for the table browser and SQL
// console — same shape as preflightUser, but postgres/mysql only (no minio: mc has no table concept).
func preflightQuery(svc db.DataService) error {
	if err := usable(svc); err != nil {
		return err
	}
	switch svc.Kind {
	case "postgres", "mysql":
	case "redis":
		return invalid("Redis has no tables or SQL — use the data service's own client")
	case "minio":
		return invalid("MinIO buckets have no tables or SQL — use the data service's own client")
	default:
		return invalid("the table browser and SQL console apply to postgres and mysql")
	}
	if svc.Status != "running" {
		return conflict("the service is not running")
	}
	return nil
}

// Query runs one db.query call against a running Postgres/MySQL service, synchronously, and decodes
// the agent's result.
func (s *Service) Query(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, in QueryInput) (agentproto.DBQueryResult, error) {
	if err := preflightQuery(svc); err != nil {
		return agentproto.DBQueryResult{}, err
	}
	if !agentproto.ValidDBQueryMode(in.Mode) {
		return agentproto.DBQueryResult{}, invalid("mode is tables, columns, rows or sql")
	}
	if strings.TrimSpace(in.Database) == "" {
		return agentproto.DBQueryResult{}, invalid("database is required")
	}
	target, err := findDatabaseOfService(ctx, q, teamID, svc, in.Database)
	if err != nil {
		return agentproto.DBQueryResult{}, err
	}
	switch in.Mode {
	case "columns", "rows":
		if strings.TrimSpace(in.Table) == "" || !agentproto.ValidDBQualifiedTable(in.Table) {
			return agentproto.DBQueryResult{}, invalid("a valid table (or schema.table) is required")
		}
		if svc.Kind == "mysql" && strings.Contains(in.Table, ".") {
			return agentproto.DBQueryResult{}, invalid("mysql tables are not schema-qualified")
		}
	case "sql":
		if strings.TrimSpace(in.SQL) == "" {
			return agentproto.DBQueryResult{}, invalid("sql is required")
		}
		if len(in.SQL) > agentproto.DBQueryMaxSQLBytes {
			return agentproto.DBQueryResult{}, invalid("sql is too long")
		}
	}
	// Both read and write reach here only once the caller has passed AbilityRevealSecrets at the
	// route (REST) or tool (MCP) layer — owner/admin only, the same tier F1 uses for a user's
	// password. There is no broader tier to further restrict write=true against.
	limit := in.Limit
	if limit <= 0 || limit > agentproto.DBQueryMaxLimit {
		limit = 100
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}

	var result agentproto.DBQueryResult
	err = s.withServer(ctx, teamID, svc.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		rootBox, err := s.sealRoot(agentKey, dek, teamID, svc)
		if err != nil {
			return nil, "", err
		}
		tgt, vmid, guestType, container := execTarget(svc)
		p := agentproto.DBQueryParams{
			ServiceID: svc.ID, Kind: svc.Kind, Target: tgt, VMID: vmid, GuestType: guestType, Container: container, Database: target.Name, Mode: in.Mode,
			Table: in.Table, Limit: limit, Offset: offset, SQL: in.SQL, Write: in.Write, TimeoutSeconds: 30,
			RootSecret: rootBox,
		}
		if err := p.Validate(); err != nil {
			return nil, "", invalid(err.Error())
		}
		return p, opID(svc.ID, "query:"+newUUID()), nil
	}, agentproto.KindDBQuery, &result)
	if err != nil {
		return agentproto.DBQueryResult{}, err
	}

	if in.Write {
		stmt := in.SQL
		if len(stmt) > auditSQLClip {
			stmt = stmt[:auditSQLClip]
		}
		_ = tenancy.Record(ctx, q, teamID, actor.UserID, "database_query.write", "data_service", svc.ID,
			auditExtra(actor, map[string]any{"database": target.Name, "sql": stmt}))
	}
	return result, nil
}
