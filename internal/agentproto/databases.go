package agentproto

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// P3 operation kinds: managed data services (Postgres/MySQL/Redis) and restic backups. Typed like
// everything else — each has fixed params below, and nothing in them reaches a host shell: the agent
// turns them into fixed argv lists (docker run / docker exec / restic), and the only strings a user
// influences (a database or user name) are validated to a strict identifier before they are used.
const (
	KindDBProvision = "db.provision" // run a Postgres/MySQL/Redis container with a named volume, bound to a private address
	KindDBCreate    = "db.create"    // create a database + user + grants inside a running service
	KindDBBackup    = "db.backup"    // pg_dump -Fc / mysqldump / Redis RDB, pushed into a restic repository
	KindDBRestore   = "db.restore"   // restore a snapshot into a throwaway container and verify it (restore-test)
	KindDBRemove    = "db.remove"    // stop and remove a service's container and its volume (the service was deleted)
	KindDBDrop      = "db.drop"      // drop one database (bucket) and its users inside a running service
	KindDBUser      = "db.user"      // create, rotate, delete, grant or revoke a user's password/grants (MH-574 F1)
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
	snapshotRE  = regexp.MustCompile(`^[0-9a-f]{8,64}$`)
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

// ValidDBVersion: a container image tag like "16", "8.4", "7.2-alpine".
func ValidDBVersion(s string) bool { return dbVersionRE.MatchString(s) }

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
	case !ValidDBName(p.Database):
		return fmt.Errorf("bad database name %q", p.Database)
	case !ValidDBName(p.User):
		return fmt.Errorf("bad user name %q", p.User)
	case !ValidGrants(p.Grants):
		return fmt.Errorf("bad grants %q", p.Grants)
	case p.RootSecret == "" || p.UserSecret == "":
		return errors.New("db.create needs the root and user secrets")
	}
	return nil
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
	case !ValidDBUserAction(p.Action):
		return fmt.Errorf("bad action %q", p.Action)
	case !ValidDBName(p.User):
		return fmt.Errorf("bad user name %q", p.User)
	case p.Container == "":
		return errors.New("bad container")
	case p.RootSecret == "":
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
	case !ValidDBName(p.Database):
		return fmt.Errorf("bad database name %q", p.Database)
	case p.Container == "" || p.RootSecret == "":
		return errors.New("container and root secret are required")
	}
	for _, u := range p.Users {
		if !ValidDBName(u) {
			return fmt.Errorf("bad user name %q", u)
		}
	}
	return nil
}
