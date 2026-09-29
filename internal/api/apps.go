package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/services"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/traffic"
)

// ---- contract ------------------------------------------------------------------------------------

// AppLogsSearchOut is a log search's answer, or null result while the operation is still running.
type AppLogsSearchOut struct {
	Operation OperationOut                 `json:"operation"`
	Result    *agentproto.LogsSearchResult `json:"result"`
}

// AppOut is an app as the API shows it. Never the deploy-hook token or its hash.
type AppOut struct {
	ID                  string           `json:"id"`
	Name                string           `json:"name" doc:"The slug used in URLs, containers and hostnames; never changes."`
	Title               *string          `json:"title" doc:"Display name (at most 80 characters); null: show name."`
	Description         *string          `json:"description" doc:"A short note (at most 280 characters)."`
	Color               *string          `json:"color" doc:"#RRGGBB (uppercase), or null for the default."`
	Icon                *string          `json:"icon" doc:"Icon key (the servers' list), or null for the default."`
	LogoURL             *string          `json:"logo_url" doc:"The uploaded logo (PUT …/apps/{app}/logo), shown instead of the icon: a relative URL versioned by the image's hash, or null."`
	ServerID            string           `json:"server_id"`
	ServerName          string           `json:"server_name,omitempty"`
	RepoURL             *string          `json:"repo_url" doc:"null for an imported app"`
	Branch              *string          `json:"branch" doc:"null for an imported app"`
	Runtime             *string          `json:"runtime"`
	Port                int64            `json:"port"`
	HealthcheckPath     string           `json:"healthcheck_path"`
	HealthcheckTimeout  int64            `json:"healthcheck_timeout"`
	AutodeployPaused    bool             `json:"autodeploy_paused"`
	Status              string           `json:"status" enum:"new,deploying,running,failed,stopped"`
	CurrentReleaseID    *string          `json:"current_release_id"`
	Source              string           `json:"source" enum:"git,imported" doc:"imported: found running on the server; controlled through power, stats, logs, snapshots, resize, backup, recreate, destroy and domains, never deployed from git"`
	OriginKind          *string          `json:"origin_kind" doc:"imported only: container, systemd or guest"`
	Origin              *importer.Origin `json:"origin" doc:"imported only: what discovery reported"`
	ImportedState       *string          `json:"imported_state" doc:"imported only: running, stopped or missing (no longer discovered)"`
	LastSeenAt          *time.Time       `json:"last_seen_at" doc:"imported only: when discovery last reported it"`
	ConnectedServerID   *string          `json:"connected_server_id,omitempty" doc:"imported guest only: the server put inside it by connect-guest"`
	ConnectedServerName string           `json:"connected_server_name,omitempty" doc:"imported guest only: connected_server_id's name"`
	CreatedAt           time.Time        `json:"created_at"`
	LatestDeployment    *DeploymentOut   `json:"latest_deployment,omitempty"`
	Domains             []DomainOut      `json:"domains,omitempty"`
}

// AppDetailOut is the app page: the app, what serves it, and how it is built.
type AppDetailOut struct {
	App         AppOut       `json:"app"`
	Live        *ReleaseOut  `json:"live_release"`
	Previous    *ReleaseOut  `json:"previous_release" doc:"What one-click rollback would re-point to."`
	Domains     []DomainOut  `json:"domains"`
	Processes   []ProcessOut `json:"processes"`
	HookURLHint string       `json:"deploy_hook_hint" doc:"The deploy-hook URL with the token elided; the full URL is shown once, at creation or rotation."`
}

// DeploymentOut is one run of the pipeline.
type DeploymentOut struct {
	ID              string     `json:"id"`
	AppID           string     `json:"app_id"`
	Number          int64      `json:"number"`
	Ref             string     `json:"ref"`
	CommitSHA       *string    `json:"commit_sha"`
	CommitMessage   *string    `json:"commit_message"`
	Trigger         string     `json:"trigger" enum:"manual,hook,mcp,github"`
	Kind            string     `json:"kind" enum:"deploy,rollback,restart"`
	Status          string     `json:"status" enum:"queued,running,succeeded,failed,cancelled"`
	Track           *string    `json:"track" enum:"fast,full" doc:"Whether this run took the fast (in-place) or full (rebuild) path. Null for rollback and restart."`
	TrackReason     *string    `json:"track_reason" doc:"Why this track was chosen."`
	Step            *string    `json:"step"`
	Image           *string    `json:"image"`
	Runtime         *string    `json:"runtime"`
	Error           *string    `json:"error"`
	ReleaseID       *string    `json:"release_id"`
	TargetReleaseID *string    `json:"target_release_id"`
	CancelRequested bool       `json:"cancel_requested"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
}

// ReleaseOut is an image that took traffic.
type ReleaseOut struct {
	ID           string     `json:"id"`
	Version      int64      `json:"version"`
	Image        string     `json:"image"`
	CommitSHA    *string    `json:"commit_sha"`
	Status       string     `json:"status" enum:"live,standby,pruned"`
	DeploymentID string     `json:"deployment_id"`
	ActivatedAt  *time.Time `json:"activated_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

// DomainOut is a hostname of an app, with how to verify it.
type DomainOut struct {
	ID          string     `json:"id"`
	Hostname    string     `json:"hostname"`
	TLS         string     `json:"tls" enum:"auto,internal,off"`
	Verified    bool       `json:"verified"`
	VerifiedAt  *time.Time `json:"verified_at"`
	VerifyName  string     `json:"verify_name" doc:"TXT record name that proves control of the hostname"`
	VerifyValue string     `json:"verify_value"`
	Upstream    *string    `json:"upstream" doc:"Imported apps: where Hosbah's Caddy on the app's server proxies the hostname (a container's 127.0.0.1:<port>, a guest's http://<address>:<port>); null for a Hosbah-built app."`
	CreatedAt   time.Time  `json:"created_at"`
}

// EnvVarOut is a variable's name — never its value.
type EnvVarOut struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProcessOut is one process type.
type ProcessOut struct {
	Name    string  `json:"name"`
	Command *string `json:"command"`
}

// NotificationOut is one in-app notification.
type NotificationOut struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Title       string     `json:"title"`
	Body        *string    `json:"body"`
	SubjectType *string    `json:"subject_type"`
	SubjectID   *string    `json:"subject_id"`
	ReadAt      *time.Time `json:"read_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

func toAppOut(a db.App, server string) AppOut {
	return AppOut{ID: a.ID, Name: a.Name, Title: a.Title, Description: a.Description, Color: a.Color, Icon: a.Icon, ServerID: a.ServerID, ServerName: server, RepoURL: a.RepoURL, Branch: a.Branch,
		Runtime: a.Runtime, Port: a.Port, HealthcheckPath: a.HealthcheckPath, HealthcheckTimeout: a.HealthcheckTimeout,
		AutodeployPaused: a.AutodeployPaused, Status: a.Status, CurrentReleaseID: a.CurrentReleaseID, CreatedAt: a.CreatedAt,
		Source: a.Source, OriginKind: a.OriginKind, Origin: originOf(a.Origin), ImportedState: a.ImportedState, LastSeenAt: a.LastSeenAt,
		ConnectedServerID: a.ConnectedServerID}
}

// originOf decodes an imported record's origin (nil for a git app or a managed service).
func originOf(raw *string) *importer.Origin {
	if raw == nil || *raw == "" {
		return nil
	}
	var o importer.Origin
	if json.Unmarshal([]byte(*raw), &o) != nil {
		return nil
	}
	if o.Ports == nil {
		o.Ports = []string{}
	}
	return &o
}

// codedErr is a refusal with a machine-readable code: {"code":"imported_workload","message":…} —
// imported_workload (a git-only action on an imported app), credentials_required, unsupported_origin,
// hosbah_owned, workload_missing, not_imported, credentials_rejected, operation_failed…
type codedErr struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *codedErr) Error() string  { return e.Message }
func (e *codedErr) GetStatus() int { return e.Status }

// asImported turns a deploy.ActionError that carries a code into codedErr.
func asImported(err error) error {
	var ae *deploy.ActionError
	if errors.As(err, &ae) && ae.Code != "" {
		st := ae.Status
		if st == 0 {
			st = http.StatusConflict
		}
		return &codedErr{Status: st, Code: ae.Code, Message: ae.Msg}
	}
	return nil
}

func toDeploymentOut(d db.Deployment) DeploymentOut {
	return DeploymentOut{ID: d.ID, AppID: d.AppID, Number: d.Number, Ref: d.Ref, CommitSHA: d.CommitSha, CommitMessage: d.CommitMessage,
		Trigger: d.Trigger, Kind: d.Kind, Status: d.Status, Track: d.Track, TrackReason: d.TrackReason, Step: d.Step, Image: d.Image, Runtime: d.Runtime, Error: d.Error,
		ReleaseID: d.ReleaseID, TargetReleaseID: d.TargetReleaseID, CancelRequested: d.CancelRequested, CreatedAt: d.CreatedAt,
		StartedAt: d.StartedAt, FinishedAt: d.FinishedAt}
}

func toReleaseOut(r db.Release) ReleaseOut {
	return ReleaseOut{ID: r.ID, Version: r.Version, Image: r.Image, CommitSHA: r.CommitSha, Status: r.Status,
		DeploymentID: r.DeploymentID, ActivatedAt: r.ActivatedAt, CreatedAt: r.CreatedAt}
}

func toDomainOut(d db.Domain) DomainOut {
	return DomainOut{ID: d.ID, Hostname: d.Hostname, TLS: d.Tls, Verified: d.VerifiedAt != nil, VerifiedAt: d.VerifiedAt,
		VerifyName: deploy.VerifyName(d.Hostname), VerifyValue: deploy.VerifyValue(d.VerifyToken), Upstream: d.Upstream, CreatedAt: d.CreatedAt}
}

// AppPath addresses one app of a team (id or name).
type AppPath struct {
	TeamPath
	App string `path:"app" maxLength:"64"`
}

// DeploymentPath addresses one deployment of an app (id or number).
type DeploymentPath struct {
	AppPath
	Deployment string `path:"deployment" maxLength:"64"`
}

// EnvPath addresses one variable of an app.
type EnvPath struct {
	AppPath
	Name string `path:"name" maxLength:"128"`
}

// DomainPath addresses one domain of an app (id or hostname).
type DomainPath struct {
	AppPath
	Domain string `path:"domain" maxLength:"253"`
}

func (d *deps) deploys() *deploy.Service {
	if d.a == nil {
		return nil
	}
	s, _ := d.a.Deploy.(*deploy.Service)
	return s
}

// actionErr maps a deploy.ActionError to its status; anything else is a 500 that says nothing.
func actionErr(err error, fallback string) error {
	if err == nil {
		return nil
	}
	if ie := asImported(err); ie != nil {
		return ie
	}
	var ae *deploy.ActionError
	if errors.As(err, &ae) {
		switch ae.Status {
		case http.StatusNotFound:
			return huma.Error404NotFound(ae.Msg)
		case http.StatusConflict:
			return huma.Error409Conflict(ae.Msg)
		case http.StatusUnprocessableEntity:
			return huma.Error422UnprocessableEntity(ae.Msg)
		case http.StatusForbidden:
			return huma.Error403Forbidden(ae.Msg)
		}
	}
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	return huma.Error500InternalServerError(fallback)
}

func appIn(ctx context.Context, q *db.Queries, m *tenancy.Membership, ref string) (db.App, error) {
	a, err := deploy.AppByRef(ctx, q, m.TeamID, ref)
	if err != nil {
		return db.App{}, errNotFound
	}
	return a, nil
}

func webActor(id *auth.Identity) deploy.Actor { return deploy.Actor{UserID: id.ID, Via: "web"} }

func need(ctx context.Context, m *tenancy.Membership, ability string) error {
	if !tenancy.Can(ctx, m, ability) {
		return errForbidden
	}
	return nil
}

// appDetail is the app page's snapshot (also the SSE "app" event).
func (d *deps) appDetail(ctx context.Context, q *db.Queries, m *tenancy.Membership, a db.App) (AppDetailOut, error) {
	out := AppDetailOut{Domains: []DomainOut{}, Processes: []ProcessOut{}}
	srvName := ""
	if s, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: m.TeamID}); err == nil {
		srvName = s.Name
	}
	out.App = appOutWithLogo(ctx, q, m.TeamID, a, srvName)
	if r, err := q.LiveReleaseOfApp(ctx, a.ID); err == nil && r.TeamID == m.TeamID {
		ro := toReleaseOut(r)
		out.Live = &ro
	}
	if r, err := q.PreviousReleaseOfApp(ctx, a.ID); err == nil && r.TeamID == m.TeamID {
		ro := toReleaseOut(r)
		out.Previous = &ro
	}
	domains, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: m.TeamID})
	if err != nil {
		return out, err
	}
	for _, dm := range domains {
		out.Domains = append(out.Domains, toDomainOut(dm))
	}
	procs, err := q.ListProcessTypesOfApp(ctx, db.ListProcessTypesOfAppParams{AppID: a.ID, TeamID: m.TeamID})
	if err != nil {
		return out, err
	}
	for _, p := range procs {
		out.Processes = append(out.Processes, ProcessOut{Name: p.Name, Command: p.Command})
	}
	if s := d.deploys(); s != nil {
		out.HookURLHint = s.HookURL(deploy.HookTokenPrefix + "…")
	}
	return out, nil
}

func (d *deps) deploymentsOf(ctx context.Context, q *db.Queries, m *tenancy.Membership, a db.App, limit int32) ([]DeploymentOut, error) {
	rows, err := q.ListDeploymentsOfApp(ctx, db.ListDeploymentsOfAppParams{AppID: a.ID, TeamID: m.TeamID, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := []DeploymentOut{}
	for _, r := range rows {
		out = append(out, toDeploymentOut(r))
	}
	return out, nil
}

// deploymentWithLogs loads a deployment of the team's app and (limit > 0) its decrypted lines.
func (d *deps) deploymentWithLogs(ctx context.Context, q *db.Queries, m *tenancy.Membership, appRef, depRef string, after int64, limit int32) (db.Deployment, []ops.LogLine, error) {
	a, err := appIn(ctx, q, m, appRef)
	if err != nil {
		return db.Deployment{}, nil, err
	}
	dep, err := deploy.DeploymentByRef(ctx, q, m.TeamID, a, depRef)
	if err != nil {
		return db.Deployment{}, nil, errNotFound
	}
	lines := []ops.LogLine{}
	if limit <= 0 {
		return dep, lines, nil
	}
	rows, err := q.DeploymentLogsAfter(ctx, db.DeploymentLogsAfterParams{DeploymentID: dep.ID, TeamID: m.TeamID, Seq: after, Limit: limit})
	if err != nil {
		return dep, nil, huma.Error500InternalServerError("could not read the log")
	}
	if len(rows) == 0 {
		return dep, lines, nil
	}
	dek, err := d.ops.TeamDEK(ctx, q, m.TeamID)
	if err != nil {
		return dep, nil, huma.Error500InternalServerError("team key unavailable")
	}
	for _, r := range rows {
		l, err := ops.OpenDeployLog(dek, m.TeamID, r)
		if err != nil {
			l = ops.LogLine{Seq: r.Seq, Kind: "log", Stream: "error", Line: "(this line could not be decrypted)", At: r.CreatedAt}
		}
		lines = append(lines, l)
	}
	return dep, lines, nil
}

func deploymentDone(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled"
}

// ---- routes --------------------------------------------------------------------------------------

func (d *deps) registerApps(api huma.API) {
	tags := []string{"Apps"}

	type appsOut struct {
		Body struct {
			Apps []AppOut `json:"apps"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-apps", Method: http.MethodGet, Path: "/api/teams/{team}/apps",
		Summary: "The team's apps with their latest deployment", Tags: tags,
	}, func(ctx context.Context, in *TeamPath) (*appsOut, error) {
		out := &appsOut{}
		out.Body.Apps = []AppOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListAppsOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list apps")
			}
			for _, r := range rows {
				a := db.App{ID: r.ID, TeamID: r.TeamID, ServerID: r.ServerID, Name: r.Name, RepoURL: r.RepoURL, Branch: r.Branch, Runtime: r.Runtime,
					Port: r.Port, HealthcheckPath: r.HealthcheckPath, HealthcheckTimeout: r.HealthcheckTimeout, AutodeployPaused: r.AutodeployPaused,
					Status: r.Status, CurrentReleaseID: r.CurrentReleaseID, CreatedAt: r.CreatedAt, Source: r.Source, OriginKind: r.OriginKind,
					OriginKey: r.OriginKey, Origin: r.Origin, ImportedState: r.ImportedState, LastSeenAt: r.LastSeenAt,
					Title: r.Title, Description: r.Description, Color: r.Color, Icon: r.Icon}
				o := toAppOut(a, r.ServerName)
				if deps, err := d.deploymentsOf(ctx, q, m, a, 1); err == nil && len(deps) > 0 {
					o.LatestDeployment = &deps[0]
				}
				if ds, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: m.TeamID}); err == nil {
					o.Domains = []DomainOut{}
					for _, dm := range ds {
						o.Domains = append(o.Domains, toDomainOut(dm))
					}
				}
				out.Body.Apps = append(out.Body.Apps, o)
			}
			attachAppLogos(ctx, q, m.TeamID, out.Body.Apps)
			return nil
		})
		return out, err
	})

	type createdAppOut struct {
		Body struct {
			App           AppOut         `json:"app"`
			DeployHookURL string         `json:"deploy_hook_url" doc:"POST to it to deploy. Shown once; only its hash is stored."`
			Deployment    *DeploymentOut `json:"deployment,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "create-app", Method: http.MethodPost, Path: "/api/teams/{team}/apps",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Create an app from a public git URL on one of the team's servers (owner or admin); optionally deploy at once",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body struct {
			Name               string `json:"name" minLength:"1" maxLength:"40" doc:"Lowercase letters, digits and dashes; unique in the team."`
			Server             string `json:"server" minLength:"1" maxLength:"64" doc:"Server id or name."`
			RepoURL            string `json:"repo_url" minLength:"1" maxLength:"500" doc:"A public https/http/git URL."`
			Branch             string `json:"branch,omitempty" maxLength:"200"`
			Port               int    `json:"port,omitempty" minimum:"0" maximum:"65535" doc:"Default 8080; hosbah.toml wins."`
			HealthcheckPath    string `json:"healthcheck_path,omitempty" maxLength:"200"`
			HealthcheckTimeout int    `json:"healthcheck_timeout,omitempty" minimum:"0" maximum:"600"`
			Deploy             bool   `json:"deploy,omitempty" doc:"Queue the first deploy right away."`
			AllowDuplicate     bool   `json:"allow_duplicate,omitempty" doc:"Build this app anyway, even though the team already has an imported app by this name or repo."`
		}
	}) (*createdAppOut, error) {
		out := &createdAppOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s := d.deploys()
			b := in.Body
			a, hook, err := s.CreateApp(ctx, q, m.TeamID, webActor(id), deploy.CreateAppInput{Name: b.Name, Server: b.Server, RepoURL: b.RepoURL,
				Branch: b.Branch, Port: b.Port, HealthPath: b.HealthcheckPath, HealthTimeout: b.HealthcheckTimeout, AllowDuplicate: b.AllowDuplicate})
			if err != nil {
				return actionErr(err, "could not create the app")
			}
			out.Body.App, out.Body.DeployHookURL = toAppOut(a, ""), hook
			if b.Deploy {
				dep, err := s.QueueDeployment(ctx, q, m.TeamID, webActor(id), a, "deploy", "", nil, "")
				if err != nil {
					return actionErr(err, "could not queue the deploy")
				}
				do := toDeploymentOut(dep)
				out.Body.Deployment = &do
			}
			return nil
		})
		return out, err
	})

	type appDetailOut struct{ Body AppDetailOut }
	huma.Register(api, huma.Operation{OperationID: "get-app", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}",
		Summary: "One app: settings, live and previous release, domains, processes", Tags: tags,
	}, func(ctx context.Context, in *AppPath) (*appDetailOut, error) {
		out := &appDetailOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body, err = d.appDetail(ctx, q, m, a)
			return err
		})
		return out, err
	})

	type analyticsIn struct {
		AppPath
		Range string `query:"range" enum:"1h,24h,7d,30d" default:"1h"`
	}
	type analyticsOut struct{ Body traffic.AnalyticsSeries }
	huma.Register(api, huma.Operation{OperationID: "get-app-analytics", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/analytics",
		Summary: "An app's traffic history for a range: requests over time, status breakdown, top paths and referrers, visitors", Tags: tags,
	}, func(ctx context.Context, in *analyticsIn) (*analyticsOut, error) {
		r, ok := traffic.Ranges[in.Range]
		if !ok {
			r = traffic.Ranges["1h"]
		}
		out := &analyticsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body, err = traffic.AppSeries(ctx, q, m.TeamID, a.ID, r, time.Now())
			if err != nil {
				return huma.Error500InternalServerError("could not read analytics")
			}
			return nil
		})
		return out, err
	})

	type appOut struct{ Body AppOut }
	huma.Register(api, huma.Operation{OperationID: "update-app", Method: http.MethodPatch, Path: "/api/teams/{team}/apps/{app}",
		Summary: "Change an app's branch, port, health check or auto-deploy pause (owner or admin; applies from the next deploy; git apps only), " +
			"and its appearance — title, description, color, icon (any app, imported ones too; audited as app.appearance_updated). " +
			"The name is the slug of URLs, containers and hostnames and cannot be changed.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Branch             *string `json:"branch,omitempty" maxLength:"200"`
			Port               *int    `json:"port,omitempty" minimum:"1" maximum:"65535"`
			HealthcheckPath    *string `json:"healthcheck_path,omitempty" maxLength:"200"`
			HealthcheckTimeout *int    `json:"healthcheck_timeout,omitempty" minimum:"5" maximum:"600"`
			AutodeployPaused   *bool   `json:"autodeploy_paused,omitempty"`
			Title              *string `json:"title,omitempty" maxLength:"200" doc:"Display name: at most 80 characters once trimmed; empty clears it."`
			Description        *string `json:"description,omitempty" maxLength:"1000" doc:"At most 280 characters once trimmed; empty clears it."`
			Color              *string `json:"color,omitempty" maxLength:"16" doc:"#RRGGBB; empty clears it."`
			Icon               *string `json:"icon,omitempty" maxLength:"32" doc:"An icon key (the servers' list); empty clears it."`
		}
	}) (*appOut, error) {
		out := &appOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			b := in.Body
			branch, port, path, timeout, paused := deploy.BranchOf(a), int(a.Port), a.HealthcheckPath, int(a.HealthcheckTimeout), a.AutodeployPaused
			if b.Branch != nil {
				branch = *b.Branch
			}
			if b.Port != nil {
				port = *b.Port
			}
			if b.HealthcheckPath != nil {
				path = *b.HealthcheckPath
			}
			if b.HealthcheckTimeout != nil {
				timeout = *b.HealthcheckTimeout
			}
			if b.AutodeployPaused != nil {
				paused = *b.AutodeployPaused
			}
			look := deploy.AppearancePatch{Title: b.Title, Description: b.Description, Color: b.Color, Icon: b.Icon}
			settings := b.Branch != nil || b.Port != nil || b.HealthcheckPath != nil || b.HealthcheckTimeout != nil || b.AutodeployPaused != nil
			updated := a
			// Settings are a git app's (an imported one is refused); appearance is any app's. One
			// transaction: a refusal of either leaves both unchanged.
			if settings || look.Empty() {
				if updated, err = d.deploys().UpdateSettings(ctx, q, m.TeamID, webActor(id), a, branch, port, timeout, path, paused); err != nil {
					return actionErr(err, "could not update the app")
				}
			}
			if !look.Empty() {
				if updated, err = d.deploys().UpdateAppearance(ctx, q, m.TeamID, webActor(id), updated, look); err != nil {
					return actionErr(err, "could not update the app")
				}
			}
			out.Body = appOutWithLogo(ctx, q, m.TeamID, updated, "")
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-app", Method: http.MethodDelete, Path: "/api/teams/{team}/apps/{app}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Delete an app (owner or admin; confirm = its name): its route, containers and images on the server go too. " +
			"An imported app: without destroy only Hosbah's record goes (tombstoned; the workload keeps running); with ?destroy=true " +
			"the real guest or container is destroyed (202 with the operation) and the record goes once the agent reports it gone.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Destroy bool `query:"destroy" doc:"Imported apps: destroy the real workload (a guest is stopped and destroyed, a container removed), then the record."`
		Body    struct {
			Confirm string `json:"confirm" minLength:"1" maxLength:"64" doc:"The app's name, exactly."`
			Purge   bool   `json:"purge,omitempty" doc:"With destroy: also purge the guest from backup jobs, replication and HA (pvesh --purge), or remove a container's anonymous volumes."`
		}
	}) (*workloadOpOut, error) {
		out := &workloadOpOut{Status: http.StatusNoContent}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if in.Body.Confirm != a.Name {
				return huma.Error422UnprocessableEntity("confirm must equal the app's name — nothing was deleted")
			}
			if in.Destroy {
				op, err := d.deploys().Destroy(ctx, q, m.TeamID, webActor(id), deploy.WorkloadOfApp(a), in.Body.Purge)
				if err != nil {
					return actionErr(err, "could not destroy the workload")
				}
				out.Status, out.Body = http.StatusAccepted, &workloadOpBody{Operation: toOperationOut(op)}
				return nil
			}
			if in.Body.Purge {
				return huma.Error422UnprocessableEntity("purge applies with destroy=true")
			}
			return actionErr(d.deploys().DeleteApp(ctx, q, m.TeamID, webActor(id), a), "could not delete the app")
		})
		return out, err
	})

	type hookOut struct {
		Body struct {
			URL string `json:"url" doc:"Shown once."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "rotate-app-deploy-hook", Method: http.MethodPost, Path: "/api/teams/{team}/apps/{app}/deploy-hook",
		Summary: "Replace the app's deploy-hook URL (owner or admin); the old one stops working", Tags: tags,
	}, func(ctx context.Context, in *AppPath) (*hookOut, error) {
		out := &hookOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body.URL, err = d.deploys().RotateHook(ctx, q, m.TeamID, webActor(id), a)
			return actionErr(err, "could not rotate the deploy hook")
		})
		return out, err
	})

	// ---- deployments ----

	type deploymentOut struct {
		Status int
		Body   DeploymentOut
	}
	huma.Register(api, huma.Operation{OperationID: "create-deployment", Method: http.MethodPost, Path: "/api/teams/{team}/apps/{app}/deployments",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Deploy a ref — a branch, a tag or a chosen commit (its full sha) — default: the app's branch. Owner, admin or deployer. A deploy still waiting is superseded.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Ref   string `json:"ref,omitempty" maxLength:"200" doc:"Branch, tag or a full 40-character commit sha (e.g. one from …/repository/commits) — the build checks out exactly this ref instead of the branch head. Default: the app's branch. A short sha cannot be fetched."`
			Track string `json:"track,omitempty" enum:"auto,fast,full" doc:"Override the app's deploy configuration for this run only: auto defers to it, fast and full are stored as asked. Default: auto."`
		}
	}) (*deploymentOut, error) {
		out := &deploymentOut{Status: http.StatusCreated}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDeploy, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			dep, err := d.deploys().QueueDeployment(ctx, q, m.TeamID, webActor(id), a, "deploy", in.Body.Ref, nil, in.Body.Track)
			if err != nil {
				return actionErr(err, "could not queue the deploy")
			}
			out.Body = toDeploymentOut(dep)
			return nil
		})
		return out, err
	})

	type deploymentsOut struct {
		Body struct {
			Deployments []DeploymentOut `json:"deployments"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-deployments", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/deployments",
		Summary: "The app's deployments, newest first", Tags: tags,
	}, func(ctx context.Context, in *struct {
		AppPath
		Limit int `query:"limit" minimum:"1" maximum:"200" default:"30"`
	}) (*deploymentsOut, error) {
		out := &deploymentsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body.Deployments, err = d.deploymentsOf(ctx, q, m, a, int32(in.Limit))
			return err
		})
		return out, err
	})

	type deploymentDetailOut struct {
		Body struct {
			Deployment DeploymentOut `json:"deployment"`
			Logs       []ops.LogLine `json:"logs"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "get-deployment", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/deployments/{deployment}",
		Summary: "One deployment (id or number) with its build and deploy log", Tags: tags,
	}, func(ctx context.Context, in *DeploymentPath) (*deploymentDetailOut, error) {
		out := &deploymentDetailOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			dep, lines, err := d.deploymentWithLogs(ctx, q, m, in.App, in.Deployment, 0, 10000)
			if err != nil {
				return err
			}
			out.Body.Deployment, out.Body.Logs = toDeploymentOut(dep), lines
			return nil
		})
		return out, err
	})

	// Live deployment log: checked before a byte is streamed and on every read, like operation logs.
	huma.Register(api, huma.Operation{OperationID: "stream-deployment", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/deployments/{deployment}/stream",
		Summary: "Live build and deploy log (text/event-stream: events line, status, done)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		DeploymentPath
		LastEventID string `header:"Last-Event-ID" maxLength:"20"`
		After       int64  `query:"after" minimum:"0"`
	}) (*huma.StreamResponse, error) {
		var depID string
		if err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			dep, _, err := d.deploymentWithLogs(ctx, q, m, in.App, in.Deployment, 0, 0)
			depID = dep.ID
			return err
		}); err != nil {
			return nil, err
		}
		after := in.After
		if n, err := strconv.ParseInt(in.LastEventID, 10, 64); err == nil && n > after {
			after = n
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			sse := newSSE(hctx)
			wake, unsub := d.ops.Broker().Subscribe(ops.DeployTopic(depID))
			defer unsub()
			lastStatus := ""
			for {
				var dep db.Deployment
				var lines []ops.LogLine
				err := d.inTeam(sse.ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
					var err error
					dep, lines, err = d.deploymentWithLogs(sse.ctx, q, m, in.App, depID, after, 500)
					return err
				})
				if err != nil {
					sse.event("gone", "", map[string]string{"error": "not found"})
					return
				}
				for _, l := range lines {
					sse.event("line", strconv.FormatInt(l.Seq, 10), l)
					after = l.Seq
				}
				if b, _ := json.Marshal(toDeploymentOut(dep)); string(b) != lastStatus {
					sse.raw("status", "", b)
					lastStatus = string(b)
				}
				if deploymentDone(dep.Status) && len(lines) < 500 {
					sse.event("done", "", map[string]string{"status": dep.Status})
					return
				}
				if !sse.wait(wake, 3*time.Second) {
					return
				}
			}
		}}, nil
	})

	type cancelOut struct {
		Body struct {
			Message    string        `json:"message"`
			Deployment DeploymentOut `json:"deployment"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "cancel-deployment", Method: http.MethodPost, Path: "/api/teams/{team}/apps/{app}/deployments/{deployment}/cancel",
		Summary: "Cancel a waiting deployment, or stop a running one before it switches traffic (owner, admin or deployer)", Tags: tags,
	}, func(ctx context.Context, in *DeploymentPath) (*cancelOut, error) {
		out := &cancelOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDeploy, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if err := refuseImportedApp(a); err != nil {
				return err
			}
			dep, err := deploy.DeploymentByRef(ctx, q, m.TeamID, a, in.Deployment)
			if err != nil {
				return errNotFound
			}
			msg, err := d.deploys().CancelDeployment(ctx, q, m.TeamID, webActor(id), dep)
			if err != nil {
				return actionErr(err, "could not cancel")
			}
			fresh, _ := q.DeploymentInTeam(ctx, db.DeploymentInTeamParams{ID: dep.ID, TeamID: m.TeamID})
			out.Body.Message, out.Body.Deployment = msg, toDeploymentOut(fresh)
			return nil
		})
		return out, err
	})

	type rollbackOut struct {
		Status int
		Body   struct {
			Deployment DeploymentOut `json:"deployment"`
			Release    ReleaseOut    `json:"release"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "rollback-app", Method: http.MethodPost, Path: "/api/teams/{team}/apps/{app}/rollback",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Re-point the app at a kept release — the previous one by default — without a build (owner, admin or deployer)",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Release string `json:"release,omitempty" maxLength:"64" doc:"Release id or version (\"3\" / \"v3\"). Default: the previous release."`
		}
	}) (*rollbackOut, error) {
		out := &rollbackOut{Status: http.StatusCreated}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDeploy, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			dep, rel, err := d.deploys().Rollback(ctx, q, m.TeamID, webActor(id), a, in.Body.Release)
			if err != nil {
				return actionErr(err, "could not roll back")
			}
			out.Body.Deployment, out.Body.Release = toDeploymentOut(dep), toReleaseOut(rel)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "restart-app", Method: http.MethodPost, Path: "/api/teams/{team}/apps/{app}/restart",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Start the live release in fresh containers (new env values apply), health-gated like a deploy (owner, admin or deployer)",
	}, func(ctx context.Context, in *AppPath) (*deploymentOut, error) {
		out := &deploymentOut{Status: http.StatusCreated}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDeploy, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			dep, err := d.deploys().Restart(ctx, q, m.TeamID, webActor(id), a)
			if err != nil {
				return actionErr(err, "could not restart")
			}
			out.Body = toDeploymentOut(dep)
			return nil
		})
		return out, err
	})

	type adoptProposalOut struct {
		Status int
		Body   deploy.AdoptProposal
	}
	huma.Register(api, huma.Operation{OperationID: "adopt-app-proposal", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/adopt/proposal",
		Summary: "Guessed fields to pre-fill the adopt dialog for an imported app — a repo, branch, build type and port, each with a confidence and where it came from (owner, admin or deployer)",
		Tags:    tags,
	}, func(ctx context.Context, in *AppPath) (*adoptProposalOut, error) {
		out := &adoptProposalOut{Status: http.StatusOK}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDeploy, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			prop, err := d.deploys().Propose(ctx, q, m.TeamID, a)
			if err != nil {
				if coded := asImported(err); coded != nil {
					return coded
				}
				return actionErr(err, "could not put together an adopt proposal")
			}
			out.Body = prop
			return nil
		})
		return out, err
	})

	type adoptOut struct {
		Status int
		Body   struct {
			App     AppOut      `json:"app"`
			Release *ReleaseOut `json:"release,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "adopt-app", Method: http.MethodPost, Path: "/api/teams/{team}/apps/{app}/adopt",
		DefaultStatus: http.StatusOK, Tags: tags,
		Summary: "Turn an imported app into a Hosbah-built git app in place — no rebuild is required to take it over; the running workload keeps serving until the first real deploy (owner, admin or deployer)",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			RepoURL string `json:"repo_url" maxLength:"512" doc:"The app's git repository (github.com/owner/repo, or a full URL)."`
			Branch  string `json:"branch,omitempty" maxLength:"200" doc:"Default: main."`
			Build   string `json:"build,omitempty" enum:"auto,dockerfile,railpack" doc:"Default: auto — Hosbah still detects a Dockerfile at deploy time."`
			Port    int    `json:"port,omitempty" doc:"Default: the app's current port."`
		}
	}) (*adoptOut, error) {
		out := &adoptOut{Status: http.StatusOK}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDeploy, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			updated, rel, err := d.deploys().Adopt(ctx, q, m.TeamID, webActor(id), a, deploy.AdoptInput{
				RepoURL: in.Body.RepoURL, Branch: in.Body.Branch, Build: in.Body.Build, Port: in.Body.Port})
			if err != nil {
				if coded := asImported(err); coded != nil {
					return coded
				}
				return actionErr(err, "could not adopt")
			}
			server, _ := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: updated.ServerID, TeamID: m.TeamID})
			out.Body.App = toAppOut(updated, server.Name)
			if rel != nil {
				o := toReleaseOut(*rel)
				out.Body.Release = &o
			}
			return nil
		})
		return out, err
	})

	type confirmStopOut struct{ Body AppOut }
	huma.Register(api, huma.Operation{OperationID: "confirm-adopted-workload-stopped", Method: http.MethodPost,
		Path: "/api/teams/{team}/apps/{app}/adopt/confirm-stop", DefaultStatus: http.StatusOK, Tags: tags,
		Summary: "Confirm the old workload of an adopted guest or systemd unit has stopped — Hosbah never stops one on its own; a container is retired automatically by its first deploy instead (owner, admin or deployer)",
	}, func(ctx context.Context, in *AppPath) (*confirmStopOut, error) {
		out := &confirmStopOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDeploy, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			updated, err := d.deploys().ConfirmAdoptedWorkloadStopped(ctx, q, m.TeamID, webActor(id), a)
			if err != nil {
				if coded := asImported(err); coded != nil {
					return coded
				}
				return actionErr(err, "could not confirm")
			}
			server, _ := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: updated.ServerID, TeamID: m.TeamID})
			out.Body = toAppOut(updated, server.Name)
			return nil
		})
		return out, err
	})

	type connectGuestOut struct {
		Status int
		Body   struct {
			Server  ServerOut    `json:"server"`
			Install *ops.Install `json:"install,omitempty" doc:"The new server's one-time install command, already queued to run inside the guest. Shown once; the token is not stored. Absent when this app was already connected."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "connect-guest", Method: http.MethodPost, Path: "/api/teams/{team}/apps/{app}/connect-guest",
		DefaultStatus: http.StatusOK, Tags: tags,
		Summary: "Put a Hosbah agent inside an imported Proxmox guest: creates a server for it nested under the node and asks the node to install the agent inside the guest (owner or admin)",
	}, func(ctx context.Context, in *AppPath) (*connectGuestOut, error) {
		out := &connectGuestOut{Status: http.StatusOK}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			row, inst, err := d.deploys().ConnectGuest(ctx, q, m.TeamID, webActor(id), a)
			if err != nil {
				return actionErr(err, "could not connect the guest")
			}
			out.Body.Server = toServerOut(row, time.Now(), true)
			out.Body.Install = inst
			return nil
		})
		return out, err
	})

	type releasesOut struct {
		Body struct {
			Releases []ReleaseOut `json:"releases"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-releases", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/releases",
		Summary: "Releases that took traffic: live, kept for rollback, or pruned", Tags: tags,
	}, func(ctx context.Context, in *AppPath) (*releasesOut, error) {
		out := &releasesOut{}
		out.Body.Releases = []ReleaseOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			rows, err := q.ListReleasesOfApp(ctx, db.ListReleasesOfAppParams{AppID: a.ID, TeamID: m.TeamID, Limit: 50})
			if err != nil {
				return huma.Error500InternalServerError("could not list releases")
			}
			for _, r := range rows {
				out.Body.Releases = append(out.Body.Releases, toReleaseOut(r))
			}
			return nil
		})
		return out, err
	})

	// ---- env ----

	type envOut struct {
		Body struct {
			Variables []EnvVarOut `json:"variables"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-env", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/env",
		Summary: "The app's environment variables — names only", Tags: tags,
	}, func(ctx context.Context, in *AppPath) (*envOut, error) {
		out := &envOut{}
		out.Body.Variables = []EnvVarOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			rows, err := q.ListEnvVarsOfApp(ctx, db.ListEnvVarsOfAppParams{AppID: a.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not list variables")
			}
			for _, r := range rows {
				out.Body.Variables = append(out.Body.Variables, EnvVarOut{Name: r.Name, UpdatedAt: r.UpdatedAt})
			}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "set-env", Method: http.MethodPut, Path: "/api/teams/{team}/apps/{app}/env/{name}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Set a variable (owner or admin). Sealed with the team key; applies from the next deploy or restart.",
	}, func(ctx context.Context, in *struct {
		EnvPath
		Body struct {
			Value string `json:"value" maxLength:"32768"`
		}
	}) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if err := actionErr(d.deploys().SetEnv(ctx, q, m.TeamID, webActor(id), a, in.Name, in.Body.Value), "could not set the variable"); err != nil {
				return err
			}
			// Best-effort: re-match the app's env to shared/data services. Never blocks the save.
			_, _ = services.Detect(ctx, q, d.ops, m.TeamID, a)
			return nil
		})
		return nil, err
	})

	huma.Register(api, huma.Operation{OperationID: "unset-env", Method: http.MethodDelete, Path: "/api/teams/{team}/apps/{app}/env/{name}",
		DefaultStatus: http.StatusNoContent, Tags: tags, Summary: "Remove a variable (owner or admin)",
	}, func(ctx context.Context, in *EnvPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if err := actionErr(d.deploys().UnsetEnv(ctx, q, m.TeamID, webActor(id), a, in.Name), "could not remove the variable"); err != nil {
				return err
			}
			_, _ = services.Detect(ctx, q, d.ops, m.TeamID, a)
			return nil
		})
		return nil, err
	})

	type envRevealOut struct {
		Body struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "reveal-env", Method: http.MethodPost, Path: "/api/teams/{team}/apps/{app}/env/{name}/reveal",
		Tags: tags, Summary: "Reveal one value (owner or admin). Audited.",
	}, func(ctx context.Context, in *EnvPath) (*envRevealOut, error) {
		out := &envRevealOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityRevealSecrets, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			v, err := d.deploys().RevealEnv(ctx, q, m.TeamID, webActor(id), a, in.Name)
			if err != nil {
				return actionErr(err, "could not reveal the variable")
			}
			out.Body.Name, out.Body.Value = in.Name, v
			return nil
		})
		return out, err
	})

	// ---- domains ----

	type domainsOut struct {
		Body struct {
			Domains []DomainOut `json:"domains"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-domains", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/domains",
		Summary: "The app's hostnames", Tags: tags,
	}, func(ctx context.Context, in *AppPath) (*domainsOut, error) {
		out := &domainsOut{}
		out.Body.Domains = []DomainOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			rows, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not list domains")
			}
			for _, r := range rows {
				out.Body.Domains = append(out.Body.Domains, toDomainOut(r))
			}
			return nil
		})
		return out, err
	})

	type domainOut struct{ Body DomainOut }
	huma.Register(api, huma.Operation{OperationID: "add-domain", Method: http.MethodPost, Path: "/api/teams/{team}/apps/{app}/domains",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Route a hostname to the app (owner or admin); a live app is re-routed at once",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Hostname string `json:"hostname" minLength:"3" maxLength:"253"`
			TLS      string `json:"tls,omitempty" enum:"auto,internal,off" doc:"auto: public certificate once verified; internal: Caddy's local CA; off: plain HTTP."`
			Port     int    `json:"port,omitempty" minimum:"0" maximum:"65535" doc:"Imported apps only: the port to route to — a container's published host port (default: the first one), or the port a Proxmox guest's site listens on (required for a guest)."`
		}
	}) (*domainOut, error) {
		out := &domainOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			dm, err := d.deploys().AddDomain(ctx, q, m.TeamID, webActor(id), a, in.Body.Hostname, in.Body.TLS, in.Body.Port)
			if err != nil {
				return actionErr(err, "could not add the domain")
			}
			out.Body = toDomainOut(dm)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "remove-domain", Method: http.MethodDelete, Path: "/api/teams/{team}/apps/{app}/domains/{domain}",
		DefaultStatus: http.StatusNoContent, Tags: tags, Summary: "Stop routing a hostname to the app (owner or admin)",
	}, func(ctx context.Context, in *DomainPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			dm, err := deploy.DomainByRef(ctx, q, m.TeamID, a, in.Domain)
			if err != nil {
				return errNotFound
			}
			return actionErr(d.deploys().RemoveDomain(ctx, q, m.TeamID, webActor(id), a, dm), "could not remove the domain")
		})
		return nil, err
	})

	type verifyOut struct {
		Body struct {
			Verified bool      `json:"verified"`
			Message  string    `json:"message"`
			Domain   DomainOut `json:"domain"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "verify-domain", Method: http.MethodPost, Path: "/api/teams/{team}/apps/{app}/domains/{domain}/verify",
		Tags: tags, Summary: "Check the _hosbah-verify TXT record (a pre-rename _cloudy-verify record also counts) (owner or admin). Verification gates public certificates.",
	}, func(ctx context.Context, in *DomainPath) (*verifyOut, error) {
		out := &verifyOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			dm, err := deploy.DomainByRef(ctx, q, m.TeamID, a, in.Domain)
			if err != nil {
				return errNotFound
			}
			ok, msg, err := d.deploys().VerifyDomain(ctx, q, m.TeamID, webActor(id), a, dm)
			if err != nil {
				return actionErr(err, "could not verify the domain")
			}
			fresh, _ := deploy.DomainByRef(ctx, q, m.TeamID, a, dm.ID)
			out.Body.Verified, out.Body.Message, out.Body.Domain = ok, msg, toDomainOut(fresh)
			return nil
		})
		return out, err
	})

	// ---- runtime logs, live app page ----

	type appOpOut struct {
		Status int
		Body   OperationOut
	}
	huma.Register(api, huma.Operation{OperationID: "app-logs", Method: http.MethodPost, Path: "/api/teams/{team}/apps/{app}/logs",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Relay runtime logs (docker logs --follow, bounded) as an operation; stream it like any other (owner, admin or deployer)",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Process       string `json:"process,omitempty" maxLength:"40" doc:"web (default) or worker:<name>"`
			Tail          int    `json:"tail,omitempty" minimum:"0" maximum:"1000" doc:"Lines of history (default 200)."`
			FollowSeconds int    `json:"follow_seconds,omitempty" minimum:"0" maximum:"300" doc:"Then follow for this long (default 60)."`
		}
	}) (*appOpOut, error) {
		out := &appOpOut{Status: http.StatusCreated}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityDeploy, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			tail, follow := in.Body.Tail, in.Body.FollowSeconds
			if tail == 0 {
				tail = 200
			}
			if follow == 0 {
				follow = 60
			}
			op, err := d.deploys().RuntimeLogs(ctx, q, m.TeamID, webActor(id), a, in.Body.Process, tail, follow)
			if err != nil {
				return actionErr(err, "could not relay the logs")
			}
			out.Body = toOperationOut(op)
			return nil
		})
		return out, err
	})

	// ---- H4: log search ----

	type appLogSearchOut struct{ Body AppLogsSearchOut }
	huma.Register(api, huma.Operation{OperationID: "search-app-logs", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/logs/search", Tags: tags,
		Summary: "Search the app's current containers' logs (a plain substring or an RE2 regex, optionally case-insensitive, with a level " +
			"filter and a time window up to 7 days). The agent filters the lines itself and relays back only the matches. result is null if " +
			"the agent did not answer within wait (owner, admin, deployer or viewer).",
	}, func(ctx context.Context, in *struct {
		AppPath
		WaitQuery
		Query           string `query:"q" maxLength:"500" doc:"A plain substring, or an RE2 pattern when regex=true. Empty matches every line."`
		Regex           bool   `query:"regex" doc:"Treat q as an RE2 regular expression."`
		CaseInsensitive bool   `query:"case_insensitive"`
		Process         string `query:"process" maxLength:"40" doc:"web (default: every process) or worker:<name>"`
		Level           string `query:"level" enum:",error,warn" doc:"Only lines the heuristic reads as this level."`
		Since           string `query:"since" maxLength:"40" doc:"15m, 1h, 6h, 24h, 7d, or an RFC 3339 time; default one hour before until."`
		Until           string `query:"until" maxLength:"40" doc:"15m, 1h, 6h, 24h, 7d, or an RFC 3339 time; default now."`
		Limit           int    `query:"limit" minimum:"0" maximum:"1000" doc:"At most this many matching lines (default 200)."`
	}) (*appLogSearchOut, error) {
		var op db.Operation
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			op, err = d.deploys().LogsSearch(ctx, q, m.TeamID, webActor(id), a, agentproto.LogsSearchParams{
				Process: in.Process, Query: in.Query, Regex: in.Regex, CaseInsensitive: in.CaseInsensitive,
				Level: in.Level, Since: in.Since, Until: in.Until, Limit: in.Limit,
			})
			if err != nil {
				return actionErr(err, "could not search the logs")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		var res agentproto.LogsSearchResult
		done, ok, err := d.awaitInto(ctx, op, in.Wait, &res)
		if err != nil {
			return nil, err
		}
		out := &appLogSearchOut{Body: AppLogsSearchOut{Operation: toOperationOut(done)}}
		if ok {
			out.Body.Result = &res
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{OperationID: "stream-app", Method: http.MethodGet, Path: "/api/teams/{team}/apps/{app}/stream",
		Summary: "Live app page: the app (status, releases, domains) and its deployments (text/event-stream: events app, deployments)", Tags: tags,
	}, func(ctx context.Context, in *AppPath) (*huma.StreamResponse, error) {
		var appID string
		if err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			appID = a.ID
			return err
		}); err != nil {
			return nil, err
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			sse := newSSE(hctx)
			wake, unsub := d.ops.Broker().Subscribe(ops.AppTopic(appID))
			defer unsub()
			var lastApp, lastDeps []byte
			for {
				var detail AppDetailOut
				var deps []DeploymentOut
				err := d.inTeam(sse.ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
					a, err := appIn(sse.ctx, q, m, appID)
					if err != nil {
						return err
					}
					if detail, err = d.appDetail(sse.ctx, q, m, a); err != nil {
						return err
					}
					deps, err = d.deploymentsOf(sse.ctx, q, m, a, 30)
					return err
				})
				if err != nil {
					sse.event("gone", "", map[string]string{"error": "not found"})
					return
				}
				if b, _ := json.Marshal(detail); string(b) != string(lastApp) {
					sse.raw("app", "", b)
					lastApp = b
				}
				if b, _ := json.Marshal(deps); string(b) != string(lastDeps) {
					sse.raw("deployments", "", b)
					lastDeps = b
				}
				if !sse.wait(wake, 5*time.Second) {
					return
				}
			}
		}}, nil
	})

	// ---- the create-app wizard's runtime detection ----

	huma.Register(api, huma.Operation{OperationID: "detect-app-runtime", Method: http.MethodPost, Path: "/api/teams/{team}/servers/{server}/detect",
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Clone a public repository on the server and say how it would build (Dockerfile, or the language Railpack detects). Owner or admin.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			RepoURL string `json:"repo_url" minLength:"1" maxLength:"500"`
			Branch  string `json:"branch,omitempty" maxLength:"200"`
		}
	}) (*appOpOut, error) {
		out := &appOpOut{Status: http.StatusCreated}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			op, err := d.deploys().Detect(ctx, q, m.TeamID, webActor(id), s, in.Body.RepoURL, in.Body.Branch)
			if err != nil {
				return actionErr(err, "could not start the detection")
			}
			out.Body = toOperationOut(op)
			return nil
		})
		return out, err
	})

	// ---- integrations, notifications ----

	type integrationsOut struct {
		Body struct {
			GitHubApp struct {
				Configured bool   `json:"configured"`
				Slug       string `json:"slug,omitempty"`
				InstallURL string `json:"install_url,omitempty"`
			} `json:"github_app"`
			AppsDomain struct {
				Configured bool `json:"configured" doc:"A Hosbah-owned apex for default app hostnames (<app>-<team>.<apex>). Not decided yet: apps use their own domains."`
			} `json:"apps_domain"`
			RailpackVersion string `json:"railpack_version"`
			CaddyVersion    string `json:"caddy_version"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "get-integrations", Method: http.MethodGet, Path: "/api/teams/{team}/integrations",
		Summary: "What this Hosbah can do for the team's apps: the GitHub App, a default apps domain, tool versions", Tags: tags,
	}, func(ctx context.Context, in *TeamPath) (*integrationsOut, error) {
		out := &integrationsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(*db.Queries, *sql.Tx, *auth.Identity, *tenancy.Membership) error {
			if s := d.deploys(); s != nil && s.GitHub().Configured() {
				out.Body.GitHubApp.Configured = true
				out.Body.GitHubApp.Slug = s.GitHub().Slug()
				if slug := s.GitHub().Slug(); slug != "" {
					out.Body.GitHubApp.InstallURL = "https://github.com/apps/" + slug + "/installations/new"
				}
			}
			out.Body.RailpackVersion, out.Body.CaddyVersion = deploy.RailpackVersion, deploy.CaddyVersion
			return nil
		})
		return out, err
	})

	type notificationsOut struct {
		Body struct {
			Notifications []NotificationOut `json:"notifications"`
			Unread        int               `json:"unread"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-notifications", Method: http.MethodGet, Path: "/api/teams/{team}/notifications",
		Summary: "The team's recent notifications (failed deploys…)", Tags: []string{"Notifications"},
	}, func(ctx context.Context, in *TeamPath) (*notificationsOut, error) {
		out := &notificationsOut{}
		out.Body.Notifications = []NotificationOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListNotificationsOfTeam(ctx, db.ListNotificationsOfTeamParams{TeamID: m.TeamID, UserID: id.ID, Lim: 50})
			if err != nil {
				return huma.Error500InternalServerError("could not list notifications")
			}
			for _, n := range rows {
				out.Body.Notifications = append(out.Body.Notifications, NotificationOut{ID: n.ID, Kind: n.Kind, Title: n.Title, Body: n.Body,
					SubjectType: n.SubjectType, SubjectID: n.SubjectID, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt})
				if n.ReadAt == nil {
					out.Body.Unread++
				}
			}
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "mark-notifications-read", Method: http.MethodPost, Path: "/api/teams/{team}/notifications/read",
		DefaultStatus: http.StatusNoContent, Summary: "Mark the team's notifications read", Tags: []string{"Notifications"},
	}, func(ctx context.Context, in *TeamPath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			return q.MarkNotificationsRead(ctx, db.MarkNotificationsReadParams{TeamID: m.TeamID, UserID: id.ID})
		})
		return nil, err
	})

	huma.Register(api, huma.Operation{OperationID: "stream-notifications", Method: http.MethodGet, Path: "/api/teams/{team}/notifications/stream",
		Summary: "Live notifications bell (text/event-stream: event notifications, the same shape as list-notifications)", Tags: []string{"Notifications"},
	}, func(ctx context.Context, in *TeamPath) (*huma.StreamResponse, error) {
		var teamID string
		if err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(_ *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			teamID = m.TeamID
			return nil
		}); err != nil {
			return nil, err
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			sse := newSSE(hctx)
			wake, unsub := d.ops.Broker().Subscribe(ops.TeamTopic(teamID))
			defer unsub()
			var last []byte
			for {
				out := notificationsOut{}
				out.Body.Notifications = []NotificationOut{}
				err := d.inTeam(sse.ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
					rows, err := q.ListNotificationsOfTeam(sse.ctx, db.ListNotificationsOfTeamParams{TeamID: m.TeamID, UserID: id.ID, Lim: 50})
					if err != nil {
						return err
					}
					for _, n := range rows {
						out.Body.Notifications = append(out.Body.Notifications, NotificationOut{ID: n.ID, Kind: n.Kind, Title: n.Title, Body: n.Body,
							SubjectType: n.SubjectType, SubjectID: n.SubjectID, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt})
						if n.ReadAt == nil {
							out.Body.Unread++
						}
					}
					return nil
				})
				if err != nil {
					sse.event("gone", "", map[string]string{"error": "not found"})
					return
				}
				if b, _ := json.Marshal(out.Body); string(b) != string(last) {
					sse.raw("notifications", "", b)
					last = b
				}
				if !sse.wait(wake, 15*time.Second) {
					return
				}
			}
		}}, nil
	})

	_ = agentproto.KindAppBuild
}

// refuseImportedApp answers 409 imported_workload before a route looks anything else up (a domain,
// a deployment), so the refusal does not depend on what an imported app happens not to have.
func refuseImportedApp(a db.App) error {
	if deploy.IsImported(a) {
		return &codedErr{Status: http.StatusConflict, Code: deploy.ImportedCode, Message: deploy.ImportedMessage(a.Name)}
	}
	return nil
}
