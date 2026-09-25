package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// db.insights (MH-575 F6): DB metrics and slow queries for the Postgres/MySQL services Hosbah runs.
// Every statement here is fixed, agent-owned SQL — nothing a member typed ever reaches the engine
// (that is db.query's job, guarded separately). Each engine gathers everything in one statement that
// builds a single JSON payload (json_build_object / JSON_OBJECT), so the agent never parses a table:
// it reads one column, one row, and unmarshals it.

func dbInsights(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DBInsightsParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	p.Container = resolveName(oc.Ctx, appDocker, kindContainer, p.Container)
	root, err := oc.openSecret(p.ServiceID, "root", p.RootSecret)
	if err != nil {
		return nil, 1, err
	}
	if !containerRunning(oc, p.Container) {
		return nil, 1, errors.New("the data service is not running")
	}
	if p.TimeoutSeconds <= 0 {
		p.TimeoutSeconds = 15
	}

	var res *agentproto.DBInsightsResult
	switch p.Kind {
	case "postgres":
		res, err = pgInsights(oc, p, root)
	case "mysql":
		res, err = mysqlInsights(oc, p, root)
	default:
		return nil, 2, errors.New("unsupported kind")
	}
	if err != nil {
		return nil, 1, err
	}
	return res, 0, nil
}

// ---- Postgres --------------------------------------------------------------------------------

// pgInsightsSQL gathers everything except top_statements. Postgres plans every branch of a CASE
// expression at parse time (not just the one taken), so a single statement that references
// pg_stat_statements — even behind "CASE WHEN <it's installed> THEN ... ELSE '[]' END" — fails to
// even parse when the extension isn't installed ("relation pg_stat_statements does not exist"), a
// live postgres:16 smoke test without the extension caught this. So top_statements is its own
// statement (pgTopStatementsSQL below), only run once stat_statements_available is known to be true.
const pgInsightsSQL = `SELECT json_build_object(
  'max_connections', (SELECT setting::int FROM pg_settings WHERE name = 'max_connections'),
  'connections_by_state', (SELECT COALESCE(json_object_agg(state, cnt), '{}'::json) FROM (
      SELECT COALESCE(state, 'unknown') AS state, count(*) AS cnt FROM pg_stat_activity
      WHERE pid <> pg_backend_pid() GROUP BY 1) s),
  'xact_commit', (SELECT COALESCE(SUM(xact_commit), 0) FROM pg_stat_database),
  'xact_rollback', (SELECT COALESCE(SUM(xact_rollback), 0) FROM pg_stat_database),
  'blks_hit', (SELECT COALESCE(SUM(blks_hit), 0) FROM pg_stat_database),
  'blks_read', (SELECT COALESCE(SUM(blks_read), 0) FROM pg_stat_database),
  'deadlocks', (SELECT COALESCE(SUM(deadlocks), 0) FROM pg_stat_database),
  'temp_bytes', (SELECT COALESCE(SUM(temp_bytes), 0) FROM pg_stat_database),
  'databases', (SELECT COALESCE(json_agg(json_build_object('name', datname, 'size_bytes', pg_database_size(datname))
      ORDER BY datname), '[]'::json) FROM pg_database WHERE NOT datistemplate),
  'long_running', (SELECT COALESCE(json_agg(json_build_object(
      'pid', pid, 'duration_seconds', EXTRACT(EPOCH FROM (clock_timestamp() - query_start)),
      'state', state, 'query', left(query, 500), 'database', datname, 'user', usename)
      ORDER BY query_start) , '[]'::json)
      FROM pg_stat_activity
      WHERE pid <> pg_backend_pid() AND state IS DISTINCT FROM 'idle' AND query_start IS NOT NULL
        AND clock_timestamp() - query_start > interval '1 second'),
  'locks_waiting', (SELECT count(*) FROM pg_locks WHERE NOT granted),
  'stat_statements_available', (SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements'))
) AS payload`

// pgTopStatementsSQL: only run once pgInsightsSQL reports stat_statements_available = true, so
// pg_stat_statements is guaranteed to exist as a relation when this parses.
const pgTopStatementsSQL = `SELECT COALESCE(json_agg(row_to_json(t)), '[]'::json) FROM (
  SELECT left(query, 500) AS query, calls, total_exec_time AS total_time_ms, mean_exec_time AS mean_time_ms, rows
  FROM pg_stat_statements WHERE query NOT LIKE '%pg_stat_statements%' ORDER BY total_exec_time DESC LIMIT 20
) t`

type pgInsightsPayload struct {
	MaxConnections     int            `json:"max_connections"`
	ConnectionsByState map[string]int `json:"connections_by_state"`
	XactCommit         int64          `json:"xact_commit"`
	XactRollback       int64          `json:"xact_rollback"`
	BlksHit            int64          `json:"blks_hit"`
	BlksRead           int64          `json:"blks_read"`
	Deadlocks          int64          `json:"deadlocks"`
	TempBytes          int64          `json:"temp_bytes"`
	Databases          []struct {
		Name      string `json:"name"`
		SizeBytes int64  `json:"size_bytes"`
	} `json:"databases"`
	LongRunning []struct {
		PID             int64   `json:"pid"`
		DurationSeconds float64 `json:"duration_seconds"`
		State           string  `json:"state"`
		Query           string  `json:"query"`
		Database        string  `json:"database"`
		User            string  `json:"user"`
	} `json:"long_running"`
	LocksWaiting            int  `json:"locks_waiting"`
	StatStatementsAvailable bool `json:"stat_statements_available"`
}

// pgTopStatementRow: one row of pgTopStatementsSQL's json_agg(row_to_json(...)).
type pgTopStatementRow struct {
	Query       string  `json:"query"`
	Calls       int64   `json:"calls"`
	TotalTimeMS float64 `json:"total_time_ms"`
	MeanTimeMS  float64 `json:"mean_time_ms"`
	Rows        int64   `json:"rows"`
}

const pgStatStatementsHint = "pg_stat_statements is not loaded. Add it to shared_preload_libraries and restart Postgres, then CREATE EXTENSION pg_stat_statements; — Hosbah never changes the config itself."

func pgInsights(oc *OpContext, p agentproto.DBInsightsParams, root map[string]string) (*agentproto.DBInsightsResult, error) {
	qp := agentproto.DBQueryParams{Container: p.Container, TimeoutSeconds: p.TimeoutSeconds}
	_, rows, err := runPostgresQuery(oc, qp, root, p.Database, pgInsightsSQL, false)
	if err != nil {
		return nil, err
	}
	payload, err := onePayloadCell(rows)
	if err != nil {
		return nil, err
	}
	var pl pgInsightsPayload
	if err := json.Unmarshal([]byte(payload), &pl); err != nil {
		return nil, fmt.Errorf("could not parse the insights result: %w", err)
	}
	res := buildPgInsightsResult(pl)
	if pl.StatStatementsAvailable {
		_, rows, err := runPostgresQuery(oc, qp, root, p.Database, pgTopStatementsSQL, false)
		if err != nil {
			return nil, err
		}
		tsPayload, err := onePayloadCell(rows)
		if err != nil {
			return nil, err
		}
		var top []pgTopStatementRow
		if err := json.Unmarshal([]byte(tsPayload), &top); err != nil {
			return nil, fmt.Errorf("could not parse the top-statements result: %w", err)
		}
		for _, t := range top {
			res.TopStatements = append(res.TopStatements, agentproto.DBTopStatement{
				Query: t.Query, Calls: t.Calls, TotalTimeMS: round2(t.TotalTimeMS), MeanTimeMS: round2(t.MeanTimeMS), Rows: t.Rows,
			})
		}
	}
	return res, nil
}

// buildPgInsightsResult turns the one JSON payload pgInsightsSQL returns into the wire result. Kept
// separate from pgInsights (which has to shell out to psql) so it can be unit tested with fixtures.
func buildPgInsightsResult(pl pgInsightsPayload) *agentproto.DBInsightsResult {
	res := &agentproto.DBInsightsResult{
		Kind: "postgres", MaxConnections: pl.MaxConnections, ConnectionsByState: pl.ConnectionsByState,
		XactCommit: pl.XactCommit, XactRollback: pl.XactRollback, Deadlocks: pl.Deadlocks, TempBytes: pl.TempBytes,
		LocksWaiting: pl.LocksWaiting, StatStatementsAvailable: pl.StatStatementsAvailable,
	}
	for _, c := range pl.ConnectionsByState {
		res.Connections += c
	}
	if total := pl.BlksHit + pl.BlksRead; total > 0 {
		res.CacheHitRatio = round2(float64(pl.BlksHit) / float64(total) * 100)
	}
	for _, d := range pl.Databases {
		res.Databases = append(res.Databases, agentproto.DBSizeInfo{Name: d.Name, SizeBytes: d.SizeBytes})
	}
	for _, r := range pl.LongRunning {
		res.LongRunning = append(res.LongRunning, agentproto.DBLongQuery{
			PID: strconv.FormatInt(r.PID, 10), DurationSeconds: round2(r.DurationSeconds), State: r.State,
			Query: r.Query, Database: r.Database, User: r.User,
		})
	}
	if !res.StatStatementsAvailable {
		res.StatStatementsHint = pgStatStatementsHint
	}
	return res
}

// ---- MySQL / MariaDB -------------------------------------------------------------------------

const mysqlInsightsSQL = "SELECT JSON_OBJECT(\n" +
	"  'threads_connected', (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Threads_connected'),\n" +
	"  'threads_running', (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Threads_running'),\n" +
	"  'questions', (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Questions'),\n" +
	"  'slow_queries', (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Slow_queries'),\n" +
	"  'uptime_seconds', (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Uptime'),\n" +
	"  'innodb_buffer_pool_reads', (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Innodb_buffer_pool_reads'),\n" +
	"  'innodb_buffer_pool_read_requests', (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Innodb_buffer_pool_read_requests'),\n" +
	"  'max_connections', (SELECT VARIABLE_VALUE FROM performance_schema.global_variables WHERE VARIABLE_NAME = 'max_connections'),\n" +
	"  'slow_query_log', (SELECT VARIABLE_VALUE FROM performance_schema.global_variables WHERE VARIABLE_NAME = 'slow_query_log'),\n" +
	"  'locks_waiting', (SELECT COUNT(*) FROM information_schema.INNODB_TRX WHERE trx_state = 'LOCK WAIT'),\n" +
	"  'stat_statements_available', (SELECT COUNT(*) > 0 FROM performance_schema.setup_consumers WHERE NAME = 'statements_digest' AND ENABLED = 'YES'),\n" +
	"  'databases', (SELECT IFNULL(JSON_ARRAYAGG(JSON_OBJECT('name', table_schema, 'size_bytes', size_bytes)), JSON_ARRAY()) FROM (\n" +
	"      SELECT table_schema, SUM(COALESCE(data_length,0)+COALESCE(index_length,0)) AS size_bytes FROM information_schema.tables\n" +
	"      WHERE table_schema NOT IN ('mysql','performance_schema','information_schema','sys') GROUP BY table_schema) t),\n" +
	"  'long_running', (SELECT IFNULL(JSON_ARRAYAGG(JSON_OBJECT('pid', id, 'duration_seconds', time, 'state', state,\n" +
	"      'query', LEFT(COALESCE(info,''), 500), 'database', db, 'user', user)), JSON_ARRAY()) FROM information_schema.processlist\n" +
	"      WHERE command <> 'Sleep' AND id <> CONNECTION_ID() AND time > 1),\n" +
	"  'top_statements', (SELECT IFNULL(JSON_ARRAYAGG(JSON_OBJECT('query', LEFT(digest_text, 500), 'calls', count_star,\n" +
	"      'total_time_ms', sum_timer_wait/1000000000, 'mean_time_ms', avg_timer_wait/1000000000, 'rows', sum_rows_examined)), JSON_ARRAY())\n" +
	"      FROM (SELECT digest_text, count_star, sum_timer_wait, avg_timer_wait, sum_rows_examined\n" +
	"            FROM performance_schema.events_statements_summary_by_digest\n" +
	"            WHERE digest_text IS NOT NULL ORDER BY sum_timer_wait DESC LIMIT 20) t)\n" +
	") AS payload"

type mysqlInsightsPayload struct {
	ThreadsConnected             string `json:"threads_connected"`
	ThreadsRunning               string `json:"threads_running"`
	Questions                    string `json:"questions"`
	SlowQueries                  string `json:"slow_queries"`
	UptimeSeconds                string `json:"uptime_seconds"`
	InnodbBufferPoolReads        string `json:"innodb_buffer_pool_reads"`
	InnodbBufferPoolReadRequests string `json:"innodb_buffer_pool_read_requests"`
	MaxConnections               string `json:"max_connections"`
	SlowQueryLog                 string `json:"slow_query_log"`
	LocksWaiting                 int    `json:"locks_waiting"`
	StatStatementsAvailable      bool   `json:"stat_statements_available"`
	Databases                    []struct {
		Name      string `json:"name"`
		SizeBytes int64  `json:"size_bytes"`
	} `json:"databases"`
	LongRunning []struct {
		PID             int64   `json:"pid"`
		DurationSeconds float64 `json:"duration_seconds"`
		State           string  `json:"state"`
		Query           string  `json:"query"`
		Database        string  `json:"database"`
		User            string  `json:"user"`
	} `json:"long_running"`
	TopStatements []struct {
		Query       string  `json:"query"`
		Calls       int64   `json:"calls"`
		TotalTimeMS float64 `json:"total_time_ms"`
		MeanTimeMS  float64 `json:"mean_time_ms"`
		Rows        int64   `json:"rows"`
	} `json:"top_statements"`
}

const mysqlStatStatementsHint = "the performance_schema statements_digest consumer is disabled. Enable it (performance-schema-consumer-statements-digest=ON, needs a restart to persist), then top statements are available — Hosbah never changes the config itself."

func mysqlInsights(oc *OpContext, p agentproto.DBInsightsParams, root map[string]string) (*agentproto.DBInsightsResult, error) {
	qp := agentproto.DBQueryParams{Container: p.Container, TimeoutSeconds: p.TimeoutSeconds}
	_, rows, err := runMySQLQuery(oc, qp, root, mysqlInsightsSQL, false)
	if err != nil {
		return nil, err
	}
	payload, err := onePayloadCell(rows)
	if err != nil {
		return nil, err
	}
	var pl mysqlInsightsPayload
	if err := json.Unmarshal([]byte(payload), &pl); err != nil {
		return nil, fmt.Errorf("could not parse the insights result: %w", err)
	}
	return buildMySQLInsightsResult(pl), nil
}

// buildMySQLInsightsResult turns the one JSON payload mysqlInsightsSQL returns into the wire result.
// Kept separate from mysqlInsights (which has to shell out to mysql) so it can be unit tested.
func buildMySQLInsightsResult(pl mysqlInsightsPayload) *agentproto.DBInsightsResult {
	res := &agentproto.DBInsightsResult{
		Kind: "mysql", MaxConnections: atoiOr(pl.MaxConnections, 0), LocksWaiting: pl.LocksWaiting,
		StatStatementsAvailable: pl.StatStatementsAvailable, SlowQueryLog: pl.SlowQueryLog,
		ThreadsConnected: atoi64Or(pl.ThreadsConnected, 0), ThreadsRunning: atoi64Or(pl.ThreadsRunning, 0),
		Questions: atoi64Or(pl.Questions, 0), SlowQueries: atoi64Or(pl.SlowQueries, 0),
		UptimeSeconds: atoi64Or(pl.UptimeSeconds, 0),
	}
	res.Connections = int(res.ThreadsConnected)
	reads, reqs := atoi64Or(pl.InnodbBufferPoolReads, 0), atoi64Or(pl.InnodbBufferPoolReadRequests, 0)
	if reqs > 0 {
		res.CacheHitRatio = round2((1 - float64(reads)/float64(reqs)) * 100)
	}
	for _, d := range pl.Databases {
		res.Databases = append(res.Databases, agentproto.DBSizeInfo{Name: d.Name, SizeBytes: d.SizeBytes})
	}
	for _, r := range pl.LongRunning {
		res.LongRunning = append(res.LongRunning, agentproto.DBLongQuery{
			PID: strconv.FormatInt(r.PID, 10), DurationSeconds: round2(r.DurationSeconds), State: r.State,
			Query: r.Query, Database: r.Database, User: r.User,
		})
	}
	if !res.StatStatementsAvailable {
		res.StatStatementsHint = mysqlStatStatementsHint
	}
	for _, t := range pl.TopStatements {
		res.TopStatements = append(res.TopStatements, agentproto.DBTopStatement{
			Query: t.Query, Calls: t.Calls, TotalTimeMS: round2(t.TotalTimeMS), MeanTimeMS: round2(t.MeanTimeMS), Rows: t.Rows,
		})
	}
	return res
}

// ---- db.cancel_query ---------------------------------------------------------------------------

func dbCancelQuery(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DBCancelQueryParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	p.Container = resolveName(oc.Ctx, appDocker, kindContainer, p.Container)
	root, err := oc.openSecret(p.ServiceID, "root", p.RootSecret)
	if err != nil {
		return nil, 1, err
	}
	if !containerRunning(oc, p.Container) {
		return nil, 1, errors.New("the data service is not running")
	}

	var res agentproto.DBCancelQueryResult
	switch p.Kind {
	case "postgres":
		sql := fmt.Sprintf("SELECT pg_cancel_backend(%d)", p.ID)
		qp := agentproto.DBQueryParams{Container: p.Container, TimeoutSeconds: 10}
		_, rows, err := runPostgresQuery(oc, qp, root, p.Database, sql, true)
		if err != nil {
			return nil, 1, err
		}
		ok := false
		if len(rows) > 0 && len(rows[0]) > 0 && rows[0][0] != nil {
			ok = *rows[0][0] == "t"
		}
		res = agentproto.DBCancelQueryResult{OK: ok}
		if !ok {
			res.Detail = "no such backend, or it had already finished"
		}
	case "mysql":
		env := []string{"MYSQL_PWD=" + root["password"]}
		args := append(mysqlLogin(root), "-e", fmt.Sprintf("KILL QUERY %d", p.ID))
		_, errOut, err := execInTimeout(oc, env, "", 10*time.Second, p.Container, append([]string{"mysql"}, args...)...)
		if err != nil {
			res = agentproto.DBCancelQueryResult{OK: false, Detail: clip(firstLine(strings.TrimSpace(errOut)), 300)}
		} else {
			res = agentproto.DBCancelQueryResult{OK: true}
		}
	default:
		return nil, 2, errors.New("unsupported kind")
	}
	return res, 0, nil
}

// ---- shared helpers -----------------------------------------------------------------------------

func onePayloadCell(rows [][]*string) (string, error) {
	if len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] == nil {
		return "", errors.New("the insights query returned no result")
	}
	return *rows[0][0], nil
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func atoi64Or(s string, def int64) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return def
	}
	return n
}
