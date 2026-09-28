package agent

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// ---- db.import -------------------------------------------------------------------------------
//
// pg_dump the source (run inside the managed service's own container, which carries client
// binaries of its own major version) piped straight into pg_restore against the target database,
// in the same container — never touching disk in between (pipeDumpToRestic's shape, for two
// processes instead of one plus restic). The source URL is opened from its sealed box into
// host/port/user/password/dbname and only ever reaches a child process's environment (PGPASSWORD)
// or a fixed -h/-p/-U/-d flag — never a single connection string that would carry the password on
// an argv visible to `ps`.

func dbImport(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DBImportParams](oc)
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
	src, err := oc.openSecret(p.ServiceID, "import:"+p.Database, p.SourceSecret)
	if err != nil {
		return nil, 1, err
	}
	host, port, suser, spass, sdb, err := parsePGSourceURL(src["source_url"])
	if err != nil {
		return nil, 2, err
	}
	t := dbTargetOf(p.VMID, p.Container)
	if !dbTargetRunning(oc, t) {
		return nil, 1, errors.New("the data service is not running")
	}

	if p.Replace {
		oc.Info("replace: dropping and recreating %q (owner %q)", p.Database, p.Owner)
		sql := fmt.Sprintf("\\set ON_ERROR_STOP on\nDROP DATABASE IF EXISTS \"%[1]s\" WITH (FORCE);\nCREATE DATABASE \"%[1]s\" OWNER \"%[2]s\";\n", p.Database, p.Owner)
		login, env := clientLogin("postgres", t, root, "")
		argv := clientArgv("postgres", t, append([]string{"psql", "-v", "ON_ERROR_STOP=1"}, login...))
		if out, err := execInTarget(oc, env, sql, t, argv...); err != nil {
			oc.Info("%s", strings.TrimSpace(out))
			return nil, 1, errors.New("could not replace the target database")
		}
	} else if err := refuseIfTablesExist(oc, t, p, root); err != nil {
		return nil, 1, err
	}

	oc.Info("importing %s@%s:%s/%s into %q (owner %q)", orRedacted(suser), host, port, sdb, p.Database, p.Owner)
	res, code, err := dbImportPipe(oc, t, p, host, port, suser, spass, sdb, root)
	if err != nil {
		return nil, code, err
	}
	oc.Info("import finished: %d bytes, %d table(s), %d warning(s)", res.BytesRestored, res.TablesRestored, res.WarningCount)
	return res, 0, nil
}

// orRedacted never logs an empty username as blank noise but never logs a password either — the
// username on its own names nothing secret.
func orRedacted(s string) string {
	if s == "" {
		return "(the url's default user)"
	}
	return s
}

// refuseIfTablesExist refuses the import when the target database already has any user table,
// unless the caller asked for Replace — so a re-run without it never clobbers real data by accident.
func refuseIfTablesExist(oc *OpContext, t dbTarget, p agentproto.DBImportParams, root map[string]string) error {
	login, env := clientLogin("postgres", t, root, p.Database)
	argv := clientArgv("postgres", t, append([]string{"psql", "-v", "ON_ERROR_STOP=1", "-tAc",
		"SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')"}, login...))
	out, err := execInTarget(oc, env, "", t, argv...)
	if err != nil {
		oc.Info("%s", strings.TrimSpace(out))
		return errors.New("could not check the target database for existing tables")
	}
	if n, convErr := strconv.Atoi(strings.TrimSpace(out)); convErr == nil && n > 0 {
		return errors.New("the target database already has tables — pass replace to overwrite it")
	}
	return nil
}

// parsePGSourceURL splits a postgres:// URL into its pieces. Never logged whole: only host/port/
// dbname (and the bare username) ever reach a log line; the password only ever reaches an exec's
// environment.
func parsePGSourceURL(raw string) (host, port, user, pass, dbname string, err error) {
	if !agentproto.ValidDBSourceURL(raw) {
		return "", "", "", "", "", errors.New("bad source url")
	}
	u, e := url.Parse(raw)
	if e != nil {
		return "", "", "", "", "", errors.New("the source url could not be parsed")
	}
	host = u.Hostname()
	port = u.Port()
	if port == "" {
		port = "5432"
	}
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	dbname = strings.TrimPrefix(u.Path, "/")
	if host == "" || dbname == "" {
		return "", "", "", "", "", errors.New("the source url needs a host and a database")
	}
	return host, port, user, pass, dbname, nil
}

// countingReader counts the bytes pg_restore reads off pg_dump's pipe (BytesRestored is best-effort:
// the size of the custom-format dump stream, not the target's on-disk size).
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// dbImportPipe runs pg_dump and pg_restore as two docker-exec (or pct-exec) children of this
// container, wiring the dump's stdout straight into the restore's stdin — the same shape as
// pipeDumpToRestic, just two processes instead of a dump plus restic. Nothing touches disk between
// them, so a database far larger than the server's free disk still imports.
func dbImportPipe(oc *OpContext, t dbTarget, p agentproto.DBImportParams, host, port, suser, spass, sdb string, root map[string]string) (agentproto.DBImportResult, int, error) {
	if err := checkDBTarget(oc, t); err != nil {
		return agentproto.DBImportResult{}, 1, err
	}
	dumpArgs := []string{"pg_dump", "-h", host, "-p", port, "-d", sdb, "--format=custom", "--no-owner", "--no-acl", "--verbose"}
	if suser != "" {
		dumpArgs = append(dumpArgs, "-U", suser)
	}
	var dumpEnv []string
	if spass != "" {
		dumpEnv = []string{"PGPASSWORD=" + spass}
	}
	dumpBin, dumpFull := dbTargetArgv(dumpEnv, t, dumpArgs)

	restoreLogin, restoreEnv := clientLogin("postgres", t, root, p.Database)
	restoreArgs := append([]string{"pg_restore"}, restoreLogin...)
	restoreArgs = append(restoreArgs, "--no-owner", "--no-acl", "--role="+p.Owner, "--verbose")
	restoreArgs = clientArgv("postgres", t, restoreArgs)
	restoreBin, restoreFull := dbTargetArgv(restoreEnv, t, restoreArgs)

	dump := exec.CommandContext(oc.Ctx, dumpBin, dumpFull...) //#nosec G204 -- fixed argv; host/port/user come from a validated url, password only ever on env
	if dumpBin == "docker" {
		dump.Env = append(dockerBaseEnv(), dumpEnv...)
	}
	restore := exec.CommandContext(oc.Ctx, restoreBin, restoreFull...) //#nosec G204 -- fixed argv; names are validated identifiers
	if restoreBin == "docker" {
		restore.Env = append(dockerBaseEnv(), restoreEnv...)
	}

	pipe, err := dump.StdoutPipe()
	if err != nil {
		return agentproto.DBImportResult{}, 1, err
	}
	counter := &countingReader{r: pipe}
	restore.Stdin = counter

	dumpStderr, err := dump.StderrPipe()
	if err != nil {
		return agentproto.DBImportResult{}, 1, err
	}
	restoreStderr, err := restore.StderrPipe()
	if err != nil {
		return agentproto.DBImportResult{}, 1, err
	}

	if err := restore.Start(); err != nil {
		return agentproto.DBImportResult{}, 1, fmt.Errorf("could not start pg_restore: %w", err)
	}
	if err := dump.Start(); err != nil {
		return agentproto.DBImportResult{}, 1, fmt.Errorf("could not start pg_dump: %w", err)
	}

	var tables, warnings int
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(dumpStderr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			oc.Info("pg_dump: %s", clip(strings.TrimSpace(sc.Text()), 300))
		}
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(restoreStderr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			low := strings.ToLower(line)
			if strings.Contains(low, "warning") || strings.Contains(low, "error") {
				warnings++
			}
			if strings.Contains(line, "processing data for table") {
				tables++
			}
			oc.Info("pg_restore: %s", clip(strings.TrimSpace(line), 300))
		}
	}()
	wg.Wait()

	derr := dump.Wait()
	rerr := restore.Wait()
	if derr != nil {
		return agentproto.DBImportResult{}, 1, errors.New("pg_dump failed reading the source database")
	}
	if rerr != nil {
		return agentproto.DBImportResult{}, 1, errors.New("pg_restore failed writing the target database")
	}
	return agentproto.DBImportResult{BytesRestored: counter.n, TablesRestored: tables, WarningCount: warnings}, 0, nil
}
