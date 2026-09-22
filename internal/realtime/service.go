package realtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Like internal/storage, every function runs in the CALLER's team-scoped transaction after the
// caller checked the member's role, and writes its audit entry on that transaction.

// What a change answers with.
const (
	// AppliesNow: nothing had to start — the route was changed at once (or there was none to change).
	AppliesNow = "now"
	// AppliesNextDeploy: the realtime worker has to be started with its port published.
	AppliesNextDeploy = "next_deploy"
)

// ConnectionsReason is why connections is null: no kind here has a dependable local source for the
// number of open sockets (Reverb's needs its app secret and an HTTP call into the app; socket.io,
// ws and Go servers expose none), and Hosbah does not make one up.
const ConnectionsReason = "not exposed by this kind"

// Note is the behaviour a caller should know about, said on every status.
const Note = "During a deploy the old release's realtime process keeps serving until traffic switches; " +
	"connections open to it drop when its container drains, and clients reconnect to the new release. " +
	"A changed process or port, or enabling realtime while a release runs without it, applies from the next deploy or restart; " +
	"a changed path, disabling and removing apply at once. The process must listen on 0.0.0.0 inside its container."

func invalid(msg string) error { return &deploy.ActionError{Status: 422, Msg: msg} }

// RefuseImported: realtime is a worker of a release Hosbah deploys. An imported app has none: 422.
func RefuseImported(a db.App) error {
	if deploy.IsImported(a) {
		return invalid("realtime is a process of a site Hosbah deploys — " + a.Name + " is an imported app")
	}
	return nil
}

// RealtimeConfig is the saved configuration as the API shows it.
type RealtimeConfig struct {
	Enabled   bool      `json:"enabled"`
	Kind      string    `json:"kind" enum:"reverb,socketio,ws,go,custom"`
	Process   string    `json:"process" doc:"The worker that is the realtime server, by its bare name: reverb is hosbah.toml's \"worker:reverb\"."`
	Port      int       `json:"port" doc:"The port the process listens on INSIDE its container. The agent publishes it on a loopback port it picks."`
	Path      string    `json:"path" doc:"Requests of the site's hostnames whose path starts with this go to the process. / is the whole site (kind custom only)."`
	WholeSite bool      `json:"whole_site" doc:"true when path is / — everything, not only websockets, goes to the realtime process."`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func configOf(r db.AppRealtime) *RealtimeConfig {
	return &RealtimeConfig{Enabled: r.Enabled, Kind: r.Kind, Process: r.Process, Port: int(r.Port), Path: r.Path,
		WholeSite: deploy.RealtimeWholeSite(r), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// RealtimeStatus is GET …/realtime.
type RealtimeStatus struct {
	Config     *RealtimeConfig    `json:"config" doc:"The saved configuration; null when the site has none."`
	Suggestion RealtimeSuggestion `json:"suggestion" doc:"What the site looks like it needs, to prefill a form. Never saved by itself."`
	Routed     bool               `json:"routed" doc:"true when the live release started the configured process with the configured port published and realtime is enabled: Caddy sends path* of every hostname to it. Computed from what the live release recorded, not from the configuration alone."`
	// ProcessRunning is what the release recorded, not a probe of the server.
	ProcessRunning    bool     `json:"process_running" doc:"true when the live release started the configured process (it recorded that worker's container). It is the release's record, not a live probe of the server: a process that crashed afterwards still reads true here — see the site's processes and logs."`
	DeclaredProcesses []string `json:"declared_processes" doc:"The workers the live release declares (bare names); empty when nothing is live."`
	Hostnames         []string `json:"hostnames" doc:"Where clients connect: wss://<hostname><path> for each hostname of the site (ws:// for a hostname without TLS). Empty without a configuration."`
	Connections       *int64   `json:"connections" doc:"Open connections. Always null today: see connections_reason."`
	ConnectionsReason string   `json:"connections_reason" doc:"Why connections is null."`
	Note              string   `json:"note"`
}

// RealtimeSaved is PUT …/realtime.
type RealtimeSaved struct {
	RealtimeStatus
	Applies   string  `json:"applies" enum:"now,next_deploy" doc:"next_deploy: the process has to be (re)started with its port published — deploy or restart the site. now: the live release already publishes it (or realtime is disabled), and the route was changed at once."`
	Operation *string `json:"operation" doc:"The realtime.route operation on the site's server when the route was changed at once; null otherwise."`
}

// RealtimeRemoved is DELETE …/realtime.
type RealtimeRemoved struct {
	Removed   bool    `json:"removed"`
	Routed    bool    `json:"routed" doc:"Always false: the route is removed at once."`
	Applies   string  `json:"applies" enum:"now,next_deploy" doc:"now: the route is gone at once. The process itself keeps running, with its port published on loopback only, until the next deploy or restart."`
	Operation *string `json:"operation" doc:"The realtime.route operation that removes the route; null when the live release was not routed."`
}

// Input is what a caller sends to Set.
type Input struct {
	Enabled   *bool // default true
	Kind      string
	Process   string
	Port      int
	Path      string // default: the kind's
	WholeSite bool
}

func liveRelease(ctx context.Context, q *db.Queries, teamID string, a db.App) *db.Release {
	if live, err := q.LiveReleaseOfApp(ctx, a.ID); err == nil && live.AppID == a.ID && live.TeamID == teamID {
		return &live
	}
	return nil
}

// declared is the bare names of the workers a release declares, sorted.
func declared(live *db.Release) []string {
	out := []string{}
	if live == nil {
		return out
	}
	var eff deploy.Effective
	if json.Unmarshal([]byte(live.Config), &eff) != nil {
		return out
	}
	for name := range eff.Processes {
		if w, ok := strings.CutPrefix(name, "worker:"); ok {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

func current(ctx context.Context, q *db.Queries, teamID string, a db.App) (*db.AppRealtime, error) {
	row, err := q.RealtimeOfApp(ctx, db.RealtimeOfAppParams{AppID: a.ID, TeamID: teamID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// Get is the site's realtime status. 422: an imported app.
func Get(ctx context.Context, q *db.Queries, teamID string, a db.App) (RealtimeStatus, error) {
	if err := RefuseImported(a); err != nil {
		return RealtimeStatus{}, err
	}
	cfg, err := current(ctx, q, teamID, a)
	if err != nil {
		return RealtimeStatus{}, err
	}
	return status(ctx, q, teamID, a, cfg, liveRelease(ctx, q, teamID, a))
}

func status(ctx context.Context, q *db.Queries, teamID string, a db.App, cfg *db.AppRealtime, live *db.Release) (RealtimeStatus, error) {
	out := RealtimeStatus{Hostnames: []string{}, DeclaredProcesses: declared(live), ConnectionsReason: ConnectionsReason, Note: Note}

	in := Inputs{Processes: map[string]string{}}
	if a.Runtime != nil {
		in.Runtime = *a.Runtime
	}
	if live != nil {
		var eff deploy.Effective
		if json.Unmarshal([]byte(live.Config), &eff) == nil && eff.Processes != nil {
			in.Processes = eff.Processes
		}
	}
	vars, err := q.ListEnvVarsOfApp(ctx, db.ListEnvVarsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return out, err
	}
	for _, v := range vars {
		in.EnvNames = append(in.EnvNames, v.Name) // names only: values stay sealed
	}
	out.Suggestion = Detect(in)
	if out.Suggestion.Evidence == nil {
		out.Suggestion.Evidence = []string{}
	}
	if cfg == nil {
		return out, nil
	}

	out.Config = configOf(*cfg)
	out.Routed = deploy.RealtimeRouteOf(cfg, live) != nil
	if rec := deploy.RealtimeOfRelease(live); rec != nil && rec.Process == cfg.Process && rec.Container != "" {
		out.ProcessRunning = true
	}
	domains, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return out, err
	}
	for _, h := range deploy.HostsOf(domains) {
		scheme := "wss://"
		if h.TLS == "off" {
			scheme = "ws://"
		}
		out.Hostnames = append(out.Hostnames, scheme+h.Hostname+cfg.Path)
	}
	return out, nil
}

// Clean validates what a caller sent and returns the row to save. Pure: no database.
func Clean(in Input) (db.UpsertAppRealtimeParams, error) {
	var p db.UpsertAppRealtimeParams
	p.Enabled = in.Enabled == nil || *in.Enabled
	p.Kind = strings.TrimSpace(in.Kind)
	if !ValidKind(p.Kind) {
		return p, invalid("kind is one of " + strings.Join(Kinds, ", "))
	}
	p.Process = strings.TrimSpace(in.Process)
	if p.Process == "web" {
		return p, invalid("web is the site itself — the realtime process is a worker of hosbah.toml or cloudy.toml (\"worker:<name>\"), named here without the worker: prefix")
	}
	if !agentproto.ValidRealtimeProcess(p.Process) {
		return p, invalid("process is a worker's name: a lowercase letter, then up to 30 lowercase letters, digits, - or _ (reverb for hosbah.toml's \"worker:reverb\")")
	}
	if in.Port < 1 || in.Port > 65535 {
		return p, invalid("port is the port the process listens on inside its container: 1–65535")
	}
	p.Port = int64(in.Port)
	raw := strings.TrimSpace(in.Path)
	if raw == "" {
		raw = DefaultPath(p.Kind)
	}
	clean, err := agentproto.CleanRealtimePath(raw)
	if err != nil {
		return p, invalid(err.Error())
	}
	if clean == "/" {
		switch {
		case p.Kind != KindCustom:
			return p, invalid("the path / sends the whole site to the realtime process — only kind custom may, with whole_site: true")
		case !in.WholeSite:
			return p, invalid("the path / sends the whole site to the realtime process — pass whole_site: true if that is what you mean")
		}
	} else if in.WholeSite {
		return p, invalid("whole_site goes with the path / only")
	}
	p.Path = clean
	return p, nil
}

// Set saves the configuration (upsert). 422: a bad field, an imported app, or a process the live
// release does not declare. When the live release already publishes the configured process's port
// the route is changed at once; otherwise the change applies from the next deploy or restart.
func Set(ctx context.Context, q *db.Queries, o *ops.Service, teamID string, actor deploy.Actor, a db.App, in Input) (RealtimeSaved, error) {
	if err := RefuseImported(a); err != nil {
		return RealtimeSaved{}, err
	}
	p, err := Clean(in)
	if err != nil {
		return RealtimeSaved{}, err
	}
	live := liveRelease(ctx, q, teamID, a)
	if live != nil {
		names := declared(live)
		found := false
		for _, n := range names {
			found = found || n == p.Process
		}
		if !found {
			list := "none — add one under [processes] in hosbah.toml (or cloudy.toml), for example \"worker:" + p.Process + "\""
			if len(names) > 0 {
				list = strings.Join(names, ", ")
			}
			return RealtimeSaved{}, invalid(fmt.Sprintf("the live release declares no process \"worker:%s\" in hosbah.toml (or cloudy.toml). Declared workers: %s", p.Process, list))
		}
	}
	prev, err := current(ctx, q, teamID, a)
	if err != nil {
		return RealtimeSaved{}, err
	}
	p.TeamID, p.AppID = teamID, a.ID
	row, err := q.UpsertAppRealtime(ctx, p)
	if err != nil {
		return RealtimeSaved{}, err
	}

	was, now := deploy.RealtimeRouteOf(prev, live), deploy.RealtimeRouteOf(&row, live)
	out := RealtimeSaved{Applies: AppliesNextDeploy}
	if now != nil || !row.Enabled {
		out.Applies = AppliesNow
	}
	extra := map[string]any{"enabled": row.Enabled, "kind": row.Kind, "process": row.Process, "port": row.Port, "path": row.Path, "applies": out.Applies}
	if (was == nil) != (now == nil) || (was != nil && *was != *now) {
		op, err := queueRoute(ctx, q, o, teamID, actor, a, now)
		if err != nil {
			return RealtimeSaved{}, err
		}
		out.Operation, extra["operation"] = &op.ID, op.ID
	}
	if out.RealtimeStatus, err = status(ctx, q, teamID, a, &row, live); err != nil {
		return RealtimeSaved{}, err
	}
	return out, tenancy.Record(ctx, q, teamID, actor.UserID, "realtime.configured", "app", a.ID, auditOf(actor, extra))
}

// Remove deletes the configuration; a route the live release has is removed at once. 404: there is
// no configuration. 422: an imported app.
func Remove(ctx context.Context, q *db.Queries, o *ops.Service, teamID string, actor deploy.Actor, a db.App) (RealtimeRemoved, error) {
	out := RealtimeRemoved{Applies: AppliesNow}
	if err := RefuseImported(a); err != nil {
		return out, err
	}
	prev, err := current(ctx, q, teamID, a)
	if err != nil {
		return out, err
	}
	if prev == nil {
		return out, &deploy.ActionError{Status: 404, Msg: "this site has no realtime configuration"}
	}
	if n, err := q.DeleteRealtimeOfApp(ctx, db.DeleteRealtimeOfAppParams{AppID: a.ID, TeamID: teamID}); err != nil || n == 0 {
		if err == nil {
			err = &deploy.ActionError{Status: 404, Msg: "this site has no realtime configuration"}
		}
		return out, err
	}
	out.Removed = true
	extra := map[string]any{"kind": prev.Kind, "process": prev.Process, "port": prev.Port, "path": prev.Path}
	if deploy.RealtimeRouteOf(prev, liveRelease(ctx, q, teamID, a)) != nil {
		op, err := queueRoute(ctx, q, o, teamID, actor, a, nil)
		if err != nil {
			return out, err
		}
		out.Operation, extra["operation"] = &op.ID, op.ID
	}
	return out, tenancy.Record(ctx, q, teamID, actor.UserID, "realtime.removed", "app", a.ID, auditOf(actor, extra))
}

// queueRoute queues realtime.route on the app's server: rt nil removes the route.
func queueRoute(ctx context.Context, q *db.Queries, o *ops.Service, teamID string, actor deploy.Actor, a db.App, rt *agentproto.RealtimeRoute) (db.Operation, error) {
	p := agentproto.RealtimeRouteParams{AppID: a.ID, Enabled: rt != nil, Route: rt}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	raw, _ := json.Marshal(p)
	op, _, err := o.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: a.ServerID, Kind: agentproto.KindRealtimeRoute, Params: raw, CreatedBy: createdBy(actor)})
	return op, err
}

func createdBy(a deploy.Actor) string {
	if a.UserID != "" {
		return a.UserID
	}
	return "via:" + a.Via
}

func auditOf(a deploy.Actor, extra map[string]any) map[string]any {
	if a.Via != "" && a.Via != "web" {
		extra["via"] = a.Via
	}
	if a.TokenID != "" {
		extra["mcp_token_id"] = a.TokenID
	}
	return extra
}
