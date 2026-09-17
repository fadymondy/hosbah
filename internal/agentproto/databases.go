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
	KindDBRemove    = "db.remove"     // stop and remove a service's container and its volume (the service was deleted)
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

// ValidDBKind: the three managed engines.
func ValidDBKind(s string) bool { return s == "postgres" || s == "mysql" || s == "redis" }

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
func DBContainerName(name string) string { return "cloudy-db-" + name }
func DBVolumeName(name string) string     { return "cloudy-dbdata-" + name }

// DBThrowawayName is the restore-test's disposable container/volume, named from the backup id.
func DBThrowawayName(backupID string) string {
	short := backupID
	if len(short) > 8 {
		short = short[:8]
	}
	return "cloudy-dbrestore-" + short
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
		if !strings.HasPrefix(r.Repo, "/var/lib/cloudy/") {
			return errors.New("a local restic repository must be under /var/lib/cloudy")
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
	case p.Container != DBContainerName(p.Name) || p.Volume != DBVolumeName(p.Name):
		return errors.New("container or volume name does not match the service")
	case p.Bind == "" || p.Bind == "0.0.0.0" || p.Port < 1 || p.Port > 65535:
		return errors.New("a data service binds to a private address and a port")
	case p.Kind != "redis" && p.RootSecret == "":
		return errors.New("postgres and mysql need a root secret")
	}
	return nil
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
	case p.Kind != "postgres" && p.Kind != "mysql":
		return errors.New("databases and users apply to postgres and mysql only")
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
	case p.Container != DBThrowawayName(p.BackupID):
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
