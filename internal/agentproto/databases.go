package agentproto

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// P3 operation kinds: managed data services (Postgres/MySQL/Redis) and restic backups. Typed like
// everything else — each has fixed params below, and nothing in them reaches a host shell: the agent
// turns them into fixed argv lists (docker run / docker exec / restic), and the only strings a user
// influences (a database or user name) are validated to a strict identifier before they are used.
const (
	KindDBProvision   = "db.provision"    // run a Postgres/MySQL/Redis container with a named volume, bound to a private address
	KindDBCreate      = "db.create"       // create a database + user + grants inside a running service
	KindDBBackup      = "db.backup"       // pg_dump -Fc / mysqldump / Redis RDB, pushed into a restic repository
	KindDBRestore     = "db.restore"      // restore a snapshot into a throwaway container and verify it (restore-test)
	KindDBRemove      = "db.remove"       // stop and remove a service's container and its volume (the service was deleted)
	KindDBDrop        = "db.drop"         // drop one database (bucket) and its users inside a running service
	KindDBUser        = "db.user"         // create, rotate, delete, grant or revoke a user's password/grants (MH-574 F1)
	KindDBQuery       = "db.query"        // table browser and SQL console: tables/columns/rows/sql (MH-574 F2)
	KindDBInsights    = "db.insights"     // metrics and slow queries for postgres/mysql (MH-575 F6)
	KindDBCancelQuery = "db.cancel_query" // pg_cancel_backend / KILL QUERY (MH-575 F6)
	KindDBImport      = "db.import"       // pg_dump | pg_restore an external Postgres database into a managed one
)

// StepRestic is the provisioning step that installs the pinned restic release (checksum-verified),
// added to P1/P2's idempotent steps.
const StepRestic = "restic.install"

// DBSecretContext binds a db secret box to a service and a purpose: "root", "user:<name>" or "repo".
// A box sealed for one purpose does not open as another (it is the AEAD additional data).
func DBSecretContext(serviceID, purpose string) string { return "db:" + serviceID + ":" + purpose }

var (
	dbNameRE    = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	dbVersionRE = regexp.MustCompile(`^[0-9][0-9A-Za-z._-]{0,31}$`)
	// pgvectorVersionRE is a Postgres version of the form "pgvector-<tag>", e.g. "pgvector-pg17" or
	// "pgvector-0.8.0-pg17" — <tag> is the tag on Docker Hub's official pgvector/pgvector image,
	// which is Postgres plus the vector extension built in (same entrypoint, env and data dir as the
	// postgres image), so every other postgres feature (create, users, backups, restore-test,
	// insights, query) keeps working unchanged.
	pgvectorVersionRE = regexp.MustCompile(`^pgvector-[0-9A-Za-z][0-9A-Za-z._-]{0,40}$`)
	// pgMajorRE picks the Postgres major version out of a pgvector tag ("pg17" in "pgvector-pg17" or
	// "pgvector-0.8.0-pg17").
	pgMajorRE  = regexp.MustCompile(`pg(\d{1,3})`)
	snapshotRE = regexp.MustCompile(`^[0-9a-f]{8,64}$`)
)

// ValidDBKind: the managed engines. minio is S3-compatible object storage (buckets).
func ValidDBKind(s string) bool {
	return s == "postgres" || s == "mysql" || s == "redis" || s == "minio"
}

// BucketName maps a database name (lower snake) to an S3 bucket name (no underscores allowed).
func BucketName(db string) string { return strings.ReplaceAll(db, "_", "-") }

// MinIORootUser is the fixed root access key of a Hosbah MinIO service (the secret is generated).
// It keeps its pre-rename value on purpose: it is a credential. Every MinIO service created before
// the rename was started with it, and every mc call the agent makes logs in with it, so a new value
// would lock the agent out of those services. It never names anything a person sees.
const MinIORootUser = "cloudy-root"

// ValidDBName: a database or role identifier — lower snake, so it is safe verbatim in the fixed argv
// (createdb / CREATE DATABASE / CREATE USER) the agent builds. No quoting games, no keywords slipping
// through: the same class the schema's CHECK constraints enforce.
func ValidDBName(s string) bool { return dbNameRE.MatchString(s) }

// ValidDBVersion: a container image tag like "16", "8.4", "7.2-alpine", or (postgres only, checked
// by DBImage/Validate against the kind) a pgvector form like "pgvector-pg17".
func ValidDBVersion(s string) bool { return dbVersionRE.MatchString(s) || pgvectorVersionRE.MatchString(s) }

// IsPgvectorVersion reports whether a postgres Version names the pgvector image (Docker Hub's
// pgvector/pgvector, Postgres plus the vector extension built in) rather than the plain postgres one.
func IsPgvectorVersion(s string) bool { return pgvectorVersionRE.MatchString(s) }

// PGMajorVersion reads the Postgres major version out of a postgres Version string, plain
// ("16" -> 16, "16.4" -> 16) or pgvector ("pgvector-pg17" -> 17, "pgvector-0.8.0-pg17" -> 17).
// Used wherever a major version decides something (a restore-test's throwaway image, a future
// upgrade path, a version badge) so the pgvector form does not need its own parsing at each site.
func PGMajorVersion(version string) (int, bool) {
	if IsPgvectorVersion(version) {
		m := pgMajorRE.FindStringSubmatch(version)
		if m == nil {
			return 0, false
		}
		n, err := strconv.Atoi(m[1])
		return n, err == nil
	}
	i := 0
	for i < len(version) && version[i] >= '0' && version[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(version[:i])
	return n, err == nil
}

// ValidGrants: the grant level a user gets on its database.
func ValidGrants(s string) bool { return s == "all" || s == "readwrite" || s == "readonly" }

// DBContainerName / DBVolumeName / DBThrowawayName name a service's docker objects deterministically
// from its slug (a service name, validated by ValidAppName), so a re-run finds the same container.
// A service created before the rename has cloudy-db-<name> / cloudy-dbdata-<name>; the agent keeps
// using those (ResolveName), and the validation below accepts either form.
func DBContainerName(name string) string { return NamePrefix + "db-" + name }
func DBVolumeName(name string) string    { return NamePrefix + "dbdata-" + name }

// DBThrowawayName is the restore-test's disposable container/volume, named from the backup id.
func DBThrowawayName(backupID string) string {
	short := backupID
	if len(short) > 8 {
		short = short[:8]
	}
	return NamePrefix + "dbrestore-" + short
}

// DBInternalPort is the port the engine listens on inside its container.
func DBInternalPort(kind string) int {
	switch kind {
	case "postgres":
		return 5432
	case "mysql":
		return 3306
	case "redis":
		return 6379
	case "minio":
		return 9000
	}
	return 0
}

// DBImage is the pinned Docker Hub image for a kind and version. The control plane passes it in the
// params (so the signature covers the exact image), and the agent checks it matches this mapping.
func DBImage(kind, version string) string {
	switch kind {
	case "postgres":
		if IsPgvectorVersion(version) {
			return "pgvector/pgvector:" + strings.TrimPrefix(version, "pgvector-")
		}
		return "postgres:" + version
	case "mysql":
		return "mysql:" + version
	case "redis":
		return "redis:" + version
	case "minio":
		return "quay.io/minio/minio:RELEASE." + version // Docker Hub's minio/minio is no longer published
	}
	return ""
}

// ---- imported-guest exec target ---------------------------------------------------------------
//
// db.create, db.drop, db.user, db.query, db.insights and db.cancel_query all run an engine's own
// client against a running service. For an imported service that is a Docker container, that is
// `docker exec <container> …`, exactly as for a Hosbah-managed one. For an imported service that is a
// Proxmox LXC guest, there is no docker on the hypervisor side to reach into — the agent instead runs
// `pct exec <vmid> -- …` on the node, against the engine installed natively in the guest (peer auth for
// Postgres, auth_socket for MySQL/MariaDB — see internal/agent/dbtarget.go), so no stored credential is
// even needed, let alone one that could travel on the argv. A guest that itself runs the engine in
// Docker is not supported: pct exec has no flag to carry an environment variable into the nested
// `docker exec`, so a password given that way would arrive empty — Container must be "" for a guest
// target. A QEMU guest has no target at all: there is no client to run inside one without its guest
// agent (a possible future addition), so GuestType only ever accepts "lxc" here — refused with a
// precise message on both sides of the wire before any operation is even built.

// validDBExecTarget checks the (target, vmid, guest_type, container) tuple these six params share.
func validDBExecTarget(target, vmid, guestType, container string) error {
	switch target {
	case "", TargetContainer:
		if !ValidContainerName(container) {
			return fmt.Errorf("bad container %q", container)
		}
	case TargetGuest:
		if !ValidVMID(vmid) {
			return errors.New("a guest needs a vmid")
		}
		if guestType != "lxc" {
			return errors.New("only a Proxmox LXC guest can run a database client — a QEMU VM has no client without its guest agent, not supported yet")
		}
		if container != "" {
			return errors.New("a database in Docker inside a guest is not supported yet")
		}
	default:
		return errors.New("target is container or guest")
	}
	return nil
}

// rootSecretRequired reports whether Validate must refuse an empty RootSecret. Every target does
// except a native Proxmox LXC guest (target guest, container "" — validDBExecTarget refuses any other
// container there): the agent authenticates as the matching OS user instead (Postgres peer auth,
// MySQL/MariaDB auth_socket), so an imported guest with no stored credential at all still works.
func rootSecretRequired(target, container string) bool {
	return !(target == TargetGuest && container == "")
}

// RepoSpec is where a backup's restic repository lives. Target 'local' is a filesystem repo on the
// server (the demo default); 'minio'/'s3' is an S3 endpoint. Secret is a box, context
// DBSecretContext(service, "repo"), carrying RESTIC_PASSWORD and, for minio/s3, AWS_ACCESS_KEY_ID /
// AWS_SECRET_ACCESS_KEY. The plaintext repo location never contains a credential.
type RepoSpec struct {
	Target string `json:"target"`
	Repo   string `json:"repo"`   // local: an absolute path; minio/s3: s3:<endpoint>/<bucket>/<prefix>
	Secret string `json:"secret"` // env box
}

func (r RepoSpec) Validate() error {
	switch r.Target {
	case "local":
		if !InWorkDir(r.Repo) {
			return errors.New("a local restic repository must be under " + WorkDir)
		}
	case "minio", "s3":
		if !strings.HasPrefix(r.Repo, "s3:") {
			return errors.New("an s3 restic repository must be s3:<endpoint>/<bucket>/<prefix>")
		}
	default:
		return fmt.Errorf("unknown backup target %q", r.Target)
	}
	return nil
}

// DBProvisionParams: db.provision. Bind is the address the port is published on — the server's
// private interface (never 0.0.0.0), so the service is reachable by co-located app containers but not
// from the internet. RootSecret is a box for DBSecretContext(ServiceID,"root"): {"password": ...}.
type DBProvisionParams struct {
	ServiceID  string `json:"service_id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Version    string `json:"version"`
	Image      string `json:"image"`
	Container  string `json:"container"`
	Volume     string `json:"volume"`
	Bind       string `json:"bind"`
	Port       int    `json:"port"`
	RootSecret string `json:"root_secret"`
	// Resources (MH-574 E13): memory/CPU caps for the service's container. Zero value = unlimited.
	Resources ResourceLimits `json:"resources,omitempty"`
}

func (p DBProvisionParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID):
		return errors.New("bad service id")
	case !ValidAppName(p.Name):
		return fmt.Errorf("bad service name %q", p.Name)
	case !ValidDBKind(p.Kind) || !ValidDBVersion(p.Version):
		return errors.New("bad kind or version")
	case p.Image != DBImage(p.Kind, p.Version):
		return fmt.Errorf("image %q is not the pinned image for %s:%s", p.Image, p.Kind, p.Version)
	case !sameDerived(p.Container, DBContainerName(p.Name)) || !sameDerived(p.Volume, DBVolumeName(p.Name)):
		return errors.New("container or volume name does not match the service")
	case p.Bind == "" || p.Bind == "0.0.0.0" || p.Port < 1 || p.Port > 65535:
		return errors.New("a data service binds to a private address and a port")
	case p.Kind != "redis" && p.RootSecret == "":
		return errors.New("postgres and mysql need a root secret")
	}
	return p.Resources.Validate()
}

// DBProvisionResult reports the private endpoint the control plane records on the service.
type DBProvisionResult struct {
	Container string `json:"container"`
	Volume    string `json:"volume"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Version   string `json:"version,omitempty"`
}

// DBCreateParams: db.create. Database and User are strict identifiers. UserSecret is a box for
// DBSecretContext(ServiceID,"user:<User>"): {"password": ...}. Redis has neither database nor user,
// so db.create is refused for it.
type DBCreateParams struct {
	ServiceID  string `json:"service_id"`
	Kind       string `json:"kind"`
	Container  string `json:"container"`
	Target     string `json:"target,omitempty"`     // container | guest ("" = container, back-compat)
	VMID       string `json:"vmid,omitempty"`       // guest
	GuestType  string `json:"guest_type,omitempty"` // guest: lxc only
	Database   string `json:"database"`
	User       string `json:"user"`
	Grants     string `json:"grants"`
	RootSecret string `json:"root_secret"`
	UserSecret string `json:"user_secret"`
}

func (p DBCreateParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID):
		return errors.New("bad service id")
	case p.Kind != "postgres" && p.Kind != "mysql" && p.Kind != "minio":
		return errors.New("databases (buckets) and users apply to postgres, mysql and minio")
	case p.Target == TargetGuest && p.Kind == "minio":
		return errors.New("minio buckets in a Proxmox guest are not supported yet")
	case !ValidDBName(p.Database):
		return fmt.Errorf("bad database name %q", p.Database)
	case !ValidDBName(p.User):
		return fmt.Errorf("bad user name %q", p.User)
	case !ValidGrants(p.Grants):
		return fmt.Errorf("bad grants %q", p.Grants)
	case p.UserSecret == "":
		return errors.New("db.create needs the user secret")
	case rootSecretRequired(p.Target, p.Container) && p.RootSecret == "":
		return errors.New("db.create needs the root secret")
	}
	return validDBExecTarget(p.Target, p.VMID, p.GuestType, p.Container)
}

// DBCreateResult: the database and user created.
type DBCreateResult struct {
	Database string `json:"database"`
	User     string `json:"user"`
}

// DBBackupParams: db.backup. Database "" means the whole cluster (pg_dumpall / all databases /
// the Redis RDB). BackupID names the restic snapshot's file so a restore can find it.
type DBBackupParams struct {
	ServiceID  string   `json:"service_id"`
	Kind       string   `json:"kind"`
	Container  string   `json:"container"`
	BackupID   string   `json:"backup_id"`
	Database   string   `json:"database,omitempty"`
	Repo       RepoSpec `json:"repo"`
	Retention  int      `json:"retention,omitempty"` // keep the newest N snapshots of this service (0 = keep all)
	RootSecret string   `json:"root_secret"`
}

func (p DBBackupParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID) || !ValidID(p.BackupID):
		return errors.New("bad service or backup id")
	case !ValidDBKind(p.Kind):
		return errors.New("bad kind")
	case p.Database != "" && !ValidDBName(p.Database):
		return fmt.Errorf("bad database name %q", p.Database)
	case p.Kind != "redis" && p.RootSecret == "":
		return errors.New("postgres and mysql need the root secret to back up")
	}
	return p.Repo.Validate()
}

// DBBackupResult: restic's snapshot id and the dump size.
type DBBackupResult struct {
	SnapshotID string `json:"snapshot_id"`
	SizeBytes  int64  `json:"size_bytes"`
}

// DBRestoreParams: db.restore — restore a snapshot into a fresh throwaway container from the same
// image and verify it opens. Destructive only in that it writes a disposable container/volume, which
// it removes afterwards; it never touches the live service.
type DBRestoreParams struct {
	ServiceID  string   `json:"service_id"`
	Kind       string   `json:"kind"`
	Version    string   `json:"version"`
	Image      string   `json:"image"`
	BackupID   string   `json:"backup_id"`
	SnapshotID string   `json:"snapshot_id"`
	Database   string   `json:"database,omitempty"`
	Container  string   `json:"container"`
	Volume     string   `json:"volume"`
	Repo       RepoSpec `json:"repo"`
	RootSecret string   `json:"root_secret"`
}

func (p DBRestoreParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID) || !ValidID(p.BackupID):
		return errors.New("bad service or backup id")
	case !ValidDBKind(p.Kind) || !ValidDBVersion(p.Version):
		return errors.New("bad kind or version")
	case p.Image != DBImage(p.Kind, p.Version):
		return errors.New("image does not match the service kind and version")
	case !snapshotRE.MatchString(p.SnapshotID):
		return errors.New("bad snapshot id")
	case !sameDerived(p.Container, DBThrowawayName(p.BackupID)):
		return errors.New("the restore container must be the throwaway for this backup")
	case p.Kind != "redis" && p.RootSecret == "":
		return errors.New("postgres and mysql need a root secret for the throwaway")
	}
	return p.Repo.Validate()
}

// DBRestoreResult: whether the snapshot restored and opened cleanly.
type DBRestoreResult struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// DBRemoveParams: db.remove.
type DBRemoveParams struct {
	ServiceID string `json:"service_id"`
	Container string `json:"container"`
	Volume    string `json:"volume"`
}

func (p DBRemoveParams) Validate() error {
	if !ValidID(p.ServiceID) || p.Container == "" || p.Volume == "" {
		return errors.New("bad remove params")
	}
	return nil
}

// ValidDBUserAction: the actions db.user supports.
func ValidDBUserAction(s string) bool {
	switch s {
	case "create", "rotate", "delete", "grant", "revoke":
		return true
	}
	return false
}

// DBUserParams: db.user — a standalone user lifecycle action on a running Postgres/MySQL/MinIO
// service, independent of CreateDatabaseAndUser's combined database+user flow (MH-574 F1):
//
//   - create: adds a login/role/access-key (Database is the one it is granted on; Grants is the preset).
//   - rotate: changes the password/secret key of an existing user; UserSecret carries the new one.
//   - delete: drops the login/role/access-key. Never the service's own root credential (that has no
//     database_users row — the control plane refuses before this op is even built).
//   - grant / revoke: sets or removes the user's privileges on Database (revoke needs no Grants; it
//     always removes everything the preset levels would have given).
//
// RootSecret is always required (the agent acts as the service administrator). UserSecret is the
// user's own new secret, required for create and rotate. Database is required for create/grant/revoke
// (a user's privileges are always scoped to one database/bucket); rotate and delete act on the user
// only and leave it empty.
type DBUserParams struct {
	ServiceID  string `json:"service_id"`
	Kind       string `json:"kind"`
	Container  string `json:"container"`
	Target     string `json:"target,omitempty"`     // container | guest ("" = container, back-compat)
	VMID       string `json:"vmid,omitempty"`       // guest
	GuestType  string `json:"guest_type,omitempty"` // guest: lxc only
	Action     string `json:"action"`
	Database   string `json:"database,omitempty"`
	User       string `json:"user"`
	Grants     string `json:"grants,omitempty"`
	RootSecret string `json:"root_secret"`
	UserSecret string `json:"user_secret,omitempty"`
}

func (p DBUserParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID):
		return errors.New("bad service id")
	case p.Kind != "postgres" && p.Kind != "mysql" && p.Kind != "minio":
		return errors.New("users apply to postgres, mysql and minio")
	case p.Target == TargetGuest && p.Kind == "minio":
		return errors.New("minio buckets in a Proxmox guest are not supported yet")
	case !ValidDBUserAction(p.Action):
		return fmt.Errorf("bad action %q", p.Action)
	case !ValidDBName(p.User):
		return fmt.Errorf("bad user name %q", p.User)
	case validDBExecTarget(p.Target, p.VMID, p.GuestType, p.Container) != nil:
		return validDBExecTarget(p.Target, p.VMID, p.GuestType, p.Container)
	case rootSecretRequired(p.Target, p.Container) && p.RootSecret == "":
		return errors.New("db.user needs the root secret")
	}
	switch p.Action {
	case "create":
		if !ValidDBName(p.Database) {
			return fmt.Errorf("bad database name %q", p.Database)
		}
		if !ValidGrants(p.Grants) {
			return fmt.Errorf("bad grants %q", p.Grants)
		}
		if p.UserSecret == "" {
			return errors.New("db.user create needs the user secret")
		}
	case "rotate":
		if p.UserSecret == "" {
			return errors.New("db.user rotate needs the user secret")
		}
	case "grant":
		if !ValidDBName(p.Database) {
			return fmt.Errorf("bad database name %q", p.Database)
		}
		if !ValidGrants(p.Grants) {
			return fmt.Errorf("bad grants %q", p.Grants)
		}
	case "revoke":
		if !ValidDBName(p.Database) {
			return fmt.Errorf("bad database name %q", p.Database)
		}
	case "delete":
		// no extra fields
	}
	return nil
}

// DBUserResult: db.user's answer — nothing sensitive.
type DBUserResult struct {
	User   string `json:"user"`
	Action string `json:"action"`
}

// DBDropParams: db.drop — drop one database (a MinIO bucket) and the users created for it. Idempotent:
// dropping what is already gone succeeds.
type DBDropParams struct {
	ServiceID  string   `json:"service_id"`
	Kind       string   `json:"kind"`
	Container  string   `json:"container"`
	Target     string   `json:"target,omitempty"`     // container | guest ("" = container, back-compat)
	VMID       string   `json:"vmid,omitempty"`       // guest
	GuestType  string   `json:"guest_type,omitempty"` // guest: lxc only
	Database   string   `json:"database"`
	Users      []string `json:"users,omitempty"`
	RootSecret string   `json:"root_secret"`
}

func (p DBDropParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID):
		return errors.New("bad service id")
	case p.Kind != "postgres" && p.Kind != "mysql" && p.Kind != "minio":
		return errors.New("databases apply to postgres, mysql and minio")
	case p.Target == TargetGuest && p.Kind == "minio":
		return errors.New("minio buckets in a Proxmox guest are not supported yet")
	case !ValidDBName(p.Database):
		return fmt.Errorf("bad database name %q", p.Database)
	case rootSecretRequired(p.Target, p.Container) && p.RootSecret == "":
		return errors.New("root secret is required")
	}
	for _, u := range p.Users {
		if !ValidDBName(u) {
			return fmt.Errorf("bad user name %q", u)
		}
	}
	return validDBExecTarget(p.Target, p.VMID, p.GuestType, p.Container)
}

// ---- db.query (MH-574 F2: table browser and SQL console) -----------------------------------------

const (
	DBQueryMaxLimit      = 500             // rows mode
	DBQueryMaxSQLBytes   = 64 * 1024       // one statement
	DBQueryMaxTimeout    = 60              // seconds
	DBQueryMaxResultSize = 1 * 1024 * 1024 // total encoded result, capped
	DBQueryMaxCellBytes  = 4 * 1024        // per cell, truncated
)

// ValidDBQueryMode: the four things db.query can do.
func ValidDBQueryMode(s string) bool {
	switch s {
	case "tables", "columns", "rows", "sql":
		return true
	}
	return false
}

// ValidDBIdent: a single identifier segment (schema or table name) — the same strict class as
// ValidDBName, so nothing here is ever interpolated unescaped into a query.
func ValidDBIdent(s string) bool { return ValidDBName(s) }

// ValidDBQualifiedTable splits "table" or "schema.table" and validates each part with ValidDBIdent.
// MySQL/MariaDB has no schema qualifier in this sense (Database already names it), so callers there
// pass an unqualified table only.
func ValidDBQualifiedTable(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 2 {
		return false
	}
	for _, p := range parts {
		if !ValidDBIdent(p) {
			return false
		}
	}
	return true
}

// SplitQualifiedTable returns (schema, table); schema is "" when unqualified.
func SplitQualifiedTable(s string) (string, string) {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

// DBQueryParams: db.query. Table is required for columns/rows (optionally schema-qualified for
// Postgres). SQL is required for mode "sql" and is fed to the client on stdin, never an argv — the
// only thing db.query ever puts in an argv is the fixed client binary and its flags. Write=true runs
// in a normal transaction (COMMIT); otherwise the statement runs read-only and is always rolled back.
type DBQueryParams struct {
	ServiceID      string `json:"service_id"`
	Kind           string `json:"kind"`
	Container      string `json:"container"`
	Target         string `json:"target,omitempty"`     // container | guest ("" = container, back-compat)
	VMID           string `json:"vmid,omitempty"`       // guest
	GuestType      string `json:"guest_type,omitempty"` // guest: lxc only
	Database       string `json:"database"`
	Mode           string `json:"mode"`
	Table          string `json:"table,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	Offset         int    `json:"offset,omitempty"`
	SQL            string `json:"sql,omitempty"`
	Write          bool   `json:"write,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	RootSecret     string `json:"root_secret"`
}

func (p DBQueryParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID):
		return errors.New("bad service id")
	case p.Kind != "postgres" && p.Kind != "mysql":
		return errors.New("the table browser and SQL console apply to postgres and mysql")
	case !ValidDBName(p.Database):
		return fmt.Errorf("bad database name %q", p.Database)
	case !ValidDBQueryMode(p.Mode):
		return fmt.Errorf("bad mode %q", p.Mode)
	case validDBExecTarget(p.Target, p.VMID, p.GuestType, p.Container) != nil:
		return validDBExecTarget(p.Target, p.VMID, p.GuestType, p.Container)
	case rootSecretRequired(p.Target, p.Container) && p.RootSecret == "":
		return errors.New("db.query needs the root secret")
	case p.Limit < 0 || p.Limit > DBQueryMaxLimit:
		return fmt.Errorf("limit must be between 0 and %d", DBQueryMaxLimit)
	case p.Offset < 0:
		return errors.New("offset must not be negative")
	case p.TimeoutSeconds < 1 || p.TimeoutSeconds > DBQueryMaxTimeout:
		return fmt.Errorf("timeout_seconds must be between 1 and %d", DBQueryMaxTimeout)
	}
	switch p.Mode {
	case "columns", "rows":
		if p.Table == "" || !ValidDBQualifiedTable(p.Table) {
			return fmt.Errorf("bad table %q", p.Table)
		}
		if p.Kind == "mysql" && strings.Contains(p.Table, ".") {
			return errors.New("mysql tables are not schema-qualified")
		}
	case "sql":
		if p.SQL == "" {
			return errors.New("sql is required")
		}
		if len(p.SQL) > DBQueryMaxSQLBytes {
			return fmt.Errorf("sql must be at most %d bytes", DBQueryMaxSQLBytes)
		}
		if _, err := SingleSQLStatement(p.Kind, p.SQL); err != nil {
			return err
		}
	}
	return nil
}

// DBQueryColumn: one result column.
type DBQueryColumn struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// DBTableInfo: one row of a "tables" listing.
type DBTableInfo struct {
	Schema       string `json:"schema,omitempty"`
	Name         string `json:"name"`
	RowsEstimate int64  `json:"rows_estimate"`
	SizeBytes    int64  `json:"size_bytes"`
}

// DBQueryResult: db.query's answer. Rows cells are nil for SQL NULL; every non-nil cell is a string,
// truncated to DBQueryMaxCellBytes. Truncated is set when either the row count or the total encoded
// size hit their cap.
type DBQueryResult struct {
	Columns    []DBQueryColumn `json:"columns,omitempty"`
	Rows       [][]*string     `json:"rows,omitempty"`
	RowCount   int             `json:"row_count"`
	Truncated  bool            `json:"truncated"`
	DurationMS int64           `json:"duration_ms"`
	Tables     []DBTableInfo   `json:"tables,omitempty"`
}

// ---- db.insights / db.cancel_query (MH-575 F6: DB metrics and slow queries) ----------------------
//
// Unlike db.query, every statement here is fixed and agent-owned — nothing a member typed ever
// reaches the engine. Postgres and MySQL only (the same engines db.query supports); Redis and MinIO
// have no such notion and are refused before this op is even built, same as db.query.

// DBInsightsMaxTimeout bounds the single statement db.insights runs.
const DBInsightsMaxTimeout = 30

// DBInsightsParams: db.insights. Database is the database to connect as (optional for both engines:
// Postgres defaults to "postgres", MySQL needs none — the figures gathered are cluster/server-wide).
type DBInsightsParams struct {
	ServiceID      string `json:"service_id"`
	Kind           string `json:"kind"`
	Container      string `json:"container"`
	Target         string `json:"target,omitempty"`     // container | guest ("" = container, back-compat)
	VMID           string `json:"vmid,omitempty"`       // guest
	GuestType      string `json:"guest_type,omitempty"` // guest: lxc only
	Database       string `json:"database,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	RootSecret     string `json:"root_secret"`
}

func (p DBInsightsParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID):
		return errors.New("bad service id")
	case p.Kind != "postgres" && p.Kind != "mysql":
		return errors.New("insights apply to postgres and mysql")
	case p.Database != "" && !ValidDBName(p.Database):
		return fmt.Errorf("bad database name %q", p.Database)
	case validDBExecTarget(p.Target, p.VMID, p.GuestType, p.Container) != nil:
		return validDBExecTarget(p.Target, p.VMID, p.GuestType, p.Container)
	case rootSecretRequired(p.Target, p.Container) && p.RootSecret == "":
		return errors.New("db.insights needs the root secret")
	case p.TimeoutSeconds < 1 || p.TimeoutSeconds > DBInsightsMaxTimeout:
		return fmt.Errorf("timeout_seconds must be between 1 and %d", DBInsightsMaxTimeout)
	}
	return nil
}

// DBSizeInfo: one database's on-disk size.
type DBSizeInfo struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
}

// DBLongQuery: one long-running or active session/query.
type DBLongQuery struct {
	PID             string  `json:"pid"`
	DurationSeconds float64 `json:"duration_seconds"`
	State           string  `json:"state,omitempty"`
	Query           string  `json:"query,omitempty" doc:"Truncated to 500 characters."`
	Database        string  `json:"database,omitempty"`
	User            string  `json:"user,omitempty"`
}

// DBTopStatement: one row of pg_stat_statements / performance_schema.events_statements_summary_by_digest.
type DBTopStatement struct {
	Query       string  `json:"query" doc:"Truncated to 500 characters."`
	Calls       int64   `json:"calls"`
	TotalTimeMS float64 `json:"total_time_ms"`
	MeanTimeMS  float64 `json:"mean_time_ms"`
	Rows        int64   `json:"rows"`
}

// DBInsightsResult: db.insights' answer. Fields not gathered for the engine are left zero/empty.
type DBInsightsResult struct {
	Kind string `json:"kind"`

	Connections        int            `json:"connections"`
	MaxConnections     int            `json:"max_connections"`
	ConnectionsByState map[string]int `json:"connections_by_state,omitempty"`

	// Postgres: cumulative since the cluster started (pg_stat_database). MySQL: cumulative since
	// Uptime (SHOW GLOBAL STATUS) — Questions/SlowQueries below.
	XactCommit    int64   `json:"xact_commit,omitempty"`
	XactRollback  int64   `json:"xact_rollback,omitempty"`
	CacheHitRatio float64 `json:"cache_hit_ratio" doc:"Percent, buffer cache hits over hits+reads."`
	Deadlocks     int64   `json:"deadlocks,omitempty"`
	TempBytes     int64   `json:"temp_bytes,omitempty"`

	Databases []DBSizeInfo `json:"databases,omitempty"`

	LongRunning  []DBLongQuery `json:"long_running,omitempty"`
	LocksWaiting int           `json:"locks_waiting"`

	StatStatementsAvailable bool             `json:"stat_statements_available"`
	StatStatementsHint      string           `json:"stat_statements_hint,omitempty" doc:"Set when not available: how to enable it."`
	TopStatements           []DBTopStatement `json:"top_statements,omitempty"`

	// MySQL only.
	ThreadsConnected int64  `json:"threads_connected,omitempty"`
	ThreadsRunning   int64  `json:"threads_running,omitempty"`
	Questions        int64  `json:"questions,omitempty"`
	SlowQueries      int64  `json:"slow_queries,omitempty"`
	UptimeSeconds    int64  `json:"uptime_seconds,omitempty"`
	SlowQueryLog     string `json:"slow_query_log,omitempty"`
}

// DBCancelQueryParams: db.cancel_query. ID is the Postgres pid (pg_stat_activity.pid) or the MySQL
// processlist Id. Refused for the agent's own connection (never cancels itself).
type DBCancelQueryParams struct {
	ServiceID  string `json:"service_id"`
	Kind       string `json:"kind"`
	Container  string `json:"container"`
	Target     string `json:"target,omitempty"`     // container | guest ("" = container, back-compat)
	VMID       string `json:"vmid,omitempty"`       // guest
	GuestType  string `json:"guest_type,omitempty"` // guest: lxc only
	Database   string `json:"database,omitempty"`
	ID         int64  `json:"id"`
	RootSecret string `json:"root_secret"`
}

func (p DBCancelQueryParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID):
		return errors.New("bad service id")
	case p.Kind != "postgres" && p.Kind != "mysql":
		return errors.New("cancel query applies to postgres and mysql")
	case p.Database != "" && !ValidDBName(p.Database):
		return fmt.Errorf("bad database name %q", p.Database)
	case validDBExecTarget(p.Target, p.VMID, p.GuestType, p.Container) != nil:
		return validDBExecTarget(p.Target, p.VMID, p.GuestType, p.Container)
	case rootSecretRequired(p.Target, p.Container) && p.RootSecret == "":
		return errors.New("db.cancel_query needs the root secret")
	case p.ID <= 0:
		return errors.New("a positive process/pid id is required")
	}
	return nil
}

// DBCancelQueryResult: whether the backend/query was found and cancelled.
type DBCancelQueryResult struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// ---- db.import (import an existing Postgres database into a managed one) -----------------------
//
// The source connection URL is a secret exactly like a backup repo's or a root credential's: it
// never rides in plaintext params, never appears in the operation log, and never reaches the
// container's argv — pg_dump reads it from PGPASSFILE-style stdin/env only, same discipline as
// every other secret in this package. SourceSecret is a box for DBSecretContext(ServiceID,
// "import:<Database>"), carrying {"source_url": "postgres://user:pass@host:port/db"}.

var sourceURLSchemeRE = regexp.MustCompile(`^postgres(ql)?://`)

// ValidDBSourceURL is a light sanity check both sides run on the plaintext before it is sealed
// (control plane) or used (agent) — scheme postgres/postgresql and a host present. It is
// deliberately not a full parse: net/url parses it for real once opened.
func ValidDBSourceURL(s string) bool {
	if !sourceURLSchemeRE.MatchString(s) {
		return false
	}
	rest := s[strings.Index(s, "://")+3:]
	if i := strings.IndexAny(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	rest = strings.TrimSuffix(rest, "/")
	if idx := strings.IndexAny(rest, "/?"); idx >= 0 {
		rest = rest[:idx]
	}
	return rest != ""
}

// DBImportParams: db.import — pg_dump the source (run inside the managed service's own container,
// which carries client binaries of its own major version) piped into pg_restore against Database,
// which db.import already owns (created by db.create/CreateDatabaseAndUser). Replace drops and
// recreates Database (owned by its current owner) first, so a final cutover sync can re-run the
// same import; without it, db.import refuses when Database already has user tables. RootSecret
// authenticates as the container's own superuser (CREATE EXTENSION during restore needs it);
// Owner is the role pg_restore --role= runs as, so restored objects still belong to it.
type DBImportParams struct {
	ServiceID    string `json:"service_id"`
	Container    string `json:"container"`
	Target       string `json:"target,omitempty"`     // container | guest ("" = container, back-compat)
	VMID         string `json:"vmid,omitempty"`       // guest
	GuestType    string `json:"guest_type,omitempty"` // guest: lxc only
	Database     string `json:"database"`
	Owner        string `json:"owner"`
	Replace      bool   `json:"replace,omitempty"`
	SourceSecret string `json:"source_secret"`
	RootSecret   string `json:"root_secret"`
}

func (p DBImportParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID):
		return errors.New("bad service id")
	case !ValidDBName(p.Database):
		return fmt.Errorf("bad database name %q", p.Database)
	case !ValidDBName(p.Owner):
		return fmt.Errorf("bad owner name %q", p.Owner)
	case validDBExecTarget(p.Target, p.VMID, p.GuestType, p.Container) != nil:
		return validDBExecTarget(p.Target, p.VMID, p.GuestType, p.Container)
	case p.Target == TargetGuest:
		return errors.New("importing into a Proxmox guest's native postgres is not supported yet")
	case p.SourceSecret == "":
		return errors.New("db.import needs the source secret")
	case rootSecretRequired(p.Target, p.Container) && p.RootSecret == "":
		return errors.New("db.import needs the root secret")
	}
	return nil
}

// DBImportResult: db.import's answer. WarningCount counts pg_restore's non-fatal stderr warnings
// (e.g. an extension already present); BytesRestored/TablesRestored are best-effort figures parsed
// from pg_restore's verbose/list output.
type DBImportResult struct {
	BytesRestored  int64 `json:"bytes_restored,omitempty"`
	TablesRestored int   `json:"tables_restored,omitempty"`
	WarningCount   int   `json:"warning_count,omitempty"`
}
