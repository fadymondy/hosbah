// Package dbsvc is the control plane for P3: managed data services (Postgres/MySQL/Redis), their
// databases, users and backups. Like internal/deploy it is the one implementation the REST API and
// the MCP share, so validation, roles, audit and "a secret never leaves in a list" are written once.
//
// A data service is a container on one of the team's servers, provisioned, created-into, backed up
// and restore-tested through typed, signed agent operations (internal/agentproto db.* kinds). Root
// and user credentials are sealed at rest with the team DEK and re-sealed to the target agent's own
// key for each operation, so operations.params never holds a plaintext secret. Long-running actions
// (provision, create, backup, restore-test, remove) run in a background goroutine that updates the
// row from the operation's result; the request returns as soon as the row is queued.
package dbsvc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/app"
	"github.com/fadymondy/hosbah/internal/brand"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/quota"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// asServiceError copies a *quota.Error's status and message as this package's own *Error, so a
// quota refusal reaches the REST and MCP layers the same way every other one does.
func asServiceError(err error) error {
	var qe *quota.Error
	if errors.As(err, &qe) {
		return &Error{Status: qe.Status, Message: qe.Msg}
	}
	return err
}

// Actor is who asked (reused from deploy so audit reads the same across P2 and P3).
type Actor = deploy.Actor

// Error carries an HTTP status for the REST layer; the MCP layer just shows the message.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

func notFound(what string) error { return &Error{Status: 404, Message: what + " not found"} }
func invalid(msg string) error   { return &Error{Status: 422, Message: msg} }
func conflict(msg string) error  { return &Error{Status: 409, Message: msg} }

// Service runs P3 data-service and backup actions.
type Service struct {
	a           *app.App
	ops         *ops.Service
	deploy      *deploy.Service
	log           *slog.Logger
	schedCancel   context.CancelFunc
	metricsCancel context.CancelFunc
}

// New builds the service. deploy is used to inject a sealed connection string when a service is
// attached to an app (its SetEnv is the audited, team-sealed path).
func New(a *app.App, o *ops.Service, d *deploy.Service) *Service {
	log := a.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{a: a, ops: o, deploy: d, log: log.With("component", "dbsvc")}
}

func (s *Service) App() *app.App { return s.a }

// sys runs a system+team transaction (the background workers): app.system for the SELECT/UPDATE
// policies, and app.team_id so the team FOR ALL policy admits an INSERT/UPDATE of the team's rows.
func (s *Service) sys(ctx context.Context, teamID string, fn func(q *db.Queries) error) error {
	return tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error { return fn(q) })
}

// ---- refs --------------------------------------------------------------------------------------

// ServiceByRef resolves a data service by id or name within the team.
func ServiceByRef(ctx context.Context, q *db.Queries, teamID, ref string) (db.DataService, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.DataService{}, invalid("a data service name or id is required")
	}
	if agentproto.ValidID(ref) {
		if svc, err := q.DataServiceInTeam(ctx, db.DataServiceInTeamParams{ID: ref, TeamID: teamID}); err == nil {
			return svc, nil
		}
	}
	svc, err := q.DataServiceByNameInTeam(ctx, db.DataServiceByNameInTeamParams{TeamID: teamID, Name: strings.ToLower(ref)})
	if err != nil {
		return db.DataService{}, notFound("data service")
	}
	return svc, nil
}

// ---- create a service --------------------------------------------------------------------------

// CreateInput is what the wizard and create_database send.
type CreateInput struct {
	Name    string
	Server  string // id or name
	Kind    string
	Version string
}

// defaultVersion pins a current image tag per kind when the caller does not choose one.
func defaultVersion(kind string) string {
	switch kind {
	case "postgres":
		return "16"
	case "mysql":
		return "8.4"
	case "redis":
		return "7.2-alpine"
	case "minio":
		return "2025-09-07T16-13-09Z" // quay.io/minio/minio:RELEASE.<version>
	}
	return ""
}

// CreateService inserts the row (status provisioning), then provisions the container in the
// background. The root credential is generated here, sealed with the team DEK, and never returned.
func (s *Service) CreateService(ctx context.Context, q *db.Queries, teamID string, actor Actor, in CreateInput) (db.DataService, error) {
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if !agentproto.ValidAppName(in.Name) {
		return db.DataService{}, invalid("a service name is lowercase letters, digits and dashes")
	}
	if !agentproto.ValidDBKind(in.Kind) {
		return db.DataService{}, invalid("kind is postgres, mysql, redis or minio")
	}
	if in.Version == "" {
		in.Version = defaultVersion(in.Kind)
	}
	if !agentproto.ValidDBVersion(in.Version) {
		return db.DataService{}, invalid("that version is not a valid image tag")
	}
	srv, err := q.ServerInTeamByRef(ctx, db.ServerInTeamByRefParams{TeamID: teamID, Ref: in.Server})
	if err != nil {
		return db.DataService{}, notFound("server")
	}
	if _, err := q.DataServiceByNameInTeam(ctx, db.DataServiceByNameInTeamParams{TeamID: teamID, Name: in.Name}); err == nil {
		return db.DataService{}, conflict("a data service named " + in.Name + " already exists")
	}
	if err := quota.CheckDataServices(ctx, q, teamID); err != nil {
		return db.DataService{}, asServiceError(err)
	}
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return db.DataService{}, err
	}
	id := newUUID()
	rootSealed := "csv1:redis-no-root" // redis carries no root credential
	if in.Kind != "redis" {
		sealed, err := crypto.Seal(dek, teamID, rootContext(id), []byte(randPassword(28)))
		if err != nil {
			return db.DataService{}, err
		}
		rootSealed = sealed
	}
	svc, err := q.InsertDataService(ctx, db.InsertDataServiceParams{
		ID: id, TeamID: teamID, ServerID: srv.ID, Name: in.Name, Kind: in.Kind, Version: in.Version,
		Status: "provisioning", RootSealed: rootSealed, CreatedBy: actor.UserID,
	})
	if err != nil {
		return db.DataService{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "dataservice.create", "data_service", svc.ID,
		auditExtra(actor, map[string]any{"kind": in.Kind, "version": in.Version, "server": srv.ID})); err != nil {
		return db.DataService{}, err
	}
	s.spawn(func(bg context.Context) { s.provision(bg, teamID, svc, srv.ID) })
	return svc, nil
}

// provision runs db.provision and records the private endpoint (or the failure).
func (s *Service) provision(ctx context.Context, teamID string, svc db.DataService, serverID string) {
	var res agentproto.DBProvisionResult
	err := s.withServer(ctx, teamID, serverID, func(agentKey string, dek []byte) (any, string, error) {
		rootBox, err := s.sealRoot(agentKey, dek, teamID, svc)
		if err != nil {
			return nil, "", err
		}
		bind := privateBind(serverID)
		port := s.hostPort(ctx, teamID, serverID, svc)
		p := agentproto.DBProvisionParams{
			ServiceID: svc.ID, Name: svc.Name, Kind: svc.Kind, Version: svc.Version,
			Image: agentproto.DBImage(svc.Kind, svc.Version), Container: agentproto.DBContainerName(svc.Name),
			Volume: agentproto.DBVolumeName(svc.Name), Bind: bind, Port: port, RootSecret: rootBox,
			Resources: serviceResources(svc),
		}
		return p, opID(svc.ID, "provision"), nil
	}, agentproto.KindDBProvision, &res)
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		if err != nil {
			return q.SetDataServiceStatus(ctx, db.SetDataServiceStatusParams{ID: svc.ID, TeamID: teamID, Status: "failed", Error: strptr(err.Error())})
		}
		return q.SetDataServiceEndpoint(ctx, db.SetDataServiceEndpointParams{
			ID: svc.ID, TeamID: teamID, Container: strptr(res.Container), Volume: strptr(res.Volume),
			Host: strptr(res.Host), Port: int64ptr(int64(res.Port)),
		})
	})
}

// ---- create a database + user ------------------------------------------------------------------

// CreateDatabaseAndUser creates a database and a user with grants on a postgres/mysql service.
func (s *Service) CreateDatabaseAndUser(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, dbName, userName, grants string) (db.Database, db.DatabaseUser, error) {
	if err := usable(svc); err != nil {
		return db.Database{}, db.DatabaseUser{}, err
	}
	if svc.Kind == "redis" {
		return db.Database{}, db.DatabaseUser{}, invalid("Redis services have no databases or users")
	}
	if svc.Status != "running" {
		return db.Database{}, db.DatabaseUser{}, conflict("the service is not running yet")
	}
	dbName, userName = strings.ToLower(strings.TrimSpace(dbName)), strings.ToLower(strings.TrimSpace(userName))
	if !agentproto.ValidDBName(dbName) {
		return db.Database{}, db.DatabaseUser{}, invalid("a database name is lower snake_case")
	}
	if userName == "" {
		userName = dbName
	}
	if !agentproto.ValidDBName(userName) {
		return db.Database{}, db.DatabaseUser{}, invalid("a user name is lower snake_case")
	}
	if grants == "" {
		grants = "all"
	}
	if !agentproto.ValidGrants(grants) {
		return db.Database{}, db.DatabaseUser{}, invalid("grants are all, readwrite or readonly")
	}
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return db.Database{}, db.DatabaseUser{}, err
	}
	dbRow, err := q.InsertDatabase(ctx, db.InsertDatabaseParams{ID: newUUID(), TeamID: teamID, DataServiceID: svc.ID, Name: dbName, Status: "creating", CreatedBy: actor.UserID})
	if err != nil {
		return db.Database{}, db.DatabaseUser{}, conflict("a database named " + dbName + " already exists on this service")
	}
	pw := randPassword(24)
	sealed, err := crypto.Seal(dek, teamID, userContext(svc.ID, userName), []byte(pw))
	if err != nil {
		return db.Database{}, db.DatabaseUser{}, err
	}
	userRow, err := q.InsertDatabaseUser(ctx, db.InsertDatabaseUserParams{
		ID: newUUID(), TeamID: teamID, DataServiceID: svc.ID, DatabaseID: strptr(dbRow.ID), Name: userName,
		Grants: grants, PasswordSealed: sealed, Status: "creating", CreatedBy: actor.UserID,
	})
	if err != nil {
		return db.Database{}, db.DatabaseUser{}, conflict("a user named " + userName + " already exists on this service")
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "database.create", "data_service", svc.ID,
		auditExtra(actor, map[string]any{"database": dbName, "user": userName, "grants": grants})); err != nil {
		return db.Database{}, db.DatabaseUser{}, err
	}
	s.spawn(func(bg context.Context) { s.createInto(bg, teamID, svc, dbRow, userRow, pw) })
	return dbRow, userRow, nil
}

func (s *Service) createInto(ctx context.Context, teamID string, svc db.DataService, dbRow db.Database, userRow db.DatabaseUser, pw string) {
	err := s.withServer(ctx, teamID, svc.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		rootBox, err := s.sealRoot(agentKey, dek, teamID, svc)
		if err != nil {
			return nil, "", err
		}
		userBox, err := agentproto.SealBox(agentKey, agentproto.DBSecretContext(svc.ID, "user:"+userRow.Name), mustJSON(map[string]string{"password": pw}))
		if err != nil {
			return nil, "", err
		}
		target, vmid, guestType, container := execTarget(svc)
		p := agentproto.DBCreateParams{
			ServiceID: svc.ID, Kind: svc.Kind, Target: target, VMID: vmid, GuestType: guestType, Container: container,
			Database: dbRow.Name, User: userRow.Name, Grants: userRow.Grants, RootSecret: rootBox, UserSecret: userBox,
		}
		return p, opID(dbRow.ID, "create"), nil
	}, agentproto.KindDBCreate, nil)
	status, errStr := "ready", (*string)(nil)
	if err != nil {
		status, errStr = "failed", strptr(err.Error())
	}
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		_ = q.SetDatabaseStatus(ctx, db.SetDatabaseStatusParams{ID: dbRow.ID, TeamID: teamID, Status: status, Error: errStr})
		return q.SetDatabaseUserStatus(ctx, db.SetDatabaseUserStatusParams{ID: userRow.ID, TeamID: teamID, Status: status, Error: errStr})
	})
}

// ---- backups -----------------------------------------------------------------------------------

// Backup queues a manual backup and runs it in the background.
func (s *Service) Backup(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, kind string) (db.Backup, error) {
	if err := usable(svc); err != nil {
		return db.Backup{}, err
	}
	if err := backupable(svc); err != nil {
		return db.Backup{}, err
	}
	if svc.Status != "running" {
		return db.Backup{}, conflict("the service is not running")
	}
	if kind == "" {
		kind = "manual"
	}
	target := "local"
	if sch, err := q.BackupScheduleOfService(ctx, db.BackupScheduleOfServiceParams{DataServiceID: svc.ID, TeamID: teamID}); err == nil {
		target = sch.Target
	}
	if target != "local" && !s.remoteConfigured(target) {
		return db.Backup{}, conflict("the " + target + " backup target is not configured")
	}
	b, err := q.InsertBackup(ctx, db.InsertBackupParams{
		ID: newUUID(), TeamID: teamID, DataServiceID: svc.ID, ScheduleID: nil, Kind: kind, Status: "pending",
		Target: target, RestoreStatus: strptr("untested"), CreatedBy: actor.UserID,
	})
	if err != nil {
		return db.Backup{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "backup.start", "data_service", svc.ID, auditExtra(actor, map[string]any{"backup": b.ID, "kind": kind})); err != nil {
		return db.Backup{}, err
	}
	s.spawn(func(bg context.Context) { s.runBackup(bg, teamID, svc, b, 0) })
	return b, nil
}

func (s *Service) runBackup(ctx context.Context, teamID string, svc db.DataService, b db.Backup, retention int) {
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		return q.SetBackupRunning(ctx, db.SetBackupRunningParams{ID: b.ID, TeamID: teamID})
	})
	var res agentproto.DBBackupResult
	err := s.withServer(ctx, teamID, svc.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		rootBox, err := s.sealRoot(agentKey, dek, teamID, svc)
		if err != nil {
			return nil, "", err
		}
		repo, err := s.repoSpec(agentKey, dek, teamID, svc, b.Target)
		if err != nil {
			return nil, "", err
		}
		p := agentproto.DBBackupParams{
			ServiceID: svc.ID, Kind: svc.Kind, Container: contOf(svc), BackupID: b.ID, Repo: repo,
			Retention: retention, RootSecret: rootBox,
		}
		return p, opID(b.ID, "backup"), nil
	}, agentproto.KindDBBackup, &res)
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		if err != nil {
			return q.SetBackupResult(ctx, db.SetBackupResultParams{ID: b.ID, TeamID: teamID, Status: "failed", Error: strptr(err.Error())})
		}
		return q.SetBackupResult(ctx, db.SetBackupResultParams{
			ID: b.ID, TeamID: teamID, Status: "succeeded", SnapshotID: strptr(res.SnapshotID),
			SizeBytes: int64ptr(res.SizeBytes), TakenAt: timeptr(time.Now().UTC()),
		})
	})
}

// RestoreTest restores a backup into a throwaway container and stamps the result. The agent's
// db.restore never touches the live service; it verifies the dump opens.
func (s *Service) RestoreTest(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, b db.Backup) error {
	if err := restorable(svc); err != nil {
		return err
	}
	if b.Status != "succeeded" || b.SnapshotID == nil {
		return conflict("only a completed backup can be restore-tested")
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "backup.restore_test", "data_service", svc.ID, auditExtra(actor, map[string]any{"backup": b.ID})); err != nil {
		return err
	}
	_ = q.SetBackupRestoreTesting(ctx, db.SetBackupRestoreTestingParams{ID: b.ID, TeamID: teamID})
	s.spawn(func(bg context.Context) { s.runRestoreTest(bg, teamID, svc, b) })
	return nil
}

func (s *Service) runRestoreTest(ctx context.Context, teamID string, svc db.DataService, b db.Backup) {
	var res agentproto.DBRestoreResult
	err := s.withServer(ctx, teamID, svc.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		rootBox, err := s.sealRoot(agentKey, dek, teamID, svc)
		if err != nil {
			return nil, "", err
		}
		repo, err := s.repoSpec(agentKey, dek, teamID, svc, b.Target)
		if err != nil {
			return nil, "", err
		}
		p := agentproto.DBRestoreParams{
			ServiceID: svc.ID, Kind: svc.Kind, Version: svc.Version, Image: agentproto.DBImage(svc.Kind, svc.Version),
			BackupID: b.ID, SnapshotID: derefStr(b.SnapshotID), Container: agentproto.DBThrowawayName(b.ID),
			Volume: agentproto.DBThrowawayName(b.ID) + "-vol", Repo: repo, RootSecret: rootBox,
		}
		return p, opID(b.ID, "restore"), nil
	}, agentproto.KindDBRestore, &res)
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		status, detail := "pass", res.Detail
		if err != nil {
			status, detail = "fail", err.Error()
		} else if !res.OK {
			status = "fail"
		}
		return q.SetBackupRestoreResult(ctx, db.SetBackupRestoreResultParams{
			ID: b.ID, TeamID: teamID, RestoreStatus: strptr(status), RestoreError: strptr(detail), RestoreTestedAt: timeptr(time.Now().UTC()),
		})
	})
}

// ---- attach + reveal ---------------------------------------------------------------------------

// Attach injects a sealed DATABASE_URL / REDIS_URL into an app. Postgres/MySQL use a database user;
// Redis uses the service endpoint. The value is sealed with the team key via deploy.SetEnv and
// applies from the app's next deploy or restart.
func (s *Service) Attach(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, a db.App, userRef string) (string, error) {
	if err := usable(svc); err != nil {
		return "", err
	}
	if deploy.IsImported(a) {
		return "", deploy.ImportedError(a.Name)
	}
	if svc.Status != "running" || svc.Host == nil || svc.Port == nil {
		return "", conflict("the service is not running yet")
	}
	name, connURL, err := s.connString(ctx, q, teamID, svc, userRef)
	if err != nil {
		return "", err
	}
	vars := [][2]string{{name, connURL}}
	if svc.Kind == "minio" {
		vars = append(vars, s3Vars(connURL)...)
	}
	for _, v := range vars {
		if err := s.deploy.SetEnv(ctx, q, teamID, actor, a, v[0], v[1]); err != nil {
			return "", err
		}
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "dataservice.attach", "data_service", svc.ID, auditExtra(actor, map[string]any{"app": a.ID, "var": name})); err != nil {
		return "", err
	}
	return name, nil
}

// RevealURL returns a service's connection string (audited). secrets:read only.
func (s *Service) RevealURL(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, userRef string) (string, error) {
	if err := usable(svc); err != nil {
		return "", err
	}
	_, connURL, err := s.connString(ctx, q, teamID, svc, userRef)
	if err != nil {
		return "", err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "dataservice.reveal_url", "data_service", svc.ID, auditExtra(actor, nil)); err != nil {
		return "", err
	}
	return connURL, nil
}

// connString builds the variable name and connection URL for a service (and, for sql engines, a user).
func (s *Service) connString(ctx context.Context, q *db.Queries, teamID string, svc db.DataService, userRef string) (varName, connURL string, err error) {
	host, port := derefStr(svc.Host), int(derefInt(svc.Port))
	if svc.Kind == "redis" {
		return "REDIS_URL", fmt.Sprintf("redis://%s:%d", host, port), nil
	}
	users, err := q.ListDatabaseUsersOfService(ctx, db.ListDatabaseUsersOfServiceParams{DataServiceID: svc.ID, TeamID: teamID})
	if err != nil || len(users) == 0 {
		return "", "", conflict("create a database and user first")
	}
	u := users[0]
	if userRef != "" {
		found := false
		for _, cand := range users {
			if cand.ID == userRef || cand.Name == userRef {
				u, found = cand, true
			}
		}
		if !found {
			return "", "", notFound("database user")
		}
	}
	dbName := u.Name
	if u.DatabaseID != nil {
		if d, err := q.DatabaseInTeam(ctx, db.DatabaseInTeamParams{ID: *u.DatabaseID, TeamID: teamID}); err == nil {
			dbName = d.Name
		}
	}
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return "", "", err
	}
	pt, err := crypto.Open(dek, teamID, userContext(svc.ID, u.Name), u.PasswordSealed)
	if err != nil {
		return "", "", errors.New("the user password could not be opened")
	}
	ui := url.UserPassword(u.Name, string(pt))
	if svc.Kind == "minio" {
		// One URL carries everything; Attach expands it into the S3 variables apps expect.
		return "S3_URL", fmt.Sprintf("s3://%s@%s:%d/%s", ui.String(), host, port, agentproto.BucketName(dbName)), nil
	}
	scheme := "postgres"
	if svc.Kind == "mysql" {
		scheme = "mysql"
	}
	return "DATABASE_URL", fmt.Sprintf("%s://%s@%s:%d/%s", scheme, ui.String(), host, port, dbName), nil
}

// ---- delete ------------------------------------------------------------------------------------

// DeleteService removes the service's container and volume on the server, then its rows.
func (s *Service) DeleteService(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService) error {
	if svc.Source == SourceImported {
		return s.forgetImported(ctx, q, teamID, actor, svc)
	}
	if err := refuseServiceWithBuckets(ctx, q, teamID, svc); err != nil {
		return err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "dataservice.delete", "data_service", svc.ID, auditExtra(actor, nil)); err != nil {
		return err
	}
	if _, err := q.DeleteDataServiceInTeam(ctx, db.DeleteDataServiceInTeamParams{ID: svc.ID, TeamID: teamID}); err != nil {
		return err
	}
	s.spawn(func(bg context.Context) {
		_ = s.withServer(bg, teamID, svc.ServerID, func(agentKey string, dek []byte) (any, string, error) {
			p := agentproto.DBRemoveParams{ServiceID: svc.ID, Container: contOf(svc), Volume: agentproto.DBVolumeName(svc.Name)}
			return p, opID(svc.ID, "remove"), nil
		}, agentproto.KindDBRemove, nil)
	})
	return nil
}

// ---- operation plumbing ------------------------------------------------------------------------

// withServer resolves the server's agent key and team DEK, lets build() produce the params + op id,
// then enqueues and awaits the signed operation, decoding its result into out.
func (s *Service) withServer(ctx context.Context, teamID, serverID string, build func(agentKey string, dek []byte) (any, string, error), kind string, out any) error {
	var agentKey string
	var dek []byte
	if err := s.sys(ctx, teamID, func(q *db.Queries) error {
		k, err := q.ServerTeamKey(ctx, serverID)
		if err != nil {
			return err
		}
		if k.AgentPublicKey == nil {
			return errors.New("the server's agent has not enrolled")
		}
		agentKey = *k.AgentPublicKey
		dek, err = s.a.Keys.UnwrapDEK(k.TeamID, k.DekWrapped)
		return err
	}); err != nil {
		return err
	}
	params, id, err := build(agentKey, dek)
	if err != nil {
		return err
	}
	if err := s.sys(ctx, teamID, func(q *db.Queries) error {
		if _, err := q.OperationInTeam(ctx, db.OperationInTeamParams{ID: id, TeamID: teamID}); err == nil {
			return nil // resume: it already exists
		}
		raw, _ := json.Marshal(params)
		_, _, e := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{OpID: id, TeamID: teamID, ServerID: serverID, Kind: kind, Params: raw, CreatedBy: "hosbah"})
		return e
	}); err != nil {
		return err
	}
	s.ops.Wake()
	done, err := s.ops.Await(ctx, id, serverID, nil)
	if err != nil {
		return err
	}
	if done.Status != "ok" {
		if done.Error != nil {
			return errors.New(*done.Error)
		}
		return errors.New(done.Status)
	}
	if out != nil && done.Result != nil {
		return json.Unmarshal([]byte(*done.Result), out)
	}
	return nil
}

func (s *Service) spawn(fn func(context.Context)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
		defer cancel()
		fn(ctx)
	}()
}

// sealRoot re-seals the service's root credential to the target agent's key. An imported native
// Proxmox LXC guest with no stored credential needs none sealed at all — the agent authenticates as
// the matching OS user instead (Postgres peer auth, MySQL/MariaDB auth_socket) — so it returns ("",
// nil) rather than importedRoot's "give it credentials first" error. If a member did give it
// credentials anyway, they are harmless to seal and used as before.
func (s *Service) sealRoot(agentKey string, dek []byte, teamID string, svc db.DataService) (string, error) {
	if isImported(svc) {
		if !HasCredentials(svc) && deploy.WorkloadOfService(svc).OriginKind == "guest" {
			return "", nil
		}
		// An imported service: the credentials a member gave (user, password, database), checked by db.probe.
		root, err := importedRoot(dek, teamID, svc)
		if err != nil {
			return "", err
		}
		return agentproto.SealBox(agentKey, agentproto.DBSecretContext(svc.ID, "root"), mustJSON(root))
	}
	if svc.Kind == "redis" {
		return "", nil
	}
	if svc.RootSealed == nil {
		return "", errors.New("Hosbah holds no credential for this service")
	}
	pt, err := crypto.Open(dek, teamID, rootContext(svc.ID), *svc.RootSealed)
	if err != nil {
		return "", errors.New("the root credential could not be opened")
	}
	return agentproto.SealBox(agentKey, agentproto.DBSecretContext(svc.ID, "root"), mustJSON(map[string]string{"password": string(pt)}))
}

// repoSpec builds the restic repository spec + its sealed secret for a target. 'local' is a
// filesystem repo on the server; the demo's default. minio/s3 require configured credentials.
func (s *Service) repoSpec(agentKey string, dek []byte, teamID string, svc db.DataService, target string) (agentproto.RepoSpec, error) {
	secret := map[string]string{"RESTIC_PASSWORD": deriveRepoPassword(dek, svc.ID)}
	repo := agentproto.RepoSpec{Target: target}
	switch target {
	case "local":
		repo.Repo = agentproto.WorkDir + "/restic/" + svc.ID // /var/lib/cloudy/restic before the rename: the agent moved it, and left a link
	case "minio", "s3":
		cfg, ok := s.remoteConfig(target)
		if !ok {
			return repo, conflict("the " + target + " backup target is not configured")
		}
		repo.Repo = cfg.Repo
		secret["AWS_ACCESS_KEY_ID"] = cfg.AccessKey
		secret["AWS_SECRET_ACCESS_KEY"] = cfg.SecretKey
		if cfg.Region != "" {
			secret["AWS_DEFAULT_REGION"] = cfg.Region
		}
	default:
		return repo, invalid("unknown backup target")
	}
	box, err := agentproto.SealBox(agentKey, agentproto.DBSecretContext(svc.ID, "repo"), mustJSON(secret))
	if err != nil {
		return repo, err
	}
	repo.Secret = box
	return repo, nil
}

// ---- helpers -----------------------------------------------------------------------------------

func contOf(svc db.DataService) string {
	if svc.Container != nil && *svc.Container != "" {
		return *svc.Container
	}
	return agentproto.DBContainerName(svc.Name)
}

// execTarget derives where db.create, db.drop, db.user, db.query, db.insights and db.cancel_query
// should run their client for svc: a Docker container on the server itself (target "", the container
// contOf resolves — every managed service, and an imported container), or an imported Proxmox LXC
// guest (target "guest", its vmid, guest_type "lxc", container always "" — the engine installed
// natively there; a guest running its engine in Docker is refused before this is ever built, since pct
// exec has no way to carry a password into a nested docker exec). A QEMU guest never reaches here:
// usable() refuses it before any of these six operations is ever built.
func execTarget(svc db.DataService) (target, vmid, guestType, container string) {
	w := deploy.WorkloadOfService(svc)
	if isImported(svc) && w.OriginKind == "guest" {
		return agentproto.TargetGuest, w.Origin.VMID, w.Origin.GuestType, ""
	}
	return "", "", "", contOf(svc)
}

func rootContext(serviceID string) string       { return "dbroot:" + serviceID }
func userContext(serviceID, user string) string { return "dbuser:" + serviceID + ":" + user }

// opID derives a stable operation id from an entity and a step, so a resumed action re-attaches.
func opID(entityID, step string) string {
	// "cloudy-db" is frozen: a new seed would give an action resumed across the upgrade new op ids.
	sum := sha256.Sum256([]byte("cloudy-db\n" + entityID + "\n" + step))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

const pwAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func randPassword(n int) string {
	b := make([]byte, n)
	for i := range b {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(pwAlphabet))))
		b[i] = pwAlphabet[k.Int64()]
	}
	return string(b)
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// auditExtra builds an audit detail map from the actor's exported fields (deploy.Actor.audit is
// unexported), so an MCP token id and via are recorded the same way as in P2.
func auditExtra(a Actor, extra map[string]any) map[string]any {
	if extra == nil {
		extra = map[string]any{}
	}
	if a.Via != "" && a.Via != "web" {
		extra["via"] = a.Via
	}
	if a.TokenID != "" {
		extra["mcp_token_id"] = a.TokenID
	}
	return extra
}

// privateBind is the address a data service publishes on: never the internet. On the demo target
// (docker-in-docker) the default docker bridge gateway is reachable by the app containers on the same
// host, so it is the default; a server with a real private NIC overrides it with HOSBAH_DB_BIND (or CLOUDY_DB_BIND).
func privateBind(serverID string) string {
	if v := strings.TrimSpace(brand.Getenv("DB_BIND")); v != "" {
		return v
	}
	return "172.17.0.1"
}

// deriveRepoPassword is a stable restic repository password per service, derived from the team DEK so
// it never has to be stored and is the same on every backup and restore of that service.
func deriveRepoPassword(dek []byte, serviceID string) string {
	// "cloudy-restic" is frozen: it is part of every existing repository's password.
	sum := sha256.Sum256(append(append([]byte("cloudy-restic\n"), dek...), []byte("\n"+serviceID)...))
	return hex.EncodeToString(sum[:])
}

// remoteRepo is an S3-compatible restic target (MinIO or real S3), configured out of band. Until it
// is, the UI shows "S3 not configured" and offers only the local target — no dead control.
type remoteRepo struct {
	Repo      string
	AccessKey string
	SecretKey string
	Region    string
}

func (s *Service) remoteConfig(target string) (remoteRepo, bool) {
	pfx := "S3_" // HOSBAH_S3_* (or CLOUDY_S3_*)
	if target == "minio" {
		pfx = "MINIO_"
	}
	cfg := remoteRepo{
		Repo:      brand.Getenv(pfx + "REPO"),
		AccessKey: brand.Getenv(pfx + "ACCESS_KEY"),
		SecretKey: brand.Getenv(pfx + "SECRET_KEY"),
		Region:    brand.Getenv(pfx + "REGION"),
	}
	return cfg, cfg.Repo != "" && cfg.AccessKey != "" && cfg.SecretKey != ""
}

// RemoteConfigured reports whether a non-local target is usable (for the UI to gate its controls).
func (s *Service) remoteConfigured(target string) bool {
	_, ok := s.remoteConfig(target)
	return ok
}

// RemoteConfigured is the exported check the API surfaces so the UI can hide S3 when it is not set up.
func (s *Service) RemoteConfigured(target string) bool { return s.remoteConfigured(target) }

func strptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func int64ptr(v int64) *int64        { return &v }
func timeptr(t time.Time) *time.Time { return &t }
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func derefInt(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// s3Vars expands an s3://key:secret@host:port/bucket URL into the variables S3 clients read
// (the AWS SDKs, Laravel's s3 disk, boto3), with path-style addressing for MinIO.
func s3Vars(u string) [][2]string {
	p, err := url.Parse(u)
	if err != nil || p.User == nil {
		return nil
	}
	secret, _ := p.User.Password()
	endpoint := "http://" + p.Host
	return [][2]string{
		{"AWS_ENDPOINT_URL", endpoint}, {"AWS_ENDPOINT", endpoint},
		{"AWS_ACCESS_KEY_ID", p.User.Username()}, {"AWS_SECRET_ACCESS_KEY", secret},
		{"AWS_BUCKET", strings.TrimPrefix(p.Path, "/")}, {"AWS_DEFAULT_REGION", "us-east-1"},
		{"AWS_USE_PATH_STYLE_ENDPOINT", "true"},
	}
}

// hostPort picks the private-address port a service is published on: the one it already has (a
// resumed provision), else the engine's usual port or the next one no other service on the server
// holds. A fixed port meant a second Postgres on a server could never start.
func (s *Service) hostPort(ctx context.Context, teamID, serverID string, svc db.DataService) int {
	if svc.Port != nil && *svc.Port > 0 {
		return int(*svc.Port)
	}
	used := map[int]bool{}
	_ = s.sys(ctx, teamID, func(q *db.Queries) error {
		others, err := q.ListDataServicesOfServer(ctx, db.ListDataServicesOfServerParams{ServerID: serverID, TeamID: teamID})
		for _, o := range others {
			if o.ID != svc.ID && o.Port != nil && (o.OriginKind == nil || *o.OriginKind != "guest") {
				used[int(*o.Port)] = true
			}
		}
		return err
	})
	return nextFreePort(agentproto.DBInternalPort(svc.Kind), used)
}

func nextFreePort(base int, used map[int]bool) int {
	for p := base; p < base+1000 && p <= 65535; p++ {
		if !used[p] {
			return p
		}
	}
	return base
}

func opsSpec(teamID, serverID, kind string, params []byte, actor Actor) ops.EnqueueSpec {
	createdBy := actor.UserID
	if createdBy == "" {
		createdBy = "via:" + actor.Via
	}
	return ops.EnqueueSpec{TeamID: teamID, ServerID: serverID, Kind: kind, Params: params, CreatedBy: createdBy}
}
