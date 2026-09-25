package agent

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// db.query (MH-574 F2): the table browser and SQL console. Postgres and MySQL/MariaDB only — Redis
// and MinIO are refused before this op is even built (the control plane's preflight). Every SQL
// statement — whether generated here (tables/columns/rows) or typed by a member (mode "sql") — runs
// inside one transaction, in a single client invocation, fed on stdin (never an argv): read-only and
// rolled back unless the caller explicitly asked for a write, which commits and is what the control
// plane audits. Identifiers (database, schema, table) are validated strict identifiers before they
// are ever formatted into a query string; only a SQL literal (the caller's statement itself, or a
// generated password elsewhere in this package) ever crosses a pipe instead.

// dbQueryNullSentinel marks SQL NULL in psql's CSV output, distinct from an actual empty string
// (which --csv already quotes as "" on its own). Control characters so it can never collide with
// ordinary text data.
const dbQueryNullSentinel = "\x02NULL\x02"

func dbQuery(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DBQueryParams](oc)
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

	start := time.Now()
	var res *agentproto.DBQueryResult
	switch p.Kind {
	case "postgres":
		res, err = dbQueryPostgres(oc, p, root)
	case "mysql":
		res, err = dbQueryMySQL(oc, p, root)
	default:
		return nil, 2, errors.New("unsupported kind")
	}
	if err != nil {
		return nil, 1, err
	}
	res.DurationMS = time.Since(start).Milliseconds()
	capResult(res)
	return res, 0, nil
}

// capResult enforces the total encoded size cap (DBQueryMaxResultSize) by dropping trailing rows,
// and truncates every cell to DBQueryMaxCellBytes. Truncated is set whenever either cap bites.
func capResult(res *agentproto.DBQueryResult) {
	total := 0
	for _, c := range res.Columns {
		total += len(c.Name) + len(c.Type)
	}
	kept := res.Rows[:0:0]
	for _, row := range res.Rows {
		rowSize := 0
		clipped := make([]*string, len(row))
		for i, cell := range row {
			if cell == nil {
				clipped[i] = nil
				continue
			}
			v := *cell
			if len(v) > agentproto.DBQueryMaxCellBytes {
				v = v[:agentproto.DBQueryMaxCellBytes]
				res.Truncated = true
			}
			clipped[i] = &v
			rowSize += len(v)
		}
		if total+rowSize > agentproto.DBQueryMaxResultSize {
			res.Truncated = true
			break
		}
		total += rowSize
		kept = append(kept, clipped)
	}
	if len(kept) < len(res.Rows) {
		res.Truncated = true
	}
	res.Rows = kept
	res.RowCount = len(kept)
}

// ---- Postgres --------------------------------------------------------------------------------

func dbQueryPostgres(oc *OpContext, p agentproto.DBQueryParams, root map[string]string) (*agentproto.DBQueryResult, error) {
	switch p.Mode {
	case "tables":
		sql := `SELECT n.nspname, c.relname, c.reltuples::bigint, pg_total_relation_size(c.oid)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema')
ORDER BY n.nspname, c.relname`
		_, rows, err := runPostgresQuery(oc, p, root, p.Database, sql, false)
		if err != nil {
			return nil, err
		}
		return &agentproto.DBQueryResult{Tables: pgTablesFrom(rows)}, nil
	case "columns":
		schema, table := agentproto.SplitQualifiedTable(p.Table)
		if schema == "" {
			schema = "public"
		}
		sql := fmt.Sprintf(`SELECT column_name, data_type FROM information_schema.columns
WHERE table_schema = %s AND table_name = %s ORDER BY ordinal_position`, pgQuoteLiteral(schema), pgQuoteLiteral(table))
		_, rows, err := runPostgresQuery(oc, p, root, p.Database, sql, false)
		if err != nil {
			return nil, err
		}
		out := &agentproto.DBQueryResult{}
		for _, r := range rows {
			name, typ := "", ""
			if len(r) > 0 && r[0] != nil {
				name = *r[0]
			}
			if len(r) > 1 && r[1] != nil {
				typ = *r[1]
			}
			out.Columns = append(out.Columns, agentproto.DBQueryColumn{Name: name, Type: typ})
		}
		return out, nil
	case "rows":
		schema, table := agentproto.SplitQualifiedTable(p.Table)
		ident := `"` + table + `"`
		if schema != "" {
			ident = `"` + schema + `"."` + table + `"`
		}
		limit := p.Limit
		if limit <= 0 {
			limit = 100
		}
		sql := fmt.Sprintf("SELECT * FROM %s LIMIT %d OFFSET %d", ident, limit, p.Offset)
		cols, rows, err := runPostgresQuery(oc, p, root, p.Database, sql, false)
		if err != nil {
			return nil, err
		}
		return &agentproto.DBQueryResult{Columns: colsFrom(cols), Rows: rows}, nil
	case "sql":
		stmt, err := agentproto.SingleSQLStatement("postgres", p.SQL)
		if err != nil {
			return nil, err
		}
		cols, rows, err := runPostgresQuery(oc, p, root, p.Database, stmt, p.Write)
		if err != nil {
			return nil, err
		}
		return &agentproto.DBQueryResult{Columns: colsFrom(cols), Rows: rows}, nil
	}
	return nil, errors.New("unsupported mode")
}

func colsFrom(names []string) []agentproto.DBQueryColumn {
	out := make([]agentproto.DBQueryColumn, len(names))
	for i, n := range names {
		out[i] = agentproto.DBQueryColumn{Name: n}
	}
	return out
}

func pgTablesFrom(rows [][]*string) []agentproto.DBTableInfo {
	var out []agentproto.DBTableInfo
	for _, r := range rows {
		if len(r) < 4 {
			continue
		}
		t := agentproto.DBTableInfo{}
		if r[0] != nil {
			t.Schema = *r[0]
		}
		if r[1] != nil {
			t.Name = *r[1]
		}
		if r[2] != nil {
			t.RowsEstimate, _ = strconv.ParseInt(*r[2], 10, 64)
		}
		if r[3] != nil {
			t.SizeBytes, _ = strconv.ParseInt(*r[3], 10, 64)
		}
		out = append(out, t)
	}
	return out
}

// runPostgresQuery wraps one statement in a transaction (read-only and always rolled back, unless
// write), applies the caller's statement timeout, and returns the CSV result as columns + cells.
func runPostgresQuery(oc *OpContext, p agentproto.DBQueryParams, root map[string]string, database, sql string, write bool) ([]string, [][]*string, error) {
	timeoutMS := p.TimeoutSeconds * 1000
	var script strings.Builder
	if write {
		fmt.Fprintf(&script, "BEGIN;\nSET LOCAL statement_timeout = %d;\n%s\n;\nCOMMIT;\n", timeoutMS, sql)
	} else {
		fmt.Fprintf(&script, "BEGIN READ ONLY;\nSET LOCAL statement_timeout = %d;\n%s\n;\nROLLBACK;\n", timeoutMS, sql)
	}
	args := append([]string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "--csv", "-P", "footer=off", "-P", "null=" + dbQueryNullSentinel},
		pgLogin(root, database)...)
	env := []string{"PGPASSWORD=" + root["password"]}
	out, errOut, err := execInTimeout(oc, env, script.String(), time.Duration(p.TimeoutSeconds+5)*time.Second, p.Container, append([]string{"psql"}, args...)...)
	if err != nil {
		return nil, nil, errors.New("query failed: " + clip(firstLine(strings.TrimSpace(errOut)), 400))
	}
	return parsePSQLCSV(out)
}

// parsePSQLCSV parses psql's --csv output (header row of column names, then data rows) into columns
// and cells, mapping dbQueryNullSentinel back to a nil cell.
func parsePSQLCSV(out string) ([]string, [][]*string, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil, nil
	}
	r := csv.NewReader(strings.NewReader(out))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("could not parse the result: %w", err)
	}
	if len(records) == 0 {
		return nil, nil, nil
	}
	cols := records[0]
	rows := make([][]*string, 0, len(records)-1)
	for _, rec := range records[1:] {
		row := make([]*string, len(rec))
		for i, v := range rec {
			if v == dbQueryNullSentinel {
				row[i] = nil
				continue
			}
			vv := v
			row[i] = &vv
		}
		rows = append(rows, row)
	}
	return cols, rows, nil
}

// ---- MySQL / MariaDB -------------------------------------------------------------------------

func dbQueryMySQL(oc *OpContext, p agentproto.DBQueryParams, root map[string]string) (*agentproto.DBQueryResult, error) {
	switch p.Mode {
	case "tables":
		sql := fmt.Sprintf("SELECT table_schema, table_name, table_rows, (data_length+index_length) FROM information_schema.tables WHERE table_schema = %s ORDER BY table_name",
			mysqlQuoteLiteral(p.Database))
		_, rows, err := runMySQLQuery(oc, p, root, sql, false)
		if err != nil {
			return nil, err
		}
		return &agentproto.DBQueryResult{Tables: pgTablesFrom(rows)}, nil
	case "columns":
		_, table := agentproto.SplitQualifiedTable(p.Table)
		sql := fmt.Sprintf("SELECT column_name, data_type FROM information_schema.columns WHERE table_schema = %s AND table_name = %s ORDER BY ordinal_position",
			mysqlQuoteLiteral(p.Database), mysqlQuoteLiteral(table))
		_, rows, err := runMySQLQuery(oc, p, root, sql, false)
		if err != nil {
			return nil, err
		}
		out := &agentproto.DBQueryResult{}
		for _, r := range rows {
			name, typ := "", ""
			if len(r) > 0 && r[0] != nil {
				name = *r[0]
			}
			if len(r) > 1 && r[1] != nil {
				typ = *r[1]
			}
			out.Columns = append(out.Columns, agentproto.DBQueryColumn{Name: name, Type: typ})
		}
		return out, nil
	case "rows":
		_, table := agentproto.SplitQualifiedTable(p.Table)
		limit := p.Limit
		if limit <= 0 {
			limit = 100
		}
		sql := fmt.Sprintf("SELECT * FROM `%s`.`%s` LIMIT %d OFFSET %d", p.Database, table, limit, p.Offset)
		cols, rows, err := runMySQLQuery(oc, p, root, sql, false)
		if err != nil {
			return nil, err
		}
		return &agentproto.DBQueryResult{Columns: colsFrom(cols), Rows: rows}, nil
	case "sql":
		stmt, err := agentproto.SingleSQLStatement("mysql", p.SQL)
		if err != nil {
			return nil, err
		}
		cols, rows, err := runMySQLQuery(oc, p, root, stmt, p.Write)
		if err != nil {
			return nil, err
		}
		return &agentproto.DBQueryResult{Columns: colsFrom(cols), Rows: rows}, nil
	}
	return nil, errors.New("unsupported mode")
}

func mysqlQuoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// runMySQLQuery is runPostgresQuery's MySQL/MariaDB twin: one transaction, read-only and rolled back
// unless write, with a session execution-time cap. mysql --batch's own escaping (\t \n \\ \0, and \N
// for NULL) is exactly what parseMySQLBatch expects.
func runMySQLQuery(oc *OpContext, p agentproto.DBQueryParams, root map[string]string, sql string, write bool) ([]string, [][]*string, error) {
	timeoutMS := p.TimeoutSeconds * 1000
	var script strings.Builder
	if write {
		fmt.Fprintf(&script, "START TRANSACTION;\nSET SESSION MAX_EXECUTION_TIME=%d;\n%s\n;\nCOMMIT;\n", timeoutMS, sql)
	} else {
		fmt.Fprintf(&script, "SET SESSION TRANSACTION READ ONLY;\nSTART TRANSACTION;\nSET SESSION MAX_EXECUTION_TIME=%d;\n%s\n;\nROLLBACK;\n", timeoutMS, sql)
	}
	env := []string{"MYSQL_PWD=" + root["password"]}
	args := append([]string{"--batch"}, mysqlLogin(root)...)
	out, errOut, err := execInTimeout(oc, env, script.String(), time.Duration(p.TimeoutSeconds+5)*time.Second, p.Container, append([]string{"mysql"}, args...)...)
	if err != nil {
		return nil, nil, errors.New("query failed: " + clip(firstLine(strings.TrimSpace(errOut)), 400))
	}
	return parseMySQLBatch(out)
}

// parseMySQLBatch parses mysql --batch output (a header line of column names, then one line per row,
// tab-separated, with \t \n \\ \0 escaped and NULL as the literal two characters \N) into columns and
// cells.
func parseMySQLBatch(out string) ([]string, [][]*string, error) {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return nil, nil, nil
	}
	lines := strings.Split(out, "\n")
	cols := splitMySQLRow(lines[0])
	rows := make([][]*string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		fields := splitMySQLRow(line)
		row := make([]*string, len(fields))
		for i, f := range fields {
			if f == `\N` {
				row[i] = nil
				continue
			}
			v := unescapeMySQLBatch(f)
			row[i] = &v
		}
		rows = append(rows, row)
	}
	return cols, rows, nil
}

func splitMySQLRow(line string) []string { return strings.Split(line, "\t") }

func unescapeMySQLBatch(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 't':
				b.WriteByte('\t')
				i++
				continue
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			case '0':
				b.WriteByte(0)
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ---- shared exec helper -----------------------------------------------------------------------

// execInTimeout is execIn with stdout and stderr kept apart (so a client's error text never lands
// inside the data we parse) and a hard deadline of its own, independent of the operation's overall
// context — a runaway statement is killed rather than left running against the caller's deadline.
func execInTimeout(oc *OpContext, env []string, stdin string, timeout time.Duration, container string, args ...string) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(oc.Ctx, timeout)
	defer cancel()
	full := append([]string{"exec", "-i"}, envArgs(mapEnv(env))...)
	full = append(full, container)
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "docker", full...) //#nosec G204 -- fixed argv; names are validated identifiers
	cmd.Env = append(dockerBaseEnv(), env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return out.String(), "the statement timed out", ctx.Err()
	}
	return out.String(), errb.String(), err
}
