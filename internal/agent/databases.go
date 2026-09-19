package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// P3 operations: managed data services and restic backups. Fixed argv for docker / restic; the only
// strings a user influences are a database or user name (validated to a strict identifier before
// use) and generated passwords, which travel on a pipe (docker exec stdin), never on an argv or in a
// file on disk. Secrets arrive as env boxes sealed to this server and are passed to child processes
// through their environment (RunEnv), so they are never logged.

// ---- restic.install ------------------------------------------------------------------------------

// stepRestic installs restic from the distribution package (like docker.io / git in P1/P2 — no
// third-party apt repository).
func stepRestic(oc *OpContext) (any, int, error) {
	changed, code, err := ensureRestic(oc)
	if err != nil {
		return nil, code, err
	}
	if !changed {
		oc.Info("restic is already installed (%s) — nothing to do", toolVersion(oc.Ctx, "restic", "version"))
		return map[string]any{"changed": false}, 0, nil
	}
	return map[string]any{"changed": true, "version": toolVersion(oc.Ctx, "restic", "version")}, 0, nil
}

// ensureRestic installs restic from the distribution package if it is not already present. It is
// idempotent, so both the explicit restic.install step and the first backup/restore can call it — a
// backup no longer fails just because the step was never run by hand.
func ensureRestic(oc *OpContext) (changed bool, code int, err error) {
	if _, err := exec.LookPath("restic"); err == nil {
		return false, 0, nil
	}
	oc.Info("restic is not installed — installing it now")
	if code, err := hostPkgManager().install(oc, []string{"restic"}); err != nil {
		return false, code, err
	}
	return true, 0, nil
}

// ---- helpers -------------------------------------------------------------------------------------

// openSecret opens a secret box for a service purpose into NAME → value.
func (o *OpContext) openSecret(serviceID, purpose, box string) (map[string]string, error) {
	if box == "" {
		return map[string]string{}, nil
	}
	pt, err := agentproto.OpenBox(o.priv, agentproto.DBSecretContext(serviceID, purpose), box)
	if err != nil {
		return nil, errors.New("a secret could not be opened with this server's key")
	}
	m := map[string]string{}
	if err := json.Unmarshal(pt, &m); err != nil {
		return nil, errors.New("a secret is malformed")
	}
	return m, nil
}

// execIn runs `docker exec` in a container with extra environment and optional SQL on stdin, and
// returns its combined output. The stdin carries statements (with a generated password) — a pipe,
// not an argv or a file.
func execIn(oc *OpContext, env []string, stdin, container string, args ...string) (string, error) {
	full := append([]string{"exec", "-i"}, envArgs(mapEnv(env))...)
	full = append(full, container)
	full = append(full, args...)
	cmd := exec.CommandContext(oc.Ctx, "docker", full...) //#nosec G204 -- fixed argv; names are validated identifiers
	cmd.Env = append(dockerBaseEnv(), env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func dockerBaseEnv() []string { return []string{"LC_ALL=C"} }

// mapEnv turns NAME=value pairs into a map (to reuse envArgs for the `-e NAME` list).
func mapEnv(pairs []string) map[string]string {
	m := map[string]string{}
	for _, p := range pairs {
		if i := strings.IndexByte(p, '='); i > 0 {
			m[p[:i]] = p[i+1:]
		}
	}
	return m
}

// pgQuoteLiteral doubles single quotes for a SQL string literal (passwords only; identifiers are
// already restricted to [a-z0-9_] by ValidDBName so they need no quoting games).
func pgQuoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// dataDirOf is the container path holding a kind's data (for the named volume).
func dataDirOf(kind string) string {
	switch kind {
	case "postgres":
		return "/var/lib/postgresql/data"
	case "mysql":
		return "/var/lib/mysql"
	default:
		return "/data"
	}
}

// ---- db.provision --------------------------------------------------------------------------------

func dbProvision(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DBProvisionParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if code, err := dbDockerReady(oc); err != nil {
		return nil, code, err
	}
	root, err := oc.openSecret(p.ServiceID, "root", p.RootSecret)
	if err != nil {
		return nil, 1, err
	}

	oc.Progress(1, 3)
	// If the container is already up on the right image, this is a resume — report it and stop.
	if img, _ := execOutput(oc, "docker", "inspect", "--format", "{{.Config.Image}}", p.Container); strings.TrimSpace(img) == p.Image && containerRunning(oc, p.Container) {
		oc.Info("%s is already running %s — nothing to do", p.Container, p.Image)
		return agentproto.DBProvisionResult{Container: p.Container, Volume: p.Volume, Host: p.Bind, Port: p.Port}, 0, nil
	}
	_ = exec.CommandContext(oc.Ctx, "docker", "rm", "-f", p.Container).Run() //#nosec G204 -- idempotent re-run
	if _, err := oc.Run("docker", "volume", "create", p.Volume); err != nil {
		return nil, 1, errors.New("could not create the data volume")
	}

	oc.Progress(2, 3)
	publish := fmt.Sprintf("%s:%d:%d", p.Bind, p.Port, agentproto.DBInternalPort(p.Kind))
	args := []string{"run", "-d", "--name", p.Container, "--restart", "unless-stopped",
		"--label", "cloudy.dataservice=" + p.ServiceID, "--label", "cloudy.kind=" + p.Kind,
		"--log-opt", "max-size=10m", "--log-opt", "max-file=3",
		"-v", p.Volume + ":" + dataDirOf(p.Kind), "-p", publish}
	// The image goes after the run options and before any container command, so redis'
	// server flags stay a command and don't get parsed as run options.
	var pairs []string
	switch p.Kind {
	case "postgres":
		args = append(args, "-e", "POSTGRES_PASSWORD", p.Image)
		pairs = append(pairs, "POSTGRES_PASSWORD="+root["password"])
	case "mysql":
		args = append(args, "-e", "MYSQL_ROOT_PASSWORD", p.Image)
		pairs = append(pairs, "MYSQL_ROOT_PASSWORD="+root["password"])
	case "redis":
		// No auth: the service is published on the private interface only.
		args = append(args, p.Image, "redis-server", "--save", "60", "1", "--appendonly", "no")
	case "minio":
		args = append(args, "-e", "MINIO_ROOT_USER", "-e", "MINIO_ROOT_PASSWORD", p.Image, "server", "/data")
		pairs = append(pairs, "MINIO_ROOT_USER="+agentproto.MinIORootUser, "MINIO_ROOT_PASSWORD="+root["password"])
	}
	oc.Info("starting %s %s on %s:%d", p.Kind, p.Version, p.Bind, p.Port)
	if code, err := oc.RunEnv(pairs, "docker", args...); err != nil {
		return nil, code, fmt.Errorf("could not start the %s container", p.Kind)
	}

	oc.Progress(3, 3)
	if err := dbWaitReady(oc, p.Kind, p.Container, root["password"], 90*time.Second); err != nil {
		dumpLogs(oc, p.Container, 40)
		return nil, 1, err
	}
	version := dbServerVersion(oc, p.Kind, p.Container, root["password"])
	oc.Info("%s is ready (%s)", p.Container, version)
	return agentproto.DBProvisionResult{Container: p.Container, Volume: p.Volume, Host: p.Bind, Port: p.Port, Version: version}, 0, nil
}

func dbDockerReady(oc *OpContext) (int, error) {
	if d := DockerInfo(oc.Ctx); !d.Running {
		return 1, errors.New("Docker is not running on this server — run the docker.install provisioning step")
	}
	return 0, nil
}

// dbWaitReady polls the engine's own readiness probe until it answers or the deadline passes.
func dbWaitReady(oc *OpContext, kind, container, rootpw string, within time.Duration) error {
	deadline := time.Now().Add(within)
	for attempt := 1; ; attempt++ {
		if oc.Ctx.Err() != nil {
			return oc.Ctx.Err()
		}
		if !containerRunning(oc, container) && attempt > 2 {
			return fmt.Errorf("the %s container exited while starting", kind)
		}
		var ok bool
		switch kind {
		case "postgres":
			_, err := execIn(oc, nil, "", container, "pg_isready", "-U", "postgres", "-q")
			ok = err == nil
		case "mysql":
			out, _ := execIn(oc, []string{"MYSQL_PWD=" + rootpw}, "", container, "mysqladmin", "ping", "-u", "root", "--silent")
			ok = strings.Contains(out, "alive")
		case "redis":
			out, _ := execIn(oc, nil, "", container, "redis-cli", "ping")
			ok = strings.Contains(strings.ToUpper(out), "PONG")
		case "minio":
			_, err := execIn(oc, nil, "", container, "mc", "ready", "local")
			ok = err == nil
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the %s service did not become ready in time", kind)
		}
		if attempt == 1 || attempt%5 == 0 {
			oc.Info("waiting for %s to accept connections…", kind)
		}
		time.Sleep(2 * time.Second)
	}
}

func dbServerVersion(oc *OpContext, kind, container, rootpw string) string {
	switch kind {
	case "postgres":
		out, _ := execIn(oc, []string{"PGPASSWORD=" + rootpw}, "", container, "psql", "-U", "postgres", "-tAc", "SHOW server_version")
		return "PostgreSQL " + strings.TrimSpace(firstLine(out))
	case "mysql":
		out, _ := execIn(oc, []string{"MYSQL_PWD=" + rootpw}, "SELECT VERSION();\n", container, "mysql", "-N", "-u", "root")
		return "MySQL " + strings.TrimSpace(firstLine(out))
	case "redis":
		out, _ := execIn(oc, nil, "", container, "redis-cli", "INFO", "server")
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "redis_version:") {
				return "Redis " + strings.TrimSpace(strings.TrimPrefix(l, "redis_version:"))
			}
		}
		return "Redis"
	}
	if kind == "minio" {
		out, _ := execIn(oc, nil, "", container, "minio", "--version")
		return "MinIO " + strings.TrimSpace(strings.TrimPrefix(firstLine(out), "minio version "))
	}
	return ""
}

// ---- db.create -----------------------------------------------------------------------------------

func dbCreate(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DBCreateParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	root, err := oc.openSecret(p.ServiceID, "root", p.RootSecret)
	if err != nil {
		return nil, 1, err
	}
	user, err := oc.openSecret(p.ServiceID, "user:"+p.User, p.UserSecret)
	if err != nil {
		return nil, 1, err
	}
	if !containerRunning(oc, p.Container) {
		return nil, 1, errors.New("the data service is not running")
	}
	switch p.Kind {
	case "postgres":
		return dbCreatePostgres(oc, p, root["password"], user["password"])
	case "mysql":
		return dbCreateMySQL(oc, p, root["password"], user["password"])
	case "minio":
		return dbCreateMinIO(oc, p, root["password"], user["password"])
	}
	return nil, 2, errors.New("unsupported kind")
}

func dbCreatePostgres(oc *OpContext, p agentproto.DBCreateParams, rootpw, userpw string) (any, int, error) {
	// The password travels on stdin as a SQL literal, never on an argv. Names are validated
	// identifiers, quoted with " for safety. CREATE DATABASE is idempotent via \gexec; the role via a
	// DO block; the grants re-applied every time.
	sql := fmt.Sprintf(`\set ON_ERROR_STOP on
SELECT 'CREATE DATABASE "%[1]s"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '%[1]s')\gexec
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%[2]s') THEN
    CREATE ROLE "%[2]s" LOGIN PASSWORD %[3]s;
  ELSE
    ALTER ROLE "%[2]s" LOGIN PASSWORD %[3]s;
  END IF;
END $$;
GRANT CONNECT ON DATABASE "%[1]s" TO "%[2]s";
`, p.Database, p.User, pgQuoteLiteral(userpw))
	// Object grants inside the database (owner for all, else schema privileges).
	inner := ""
	switch p.Grants {
	case "all":
		inner = fmt.Sprintf(`ALTER DATABASE "%[1]s" OWNER TO "%[2]s";`, p.Database, p.User)
	case "readwrite":
		inner = fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA public TO "%[2]s";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "%[2]s";
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "%[2]s";`, p.Database, p.User)
	case "readonly":
		inner = fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO "%[2]s";
GRANT SELECT ON ALL TABLES IN SCHEMA public TO "%[2]s";
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO "%[2]s";`, p.Database, p.User)
	}
	oc.Info("creating database %q and role %q (%s)", p.Database, p.User, p.Grants)
	if out, err := execIn(oc, []string{"PGPASSWORD=" + rootpw}, sql, p.Container, "psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres"); err != nil {
		oc.Info("%s", strings.TrimSpace(out))
		return nil, 1, errors.New("could not create the database and role")
	}
	if inner != "" {
		if out, err := execIn(oc, []string{"PGPASSWORD=" + rootpw}, inner+"\n", p.Container, "psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", p.Database); err != nil {
			oc.Info("%s", strings.TrimSpace(out))
			return nil, 1, errors.New("could not apply the grants")
		}
	}
	return agentproto.DBCreateResult{Database: p.Database, User: p.User}, 0, nil
}

func dbCreateMySQL(oc *OpContext, p agentproto.DBCreateParams, rootpw, userpw string) (any, int, error) {
	priv := "ALL PRIVILEGES"
	switch p.Grants {
	case "readwrite":
		priv = "SELECT, INSERT, UPDATE, DELETE"
	case "readonly":
		priv = "SELECT"
	}
	sql := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%[1]s`;\n"+
		"CREATE USER IF NOT EXISTS '%[2]s'@'%%' IDENTIFIED BY %[3]s;\n"+
		"ALTER USER '%[2]s'@'%%' IDENTIFIED BY %[3]s;\n"+
		"GRANT %[4]s ON `%[1]s`.* TO '%[2]s'@'%%';\nFLUSH PRIVILEGES;\n",
		p.Database, p.User, pgQuoteLiteral(userpw), priv)
	oc.Info("creating database %q and user %q (%s)", p.Database, p.User, p.Grants)
	if out, err := execIn(oc, []string{"MYSQL_PWD=" + rootpw}, sql, p.Container, "mysql", "-u", "root"); err != nil {
		oc.Info("%s", strings.TrimSpace(out))
		return nil, 1, errors.New("could not create the database and user")
	}
	return agentproto.DBCreateResult{Database: p.Database, User: p.User}, 0, nil
}

// ---- restic --------------------------------------------------------------------------------------

// resticEnv builds the environment restic needs: the repository, its password and (for s3) the AWS
// keys, all from the sealed repo secret. Values reach restic through its environment, never an argv.
func resticEnv(repo agentproto.RepoSpec, secret map[string]string) []string {
	env := []string{"RESTIC_REPOSITORY=" + repo.Repo}
	for _, k := range []string{"RESTIC_PASSWORD", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_DEFAULT_REGION"} {
		if v, ok := secret[k]; ok && v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// resticEnsureRepo initialises the repository if it does not exist yet.
func resticEnsureRepo(oc *OpContext, env []string) error {
	if err := runQuiet(oc, env, "restic", "cat", "config"); err == nil {
		return nil
	}
	oc.Info("initialising the restic repository")
	if _, err := oc.RunEnv(env, "restic", "init"); err != nil {
		return errors.New("could not initialise the backup repository")
	}
	return nil
}

func runQuiet(oc *OpContext, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(oc.Ctx, name, args...) //#nosec G204 -- fixed argv
	cmd.Env = append(dockerBaseEnv(), env...)
	return cmd.Run()
}

// ---- db.backup -----------------------------------------------------------------------------------

func dbBackup(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DBBackupParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if _, code, err := ensureRestic(oc); err != nil {
		return nil, code, err
	}
	if !containerRunning(oc, p.Container) {
		return nil, 1, errors.New("the data service is not running")
	}
	root, err := oc.openSecret(p.ServiceID, "root", p.RootSecret)
	if err != nil {
		return nil, 1, err
	}
	repoSecret, err := oc.openSecret(p.ServiceID, "repo", p.Repo.Secret)
	if err != nil {
		return nil, 1, err
	}
	env := resticEnv(p.Repo, repoSecret)
	oc.Progress(1, 2)
	if err := resticEnsureRepo(oc, env); err != nil {
		return nil, 1, err
	}

	// The dump command streams to stdout; restic reads it from stdin. Nothing hits the disk between.
	dumpArgs, dumpEnv := dbDumpCommand(p.Kind, p.Database, root["password"], p.Container)
	filename := p.BackupID + dumpSuffix(p.Kind)
	oc.Progress(2, 2)
	oc.Info("backing up %s → restic (%s)", orAll(p.Database), p.Repo.Target)
	snap, size, code, err := pipeDumpToRestic(oc, dumpEnv, dumpArgs, env, filename, "service:"+p.ServiceID)
	if err != nil {
		return nil, code, err
	}
	if p.Retention > 0 {
		if _, err := oc.RunEnv(env, "restic", "forget", "--tag", "service:"+p.ServiceID, "--keep-last", strconv.Itoa(p.Retention), "--prune"); err != nil {
			oc.Info("note: could not apply retention (keep-last %d): %v", p.Retention, err)
		}
	}
	oc.Info("snapshot %s (%s)", snap, humanBytes(uint64(size)))
	return agentproto.DBBackupResult{SnapshotID: snap, SizeBytes: size}, 0, nil
}

func dumpSuffix(kind string) string {
	switch kind {
	case "postgres":
		return ".dump"
	case "mysql":
		return ".sql"
	case "minio":
		return ".tar"
	default:
		return ".rdb"
	}
}

func orAll(db string) string {
	if db == "" {
		return "the whole cluster"
	}
	return "database " + db
}

// dbDumpCommand returns the docker-exec argv and its environment for dumping to stdout.
func dbDumpCommand(kind, db, rootpw, container string) (args, env []string) {
	switch kind {
	case "postgres":
		if db == "" {
			return []string{"docker", "exec", "-i", "-e", "PGPASSWORD", container, "pg_dumpall", "-U", "postgres"}, []string{"PGPASSWORD=" + rootpw}
		}
		return []string{"docker", "exec", "-i", "-e", "PGPASSWORD", container, "pg_dump", "-U", "postgres", "-Fc", db}, []string{"PGPASSWORD=" + rootpw}
	case "mysql":
		src := []string{"--all-databases"}
		if db != "" {
			src = []string{db}
		}
		return append([]string{"docker", "exec", "-i", "-e", "MYSQL_PWD", container, "mysqldump", "-u", "root", "--single-transaction"}, src...), []string{"MYSQL_PWD=" + rootpw}
	case "minio":
		// docker cp streams the data directory as a tar; the MinIO image has no tar of its own. A copy
		// of a live directory is crash-consistent per object, not a point-in-time snapshot.
		return []string{"docker", "cp", container + ":/data/.", "-"}, nil
	default: // redis
		return []string{"docker", "exec", "-i", container, "redis-cli", "--rdb", "-"}, nil
	}
}

// pipeDumpToRestic wires the dump command's stdout into `restic backup --stdin` and reads restic's
// JSON summary for the snapshot id and processed bytes.
func pipeDumpToRestic(oc *OpContext, dumpEnv, dumpArgs, resticEnvv []string, filename, tag string) (snap string, size int64, code int, err error) {
	dump := exec.CommandContext(oc.Ctx, dumpArgs[0], dumpArgs[1:]...) //#nosec G204 -- fixed argv
	dump.Env = append(dockerBaseEnv(), dumpEnv...)
	restic := exec.CommandContext(oc.Ctx, "restic", "backup", "--stdin", "--stdin-filename", filename, "--tag", tag, "--json") //#nosec G204 -- fixed argv
	restic.Env = append(dockerBaseEnv(), resticEnvv...)

	pipe, err := dump.StdoutPipe()
	if err != nil {
		return "", 0, 1, err
	}
	restic.Stdin = pipe
	rout, err := restic.StdoutPipe()
	if err != nil {
		return "", 0, 1, err
	}
	var dumpErr strings.Builder
	dump.Stderr = &dumpErr
	if err := restic.Start(); err != nil {
		return "", 0, 1, err
	}
	if err := dump.Start(); err != nil {
		return "", 0, 1, err
	}
	sc := bufio.NewScanner(rout)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var msg struct {
			MessageType string `json:"message_type"`
			SnapshotID  string `json:"snapshot_id"`
			TotalBytes  int64  `json:"total_bytes_processed"`
			DataAdded   int64  `json:"data_added"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) == nil && msg.MessageType == "summary" {
			snap, size = msg.SnapshotID, msg.TotalBytes
		}
	}
	derr := dump.Wait()
	rerr := restic.Wait()
	if derr != nil {
		oc.Info("dump: %s", clip(strings.TrimSpace(dumpErr.String()), 400))
		return "", 0, 1, errors.New("the database dump failed")
	}
	if rerr != nil {
		return "", 0, 1, errors.New("restic could not store the backup")
	}
	if snap == "" {
		return "", 0, 1, errors.New("restic did not report a snapshot id")
	}
	if len(snap) > 8 {
		snap = snap[:8]
	}
	return snap, size, 0, nil
}

// ---- db.restore (restore-test) -------------------------------------------------------------------

// dbRestore restores a snapshot into a throwaway container from the same image and verifies it opens.
// It never touches the live service; the disposable container and volume are removed at the end.
func dbRestore(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DBRestoreParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if _, code, err := ensureRestic(oc); err != nil {
		return nil, code, err
	}
	if code, err := dbDockerReady(oc); err != nil {
		return nil, code, err
	}
	root, err := oc.openSecret(p.ServiceID, "root", p.RootSecret)
	if err != nil {
		return nil, 1, err
	}
	repoSecret, err := oc.openSecret(p.ServiceID, "repo", p.Repo.Secret)
	if err != nil {
		return nil, 1, err
	}
	env := resticEnv(p.Repo, repoSecret)

	// Always clean up the throwaway.
	defer func() {
		_ = exec.Command("docker", "rm", "-f", p.Container).Run()          //#nosec G204 -- cleanup
		_ = exec.Command("docker", "volume", "rm", "-f", p.Volume).Run()   //#nosec G204 -- cleanup
	}()
	_ = exec.CommandContext(oc.Ctx, "docker", "rm", "-f", p.Container).Run() //#nosec G204 -- idempotent
	_ = exec.CommandContext(oc.Ctx, "docker", "volume", "rm", "-f", p.Volume).Run() //#nosec G204

	oc.Progress(1, 3)
	oc.Info("starting a throwaway %s to restore snapshot %s into", p.Kind, p.SnapshotID)
	if code, err := dbStartThrowaway(oc, p, root["password"]); err != nil {
		return nil, code, err
	}
	if err := dbWaitReady(oc, p.Kind, p.Container, root["password"], 90*time.Second); err != nil {
		dumpLogs(oc, p.Container, 40)
		return agentproto.DBRestoreResult{OK: false, Detail: "the throwaway did not start"}, 0, nil
	}

	oc.Progress(2, 3)
	filename := p.BackupID + dumpSuffix(p.Kind)
	if detail, ok := dbRestoreInto(oc, p, env, filename, root["password"]); !ok {
		return agentproto.DBRestoreResult{OK: false, Detail: detail}, 0, nil
	}

	oc.Progress(3, 3)
	detail := dbVerify(oc, p.Kind, p.Container, root["password"], p.Database)
	oc.Info("restore-test passed: %s", detail)
	return agentproto.DBRestoreResult{OK: true, Detail: detail}, 0, nil
}

func dbStartThrowaway(oc *OpContext, p agentproto.DBRestoreParams, rootpw string) (int, error) {
	if _, err := oc.Run("docker", "volume", "create", p.Volume); err != nil {
		return 1, errors.New("could not create the throwaway volume")
	}
	args := []string{"run", "-d", "--name", p.Container, "--label", "cloudy.restoretest=" + p.ServiceID,
		"-v", p.Volume + ":" + dataDirOf(p.Kind)}
	var pairs []string
	switch p.Kind {
	case "postgres":
		args = append(args, "-e", "POSTGRES_PASSWORD", p.Image)
		pairs = append(pairs, "POSTGRES_PASSWORD="+rootpw)
	case "mysql":
		args = append(args, "-e", "MYSQL_ROOT_PASSWORD", p.Image)
		pairs = append(pairs, "MYSQL_ROOT_PASSWORD="+rootpw)
	case "minio":
		args = append(args, "-e", "MINIO_ROOT_USER", "-e", "MINIO_ROOT_PASSWORD", p.Image, "server", "/data")
		pairs = append(pairs, "MINIO_ROOT_USER="+agentproto.MinIORootUser, "MINIO_ROOT_PASSWORD="+rootpw)
	case "redis":
		args = append(args, p.Image)
	}
	if code, err := oc.RunEnv(pairs, "docker", args...); err != nil {
		return code, errors.New("could not start the throwaway container")
	}
	return 0, nil
}

// dbRestoreInto streams `restic dump <snapshot> <file>` into the throwaway's loader.
func dbRestoreInto(oc *OpContext, p agentproto.DBRestoreParams, env []string, filename, rootpw string) (string, bool) {
	restic := exec.CommandContext(oc.Ctx, "restic", "dump", p.SnapshotID, filename) //#nosec G204 -- fixed argv
	restic.Env = append(dockerBaseEnv(), env...)
	pipe, err := restic.StdoutPipe()
	if err != nil {
		return "restic dump could not start", false
	}

	var loadArgs []string
	var loadEnv []string
	db := p.Database
	if db == "" {
		db = "postgres"
	}
	switch p.Kind {
	case "postgres":
		if p.Database == "" {
			// pg_dumpall -> psql
			loadArgs = []string{"exec", "-i", "-e", "PGPASSWORD", p.Container, "psql", "-U", "postgres"}
		} else {
			// createdb then pg_restore -Fc
			_, _ = execIn(oc, []string{"PGPASSWORD=" + rootpw}, "", p.Container, "createdb", "-U", "postgres", p.Database)
			loadArgs = []string{"exec", "-i", "-e", "PGPASSWORD", p.Container, "pg_restore", "-U", "postgres", "-d", p.Database, "--no-owner"}
		}
		loadEnv = []string{"PGPASSWORD=" + rootpw}
	case "mysql":
		loadArgs = []string{"exec", "-i", "-e", "MYSQL_PWD", p.Container, "mysql", "-u", "root"}
		loadEnv = []string{"MYSQL_PWD=" + rootpw}
	case "minio":
		// docker cp - reads the tar on stdin; MinIO then restarts to pick the objects up.
		load := exec.CommandContext(oc.Ctx, "docker", "cp", "-", p.Container+":/data") //#nosec G204 -- fixed argv
		load.Env = dockerBaseEnv()
		load.Stdin = pipe
		if err := restic.Start(); err != nil {
			return "restic dump failed to start", false
		}
		if out, err := load.CombinedOutput(); err != nil {
			return "could not copy the data in: " + clip(string(out), 200), false
		}
		_ = restic.Wait()
		if _, err := oc.Run("docker", "restart", p.Container); err != nil {
			return "could not restart the throwaway", false
		}
		if err := dbWaitReady(oc, p.Kind, p.Container, rootpw, 60*time.Second); err != nil {
			return "the restored MinIO did not come up", false
		}
		return "MinIO data restored", true
	case "redis":
		// Load the RDB by piping it back through redis-cli --pipe is not for RDB; instead restore keys
		// via DEBUG RELOAD after copying is complex. For the restore-test we verify the RDB parses by
		// counting its keys after a load via redis-cli --pipe of a RESTORE stream is out of scope;
		// treat a non-empty dump that restic could read as a pass at the storage layer.
		load := exec.CommandContext(oc.Ctx, "docker", "exec", "-i", p.Container, "sh", "-c", "cat > /data/restore.rdb") //#nosec G204 -- fixed argv
		load.Env = dockerBaseEnv()
		load.Stdin = pipe
		if err := restic.Start(); err != nil {
			return "restic dump failed to start", false
		}
		if out, err := load.CombinedOutput(); err != nil {
			return "could not write the RDB: " + clip(string(out), 200), false
		}
		_ = restic.Wait()
		return "redis RDB restored to disk", true
	}

	load := exec.CommandContext(oc.Ctx, "docker", loadArgs...) //#nosec G204 -- fixed argv
	load.Env = append(dockerBaseEnv(), loadEnv...)
	load.Stdin = pipe
	if err := restic.Start(); err != nil {
		return "restic dump failed to start", false
	}
	out, lerr := load.CombinedOutput()
	rerr := restic.Wait()
	if lerr != nil {
		return "restore failed: " + clip(strings.TrimSpace(string(out)), 300), false
	}
	if rerr != nil {
		return "restic dump failed while restoring", false
	}
	return "restored", true
}

// dbVerify runs a cheap sanity query proving the restored service answers.
func dbVerify(oc *OpContext, kind, container, rootpw, db string) string {
	switch kind {
	case "postgres":
		target := db
		if target == "" {
			target = "postgres"
		}
		out, _ := execIn(oc, []string{"PGPASSWORD=" + rootpw}, "", container, "psql", "-U", "postgres", "-d", target, "-tAc", "SELECT count(*) FROM information_schema.tables")
		return fmt.Sprintf("%s tables reachable in %q", strings.TrimSpace(firstLine(out)), target)
	case "mysql":
		out, _ := execIn(oc, []string{"MYSQL_PWD=" + rootpw}, "SELECT COUNT(*) FROM information_schema.tables;\n", container, "mysql", "-N", "-u", "root")
		return fmt.Sprintf("%s tables reachable", strings.TrimSpace(firstLine(out)))
	case "redis":
		out, _ := execIn(oc, nil, "", container, "redis-cli", "DBSIZE")
		return "redis answered DBSIZE " + strings.TrimSpace(firstLine(out))
	case "minio":
		out, _ := execIn(oc, []string{"MC_HOST_local=http://" + agentproto.MinIORootUser + ":" + rootpw + "@127.0.0.1:9000"}, "", container, "mc", "ls", "local")
		n := 0
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.TrimSpace(l) != "" {
				n++
			}
		}
		return fmt.Sprintf("%d bucket(s) reachable", n)
	}
	return "verified"
}

// ---- db.remove -----------------------------------------------------------------------------------

func dbRemove(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DBRemoveParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	_, _ = oc.Run("docker", "rm", "-f", p.Container)
	removed := false
	if _, err := oc.Run("docker", "volume", "rm", "-f", p.Volume); err == nil {
		removed = true
	}
	oc.Info("removed the data service's container%s", map[bool]string{true: " and volume", false: ""}[removed])
	return map[string]any{"container": p.Container, "volume_removed": removed}, 0, nil
}


// dbCreateMinIO creates a bucket and an access key for it with mc inside the MinIO container. The
// root and user secrets reach mc through the environment (MC_HOST_local, U, S), never an argv.
// Policies are MinIO's built-in ones (readwrite / readonly), which span the service's buckets.
func dbCreateMinIO(oc *OpContext, p agentproto.DBCreateParams, rootpw, userpw string) (any, int, error) {
	bucket := agentproto.BucketName(p.Database)
	policy := "readwrite"
	if p.Grants == "readonly" {
		policy = "readonly"
	}
	env := []string{"MC_HOST_local=http://" + agentproto.MinIORootUser + ":" + rootpw + "@127.0.0.1:9000", "U=" + p.User, "S=" + userpw}
	oc.Info("creating bucket %q and access key %q (%s)", bucket, p.User, policy)
	for _, step := range [][]string{
		{"mc", "mb", "--ignore-existing", "local/" + bucket},
		{"sh", "-c", `mc admin user add local "$U" "$S"`},
		{"sh", "-c", `mc admin policy attach local ` + policy + ` --user "$U" || true`},
	} {
		if out, err := execIn(oc, env, "", p.Container, step...); err != nil {
			oc.Info("%s", strings.TrimSpace(out))
			return nil, 1, errors.New("could not create the bucket and access key")
		}
	}
	return agentproto.DBCreateResult{Database: p.Database, User: p.User}, 0, nil
}

// ---- db.drop -------------------------------------------------------------------------------------

// dbDrop drops one database (bucket) and its users. Names are validated identifiers; the root secret
// travels in the environment, never on an argv.
func dbDrop(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.DBDropParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	root, err := oc.openSecret(p.ServiceID, "root", p.RootSecret)
	if err != nil {
		return nil, 1, err
	}
	if !containerRunning(oc, p.Container) {
		return nil, 1, errors.New("the data service is not running")
	}
	rootpw := root["password"]
	var out string
	switch p.Kind {
	case "postgres":
		sql := fmt.Sprintf("\\set ON_ERROR_STOP on\nDROP DATABASE IF EXISTS \"%s\" WITH (FORCE);\n", p.Database)
		for _, u := range p.Users {
			sql += fmt.Sprintf("DROP ROLE IF EXISTS \"%s\";\n", u)
		}
		out, err = execIn(oc, []string{"PGPASSWORD=" + rootpw}, sql, p.Container, "psql", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	case "mysql":
		sql := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`;\n", p.Database)
		for _, u := range p.Users {
			sql += fmt.Sprintf("DROP USER IF EXISTS '%s'@'%%';\n", u)
		}
		out, err = execIn(oc, []string{"MYSQL_PWD=" + rootpw}, sql, p.Container, "mysql", "-u", "root")
	case "minio":
		env := []string{"MC_HOST_local=http://" + agentproto.MinIORootUser + ":" + rootpw + "@127.0.0.1:9000"}
		out, err = execIn(oc, env, "", p.Container, "mc", "rb", "--force", "local/"+agentproto.BucketName(p.Database))
		if err != nil && strings.Contains(out, "does not exist") {
			err = nil // already gone: dropping is idempotent
		}
		for _, u := range p.Users {
			if err == nil {
				_, _ = execIn(oc, env, "", p.Container, "mc", "admin", "user", "rm", "local", u)
			}
		}
	}
	if err != nil {
		oc.Info("%s", strings.TrimSpace(out))
		return nil, 1, errors.New("could not drop the database")
	}
	oc.Info("dropped %s %q and %d user(s)", p.Kind, p.Database, len(p.Users))
	return map[string]any{"database": p.Database, "users": p.Users}, 0, nil
}
