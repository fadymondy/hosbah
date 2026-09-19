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
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ---- contract ------------------------------------------------------------------------------------

// workloadOpBody is an action queued on an imported workload: the operation (stream its log with the
// server's operation stream; it shows in the server's operation log, labelled with the record's name).
type workloadOpBody struct {
	Operation OperationOut `json:"operation"`
}

type workloadOpOut struct {
	Status int
	Body   *workloadOpBody
}

// WorkloadStatsOut is a reading, or null while the operation is still running (poll the operation).
type WorkloadStatsOut struct {
	Operation OperationOut              `json:"operation"`
	Stats     *agentproto.WorkloadStats `json:"stats"`
}

// SnapshotsOut is a guest's snapshots, or null while the operation is still running.
type SnapshotsOut struct {
	Operation OperationOut          `json:"operation"`
	Snapshots []agentproto.Snapshot `json:"snapshots"`
}

// SnapshotPath addresses one snapshot of an imported guest app.
type SnapshotPath struct {
	AppPath
	Snapshot string `path:"snapshot" maxLength:"40"`
}

// WaitQuery bounds how long a read waits for the agent.
type WaitQuery struct {
	Wait int `query:"wait" minimum:"0" maximum:"60" default:"20" doc:"Seconds to wait for the agent's answer (0 = return the queued operation at once)."`
}

// ---- plumbing ------------------------------------------------------------------------------------

// workloadOf resolves an app or a data service of the caller's team as a workload.
type workloadResolver func(ctx context.Context, q *db.Queries, m *tenancy.Membership) (deploy.Workload, error)

func appWorkload(ref string) workloadResolver {
	return func(ctx context.Context, q *db.Queries, m *tenancy.Membership) (deploy.Workload, error) {
		a, err := appIn(ctx, q, m, ref)
		if err != nil {
			return deploy.Workload{}, err
		}
		return deploy.WorkloadOfApp(a), nil
	}
}

func serviceWorkload(ref string) workloadResolver {
	return func(ctx context.Context, q *db.Queries, m *tenancy.Membership) (deploy.Workload, error) {
		svc, err := serviceIn(ctx, q, m, ref)
		if err != nil {
			return deploy.Workload{}, err
		}
		return deploy.WorkloadOfService(svc), nil
	}
}

// queue runs one workload action in the team transaction with the given ability.
func (d *deps) queue(ctx context.Context, team, ability string, resolve workloadResolver,
	fn func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, w deploy.Workload) (db.Operation, error)) (db.Operation, error) {
	var op db.Operation
	err := d.inTeam(ctx, team, ability, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
		w, err := resolve(ctx, q, m)
		if err != nil {
			return err
		}
		if op, err = fn(q, webActor(id), m, w); err != nil {
			return actionErr(err, "could not queue the operation")
		}
		return nil
	})
	return op, err
}

func accepted(op db.Operation, err error) (*workloadOpOut, error) {
	if err != nil {
		return nil, err
	}
	return &workloadOpOut{Status: http.StatusAccepted, Body: &workloadOpBody{Operation: toOperationOut(op)}}, nil
}

// awaitInto waits (after the queuing transaction committed) for the agent's answer.
func (d *deps) awaitInto(ctx context.Context, op db.Operation, wait int, out any) (db.Operation, bool, error) {
	done, err := d.deploys().AwaitResult(ctx, op, time.Duration(wait)*time.Second, out)
	if errors.Is(err, deploy.ErrStillRunning) {
		return op, false, nil
	}
	if err != nil {
		return done, false, actionErr(err, "the operation failed")
	}
	return done, true, nil
}

// ---- routes --------------------------------------------------------------------------------------

func (d *deps) registerWorkloads(api huma.API) {
	d.registerAppWorkloadControls(api)
	d.registerServiceWorkloadControls(api)
	d.registerGuestControls(api)
	d.registerImportedServiceRoutes(api)
}

// Bodies shared by the app and data-service routes.

type powerBody struct {
	Action string `json:"action" enum:"start,stop,shutdown,restart" doc:"stop: a guest's hard stop; shutdown: graceful; restart: a guest's reboot."`
}

type logsBody struct {
	Tail          int    `json:"tail,omitempty" minimum:"0" maximum:"2000" doc:"Lines of history (default 200)."`
	Since         string `json:"since,omitempty" maxLength:"40" doc:"Only lines since: 15m, 2h, or an RFC 3339 time."`
	FollowSeconds int    `json:"follow_seconds,omitempty" minimum:"0" maximum:"300" doc:"Then follow for this long (default 0)."`
}

type statsOut struct{ Body WorkloadStatsOut }

// opOut is a queued operation (201), like the runtime-logs route.
type opOut struct {
	Status int
	Body   OperationOut
}

const (
	powerSummary = "Imported workloads: start, stop (a guest: hard stop), shutdown (graceful) or restart (a guest: reboot). " +
		"Owner, admin or deployer; audited; a signed operation in the server's operation log."
	statsSummary = "Imported workloads: current CPU %, memory, disk and uptime, read by the agent (owner, admin or deployer). " +
		"stats is null if the agent did not answer within wait; the operation carries on."
	logsSummary = "Imported containers: relay recent log lines (docker logs, bounded; optionally following) as an operation — stream it " +
		"like the runtime logs (owner, admin or deployer)"
	recreateSummary = "Imported containers: pull the image and recreate the container with the same configuration (refused if it cannot be " +
		"reproduced exactly; the old container comes back on any failure). Owner, admin or deployer; audited."
)

func (d *deps) power(ctx context.Context, team string, resolve workloadResolver, action string) (*workloadOpOut, error) {
	return accepted(d.queue(ctx, team, tenancy.AbilityDeploy, resolve, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, w deploy.Workload) (db.Operation, error) {
		return d.deploys().Power(ctx, q, m.TeamID, a, w, action)
	}))
}

func (d *deps) stats(ctx context.Context, team string, resolve workloadResolver, wait int) (*statsOut, error) {
	op, err := d.queue(ctx, team, tenancy.AbilityDeploy, resolve, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, w deploy.Workload) (db.Operation, error) {
		return d.deploys().Stats(ctx, q, m.TeamID, a, w)
	})
	if err != nil {
		return nil, err
	}
	var st agentproto.WorkloadStats
	done, ok, err := d.awaitInto(ctx, op, wait, &st)
	if err != nil {
		return nil, err
	}
	out := &statsOut{Body: WorkloadStatsOut{Operation: toOperationOut(done)}}
	if ok {
		out.Body.Stats = &st
	}
	return out, nil
}

func (d *deps) workloadLogs(ctx context.Context, team string, resolve workloadResolver, b logsBody) (*opOut, error) {
	tail := b.Tail
	if tail == 0 {
		tail = 200
	}
	op, err := d.queue(ctx, team, tenancy.AbilityDeploy, resolve, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, w deploy.Workload) (db.Operation, error) {
		return d.deploys().WorkloadLogs(ctx, q, m.TeamID, a, w, tail, b.Since, b.FollowSeconds)
	})
	if err != nil {
		return nil, err
	}
	return &opOut{Status: http.StatusCreated, Body: toOperationOut(op)}, nil
}

func (d *deps) recreate(ctx context.Context, team string, resolve workloadResolver) (*workloadOpOut, error) {
	return accepted(d.queue(ctx, team, tenancy.AbilityDeploy, resolve, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, w deploy.Workload) (db.Operation, error) {
		return d.deploys().Recreate(ctx, q, m.TeamID, a, w)
	}))
}

func (d *deps) registerAppWorkloadControls(api huma.API) {
	tags, base := []string{"Apps"}, "/api/teams/{team}/apps/{app}"
	huma.Register(api, huma.Operation{OperationID: "power-app", Method: http.MethodPost, Path: base + "/power",
		DefaultStatus: http.StatusAccepted, Tags: tags, Summary: powerSummary,
	}, func(ctx context.Context, in *struct {
		AppPath
		Body powerBody
	}) (*workloadOpOut, error) {
		return d.power(ctx, in.Team, appWorkload(in.App), in.Body.Action)
	})
	huma.Register(api, huma.Operation{OperationID: "app-stats", Method: http.MethodGet, Path: base + "/stats", Tags: tags, Summary: statsSummary}, func(ctx context.Context, in *struct {
		AppPath
		WaitQuery
	}) (*statsOut, error) {
		return d.stats(ctx, in.Team, appWorkload(in.App), in.Wait)
	})
	huma.Register(api, huma.Operation{OperationID: "app-workload-logs", Method: http.MethodPost, Path: base + "/workload-logs",
		DefaultStatus: http.StatusCreated, Tags: tags, Summary: logsSummary,
	}, func(ctx context.Context, in *struct {
		AppPath
		Body logsBody
	}) (*opOut, error) {
		return d.workloadLogs(ctx, in.Team, appWorkload(in.App), in.Body)
	})
	huma.Register(api, huma.Operation{OperationID: "recreate-app", Method: http.MethodPost, Path: base + "/recreate",
		DefaultStatus: http.StatusAccepted, Tags: tags, Summary: recreateSummary,
	}, func(ctx context.Context, in *AppPath) (*workloadOpOut, error) {
		return d.recreate(ctx, in.Team, appWorkload(in.App))
	})
}

func (d *deps) registerServiceWorkloadControls(api huma.API) {
	tags, base := []string{"Databases"}, "/api/teams/{team}/data-services/{service}"
	huma.Register(api, huma.Operation{OperationID: "power-data-service", Method: http.MethodPost, Path: base + "/power",
		DefaultStatus: http.StatusAccepted, Tags: tags, Summary: powerSummary,
	}, func(ctx context.Context, in *struct {
		ServicePath
		Body powerBody
	}) (*workloadOpOut, error) {
		return d.power(ctx, in.Team, serviceWorkload(in.Service), in.Body.Action)
	})
	huma.Register(api, huma.Operation{OperationID: "data-service-stats", Method: http.MethodGet, Path: base + "/stats", Tags: tags, Summary: statsSummary}, func(ctx context.Context, in *struct {
		ServicePath
		WaitQuery
	}) (*statsOut, error) {
		return d.stats(ctx, in.Team, serviceWorkload(in.Service), in.Wait)
	})
	huma.Register(api, huma.Operation{OperationID: "data-service-workload-logs", Method: http.MethodPost, Path: base + "/workload-logs",
		DefaultStatus: http.StatusCreated, Tags: tags, Summary: logsSummary,
	}, func(ctx context.Context, in *struct {
		ServicePath
		Body logsBody
	}) (*opOut, error) {
		return d.workloadLogs(ctx, in.Team, serviceWorkload(in.Service), in.Body)
	})
	huma.Register(api, huma.Operation{OperationID: "recreate-data-service", Method: http.MethodPost, Path: base + "/recreate",
		DefaultStatus: http.StatusAccepted, Tags: tags, Summary: recreateSummary,
	}, func(ctx context.Context, in *ServicePath) (*workloadOpOut, error) {
		return d.recreate(ctx, in.Team, serviceWorkload(in.Service))
	})
}

// registerGuestControls: resize, snapshots and backups — imported Proxmox guest apps.
func (d *deps) registerGuestControls(api huma.API) {
	tags := []string{"Apps"}
	base := "/api/teams/{team}/apps/{app}"

	huma.Register(api, huma.Operation{OperationID: "resize-app-guest", Method: http.MethodPost, Path: base + "/resize",
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Imported Proxmox guests: change cores and memory, grow the root disk (never shrinks). Owner, admin or deployer; audited.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Cores      int `json:"cores,omitempty" minimum:"0" maximum:"512"`
			MemoryMB   int `json:"memory_mb,omitempty" minimum:"0" maximum:"4194304"`
			DiskGrowGB int `json:"disk_grow_gb,omitempty" minimum:"0" maximum:"65536" doc:"Grow the root disk by this many GB."`
		}
	}) (*workloadOpOut, error) {
		return accepted(d.queue(ctx, in.Team, tenancy.AbilityDeploy, appWorkload(in.App), func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, w deploy.Workload) (db.Operation, error) {
			return d.deploys().Resize(ctx, q, m.TeamID, a, w, in.Body.Cores, in.Body.MemoryMB, in.Body.DiskGrowGB)
		}))
	})

	type snapsOut struct{ Body SnapshotsOut }
	huma.Register(api, huma.Operation{OperationID: "list-app-snapshots", Method: http.MethodGet, Path: base + "/snapshots", Tags: tags,
		Summary: "Imported Proxmox guests: the snapshots, read by the agent (owner, admin or deployer). snapshots is null if the agent did not answer within wait.",
	}, func(ctx context.Context, in *struct {
		AppPath
		WaitQuery
	}) (*snapsOut, error) {
		op, err := d.queue(ctx, in.Team, tenancy.AbilityDeploy, appWorkload(in.App), func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, w deploy.Workload) (db.Operation, error) {
			return d.deploys().Snapshot(ctx, q, m.TeamID, a, w, agentproto.SnapshotList, "", "")
		})
		if err != nil {
			return nil, err
		}
		var res agentproto.SnapshotResult
		done, ok, err := d.awaitInto(ctx, op, in.Wait, &res)
		if err != nil {
			return nil, err
		}
		out := &snapsOut{Body: SnapshotsOut{Operation: toOperationOut(done)}}
		if ok {
			out.Body.Snapshots = res.Snapshots
			if out.Body.Snapshots == nil {
				out.Body.Snapshots = []agentproto.Snapshot{}
			}
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{OperationID: "create-app-snapshot", Method: http.MethodPost, Path: base + "/snapshots",
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Imported Proxmox guests: take a snapshot (owner, admin or deployer; audited)",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Name        string `json:"name" minLength:"2" maxLength:"40" pattern:"^[A-Za-z][A-Za-z0-9_-]{1,39}$"`
			Description string `json:"description,omitempty" maxLength:"200"`
		}
	}) (*workloadOpOut, error) {
		return accepted(d.queue(ctx, in.Team, tenancy.AbilityDeploy, appWorkload(in.App), func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, w deploy.Workload) (db.Operation, error) {
			return d.deploys().Snapshot(ctx, q, m.TeamID, a, w, agentproto.SnapshotCreate, in.Body.Name, in.Body.Description)
		}))
	})

	huma.Register(api, huma.Operation{OperationID: "rollback-app-snapshot", Method: http.MethodPost, Path: base + "/snapshots/{snapshot}/rollback",
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Imported Proxmox guests: roll the guest back to a snapshot (owner, admin or deployer; audited)",
	}, func(ctx context.Context, in *SnapshotPath) (*workloadOpOut, error) {
		return accepted(d.queue(ctx, in.Team, tenancy.AbilityDeploy, appWorkload(in.App), func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, w deploy.Workload) (db.Operation, error) {
			return d.deploys().Snapshot(ctx, q, m.TeamID, a, w, agentproto.SnapshotRollback, in.Snapshot, "")
		}))
	})

	huma.Register(api, huma.Operation{OperationID: "delete-app-snapshot", Method: http.MethodDelete, Path: base + "/snapshots/{snapshot}",
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Imported Proxmox guests: delete a snapshot. Destructive: owner or admin, confirm = the app's name; audited.",
	}, func(ctx context.Context, in *struct {
		SnapshotPath
		Body struct {
			Confirm string `json:"confirm" minLength:"1" maxLength:"64" doc:"The app's name, exactly."`
		}
	}) (*workloadOpOut, error) {
		return accepted(d.queue(ctx, in.Team, tenancy.AbilityManageApps, appWorkload(in.App), func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, w deploy.Workload) (db.Operation, error) {
			if in.Body.Confirm != w.Name {
				return db.Operation{}, huma.Error422UnprocessableEntity("confirm must equal the app's name — nothing was deleted")
			}
			return d.deploys().Snapshot(ctx, q, m.TeamID, a, w, agentproto.SnapshotDelete, in.Snapshot, "")
		}))
	})

	huma.Register(api, huma.Operation{OperationID: "backup-app-guest", Method: http.MethodPost, Path: base + "/backup",
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Imported Proxmox guests: vzdump the guest (snapshot mode, zstd) to the node's backup storage or the one given. " +
			"The result carries the archive's volid. Owner, admin or deployer; audited.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Storage string `json:"storage,omitempty" maxLength:"64" doc:"A Proxmox storage id; default: the node's first active backup storage."`
		}
	}) (*workloadOpOut, error) {
		return accepted(d.queue(ctx, in.Team, tenancy.AbilityDeploy, appWorkload(in.App), func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, w deploy.Workload) (db.Operation, error) {
			return d.deploys().GuestBackup(ctx, q, m.TeamID, a, w, in.Body.Storage)
		}))
	})
}

// registerImportedServiceRoutes: credentials for an imported database.
func (d *deps) registerImportedServiceRoutes(api huma.API) {
	type serviceCredentialsOut struct {
		Body struct {
			Service  DataServiceOut `json:"service"`
			Verified bool           `json:"verified" doc:"true: the engine's own client logged in with them; false: only the port answered (a database in a Proxmox guest)."`
			Version  string         `json:"version,omitempty"`
			Detail   string         `json:"detail,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "set-data-service-credentials", Method: http.MethodPost,
		Path: "/api/teams/{team}/data-services/{service}/credentials", Tags: []string{"Databases"},
		Summary: "Imported databases: give Hosbah credentials. They are checked on the server (db.probe) and, if they work, sealed with the " +
			"team key like a managed service's root — databases, users and backups then work for it. Owner or admin; audited.",
	}, func(ctx context.Context, in *struct {
		ServicePath
		Body struct {
			Username string `json:"username,omitempty" maxLength:"128" doc:"Required for Postgres and MySQL; Redis: an ACL user, or empty for the default user."`
			Password string `json:"password" minLength:"1" maxLength:"1024"`
			Database string `json:"database,omitempty" maxLength:"63" doc:"Postgres: the database to log in to (default postgres)."`
		}
	}) (*serviceCredentialsOut, error) {
		var svc db.DataService
		var actor deploy.Actor
		var teamID string
		if err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			var err error
			svc, err = serviceIn(ctx, q, m, in.Service)
			actor, teamID = webActor(id), m.TeamID
			return err
		}); err != nil {
			return nil, err
		}
		res, err := d.dbs().SetCredentials(ctx, teamID, actor, svc, dbsvc.Credentials{User: in.Body.Username, Password: in.Body.Password, Database: in.Body.Database})
		if err != nil {
			return nil, dbErr(err, "could not set the credentials")
		}
		out := &serviceCredentialsOut{}
		err = d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			fresh, err := serviceIn(ctx, q, m, svc.ID)
			out.Body.Service = toDataServiceOut(fresh)
			return err
		})
		out.Body.Verified, out.Body.Version, out.Body.Detail = res.Verified, res.Version, res.Detail
		return out, err
	})
}
