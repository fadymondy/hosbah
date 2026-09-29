package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Control of imported workloads — the sites and databases Hosbah found running on a server. One
implementation for the REST API and the team MCP, like the rest of this package: every function runs
in the caller's team transaction after the caller checked the member's role, validates, enqueues ONE
typed, signed agent operation (its params carry the record's name as a label, so the server's
operation log says what it was for) and writes the audit entry in the same transaction.

What can be controlled:
  - a Proxmox guest (origin_kind guest) on a server that is the hypervisor: pvesh on that host;
  - a Docker container (origin_kind container): the docker CLI on that server.
A systemd unit is shown but not controlled (CodeUnsupportedOrigin).

What is never touched: Hosbah's own workloads (agentproto.IsHosbahOwned — the agent refuses them too),
and a guest that is itself one of the team's servers. Nothing here runs by itself: every operation is a
member's (or an MCP token's) explicit request; the importer and the boot backfill never enqueue one.
*/

// Refusal codes (409 unless noted), alongside ImportedCode.
const (
	CodeNotImported       = "not_imported"       // the action is for imported workloads; this one is Hosbah-built
	CodeUnsupportedOrigin = "unsupported_origin" // e.g. a systemd unit, or a guest-only action on a container
	CodeHosbahOwned       = "hosbah_owned"       // Hosbah's own workload, or a guest that is one of the team's servers
	CodeWorkloadMissing   = "workload_missing"   // discovery no longer reports it
	CodeServerOffline     = "server_not_enrolled"
)

func coded(status int, code, msg string) error {
	return &ActionError{Status: status, Msg: msg, Code: code}
}

// Subjects of a workload (the audit subject type).
const (
	SubjectApp         = "app"
	SubjectDataService = "data_service"
)

// Workload is an imported app or data service as the control actions see it.
type Workload struct {
	Subject    string // app | data_service
	ID         string
	Name       string
	ServerID   string
	Source     string
	OriginKind string
	OriginKey  string
	State      string
	Origin     importer.Origin
}

func originOf(raw *string) importer.Origin {
	var o importer.Origin
	if raw != nil {
		_ = json.Unmarshal([]byte(*raw), &o)
	}
	return o
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// WorkloadOfApp describes an app.
func WorkloadOfApp(a db.App) Workload {
	return Workload{Subject: SubjectApp, ID: a.ID, Name: a.Name, ServerID: a.ServerID, Source: a.Source,
		OriginKind: deref(a.OriginKind), OriginKey: deref(a.OriginKey), State: deref(a.ImportedState), Origin: originOf(a.Origin)}
}

// WorkloadOfService describes a data service.
func WorkloadOfService(d db.DataService) Workload {
	return Workload{Subject: SubjectDataService, ID: d.ID, Name: d.Name, ServerID: d.ServerID, Source: d.Source,
		OriginKind: deref(d.OriginKind), OriginKey: deref(d.OriginKey), State: deref(d.ImportedState), Origin: originOf(d.Origin)}
}

// IsGuest / IsContainer: the two controllable origins.
func (w Workload) IsGuest() bool     { return w.OriginKind == "guest" }
func (w Workload) IsContainer() bool { return w.OriginKind == "container" }

// Ref is the agent-side reference, labelled with the record's name.
func (w Workload) Ref() agentproto.WorkloadRef {
	if w.IsGuest() {
		return agentproto.Guest(w.Origin.VMID, w.Origin.GuestType, w.Name)
	}
	return agentproto.Container(w.OriginKey, w.Name)
}

// check decides whether the workload may be controlled at all, and returns its (enrolled) server.
func (s *Service) check(ctx context.Context, q *db.Queries, teamID string, w Workload) (db.Server, error) {
	if w.Source != SourceImported {
		return db.Server{}, coded(http.StatusConflict, CodeNotImported,
			w.Name+" is built by Hosbah: use deploy, restart and the app's own controls")
	}
	switch w.OriginKind {
	case "guest", "container":
	default:
		return db.Server{}, coded(http.StatusConflict, CodeUnsupportedOrigin,
			fmt.Sprintf("%s is a %s; Hosbah can control Proxmox guests and Docker containers only", w.Name, orDefault(w.OriginKind, "workload")))
	}
	if agentproto.IsHosbahOwned(w.OriginKey) || agentproto.IsHosbahOwned(w.Name) {
		return db.Server{}, coded(http.StatusConflict, CodeHosbahOwned, w.Name+" is one of Hosbah's own workloads; Hosbah does not act on itself")
	}
	if err := w.Ref().Validate(); err != nil {
		return db.Server{}, coded(http.StatusConflict, CodeUnsupportedOrigin, w.Name+": "+err.Error())
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: w.ServerID, TeamID: teamID})
	if err != nil {
		return db.Server{}, notFound("server")
	}
	if w.IsGuest() && w.Origin.Address != "" {
		servers, err := q.ListServersOfTeam(ctx, teamID)
		if err != nil {
			return db.Server{}, err
		}
		for _, other := range servers {
			if other.ID != srv.ID && other.IpAddress != nil && *other.IpAddress == w.Origin.Address {
				return db.Server{}, coded(http.StatusConflict, CodeHosbahOwned,
					fmt.Sprintf("%s is the Hosbah server %s; manage it from Servers", w.Name, other.Name))
			}
		}
	}
	if w.State == importer.StateMissing {
		return db.Server{}, coded(http.StatusConflict, CodeWorkloadMissing, w.Name+" is no longer reported by its server — refresh the server, or delete the record")
	}
	if srv.Status != "enrolled" {
		return db.Server{}, coded(http.StatusConflict, CodeServerOffline, "that server's agent has not enrolled")
	}
	return srv, nil
}

// needGuest / needContainer refuse an action that only applies to one origin.
func needGuest(w Workload, what string) error {
	if !w.IsGuest() {
		return coded(http.StatusConflict, CodeUnsupportedOrigin, what+" applies to Proxmox guests; "+w.Name+" is a "+orDefault(w.OriginKind, "workload"))
	}
	return nil
}

func needContainer(w Workload, what string) error {
	if !w.IsContainer() {
		return coded(http.StatusConflict, CodeUnsupportedOrigin, what+" applies to Docker containers; "+w.Name+" is a "+orDefault(w.OriginKind, "workload"))
	}
	return nil
}

// enqueueCommitted is enqueue in its own transaction, committed before it returns — for a caller
// that is about to synchronously AwaitResult the operation from within its own still-open request
// transaction. AwaitResult polls on a separate connection (ops.Service.Await opens its own
// tenancy.Tx per poll), so an operation enqueued on the request's own not-yet-committed transaction
// is invisible to it: Postgres never lets one transaction see another's uncommitted writes, even from
// the same request. Enqueuing here first, on its own transaction that commits immediately, is what
// makes the row visible to AwaitResult's very first poll.
func (s *Service) enqueueCommitted(ctx context.Context, teamID string, actor Actor, w Workload, kind string, params any, action string, extra map[string]any) (db.Operation, error) {
	var op db.Operation
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: teamID}, func(txq *db.Queries, _ *sql.Tx) error {
		var err error
		op, err = s.enqueue(ctx, txq, teamID, actor, w, kind, params, action, extra)
		return err
	})
	return op, err
}

// enqueueOpCommitted is s.ops.Enqueue in its own transaction, committed before it returns — the
// same visibility fix as enqueueCommitted, for a caller that builds an ops.EnqueueSpec directly
// (edge cache, HTTP rules, log drain test) rather than going through a Workload, and is about to
// synchronously AwaitResult it from within its own still-open request transaction.
func (s *Service) enqueueOpCommitted(ctx context.Context, teamID string, spec ops.EnqueueSpec) (db.Operation, error) {
	var op db.Operation
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: teamID}, func(txq *db.Queries, _ *sql.Tx) error {
		var err error
		op, _, err = s.ops.Enqueue(ctx, txq, spec)
		return err
	})
	return op, err
}

// systemTx runs fn in its own, immediately-committed System-scoped transaction — for a write a
// caller needs to make from within its own still-open, team-scoped request transaction, on a table
// that (like releases: "written only by the worker") has no team-scope write policy at all, only
// team-scope SELECT plus a System-scope ALL policy.
func (s *Service) systemTx(ctx context.Context, teamID string, fn func(txq *db.Queries) error) error {
	return tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: teamID}, func(txq *db.Queries, _ *sql.Tx) error {
		return fn(txq)
	})
}

// enqueue queues the operation on the workload's server and audits it (action "" = no audit entry:
// the read-only stats, logs and snapshot list are visible in the operation log only).
func (s *Service) enqueue(ctx context.Context, q *db.Queries, teamID string, actor Actor, w Workload, kind string, params any, action string, extra map[string]any) (db.Operation, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return db.Operation{}, err
	}
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: w.ServerID, Kind: kind, Params: raw, CreatedBy: actorID(actor)})
	if err != nil {
		return db.Operation{}, err
	}
	if action != "" {
		if extra == nil {
			extra = map[string]any{}
		}
		extra["name"], extra["operation"], extra["origin"] = w.Name, op.ID, w.OriginKind
		if err := tenancy.Record(ctx, q, teamID, actor.UserID, action, w.Subject, w.ID, actor.audit(extra)); err != nil {
			return db.Operation{}, err
		}
	}
	return op, nil
}

// ---- power -------------------------------------------------------------------------------------

// ValidPowerAction: start | stop | shutdown | restart.
func ValidPowerAction(a string) bool {
	switch a {
	case agentproto.PowerStart, agentproto.PowerStop, agentproto.PowerShutdown, agentproto.PowerRestart:
		return true
	}
	return false
}

// Power starts, stops (hard), shuts down (graceful) or restarts the workload. When the operation
// finishes, the record's state follows (the next discovery confirms it).
func (s *Service) Power(ctx context.Context, q *db.Queries, teamID string, actor Actor, w Workload, action string) (db.Operation, error) {
	if !ValidPowerAction(action) {
		return db.Operation{}, invalid("action is start, stop, shutdown or restart")
	}
	if _, err := s.check(ctx, q, teamID, w); err != nil {
		return db.Operation{}, err
	}
	op, err := s.enqueue(ctx, q, teamID, actor, w, agentproto.KindWorkloadPower,
		agentproto.PowerParams{WorkloadRef: w.Ref(), Action: action}, "workload.power", map[string]any{"action": action})
	if err != nil {
		return db.Operation{}, err
	}
	s.afterOp(teamID, op, func(ctx context.Context, done db.Operation) {
		var res agentproto.PowerResult
		if done.Status != "ok" || done.Result == nil || json.Unmarshal([]byte(*done.Result), &res) != nil {
			return
		}
		state, status := importer.StateStopped, "stopped"
		if res.Status == "running" {
			state, status = importer.StateRunning, "running"
		}
		_ = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
			if w.Subject == SubjectApp {
				return q.SetImportedAppState(ctx, db.SetImportedAppStateParams{ImportedState: &state, Status: status, ID: w.ID, TeamID: teamID})
			}
			return q.SetImportedDataServiceState(ctx, db.SetImportedDataServiceStateParams{ImportedState: &state, Status: status, ID: w.ID, TeamID: teamID})
		})
		if w.Subject == SubjectApp {
			s.ops.Broker().Publish(ops.AppTopic(w.ID))
		}
	})
	return op, nil
}

// ---- stats and logs ----------------------------------------------------------------------------

// Stats queues a reading of CPU, memory, disk and uptime (AwaitResult decodes it).
func (s *Service) Stats(ctx context.Context, q *db.Queries, teamID string, actor Actor, w Workload) (db.Operation, error) {
	if _, err := s.check(ctx, q, teamID, w); err != nil {
		return db.Operation{}, err
	}
	return s.enqueue(ctx, q, teamID, actor, w, agentproto.KindWorkloadStats, agentproto.StatsParams{WorkloadRef: w.Ref()}, "", nil)
}

// WorkloadLogs relays a container's recent lines (then follows for a while) as the operation's log —
// the same shape as an app's runtime logs, streamed through the operation stream.
func (s *Service) WorkloadLogs(ctx context.Context, q *db.Queries, teamID string, actor Actor, w Workload, tail int, since string, follow int) (db.Operation, error) {
	if _, err := s.check(ctx, q, teamID, w); err != nil {
		return db.Operation{}, err
	}
	if err := needContainer(w, "Logs"); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.WorkloadLogsParams{Name: w.OriginKey, Label: w.Name, Tail: min(max(tail, 0), agentproto.MaxWorkloadLogTail),
		Since: strings.TrimSpace(since), FollowSeconds: min(max(follow, 0), agentproto.MaxLogFollowSeconds)}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	return s.enqueue(ctx, q, teamID, actor, w, agentproto.KindWorkloadLogs, p, "", nil)
}

// ---- guests ------------------------------------------------------------------------------------

// Resize changes a guest's cores and memory and grows its root disk (0 = unchanged; never shrinks).
func (s *Service) Resize(ctx context.Context, q *db.Queries, teamID string, actor Actor, w Workload, cores, memoryMB, diskGrowGB int) (db.Operation, error) {
	if _, err := s.check(ctx, q, teamID, w); err != nil {
		return db.Operation{}, err
	}
	if err := needGuest(w, "Resizing"); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.ResizeParams{VMID: w.Origin.VMID, GuestType: w.Origin.GuestType, Label: w.Name, Cores: cores, MemoryMB: memoryMB, DiskGrowGB: diskGrowGB}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	return s.enqueue(ctx, q, teamID, actor, w, agentproto.KindGuestResize, p, "workload.resize",
		map[string]any{"cores": cores, "memory_mb": memoryMB, "disk_grow_gb": diskGrowGB})
}

// Snapshot creates, rolls back to, deletes or lists a guest's snapshots.
func (s *Service) Snapshot(ctx context.Context, q *db.Queries, teamID string, actor Actor, w Workload, action, name, description string) (db.Operation, error) {
	if _, err := s.check(ctx, q, teamID, w); err != nil {
		return db.Operation{}, err
	}
	if err := needGuest(w, "Snapshots"); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.SnapshotParams{VMID: w.Origin.VMID, GuestType: w.Origin.GuestType, Label: w.Name, Action: action,
		Name: strings.TrimSpace(name), Description: strings.TrimSpace(description)}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	audit := ""
	if action != agentproto.SnapshotList {
		audit = "workload.snapshot." + action
	}
	return s.enqueue(ctx, q, teamID, actor, w, agentproto.KindGuestSnapshot, p, audit, map[string]any{"snapshot": p.Name})
}

// GuestBackup vzdumps a guest (snapshot mode, zstd) to storage ("" = the node's first backup storage).
func (s *Service) GuestBackup(ctx context.Context, q *db.Queries, teamID string, actor Actor, w Workload, storage string) (db.Operation, error) {
	if _, err := s.check(ctx, q, teamID, w); err != nil {
		return db.Operation{}, err
	}
	if err := needGuest(w, "A guest backup"); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.GuestBackupParams{VMID: w.Origin.VMID, GuestType: w.Origin.GuestType, Label: w.Name, Storage: strings.TrimSpace(storage)}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	return s.enqueue(ctx, q, teamID, actor, w, agentproto.KindGuestBackup, p, "workload.backup", map[string]any{"storage": p.Storage})
}

// ---- containers --------------------------------------------------------------------------------

// Recreate pulls a container's image and recreates it with the same configuration (the agent refuses
// one it cannot reproduce exactly, and restores the old one on any failure).
func (s *Service) Recreate(ctx context.Context, q *db.Queries, teamID string, actor Actor, w Workload) (db.Operation, error) {
	if _, err := s.check(ctx, q, teamID, w); err != nil {
		return db.Operation{}, err
	}
	if err := needContainer(w, "Recreating"); err != nil {
		return db.Operation{}, err
	}
	return s.enqueue(ctx, q, teamID, actor, w, agentproto.KindContainerRecreate, agentproto.RecreateParams{Name: w.OriginKey, Label: w.Name},
		"workload.recreate", map[string]any{"image": w.Origin.Image})
}

// ---- destroy -----------------------------------------------------------------------------------

// Destroy removes the real workload — a guest is stopped then destroyed, a container removed — and,
// once the agent reports it gone, the record. Destructive: the caller has checked the typed confirm
// (REST) or the destructive gate and approval (MCP), and an admin role.
func (s *Service) Destroy(ctx context.Context, q *db.Queries, teamID string, actor Actor, w Workload, purge bool) (db.Operation, error) {
	if _, err := s.check(ctx, q, teamID, w); err != nil {
		return db.Operation{}, err
	}
	op, err := s.enqueue(ctx, q, teamID, actor, w, agentproto.KindWorkloadDestroy, agentproto.DestroyParams{WorkloadRef: w.Ref(), Purge: purge},
		"workload.destroy", map[string]any{"purge": purge})
	if err != nil {
		return db.Operation{}, err
	}
	s.afterOp(teamID, op, func(ctx context.Context, done db.Operation) {
		if done.Status != "ok" {
			_ = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
				return tenancy.Record(ctx, q, teamID, actor.UserID, "workload.destroy_failed", w.Subject, w.ID, actor.audit(map[string]any{
					"name": w.Name, "operation": op.ID, "error": deref(done.Error), "note": "the record is kept"}))
			})
			return
		}
		var hosts []string
		_ = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
			if w.Subject == SubjectApp {
				a, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: w.ID, TeamID: teamID})
				if err != nil {
					return nil // already gone
				}
				if hosts, err = s.dropImportedRoutes(ctx, q, teamID, actor, a); err != nil {
					return err
				}
				if _, err := q.DeleteAppInTeam(ctx, db.DeleteAppInTeamParams{ID: w.ID, TeamID: teamID}); err != nil {
					return err
				}
			} else if _, err := q.DeleteDataServiceInTeam(ctx, db.DeleteDataServiceInTeamParams{ID: w.ID, TeamID: teamID}); err != nil {
				return err
			}
			return tenancy.Record(ctx, q, teamID, actor.UserID, "workload.destroyed", w.Subject, w.ID, actor.audit(map[string]any{
				"name": w.Name, "operation": op.ID, "purge": purge}))
		})
		for _, h := range hosts {
			_ = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
				s.unwireTeamDNS(ctx, q, teamID, h)
				s.unpublishHost(ctx, q, teamID, h)
				return nil
			})
		}
		if w.Subject == SubjectApp {
			s.ops.Broker().Publish(ops.AppTopic(w.ID))
		}
	})
	return op, nil
}

// ---- awaiting ----------------------------------------------------------------------------------

// ErrStillRunning: the operation had not finished within the wait (it carries on).
var ErrStillRunning = errors.New("the operation is still running")

// AwaitResult waits up to wait for op (queued in a transaction that has since committed) and decodes
// its result into out. A failed operation is an error with the agent's message.
func (s *Service) AwaitResult(ctx context.Context, op db.Operation, wait time.Duration, out any) (db.Operation, error) {
	if wait <= 0 {
		return op, ErrStillRunning
	}
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	done, err := s.ops.Await(wctx, op.ID, op.ServerID, nil)
	if err != nil {
		if wctx.Err() != nil {
			return done, ErrStillRunning
		}
		return done, err
	}
	if done.Status != "ok" {
		msg := done.Status
		if done.Error != nil {
			msg = *done.Error
		}
		return done, &ActionError{Status: http.StatusBadGateway, Msg: msg, Code: "operation_failed"}
	}
	if out != nil && done.Result != nil {
		if err := json.Unmarshal([]byte(*done.Result), out); err != nil {
			return done, err
		}
	}
	return done, nil
}

// afterOp runs then once op has finished, in the background. op was queued in the caller's
// transaction, so it becomes visible only when that commits — a rolled-back request leaves nothing
// to wait for, and the watcher gives up.
func (s *Service) afterOp(teamID string, op db.Operation, then func(ctx context.Context, done db.Operation)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), ops.Timeout(op.Kind)+ops.QueueTTL)
		defer cancel()
		deadline := time.Now().Add(30 * time.Second)
		for {
			done, err := s.ops.Await(ctx, op.ID, op.ServerID, nil)
			if err == nil {
				then(ctx, done)
				return
			}
			if !errors.Is(err, sql.ErrNoRows) || time.Now().After(deadline) || ctx.Err() != nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	}()
	_ = teamID
}

// ---- domains on imported apps ------------------------------------------------------------------

// importedUpstream is where an imported app's hostname is proxied: a container's published port on
// its server (port "" = the first one published), or a guest's address and the port the user gave.
func importedUpstream(w Workload, port int) (string, error) {
	switch w.OriginKind {
	case "container":
		pub := importer.PublishedPorts(w.Origin.Ports)
		if len(pub) == 0 {
			return "", coded(http.StatusUnprocessableEntity, CodeUnsupportedOrigin,
				w.Name+" publishes no port on its server, so there is nothing to route a hostname to")
		}
		pick := pub[0]
		if port != 0 {
			found := false
			for _, p := range pub {
				if p.Port == port {
					pick, found = p, true
					break
				}
			}
			if !found {
				var list []string
				for _, p := range pub {
					list = append(list, strconv.Itoa(p.Port))
				}
				return "", invalid(fmt.Sprintf("%s does not publish port %d on its server (it publishes %s)", w.Name, port, strings.Join(list, ", ")))
			}
		}
		host := pick.Host
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		return net.JoinHostPort(host, strconv.Itoa(pick.Port)), nil
	case "guest":
		if port < 1 || port > 65535 {
			return "", invalid("give the port the guest's site listens on (1–65535)")
		}
		if w.Origin.Address == "" {
			return "", coded(http.StatusConflict, CodeUnsupportedOrigin, w.Name+" has no known address (set a static IP on the guest, then refresh)")
		}
		return "http://" + net.JoinHostPort(w.Origin.Address, strconv.Itoa(port)), nil
	}
	return "", coded(http.StatusConflict, CodeUnsupportedOrigin, "domains apply to imported containers and Proxmox guests; "+w.Name+" is a "+orDefault(w.OriginKind, "workload"))
}

// routeImported applies (or removes) the proxy route of one imported app's domain on its server.
func (s *Service) routeImported(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, d db.Domain, remove bool) error {
	params := agentproto.ProxyParams{ProxyID: d.ID, Remove: true}
	if !remove {
		if err := s.ensureCaddy(ctx, q, teamID, actor, a.ServerID); err != nil {
			return err
		}
		params = agentproto.ProxyParams{ProxyID: d.ID, Hostname: d.Hostname, TLS: d.Tls, Upstream: deref(d.Upstream)}
		if err := params.Validate(); err != nil {
			return invalid(err.Error())
		}
	}
	raw, _ := json.Marshal(params)
	_, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: a.ServerID, Kind: agentproto.KindProxySet, Params: raw, CreatedBy: actorID(actor)})
	return err
}

// Coded is a refusal with a machine-readable code (the API answers {"code","message"}).
func Coded(status int, code, msg string) error { return coded(status, code, msg) }

// CheckWorkload is check for other packages (dbsvc): may the workload be controlled at all?
func (s *Service) CheckWorkload(ctx context.Context, q *db.Queries, teamID string, w Workload) (db.Server, error) {
	return s.check(ctx, q, teamID, w)
}
