package dbsvc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// SourceImported marks a data service Hosbah found running on a server (not one it provisioned).
//
// An imported service becomes a normal one for Hosbah's database features (databases, users,
// backups, restore-tests, attach, reveal) once a member gives it credentials that db.probe accepted:
// they are sealed with the team DEK into root_sealed, exactly like a managed service's root, and
// re-sealed to the agent for each operation. Until then those features answer 409
// credentials_required. Power, stats, logs and destroy are the workload controls in internal/deploy.
//
// Supported: a Postgres, MySQL/MariaDB or Redis container (the engine's own client runs inside it
// through docker exec, logging in as the given user over TCP to 127.0.0.1). Not supported yet, and
// refused with 409 unsupported_origin: a database in a Proxmox guest (the hypervisor has no client for
// it — its credentials can be stored after a TCP check, but no database feature reaches it), MongoDB
// and MinIO found running, and a restore-test of an image that is not the engine's official one.
const SourceImported = deploy.SourceImported

// CodeCredentialsRequired: an imported service has no credentials yet.
const CodeCredentialsRequired = "credentials_required"

func isImported(svc db.DataService) bool { return svc.Source == SourceImported }

// HasCredentials reports whether Hosbah holds a credential for the service (always, for a managed one).
func HasCredentials(svc db.DataService) bool { return svc.RootSealed != nil && *svc.RootSealed != "" }

// usable decides whether the database features reach a service: every managed one, and an imported
// container of a supported engine that has credentials.
func usable(svc db.DataService) error {
	if !isImported(svc) {
		return nil
	}
	w := deploy.WorkloadOfService(svc)
	switch {
	case w.OriginKind == "guest":
		return deploy.Coded(http.StatusConflict, deploy.CodeUnsupportedOrigin, svc.Name+
			" runs in a Proxmox guest: databases, users and backups inside a guest are not supported yet (the hypervisor has no client for it)")
	case w.OriginKind != "container":
		return deploy.Coded(http.StatusConflict, deploy.CodeUnsupportedOrigin, svc.Name+" is not a container Hosbah can reach")
	case svc.Kind != "postgres" && svc.Kind != "mysql" && svc.Kind != "redis":
		return deploy.Coded(http.StatusConflict, deploy.CodeUnsupportedOrigin, "database features for an imported "+svc.Kind+" are not supported yet")
	case w.State == importer.StateMissing:
		return deploy.Coded(http.StatusConflict, deploy.CodeWorkloadMissing, svc.Name+" is no longer reported by its server")
	case !HasCredentials(svc):
		return deploy.Coded(http.StatusConflict, CodeCredentialsRequired, svc.Name+" is imported: give Hosbah its credentials first (POST …/credentials)")
	}
	return nil
}

// restorable additionally needs the engine's official image: the restore-test starts a throwaway
// from DBImage(kind, version), which must be what the service runs.
func restorable(svc db.DataService) error {
	if err := usable(svc); err != nil || !isImported(svc) {
		return err
	}
	w := deploy.WorkloadOfService(svc)
	if !agentproto.ValidDBVersion(svc.Version) || w.Origin.Image == "" || agentproto.DBImage(svc.Kind, svc.Version) != w.Origin.Image &&
		"docker.io/library/"+agentproto.DBImage(svc.Kind, svc.Version) != w.Origin.Image {
		return deploy.Coded(http.StatusConflict, deploy.CodeUnsupportedOrigin, "a restore-test needs the engine's official image; "+
			svc.Name+" runs "+w.Origin.Image+" (backups still work)")
	}
	return nil
}

// Credentials is what a member gives Hosbah for an imported service.
type Credentials struct {
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database,omitempty"`
}

// ProbeResult is what the probe said.
type ProbeResult = agentproto.DBProbeResult

// SetCredentials checks credentials against the running service through db.probe and, when they
// work, seals them into root_sealed. It runs its own transactions (the probe is awaited between them);
// the caller has already resolved the service and checked the member's role.
func (s *Service) SetCredentials(ctx context.Context, teamID string, actor Actor, svc db.DataService, in Credentials) (ProbeResult, error) {
	var res ProbeResult
	in.User, in.Database = strings.TrimSpace(in.User), strings.TrimSpace(in.Database)
	if !isImported(svc) {
		return res, deploy.Coded(http.StatusConflict, deploy.CodeNotImported, svc.Name+" is managed by Hosbah, which already holds its credentials")
	}
	w := deploy.WorkloadOfService(svc)
	switch {
	case svc.Kind != "postgres" && svc.Kind != "mysql" && svc.Kind != "redis":
		return res, deploy.Coded(http.StatusConflict, deploy.CodeUnsupportedOrigin, "credentials for an imported "+svc.Kind+" are not supported yet")
	case w.OriginKind != "container" && w.OriginKind != "guest":
		return res, deploy.Coded(http.StatusConflict, deploy.CodeUnsupportedOrigin, svc.Name+" is not a container or a guest")
	case in.Password == "" || len(in.Password) > 1024 || strings.ContainsAny(in.Password, "\x00\r\n"):
		return res, invalid("a password is required (one line, up to 1024 characters)")
	case svc.Kind != "redis" && in.User == "":
		return res, invalid("a username is required")
	case len(in.User) > 128 || strings.ContainsAny(in.User, "\x00\r\n\t "):
		return res, invalid("that is not a valid username")
	case in.Database != "" && !validDatabaseRef(in.Database):
		return res, invalid("that is not a valid database name")
	}
	var op db.Operation
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		if _, err := s.deploy.CheckWorkload(ctx, q, teamID, w); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	err = s.sys(ctx, teamID, func(q *db.Queries) error {
		k, err := q.ServerTeamKey(ctx, svc.ServerID)
		if err != nil || k.AgentPublicKey == nil {
			return conflict("that server's agent has not enrolled")
		}
		box, err := agentproto.SealBox(*k.AgentPublicKey, agentproto.DBSecretContext(svc.ID, "root"), mustJSON(credBox(in)))
		if err != nil {
			return err
		}
		p := agentproto.DBProbeParams{ServiceID: svc.ID, Label: svc.Name, Kind: svc.Kind, Credentials: box}
		if w.OriginKind == "container" {
			p.Target, p.Container = agentproto.TargetContainer, w.OriginKey
		} else {
			p.Target, p.Host, p.Port = agentproto.TargetGuest, derefStr(svc.Host), int(derefInt(svc.Port))
			if p.Port == 0 {
				p.Port = agentproto.DBInternalPort(svc.Kind)
			}
		}
		if err := p.Validate(); err != nil {
			return invalid(err.Error())
		}
		raw, _ := json.Marshal(p)
		op, _, err = s.ops.Enqueue(ctx, q, opsSpec(teamID, svc.ServerID, agentproto.KindDBProbe, raw, actor))
		return err
	})
	if err != nil {
		return res, err
	}
	done, err := s.deploy.AwaitResult(ctx, op, 60*time.Second, &res)
	if errors.Is(err, deploy.ErrStillRunning) {
		return res, deploy.Coded(http.StatusGatewayTimeout, "probe_timeout", "the server did not answer the credential check in time — is its agent connected? Nothing was stored")
	}
	if err != nil {
		var ae *deploy.ActionError
		if errors.As(err, &ae) && ae.Code == "operation_failed" {
			return res, deploy.Coded(http.StatusUnprocessableEntity, "credentials_rejected", ae.Msg+" — nothing was stored")
		}
		return res, err
	}
	_ = done
	if !res.OK {
		return res, deploy.Coded(http.StatusUnprocessableEntity, "credentials_rejected", "the credentials did not work: "+res.Detail)
	}
	err = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		dek, err := s.ops.TeamDEK(ctx, q, teamID)
		if err != nil {
			return err
		}
		sealed, err := crypto.Seal(dek, teamID, rootContext(svc.ID), mustJSON(credBox(in)))
		if err != nil {
			return err
		}
		if err := q.SetImportedDataServiceCredentials(ctx, db.SetImportedDataServiceCredentialsParams{RootSealed: sealed, ID: svc.ID, TeamID: teamID}); err != nil {
			return err
		}
		return tenancy.Record(ctx, q, teamID, actor.UserID, "dataservice.credentials_set", "data_service", svc.ID, auditExtra(actor, map[string]any{
			"name": svc.Name, "user": in.User, "verified": res.Verified, "operation": op.ID}))
	})
	return res, err
}

func credBox(in Credentials) map[string]string {
	m := map[string]string{"user": in.User, "password": in.Password}
	if in.Database != "" {
		m["database"] = in.Database
	}
	return m
}

func validDatabaseRef(s string) bool {
	if len(s) > 63 {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// importedRoot opens an imported service's sealed credentials ({"user","password","database"}).
func importedRoot(dek []byte, teamID string, svc db.DataService) (map[string]string, error) {
	if !HasCredentials(svc) {
		return nil, deploy.Coded(http.StatusConflict, CodeCredentialsRequired, svc.Name+" has no credentials")
	}
	pt, err := crypto.Open(dek, teamID, rootContext(svc.ID), *svc.RootSealed)
	if err != nil {
		return nil, errors.New("the stored credentials could not be opened")
	}
	m := map[string]string{}
	if err := json.Unmarshal(pt, &m); err != nil {
		return nil, errors.New("the stored credentials are malformed")
	}
	return m, nil
}

// forgetImported deletes only Hosbah's record of an imported service — no operation reaches the
// server — and leaves a tombstone so the next discovery does not bring it back.
func (s *Service) forgetImported(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService) error {
	key := ""
	if svc.OriginKey != nil {
		key = *svc.OriginKey
	}
	createdBy := actor.UserID
	if createdBy == "" {
		createdBy = "via:" + actor.Via
	}
	if key != "" {
		if err := q.IgnoreImported(ctx, db.IgnoreImportedParams{TeamID: teamID, ServerID: svc.ServerID, Kind: "data_service",
			OriginKey: key, Name: svc.Name, CreatedBy: createdBy}); err != nil {
			return err
		}
	}
	if n, err := q.DeleteDataServiceInTeam(ctx, db.DeleteDataServiceInTeamParams{ID: svc.ID, TeamID: teamID}); err != nil || n == 0 {
		return notFound("data service")
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "dataservice.import_forgotten", "data_service", svc.ID, auditExtra(actor, map[string]any{
		"name": svc.Name, "origin_key": key, "note": "record only; nothing on the server was changed"}))
}

// CheckRestorable answers, before a backup is even looked up, whether a restore-test can reach the
// service (the REST route asks first, so the refusal does not depend on the backup id).
func CheckRestorable(svc db.DataService) error { return restorable(svc) }
