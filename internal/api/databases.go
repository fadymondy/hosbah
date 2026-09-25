package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ---- contract ------------------------------------------------------------------------------------

// DataServiceOut is a managed data service. Never the sealed root credential.
type DataServiceOut struct {
	ID                  string           `json:"id"`
	Name                string           `json:"name"`
	Kind                string           `json:"kind" enum:"postgres,mysql,redis,minio,mongodb" doc:"mongodb only for imported services"`
	Version             string           `json:"version"`
	Status              string           `json:"status" enum:"new,provisioning,running,failed,stopped"`
	ServerID            string           `json:"server_id"`
	Host                *string          `json:"host"`
	Port                *int64           `json:"port"`
	Error               *string          `json:"error"`
	Container           *string          `json:"container" doc:"The container name (managed, or an imported container)"`
	Source              string           `json:"source" enum:"managed,imported" doc:"imported: found running on the server; controllable (power, stats, logs, destroy) and, once it has credentials, a normal database"`
	OriginKind          *string          `json:"origin_kind" doc:"imported only: container or guest"`
	Origin              *importer.Origin `json:"origin" doc:"imported only: what discovery reported"`
	ImportedState       *string          `json:"imported_state" doc:"imported only: running, stopped or missing (no longer discovered)"`
	LastSeenAt          *time.Time       `json:"last_seen_at" doc:"imported only: when discovery last reported it"`
	HasCredentials      bool             `json:"has_credentials" doc:"Whether Hosbah holds credentials for it (always for a managed service). An imported one gets them through POST …/credentials; until then databases, users and backups answer 409 credentials_required."`
	CredentialsRequired bool             `json:"credentials_required" doc:"Whether the credentials form must be filled in before databases, users and backups work. False for a managed service and for an imported native Proxmox LXC guest (it authenticates as the matching OS user instead — no stored credential needed), even when has_credentials is also false there."`
	CreatedAt           time.Time        `json:"created_at"`
}

// DatabaseOut / DatabaseUserOut / BackupOut / ScheduleOut are the detail page's rows.
type DatabaseOut struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Status string  `json:"status"`
	Error  *string `json:"error"`
}
type DatabaseUserOut struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Grants     string    `json:"grants" enum:"all,readwrite,readonly,none"`
	Status     string    `json:"status"`
	Error      *string   `json:"error"`
	DatabaseID *string   `json:"database_id"`
	// UpdatedAt (MH-580 J7) doubles as "last rotated at": every password rotation bumps it
	// (SetDatabaseUserPasswordSealed), and so does creation. The web card warns once this is more
	// than 90 days old.
	UpdatedAt time.Time `json:"updated_at"`
}
type BackupOut struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind" enum:"manual,scheduled"`
	Status          string     `json:"status" enum:"pending,running,succeeded,failed"`
	Target          string     `json:"target" enum:"local,minio,s3"`
	SizeBytes       *int64     `json:"size_bytes"`
	SnapshotID      *string    `json:"snapshot_id"`
	RestoreStatus   *string    `json:"restore_status"`
	RestoreError    *string    `json:"restore_error"`
	Error           *string    `json:"error"`
	TakenAt         *time.Time `json:"taken_at"`
	RestoreTestedAt *time.Time `json:"restore_tested_at"`
	CreatedAt       time.Time  `json:"created_at"`
}
type ScheduleOut struct {
	Cron        string     `json:"cron"`
	Retention   int64      `json:"retention"`
	Target      string     `json:"target" enum:"local,minio,s3"`
	RestoreTest bool       `json:"restore_test"`
	Paused      bool       `json:"paused"`
	NextRunAt   *time.Time `json:"next_run_at"`
	LastRunAt   *time.Time `json:"last_run_at"`
}

// DataServiceDetailOut is the service page.
type DataServiceDetailOut struct {
	Service         DataServiceOut    `json:"service"`
	Databases       []DatabaseOut     `json:"databases"`
	Users           []DatabaseUserOut `json:"users"`
	Backups         []BackupOut       `json:"backups"`
	Schedule        *ScheduleOut      `json:"schedule"`
	RemoteAvailable bool              `json:"remote_available" doc:"Whether an S3/MinIO backup target is configured; when false the UI offers only the local target."`
}

func toDataServiceOut(s db.DataService) DataServiceOut {
	return DataServiceOut{ID: s.ID, Name: s.Name, Kind: s.Kind, Version: s.Version, Status: s.Status,
		ServerID: s.ServerID, Host: s.Host, Port: s.Port, Error: s.Error, CreatedAt: s.CreatedAt, Container: s.Container,
		Source: s.Source, OriginKind: s.OriginKind, Origin: originOf(s.Origin), ImportedState: s.ImportedState, LastSeenAt: s.LastSeenAt,
		HasCredentials: dbsvc.HasCredentials(s), CredentialsRequired: dbsvc.CredentialsRequired(s)}
}
func toDatabaseUserOut(u db.DatabaseUser) DatabaseUserOut {
	return DatabaseUserOut{ID: u.ID, Name: u.Name, Grants: u.Grants, Status: u.Status, Error: u.Error, DatabaseID: u.DatabaseID, UpdatedAt: u.UpdatedAt}
}
func toBackupOut(b db.Backup) BackupOut {
	return BackupOut{ID: b.ID, Kind: b.Kind, Status: b.Status, Target: b.Target, SizeBytes: b.SizeBytes,
		SnapshotID: b.SnapshotID, RestoreStatus: b.RestoreStatus, RestoreError: b.RestoreError, Error: b.Error,
		TakenAt: b.TakenAt, RestoreTestedAt: b.RestoreTestedAt, CreatedAt: b.CreatedAt}
}

// ServicePath and BackupPath address a service (and a backup) under a team.
type ServicePath struct {
	TeamPath
	Service string `path:"service" maxLength:"64"`
}
type BackupPath struct {
	ServicePath
	Backup string `path:"backup" maxLength:"64"`
}

// DatabasePath / TablePath address a database (and a table — optionally schema-qualified for
// Postgres, "schema.table") under a service, for the table browser and SQL console (MH-574 F2).
type DatabasePath struct {
	ServicePath
	Database string `path:"database" maxLength:"64"`
}
type TablePath struct {
	DatabasePath
	Table string `path:"table" maxLength:"130" doc:"Table name, or schema.table for Postgres."`
}

// ColumnOut / TableOut / QueryResultOut are the table browser and SQL console's shapes.
type ColumnOut struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}
type TableOut struct {
	Schema       string `json:"schema,omitempty"`
	Name         string `json:"name"`
	RowsEstimate int64  `json:"rows_estimate"`
	SizeBytes    int64  `json:"size_bytes"`
}
type QueryResultOut struct {
	Columns    []ColumnOut `json:"columns"`
	Rows       [][]*string `json:"rows"`
	RowCount   int         `json:"row_count"`
	Truncated  bool        `json:"truncated" doc:"Set when the row count or the total result size hit their cap."`
	DurationMS int64       `json:"duration_ms"`
}

func toColumnOuts(cols []agentproto.DBQueryColumn) []ColumnOut {
	out := make([]ColumnOut, len(cols))
	for i, c := range cols {
		out[i] = ColumnOut{Name: c.Name, Type: c.Type}
	}
	return out
}
func toTableOuts(tabs []agentproto.DBTableInfo) []TableOut {
	out := make([]TableOut, len(tabs))
	for i, t := range tabs {
		out[i] = TableOut{Schema: t.Schema, Name: t.Name, RowsEstimate: t.RowsEstimate, SizeBytes: t.SizeBytes}
	}
	return out
}
func toQueryResultOut(r agentproto.DBQueryResult) QueryResultOut {
	rows := r.Rows
	if rows == nil {
		rows = [][]*string{}
	}
	return QueryResultOut{Columns: toColumnOuts(r.Columns), Rows: rows, RowCount: r.RowCount, Truncated: r.Truncated, DurationMS: r.DurationMS}
}

// ---- plumbing ------------------------------------------------------------------------------------

func (d *deps) dbs() *dbsvc.Service {
	if d.a == nil {
		return nil
	}
	s, _ := d.a.DBSvc.(*dbsvc.Service)
	return s
}

// dbErr maps a dbsvc.Error to its HTTP status.
func dbErr(err error, fallback string) error {
	if err == nil {
		return nil
	}
	if ie := asImported(err); ie != nil {
		return ie
	}
	var de *dbsvc.Error
	if errors.As(err, &de) {
		switch de.Status {
		case http.StatusNotFound:
			return huma.Error404NotFound(de.Message)
		case http.StatusConflict:
			return huma.Error409Conflict(de.Message)
		case http.StatusUnprocessableEntity:
			return huma.Error422UnprocessableEntity(de.Message)
		case http.StatusForbidden:
			return huma.Error403Forbidden(de.Message)
		}
	}
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	return huma.Error500InternalServerError(fallback)
}

func serviceIn(ctx context.Context, q *db.Queries, m *tenancy.Membership, ref string) (db.DataService, error) {
	svc, err := dbsvc.ServiceByRef(ctx, q, m.TeamID, ref)
	if err != nil {
		return db.DataService{}, errNotFound
	}
	return svc, nil
}

// ---- routes --------------------------------------------------------------------------------------

func (d *deps) registerDatabases(api huma.API) {
	tags := []string{"Databases"}

	type dsListOut struct {
		Body struct {
			Services []DataServiceOut `json:"services"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-data-services", Method: http.MethodGet, Path: "/api/teams/{team}/data-services",
		Summary: "The team's managed data services", Tags: tags,
	}, func(ctx context.Context, in *TeamPath) (*dsListOut, error) {
		out := &dsListOut{}
		out.Body.Services = []DataServiceOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListDataServicesOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list data services")
			}
			for _, r := range rows {
				out.Body.Services = append(out.Body.Services, toDataServiceOut(r))
			}
			return nil
		})
		return out, err
	})

	type dsCreateOut struct{ Body DataServiceOut }
	huma.Register(api, huma.Operation{OperationID: "create-data-service", Method: http.MethodPost, Path: "/api/teams/{team}/data-services",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Provision a Postgres, MySQL or Redis service on one of the team's servers (owner or admin)",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body struct {
			Name    string `json:"name" minLength:"1" maxLength:"40" doc:"Lowercase letters, digits and dashes; unique in the team."`
			Server  string `json:"server" minLength:"1" maxLength:"64" doc:"Server id or name."`
			Kind    string `json:"kind" enum:"postgres,mysql,redis,minio"`
			Version string `json:"version,omitempty" maxLength:"32" doc:"Image tag; defaults to a current version."`
		}
	}) (*dsCreateOut, error) {
		out := &dsCreateOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := d.dbs().CreateService(ctx, q, m.TeamID, webActor(id), dbsvc.CreateInput{Name: in.Body.Name, Server: in.Body.Server, Kind: in.Body.Kind, Version: in.Body.Version})
			if err != nil {
				return dbErr(err, "could not create the data service")
			}
			out.Body = toDataServiceOut(svc)
			return nil
		})
		return out, err
	})

	type dsDetailOut struct{ Body DataServiceDetailOut }
	huma.Register(api, huma.Operation{OperationID: "get-data-service", Method: http.MethodGet, Path: "/api/teams/{team}/data-services/{service}",
		Summary: "One data service: its databases, users, backups and schedule", Tags: tags,
	}, func(ctx context.Context, in *ServicePath) (*dsDetailOut, error) {
		out := &dsDetailOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			out.Body = d.dataServiceDetail(ctx, q, m, svc)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-data-service", Method: http.MethodDelete, Path: "/api/teams/{team}/data-services/{service}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Delete a data service, removing its container and volume from the server (owner or admin). An imported one: only Hosbah's " +
			"record goes (tombstoned), unless ?destroy=true with confirm = its name — then the real container or guest is destroyed (202 with " +
			"the operation) and the record goes once the agent reports it gone.",
	}, func(ctx context.Context, in *struct {
		ServicePath
		Destroy bool `query:"destroy" doc:"Imported services: destroy the real workload, then the record."`
		Body    *struct {
			Confirm string `json:"confirm,omitempty" maxLength:"64" doc:"With destroy: the service's name, exactly."`
			Purge   bool   `json:"purge,omitempty" doc:"With destroy: pvesh --purge for a guest; a container's anonymous volumes too."`
		}
	}) (*workloadOpOut, error) {
		out := &workloadOpOut{Status: http.StatusNoContent}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			if in.Destroy {
				if in.Body == nil || in.Body.Confirm != svc.Name {
					return huma.Error422UnprocessableEntity("confirm must equal the service's name — nothing was destroyed")
				}
				op, err := d.deploys().Destroy(ctx, q, m.TeamID, webActor(id), deploy.WorkloadOfService(svc), in.Body.Purge)
				if err != nil {
					return actionErr(err, "could not destroy the workload")
				}
				out.Status, out.Body = http.StatusAccepted, &workloadOpBody{Operation: toOperationOut(op)}
				return nil
			}
			return dbErr(d.dbs().DeleteService(ctx, q, m.TeamID, webActor(id), svc), "could not delete the data service")
		})
		return out, err
	})

	type dsDbCreatedOut struct {
		Body struct {
			Database DatabaseOut     `json:"database"`
			User     DatabaseUserOut `json:"user"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "create-database", Method: http.MethodPost, Path: "/api/teams/{team}/data-services/{service}/databases",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Create a database and a user with grants on a Postgres/MySQL service (owner or admin)",
	}, func(ctx context.Context, in *struct {
		ServicePath
		Body struct {
			Name   string `json:"name" minLength:"1" maxLength:"63" doc:"Lower snake_case."`
			User   string `json:"user,omitempty" maxLength:"63" doc:"Defaults to the database name."`
			Grants string `json:"grants,omitempty" enum:"all,readwrite,readonly"`
		}
	}) (*dsDbCreatedOut, error) {
		out := &dsDbCreatedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			dbRow, userRow, err := d.dbs().CreateDatabaseAndUser(ctx, q, m.TeamID, webActor(id), svc, in.Body.Name, in.Body.User, in.Body.Grants)
			if err != nil {
				return dbErr(err, "could not create the database")
			}
			out.Body.Database = DatabaseOut{ID: dbRow.ID, Name: dbRow.Name, Status: dbRow.Status, Error: dbRow.Error}
			out.Body.User = toDatabaseUserOut(userRow)
			return nil
		})
		return out, err
	})

	// ---- standalone database users (MH-574 F1): list is on the service detail; create, rotate,
	// delete, grant and revoke are each a synchronous agent call, so the response either carries the
	// plaintext password (once, create/rotate) or the settled row (grant/revoke) — never a queued state.
	type dsUserCreatedOut struct {
		Body struct {
			User     DatabaseUserOut `json:"user"`
			Password string          `json:"password" doc:"Shown once — Hosbah stores only the sealed form."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "create-database-user", Method: http.MethodPost, Path: "/api/teams/{team}/data-services/{service}/users",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a user to an existing database/bucket, with a grant preset (owner or admin; the password is returned once)",
	}, func(ctx context.Context, in *struct {
		ServicePath
		Body struct {
			Database string `json:"database" minLength:"1" maxLength:"64" doc:"Database or bucket name or id."`
			User     string `json:"user" minLength:"1" maxLength:"63" doc:"Lower snake_case."`
			Grants   string `json:"grants,omitempty" enum:"all,readwrite,readonly"`
		}
	}) (*dsUserCreatedOut, error) {
		out := &dsUserCreatedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityRevealSecrets, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			row, pw, err := d.dbs().CreateUser(ctx, q, m.TeamID, webActor(id), svc, in.Body.Database, in.Body.User, in.Body.Grants)
			if err != nil {
				return dbErr(err, "could not create the user")
			}
			out.Body.User, out.Body.Password = toDatabaseUserOut(row), pw
			return nil
		})
		return out, err
	})

	type UserPath struct {
		ServicePath
		User string `path:"user" maxLength:"64"`
	}
	type dsUserRotatedOut struct {
		Body struct {
			User     DatabaseUserOut `json:"user"`
			Password string          `json:"password" doc:"Shown once — Hosbah stores only the sealed form."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "rotate-database-user-password", Method: http.MethodPost, Path: "/api/teams/{team}/data-services/{service}/users/{user}/rotate",
		Tags: tags, Summary: "Rotate a database user's password (owner or admin; the new password is returned once)",
	}, func(ctx context.Context, in *UserPath) (*dsUserRotatedOut, error) {
		out := &dsUserRotatedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityRevealSecrets, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			row, pw, err := d.dbs().RotateUserPassword(ctx, q, m.TeamID, webActor(id), svc, in.User)
			if err != nil {
				return dbErr(err, "could not rotate the user's password")
			}
			out.Body.User, out.Body.Password = toDatabaseUserOut(row), pw
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-database-user", Method: http.MethodDelete, Path: "/api/teams/{team}/data-services/{service}/users/{user}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Delete a database user (owner or admin; confirm must be its name). Never the service's own root credential — that is not a user row.",
	}, func(ctx context.Context, in *struct {
		UserPath
		Body struct {
			Confirm string `json:"confirm" minLength:"1" maxLength:"63"`
		}
	}) (*struct{}, error) {
		return nil, d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			return dbErr(d.dbs().DeleteUser(ctx, q, m.TeamID, webActor(id), svc, in.User, in.Body.Confirm), "could not delete the user")
		})
	})

	type dsUserOut struct{ Body DatabaseUserOut }
	huma.Register(api, huma.Operation{OperationID: "grant-database-user", Method: http.MethodPost, Path: "/api/teams/{team}/data-services/{service}/users/{user}/grant",
		Tags: tags, Summary: "Grant a preset (all/readwrite/readonly) to a database user on one database (owner or admin)",
	}, func(ctx context.Context, in *struct {
		UserPath
		Body struct {
			Database string `json:"database,omitempty" maxLength:"64" doc:"Defaults to the database the user was created against."`
			Grants   string `json:"grants" enum:"all,readwrite,readonly"`
		}
	}) (*dsUserOut, error) {
		out := &dsUserOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			row, err := d.dbs().GrantUser(ctx, q, m.TeamID, webActor(id), svc, in.User, in.Body.Database, in.Body.Grants)
			if err != nil {
				return dbErr(err, "could not apply the grants")
			}
			out.Body = toDatabaseUserOut(row)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "revoke-database-user", Method: http.MethodPost, Path: "/api/teams/{team}/data-services/{service}/users/{user}/revoke",
		Tags: tags, Summary: "Revoke a database user's privileges on one database, leaving the login in place (owner or admin)",
	}, func(ctx context.Context, in *struct {
		UserPath
		Body struct {
			Database string `json:"database,omitempty" maxLength:"64" doc:"Defaults to the database the user was created against."`
		}
	}) (*dsUserOut, error) {
		out := &dsUserOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			row, err := d.dbs().RevokeUser(ctx, q, m.TeamID, webActor(id), svc, in.User, in.Body.Database)
			if err != nil {
				return dbErr(err, "could not revoke the grants")
			}
			out.Body = toDatabaseUserOut(row)
			return nil
		})
		return out, err
	})

	type dsAttachOut struct {
		Body struct {
			Variable string `json:"variable" doc:"The env var injected (DATABASE_URL or REDIS_URL). Its value is sealed and applies on the next deploy or restart."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "attach-data-service", Method: http.MethodPost, Path: "/api/teams/{team}/data-services/{service}/attach",
		Tags: tags, Summary: "Attach a data service to an app, injecting a sealed DATABASE_URL/REDIS_URL (owner or admin)",
	}, func(ctx context.Context, in *struct {
		ServicePath
		Body struct {
			App  string `json:"app" minLength:"1" maxLength:"64" doc:"App id or name."`
			User string `json:"user,omitempty" maxLength:"64" doc:"Database user id or name (Postgres/MySQL); the first user by default."`
		}
	}) (*dsAttachOut, error) {
		out := &dsAttachOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			a, err := appIn(ctx, q, m, in.Body.App)
			if err != nil {
				return err
			}
			name, err := d.dbs().Attach(ctx, q, m.TeamID, webActor(id), svc, a, in.Body.User)
			if err != nil {
				return dbErr(err, "could not attach the data service")
			}
			out.Body.Variable = name
			return nil
		})
		return out, err
	})

	type dsRevealOut struct {
		Body struct {
			URL string `json:"url"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "reveal-data-service-url", Method: http.MethodPost, Path: "/api/teams/{team}/data-services/{service}/reveal-url",
		Tags: tags, Summary: "Reveal a data service's connection string (audited; owner or admin)",
	}, func(ctx context.Context, in *struct {
		ServicePath
		Body struct {
			User string `json:"user,omitempty" maxLength:"64"`
		}
	}) (*dsRevealOut, error) {
		out := &dsRevealOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityRevealSecrets, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			url, err := d.dbs().RevealURL(ctx, q, m.TeamID, webActor(id), svc, in.Body.User)
			if err != nil {
				return dbErr(err, "could not reveal the connection string")
			}
			out.Body.URL = url
			return nil
		})
		return out, err
	})

	// ---- backups ----
	type dsBackupsOut struct {
		Body struct {
			Backups []BackupOut `json:"backups"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-backups", Method: http.MethodGet, Path: "/api/teams/{team}/data-services/{service}/backups",
		Summary: "A data service's backups, newest first", Tags: tags,
	}, func(ctx context.Context, in *ServicePath) (*dsBackupsOut, error) {
		out := &dsBackupsOut{}
		out.Body.Backups = []BackupOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			rows, err := q.ListBackupsOfService(ctx, db.ListBackupsOfServiceParams{DataServiceID: svc.ID, TeamID: m.TeamID, Limit: 100})
			if err != nil {
				return huma.Error500InternalServerError("could not list backups")
			}
			for _, r := range rows {
				out.Body.Backups = append(out.Body.Backups, toBackupOut(r))
			}
			return nil
		})
		return out, err
	})

	type dsBackupOut struct{ Body BackupOut }
	huma.Register(api, huma.Operation{OperationID: "backup-now", Method: http.MethodPost, Path: "/api/teams/{team}/data-services/{service}/backups",
		DefaultStatus: http.StatusCreated, Tags: tags, Summary: "Back up a data service now (owner or admin)",
	}, func(ctx context.Context, in *struct{ ServicePath }) (*dsBackupOut, error) {
		out := &dsBackupOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			b, err := d.dbs().Backup(ctx, q, m.TeamID, webActor(id), svc, "manual")
			if err != nil {
				return dbErr(err, "could not start the backup")
			}
			out.Body = toBackupOut(b)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "restore-test-backup", Method: http.MethodPost, Path: "/api/teams/{team}/data-services/{service}/backups/{backup}/restore-test",
		Tags: tags, Summary: "Restore a backup into a throwaway container and verify it (owner or admin)",
	}, func(ctx context.Context, in *BackupPath) (*struct{}, error) {
		return nil, d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			if err := dbsvc.CheckRestorable(svc); err != nil {
				return dbErr(err, "could not start the restore-test")
			}
			b, err := q.BackupInTeam(ctx, db.BackupInTeamParams{ID: in.Backup, TeamID: m.TeamID})
			if err != nil || b.DataServiceID != svc.ID {
				return errNotFound
			}
			return dbErr(d.dbs().RestoreTest(ctx, q, m.TeamID, webActor(id), svc, b), "could not start the restore-test")
		})
	})

	type dsScheduleOut struct{ Body ScheduleOut }
	huma.Register(api, huma.Operation{OperationID: "set-backup-schedule", Method: http.MethodPut, Path: "/api/teams/{team}/data-services/{service}/schedule",
		Tags: tags, Summary: "Create or update a data service's backup schedule (owner or admin)",
	}, func(ctx context.Context, in *struct {
		ServicePath
		Body struct {
			Cron        string `json:"cron" minLength:"1" maxLength:"100" doc:"Five-field cron, e.g. 0 3 * * *."`
			Retention   int    `json:"retention,omitempty" minimum:"1" maximum:"3650"`
			Target      string `json:"target,omitempty" enum:"local,minio,s3"`
			RestoreTest bool   `json:"restore_test,omitempty"`
			Paused      bool   `json:"paused,omitempty"`
		}
	}) (*dsScheduleOut, error) {
		out := &dsScheduleOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			row, err := d.dbs().SetSchedule(ctx, q, m.TeamID, webActor(id), svc, dbsvc.ScheduleInput{
				Cron: in.Body.Cron, Retention: in.Body.Retention, Target: in.Body.Target, RestoreTest: in.Body.RestoreTest, Paused: in.Body.Paused})
			if err != nil {
				return dbErr(err, "could not set the schedule")
			}
			out.Body = toScheduleOut(row)
			return nil
		})
		return out, err
	})

	// ---- table browser and SQL console (MH-574 F2): postgres/mysql only, owner/admin only
	// (AbilityRevealSecrets — the same tier F1 uses for a user's password: table contents can hold
	// secrets). write=true is the same ability; there is no broader tier to further gate it with.
	type dsTablesOut struct {
		Body struct {
			Tables []TableOut `json:"tables"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-database-tables", Method: http.MethodGet,
		Path: "/api/teams/{team}/data-services/{service}/databases/{database}/tables", Tags: tags,
		Summary: "A database's tables: schema, name, estimated row count and size (owner or admin)",
	}, func(ctx context.Context, in *DatabasePath) (*dsTablesOut, error) {
		out := &dsTablesOut{}
		out.Body.Tables = []TableOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityRevealSecrets, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			res, err := d.dbs().Query(ctx, q, m.TeamID, webActor(id), svc, dbsvc.QueryInput{Database: in.Database, Mode: "tables"})
			if err != nil {
				return dbErr(err, "could not list tables")
			}
			out.Body.Tables = toTableOuts(res.Tables)
			return nil
		})
		return out, err
	})

	type dsColumnsOut struct {
		Body struct {
			Columns []ColumnOut `json:"columns"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-table-columns", Method: http.MethodGet,
		Path: "/api/teams/{team}/data-services/{service}/databases/{database}/tables/{table}/columns", Tags: tags,
		Summary: "A table's columns: name and type (owner or admin)",
	}, func(ctx context.Context, in *TablePath) (*dsColumnsOut, error) {
		out := &dsColumnsOut{}
		out.Body.Columns = []ColumnOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityRevealSecrets, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			res, err := d.dbs().Query(ctx, q, m.TeamID, webActor(id), svc, dbsvc.QueryInput{Database: in.Database, Mode: "columns", Table: in.Table})
			if err != nil {
				return dbErr(err, "could not list columns")
			}
			out.Body.Columns = toColumnOuts(res.Columns)
			return nil
		})
		return out, err
	})

	type dsRowsOut struct{ Body QueryResultOut }
	huma.Register(api, huma.Operation{OperationID: "list-table-rows", Method: http.MethodGet,
		Path: "/api/teams/{team}/data-services/{service}/databases/{database}/tables/{table}/rows", Tags: tags,
		Summary: "A page of a table's rows, read-only (owner or admin)",
	}, func(ctx context.Context, in *struct {
		TablePath
		Limit  int `query:"limit" minimum:"1" maximum:"500" doc:"Default 100."`
		Offset int `query:"offset" minimum:"0"`
	}) (*dsRowsOut, error) {
		out := &dsRowsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityRevealSecrets, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			res, err := d.dbs().Query(ctx, q, m.TeamID, webActor(id), svc, dbsvc.QueryInput{
				Database: in.Database, Mode: "rows", Table: in.Table, Limit: in.Limit, Offset: in.Offset})
			if err != nil {
				return dbErr(err, "could not list rows")
			}
			out.Body = toQueryResultOut(res)
			return nil
		})
		return out, err
	})

	type dsQueryOut struct{ Body QueryResultOut }
	huma.Register(api, huma.Operation{OperationID: "run-database-query", Method: http.MethodPost,
		Path: "/api/teams/{team}/data-services/{service}/databases/{database}/query", Tags: tags,
		Summary: "Run one SQL statement (owner or admin). Read-only by default (rolled back); write=true commits and is audited with the statement text.",
	}, func(ctx context.Context, in *struct {
		DatabasePath
		Body struct {
			SQL   string `json:"sql" minLength:"1" maxLength:"65536"`
			Write bool   `json:"write,omitempty" doc:"Commit instead of rolling back. Destructive — confirm in the UI before sending."`
		}
	}) (*dsQueryOut, error) {
		out := &dsQueryOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityRevealSecrets, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			res, err := d.dbs().Query(ctx, q, m.TeamID, webActor(id), svc, dbsvc.QueryInput{
				Database: in.Database, Mode: "sql", SQL: in.Body.SQL, Write: in.Body.Write})
			if err != nil {
				return dbErr(err, "could not run the statement")
			}
			out.Body = toQueryResultOut(res)
			return nil
		})
		return out, err
	})

	registerDataServiceInsights(d, api, tags)
	registerDataServiceMetrics(d, api, tags)
}

// ---- DB metrics and slow queries (MH-575 F6) --------------------------------------------------

// DBSizeInfoOut / DBLongQueryOut / DBTopStatementOut / DBInsightsOut mirror agentproto's shapes.
type DBSizeInfoOut struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
}
type DBLongQueryOut struct {
	PID             string  `json:"pid"`
	DurationSeconds float64 `json:"duration_seconds"`
	State           string  `json:"state,omitempty"`
	Query           string  `json:"query,omitempty"`
	Database        string  `json:"database,omitempty"`
	User            string  `json:"user,omitempty"`
}
type DBTopStatementOut struct {
	Query       string  `json:"query"`
	Calls       int64   `json:"calls"`
	TotalTimeMS float64 `json:"total_time_ms"`
	MeanTimeMS  float64 `json:"mean_time_ms"`
	Rows        int64   `json:"rows"`
}
type DBInsightsOut struct {
	Kind                    string              `json:"kind"`
	Connections             int                 `json:"connections"`
	MaxConnections          int                 `json:"max_connections"`
	ConnectionsByState      map[string]int      `json:"connections_by_state,omitempty"`
	XactCommit              int64               `json:"xact_commit,omitempty"`
	XactRollback            int64               `json:"xact_rollback,omitempty"`
	CacheHitRatio           float64             `json:"cache_hit_ratio"`
	Deadlocks               int64               `json:"deadlocks,omitempty"`
	TempBytes               int64               `json:"temp_bytes,omitempty"`
	Databases               []DBSizeInfoOut     `json:"databases,omitempty"`
	LongRunning             []DBLongQueryOut    `json:"long_running,omitempty"`
	LocksWaiting            int                 `json:"locks_waiting"`
	StatStatementsAvailable bool                `json:"stat_statements_available"`
	StatStatementsHint      string              `json:"stat_statements_hint,omitempty"`
	TopStatements           []DBTopStatementOut `json:"top_statements,omitempty"`
	ThreadsConnected        int64               `json:"threads_connected,omitempty"`
	ThreadsRunning          int64               `json:"threads_running,omitempty"`
	Questions               int64               `json:"questions,omitempty"`
	SlowQueries             int64               `json:"slow_queries,omitempty"`
	UptimeSeconds           int64               `json:"uptime_seconds,omitempty"`
	SlowQueryLog            string              `json:"slow_query_log,omitempty"`
}

func toDBInsightsOut(r agentproto.DBInsightsResult) DBInsightsOut {
	out := DBInsightsOut{
		Kind: r.Kind, Connections: r.Connections, MaxConnections: r.MaxConnections, ConnectionsByState: r.ConnectionsByState,
		XactCommit: r.XactCommit, XactRollback: r.XactRollback, CacheHitRatio: r.CacheHitRatio, Deadlocks: r.Deadlocks,
		TempBytes: r.TempBytes, LocksWaiting: r.LocksWaiting, StatStatementsAvailable: r.StatStatementsAvailable,
		StatStatementsHint: r.StatStatementsHint, ThreadsConnected: r.ThreadsConnected, ThreadsRunning: r.ThreadsRunning,
		Questions: r.Questions, SlowQueries: r.SlowQueries, UptimeSeconds: r.UptimeSeconds, SlowQueryLog: r.SlowQueryLog,
	}
	for _, d := range r.Databases {
		out.Databases = append(out.Databases, DBSizeInfoOut{Name: d.Name, SizeBytes: d.SizeBytes})
	}
	for _, lr := range r.LongRunning {
		out.LongRunning = append(out.LongRunning, DBLongQueryOut{
			PID: lr.PID, DurationSeconds: lr.DurationSeconds, State: lr.State, Query: lr.Query, Database: lr.Database, User: lr.User,
		})
	}
	for _, t := range r.TopStatements {
		out.TopStatements = append(out.TopStatements, DBTopStatementOut{
			Query: t.Query, Calls: t.Calls, TotalTimeMS: t.TotalTimeMS, MeanTimeMS: t.MeanTimeMS, Rows: t.Rows,
		})
	}
	return out
}

func registerDataServiceInsights(d *deps, api huma.API, tags []string) {
	type dsInsightsOut struct{ Body DBInsightsOut }
	huma.Register(api, huma.Operation{OperationID: "get-data-service-insights", Method: http.MethodGet,
		Path: "/api/teams/{team}/data-services/{service}/insights", Tags: tags,
		Summary: "DB metrics and slow queries: connections, cache hit ratio, long-running queries, locks and top statements (owner or admin). Postgres and MySQL only.",
	}, func(ctx context.Context, in *ServicePath) (*dsInsightsOut, error) {
		out := &dsInsightsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityRevealSecrets, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			res, err := d.dbs().Insights(ctx, q, m.TeamID, svc)
			if err != nil {
				return dbErr(err, "could not gather insights")
			}
			out.Body = toDBInsightsOut(res)
			return nil
		})
		return out, err
	})

	type dsCancelOut struct {
		Body struct {
			OK     bool   `json:"ok"`
			Detail string `json:"detail,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "cancel-data-service-query", Method: http.MethodPost,
		Path: "/api/teams/{team}/data-services/{service}/insights/cancel", Tags: tags,
		Summary: "Cancel a running backend/query (pg_cancel_backend / KILL QUERY). Owner or admin; confirm must equal the service's name. Audited.",
	}, func(ctx context.Context, in *struct {
		ServicePath
		Body struct {
			ID      int64  `json:"id" minimum:"1" doc:"The Postgres pid or the MySQL processlist Id, from insights' long_running list."`
			Confirm string `json:"confirm" minLength:"1" maxLength:"64" doc:"The service's name, exactly."`
		}
	}) (*dsCancelOut, error) {
		out := &dsCancelOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityRevealSecrets, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			if in.Body.Confirm != svc.Name {
				return huma.Error422UnprocessableEntity("confirm must equal the service's name — nothing was cancelled")
			}
			res, err := d.dbs().CancelQuery(ctx, q, m.TeamID, webActor(id), svc, in.Body.ID)
			if err != nil {
				return dbErr(err, "could not cancel the query")
			}
			out.Body.OK, out.Body.Detail = res.OK, res.Detail
			return nil
		})
		return out, err
	})
}

// registerDataServiceMetrics is F6's history charts: the same KPIs Insights shows live, sampled
// roughly once a minute (internal/dbsvc's sampler) and bucketed server-side like a server's own
// /metrics endpoint (internal/metrics). AbilityView, not AbilityRevealSecrets: unlike Insights, a
// bucket never carries query text or literal values a member typed — just aggregate numbers, the
// same sensitivity as a server's CPU/memory history.
func registerDataServiceMetrics(d *deps, api huma.API, tags []string) {
	type seriesIn struct {
		ServicePath
		Range string `query:"range" enum:"1h,6h,24h,7d,30d" default:"1h"`
	}
	type seriesOut struct{ Body dbsvc.MetricSeries }
	huma.Register(api, huma.Operation{OperationID: "get-data-service-metrics", Method: http.MethodGet,
		Path: "/api/teams/{team}/data-services/{service}/metrics", Tags: tags,
		Summary: "A data service's KPI history for a range, bucketed server-side (at most ~300 points): connections, cache hit ratio, size, slow-query count and transactions/queries per second. Postgres and MySQL only.",
	}, func(ctx context.Context, in *seriesIn) (*seriesOut, error) {
		r, err := rangeOf(in.Range)
		if err != nil {
			return nil, err
		}
		out := &seriesOut{}
		err = d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			svc, err := serviceIn(ctx, q, m, in.Service)
			if err != nil {
				return err
			}
			if out.Body, err = d.dbs().MetricsSeries(ctx, q, m.TeamID, svc.ID, r, time.Now()); err != nil {
				return huma.Error500InternalServerError("could not read metrics")
			}
			return nil
		})
		return out, err
	})
}

func toScheduleOut(s db.BackupSchedule) ScheduleOut {
	return ScheduleOut{Cron: s.Cron, Retention: s.Retention, Target: s.Target, RestoreTest: s.RestoreTest,
		Paused: s.Paused, NextRunAt: s.NextRunAt, LastRunAt: s.LastRunAt}
}

func (d *deps) dataServiceDetail(ctx context.Context, q *db.Queries, m *tenancy.Membership, svc db.DataService) DataServiceDetailOut {
	out := DataServiceDetailOut{Service: toDataServiceOut(svc), Databases: []DatabaseOut{}, Users: []DatabaseUserOut{}, Backups: []BackupOut{}}
	if dbrows, err := q.ListDatabasesOfService(ctx, db.ListDatabasesOfServiceParams{DataServiceID: svc.ID, TeamID: m.TeamID}); err == nil {
		for _, r := range dbrows {
			out.Databases = append(out.Databases, DatabaseOut{ID: r.ID, Name: r.Name, Status: r.Status, Error: r.Error})
		}
	}
	if urows, err := q.ListDatabaseUsersOfService(ctx, db.ListDatabaseUsersOfServiceParams{DataServiceID: svc.ID, TeamID: m.TeamID}); err == nil {
		for _, r := range urows {
			out.Users = append(out.Users, toDatabaseUserOut(r))
		}
	}
	if brows, err := q.ListBackupsOfService(ctx, db.ListBackupsOfServiceParams{DataServiceID: svc.ID, TeamID: m.TeamID, Limit: 50}); err == nil {
		for _, r := range brows {
			out.Backups = append(out.Backups, toBackupOut(r))
		}
	}
	if sch, err := q.BackupScheduleOfService(ctx, db.BackupScheduleOfServiceParams{DataServiceID: svc.ID, TeamID: m.TeamID}); err == nil {
		so := toScheduleOut(sch)
		out.Schedule = &so
	}
	if s := d.dbs(); s != nil {
		out.RemoteAvailable = s.RemoteConfigured("s3") || s.RemoteConfigured("minio")
	}
	return out
}
