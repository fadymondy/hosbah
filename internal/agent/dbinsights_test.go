package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// MH-575 F6: db.insights parsers (fixtures for both engines) and the fixed SQL builders.

func TestOnePayloadCell(t *testing.T) {
	if _, err := onePayloadCell(nil); err == nil {
		t.Fatal("no rows should error")
	}
	if _, err := onePayloadCell([][]*string{{}}); err == nil {
		t.Fatal("an empty row should error")
	}
	if _, err := onePayloadCell([][]*string{{nil}}); err == nil {
		t.Fatal("a nil cell should error")
	}
	v, err := onePayloadCell([][]*string{{strp(`{"a":1}`)}})
	if err != nil || v != `{"a":1}` {
		t.Fatalf("v=%q err=%v", v, err)
	}
}

func TestAtoiOrAndAtoi64Or(t *testing.T) {
	if got := atoiOr("42", -1); got != 42 {
		t.Errorf("atoiOr(42) = %d", got)
	}
	if got := atoiOr("  7 ", -1); got != 7 {
		t.Errorf("atoiOr trims whitespace, got %d", got)
	}
	if got := atoiOr("not-a-number", -1); got != -1 {
		t.Errorf("atoiOr should fall back to default, got %d", got)
	}
	if got := atoi64Or("9999999999", -1); got != 9999999999 {
		t.Errorf("atoi64Or(9999999999) = %d", got)
	}
	if got := atoi64Or("", -1); got != -1 {
		t.Errorf("atoi64Or('') should fall back to default, got %d", got)
	}
}

// A realistic pg_insights fixture: two connections, cache mostly warm, one long-running query, and
// pg_stat_statements available with two rows.
const pgInsightsFixture = `{
  "max_connections": 100,
  "connections_by_state": {"active": 2, "idle": 5},
  "xact_commit": 10234,
  "xact_rollback": 12,
  "blks_hit": 987654,
  "blks_read": 4321,
  "deadlocks": 0,
  "temp_bytes": 0,
  "databases": [{"name": "app", "size_bytes": 104857600}, {"name": "postgres", "size_bytes": 8192000}],
  "long_running": [{"pid": 4821, "duration_seconds": 12.345, "state": "active", "query": "SELECT * FROM big_table", "database": "app", "user": "app_rw"}],
  "locks_waiting": 1,
  "stat_statements_available": true
}`

// A realistic pgTopStatementsSQL fixture (its own statement, only run once the extension is known
// to be installed — see the comment on pgInsightsSQL for why they were split).
const pgTopStatementsFixture = `[
  {"query": "SELECT * FROM big_table WHERE id = $1", "calls": 500, "total_time_ms": 1234.5, "mean_time_ms": 2.469, "rows": 500},
  {"query": "UPDATE big_table SET x = $1", "calls": 10, "total_time_ms": 50.0, "mean_time_ms": 5.0, "rows": 10}
]`

func TestPgTopStatementsFixtureParses(t *testing.T) {
	var top []pgTopStatementRow
	if err := json.Unmarshal([]byte(pgTopStatementsFixture), &top); err != nil {
		t.Fatalf("fixture did not parse: %v", err)
	}
	if len(top) != 2 || top[0].Calls != 500 || top[0].Query == "" {
		t.Errorf("top = %+v", top)
	}
}

func TestBuildPgInsightsResult(t *testing.T) {
	var pl pgInsightsPayload
	if err := json.Unmarshal([]byte(pgInsightsFixture), &pl); err != nil {
		t.Fatalf("fixture did not parse: %v", err)
	}
	res := buildPgInsightsResult(pl)
	if res.Kind != "postgres" {
		t.Errorf("kind = %q", res.Kind)
	}
	if res.Connections != 7 {
		t.Errorf("connections = %d, want 7 (sum of by-state)", res.Connections)
	}
	if res.MaxConnections != 100 {
		t.Errorf("max_connections = %d", res.MaxConnections)
	}
	// cache hit ratio = 987654 / (987654+4321) * 100
	if res.CacheHitRatio < 99 || res.CacheHitRatio > 100 {
		t.Errorf("cache hit ratio out of expected range: %v", res.CacheHitRatio)
	}
	if len(res.Databases) != 2 || res.Databases[0].Name != "app" {
		t.Errorf("databases = %+v", res.Databases)
	}
	if len(res.LongRunning) != 1 || res.LongRunning[0].PID != "4821" || res.LongRunning[0].DurationSeconds != 12.35 {
		t.Errorf("long_running = %+v", res.LongRunning)
	}
	if !res.StatStatementsAvailable || res.StatStatementsHint != "" {
		t.Errorf("stat statements should be available with no hint: available=%v hint=%q", res.StatStatementsAvailable, res.StatStatementsHint)
	}
	// top_statements is filled in by pgInsights (a second, separate query) once it sees
	// StatStatementsAvailable — buildPgInsightsResult itself never touches it.
	if len(res.TopStatements) != 0 {
		t.Errorf("top_statements = %+v, want buildPgInsightsResult to leave it empty", res.TopStatements)
	}
}

func TestBuildPgInsightsResultStatStatementsUnavailable(t *testing.T) {
	var pl pgInsightsPayload
	if err := json.Unmarshal([]byte(`{"max_connections":50,"connections_by_state":{},"stat_statements_available":false}`), &pl); err != nil {
		t.Fatalf("fixture did not parse: %v", err)
	}
	res := buildPgInsightsResult(pl)
	if res.StatStatementsAvailable {
		t.Fatal("stat_statements_available should be false")
	}
	if res.StatStatementsHint != pgStatStatementsHint {
		t.Errorf("hint = %q, want the enable-it hint", res.StatStatementsHint)
	}
	if len(res.TopStatements) != 0 {
		t.Errorf("top_statements should be empty, got %+v", res.TopStatements)
	}
	if res.CacheHitRatio != 0 {
		t.Errorf("no hits/reads reported: cache hit ratio should stay 0, got %v", res.CacheHitRatio)
	}
}

// A realistic mysql_insights fixture. MySQL returns SHOW-STATUS-style values as JSON strings.
const mysqlInsightsFixture = `{
  "threads_connected": "3",
  "threads_running": "1",
  "questions": "88123",
  "slow_queries": "4",
  "uptime_seconds": "360000",
  "innodb_buffer_pool_reads": "120",
  "innodb_buffer_pool_read_requests": "48000",
  "max_connections": "151",
  "slow_query_log": "OFF",
  "locks_waiting": 0,
  "stat_statements_available": true,
  "databases": [{"name": "app", "size_bytes": 20971520}],
  "long_running": [{"pid": 55, "duration_seconds": 3, "state": "Query", "query": "SELECT SLEEP(3)", "database": "app", "user": "app_rw"}],
  "top_statements": [{"query": "SELECT * FROM orders WHERE id = ?", "calls": 200, "total_time_ms": 900.0, "mean_time_ms": 4.5, "rows": 200}]
}`

func TestBuildMySQLInsightsResult(t *testing.T) {
	var pl mysqlInsightsPayload
	if err := json.Unmarshal([]byte(mysqlInsightsFixture), &pl); err != nil {
		t.Fatalf("fixture did not parse: %v", err)
	}
	res := buildMySQLInsightsResult(pl)
	if res.Kind != "mysql" {
		t.Errorf("kind = %q", res.Kind)
	}
	if res.ThreadsConnected != 3 || res.Connections != 3 {
		t.Errorf("threads_connected/connections = %d/%d, want 3/3", res.ThreadsConnected, res.Connections)
	}
	if res.MaxConnections != 151 {
		t.Errorf("max_connections = %d", res.MaxConnections)
	}
	if res.SlowQueries != 4 || res.Questions != 88123 {
		t.Errorf("slow_queries/questions = %d/%d", res.SlowQueries, res.Questions)
	}
	if res.SlowQueryLog != "OFF" {
		t.Errorf("slow_query_log = %q", res.SlowQueryLog)
	}
	// cache hit ratio = (1 - 120/48000) * 100 = 99.75
	if res.CacheHitRatio != 99.75 {
		t.Errorf("cache hit ratio = %v, want 99.75", res.CacheHitRatio)
	}
	if len(res.Databases) != 1 || res.Databases[0].SizeBytes != 20971520 {
		t.Errorf("databases = %+v", res.Databases)
	}
	if len(res.LongRunning) != 1 || res.LongRunning[0].PID != "55" {
		t.Errorf("long_running = %+v", res.LongRunning)
	}
	if !res.StatStatementsAvailable || res.StatStatementsHint != "" {
		t.Errorf("stat statements should be available with no hint")
	}
	if len(res.TopStatements) != 1 || res.TopStatements[0].Rows != 200 {
		t.Errorf("top_statements = %+v", res.TopStatements)
	}
}

func TestBuildMySQLInsightsResultStatStatementsUnavailable(t *testing.T) {
	var pl mysqlInsightsPayload
	if err := json.Unmarshal([]byte(`{"max_connections":"151","stat_statements_available":false}`), &pl); err != nil {
		t.Fatalf("fixture did not parse: %v", err)
	}
	res := buildMySQLInsightsResult(pl)
	if res.StatStatementsAvailable {
		t.Fatal("stat_statements_available should be false")
	}
	if res.StatStatementsHint != mysqlStatStatementsHint {
		t.Errorf("hint = %q, want the enable-it hint", res.StatStatementsHint)
	}
	// Malformed/missing numeric fields should fall back to zero rather than error.
	if res.Connections != 0 || res.CacheHitRatio != 0 {
		t.Errorf("missing counters should default to zero: connections=%d cache=%v", res.Connections, res.CacheHitRatio)
	}
}

// ---- SQL builders: structural checks that the fixed, agent-owned statements ask for what the spec
// requires, without needing a live database. These are single, fixed SELECTs — never user input.

func TestPgInsightsSQLShape(t *testing.T) {
	sql := pgInsightsSQL
	for _, want := range []string{
		"max_connections", "pg_stat_activity", "xact_commit", "xact_rollback",
		"blks_hit", "blks_read", "deadlocks", "temp_bytes", "pg_database_size",
		"long_running", "left(query, 500)", "locks_waiting", "pg_locks",
		"stat_statements_available", "pg_extension",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("pgInsightsSQL is missing %q", want)
		}
	}
	// It must never select FROM the pg_stat_statements view itself: Postgres plans every branch of a
	// CASE at parse time, so a statement that does fails to parse at all when the extension isn't
	// installed, even behind "CASE WHEN <it's installed> THEN ... END" (a live postgres:16 smoke test
	// without the extension caught this). Naming it as a string literal in the EXISTS(pg_extension)
	// probe is fine — that is the only mention it should have here. The actual pg_stat_statements
	// query is pgTopStatementsSQL, run separately and only once stat_statements_available is true.
	if strings.Contains(sql, "FROM pg_stat_statements") {
		t.Error("pgInsightsSQL must not select FROM pg_stat_statements — it would fail to parse when the extension is missing")
	}
	// Excludes the agent's own backend from connection/lock accounting.
	if !strings.Contains(sql, "pg_backend_pid()") {
		t.Error("pgInsightsSQL should exclude the agent's own backend")
	}
	// A single statement — SingleSQLStatement guards db.query, not this fixed SQL, but it should
	// still be one statement (no user input ever reaches it, but sloppy semicolons are still a bug).
	if strings.Count(strings.TrimRight(sql, "\n\t "), ";") > 0 {
		t.Error("pgInsightsSQL should be a single statement with no trailing semicolon")
	}
}

func TestPgTopStatementsSQLShape(t *testing.T) {
	sql := pgTopStatementsSQL
	for _, want := range []string{"pg_stat_statements", "total_exec_time", "mean_exec_time", "LIMIT 20", "left(query, 500)"} {
		if !strings.Contains(sql, want) {
			t.Errorf("pgTopStatementsSQL is missing %q", want)
		}
	}
	if strings.Count(strings.TrimRight(sql, "\n\t "), ";") > 0 {
		t.Error("pgTopStatementsSQL should be a single statement with no trailing semicolon")
	}
}

func TestMySQLInsightsSQLShape(t *testing.T) {
	sql := mysqlInsightsSQL
	for _, want := range []string{
		"Threads_connected", "Threads_running", "Questions", "Slow_queries", "Uptime",
		"Innodb_buffer_pool_reads", "Innodb_buffer_pool_read_requests", "max_connections",
		"slow_query_log", "INNODB_TRX", "statements_digest", "processlist",
		"events_statements_summary_by_digest", "LIMIT 20",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("mysqlInsightsSQL is missing %q", want)
		}
	}
	if !strings.Contains(sql, "CONNECTION_ID()") {
		t.Error("mysqlInsightsSQL should exclude the agent's own connection from long_running")
	}
}
