package deploy

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/quota"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

/*
App actions — one implementation for the REST API and the user MCP. Every function runs in the
CALLER's team-scoped transaction (q), after the caller has checked the member's role; it names
team_id in every statement (RLS behind it), writes the audit entry on the same transaction, and
never returns a secret except RevealEnv's one value.
*/

// ActionError carries an HTTP-ish status for the API and a plain message for the MCP.
type ActionError struct {
	Status int
	Msg    string
	Code   string // machine-readable, e.g. ImportedCode; "" for most refusals
}

func (e *ActionError) Error() string { return e.Msg }

func notFound(what string) error { return &ActionError{Status: 404, Msg: what + " not found"} }
func conflict(msg string) error  { return &ActionError{Status: 409, Msg: msg} }
func invalid(msg string) error   { return &ActionError{Status: 422, Msg: msg} }

// asActionError copies a *quota.Error (or any other error) as-is when it is not one, so quota
// refusals reach the REST and MCP layers with the same 403 and message every other ActionError has.
func asActionError(err error) error {
	var qe *quota.Error
	if errors.As(err, &qe) {
		return &ActionError{Status: qe.Status, Msg: qe.Msg}
	}
	return err
}

// Actor is who asked: a member in the browser, an MCP token, a deploy hook, the GitHub App.
type Actor struct {
	UserID  string
	Via     string // web | mcp | hook | github
	TokenID string
}

func (a Actor) audit(extra map[string]any) map[string]any {
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

func (a Actor) trigger() string {
	switch a.Via {
	case "mcp", "hook", "github":
		return a.Via
	}
	return "manual"
}

// HookTokenPrefix marks an app deploy-hook token (cdh_ before the rename; still accepted).
const HookTokenPrefix = tokens.PrefixHook

// ---- apps --------------------------------------------------------------------------------------------

// CreateAppInput is what the wizard and create_app send.
type CreateAppInput struct {
	Name          string
	Server        string // id or name
	RepoURL       string
	Branch        string
	Port          int
	HealthPath    string
	HealthTimeout int
}

// CreateApp adds an app and returns it with its deploy-hook URL (shown once; only the hash is kept).
func (s *Service) CreateApp(ctx context.Context, q *db.Queries, teamID string, actor Actor, in CreateAppInput) (db.App, string, error) {
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if !agentproto.ValidAppName(in.Name) {
		return db.App{}, "", invalid("the name must be lowercase letters, digits and dashes (up to 40), starting with a letter or digit")
	}
	if err := agentproto.CheckRepoURL(in.RepoURL); err != nil {
		return db.App{}, "", invalid(err.Error())
	}
	in.Branch = strings.TrimSpace(in.Branch)
	if in.Branch == "" {
		in.Branch = "main"
	}
	if !agentproto.ValidRef(in.Branch) {
		return db.App{}, "", invalid("that is not a valid branch name")
	}
	if in.Port == 0 {
		in.Port = 8080
	}
	if in.HealthPath == "" {
		in.HealthPath = "/"
	}
	if in.HealthTimeout == 0 {
		in.HealthTimeout = 60
	}
	if in.Port < 1 || in.Port > 65535 || !strings.HasPrefix(in.HealthPath, "/") || in.HealthTimeout < 5 || in.HealthTimeout > 600 {
		return db.App{}, "", invalid("port 1–65535, a health-check path starting with /, and a timeout of 5–600 seconds")
	}
	srv, err := q.ServerInTeamByRef(ctx, db.ServerInTeamByRefParams{TeamID: teamID, Ref: in.Server})
	if err != nil {
		return db.App{}, "", notFound("server")
	}
	if srv.Status != "enrolled" {
		return db.App{}, "", conflict("that server's agent has not enrolled yet")
	}
	if err := quota.CheckApps(ctx, q, teamID); err != nil {
		return db.App{}, "", asActionError(err)
	}
	plain, hash := tokens.New(HookTokenPrefix)
	a, err := q.InsertApp(ctx, db.InsertAppParams{ID: newUUID(), TeamID: teamID, ServerID: srv.ID, Name: in.Name,
		RepoURL: agentproto.NormalizeRepoURL(in.RepoURL), Branch: in.Branch, Port: int64(in.Port), HealthcheckPath: in.HealthPath,
		HealthcheckTimeout: int64(in.HealthTimeout), DeployHookHash: &hash, CreatedBy: actor.UserID})
	if err != nil {
		if strings.Contains(err.Error(), "apps_team_name_key") {
			return db.App{}, "", conflict("an app with that name already exists in this team")
		}
		return db.App{}, "", err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "app.created", "app", a.ID, actor.audit(map[string]any{
		"name": a.Name, "server": srv.Name, "repo_url": a.RepoURL, "branch": a.Branch})); err != nil {
		return db.App{}, "", err
	}
	s.addAppsHostname(ctx, q, teamID, a, srv)
	return a, s.HookURL(plain), nil
}

// AppByRef resolves an app of the team by id or name.
func AppByRef(ctx context.Context, q *db.Queries, teamID, ref string) (db.App, error) {
	a, err := q.AppInTeamByRef(ctx, db.AppInTeamByRefParams{TeamID: teamID, Ref: strings.TrimSpace(ref)})
	if err != nil {
		return db.App{}, notFound("app")
	}
	return a, nil
}

// RotateHook replaces the app's deploy-hook token; the old URL stops working at once.
func (s *Service) RotateHook(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App) (string, error) {
	if err := refuseImported(a); err != nil {
		return "", err
	}
	plain, hash := tokens.New(HookTokenPrefix)
	if err := q.SetAppDeployHook(ctx, db.SetAppDeployHookParams{ID: a.ID, TeamID: teamID, DeployHookHash: &hash}); err != nil {
		return "", err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "app.deploy_hook_rotated", "app", a.ID, actor.audit(nil)); err != nil {
		return "", err
	}
	return s.HookURL(plain), nil
}

// UpdateSettings changes branch, port and health check (they apply from the next deploy).
func (s *Service) UpdateSettings(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, branch string, port, healthTimeout int, healthPath string, paused bool) (db.App, error) {
	if err := refuseImported(a); err != nil {
		return db.App{}, err
	}
	if !agentproto.ValidRef(branch) || port < 1 || port > 65535 || !strings.HasPrefix(healthPath, "/") || healthTimeout < 5 || healthTimeout > 600 {
		return db.App{}, invalid("a valid branch, port 1–65535, a health-check path starting with /, and a timeout of 5–600 seconds")
	}
	out, err := q.UpdateAppSettings(ctx, db.UpdateAppSettingsParams{Branch: branch, Port: int64(port), HealthcheckPath: healthPath,
		HealthcheckTimeout: int64(healthTimeout), AutodeployPaused: paused, ID: a.ID, TeamID: teamID})
	if err != nil {
		return db.App{}, err
	}
	return out, tenancy.Record(ctx, q, teamID, actor.UserID, "app.updated", "app", a.ID, actor.audit(map[string]any{
		"branch": branch, "port": port, "healthcheck_path": healthPath, "healthcheck_timeout": healthTimeout, "autodeploy_paused": paused}))
}

// SetPaused pauses or resumes deploys from the deploy hook and the GitHub App (manual deploys still work).
func (s *Service) SetPaused(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, paused bool) error {
	if err := refuseImported(a); err != nil {
		return err
	}
	if err := q.SetAppAutodeployPaused(ctx, db.SetAppAutodeployPausedParams{ID: a.ID, TeamID: teamID, AutodeployPaused: paused}); err != nil {
		return err
	}
	action := "app.autodeploy_resumed"
	if paused {
		action = "app.autodeploy_paused"
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, action, "app", a.ID, actor.audit(nil))
}

// DeleteApp removes the app: its route, containers and images on the server (an app.remove
// operation, which runs when the agent is connected), then every row it owns.
func (s *Service) DeleteApp(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App) error {
	if IsImported(a) {
		return s.forgetImportedApp(ctx, q, teamID, actor, a)
	}
	recent, err := q.ListDeploymentsOfApp(ctx, db.ListDeploymentsOfAppParams{AppID: a.ID, TeamID: teamID, Limit: 2})
	if err != nil {
		return err
	}
	for _, d := range recent {
		if d.Status == "running" {
			return conflict("a deployment is running — cancel it first")
		}
	}
	host := platformHost(ctx, q, teamID, a)
	raw, _ := json.Marshal(agentproto.RemoveParams{AppID: a.ID, App: a.Name})
	if _, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: a.ServerID, Kind: agentproto.KindAppRemove,
		Params: raw, CreatedBy: actor.UserID}); err != nil {
		return err
	}
	if n, err := q.DeleteAppInTeam(ctx, db.DeleteAppInTeamParams{ID: a.ID, TeamID: teamID}); err != nil || n == 0 {
		return notFound("app")
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "app.deleted", "app", a.ID, actor.audit(map[string]any{"name": a.Name})); err != nil {
		return err
	}
	s.removeAppsDNS(ctx, host)
	return nil
}

// ---- deployments -------------------------------------------------------------------------------------

// QueueDeployment asks for a deploy, rollback or restart. A deployment still waiting for the same
// app is superseded (the newest request wins); a running one finishes first.
//
// trackOverride is the caller's explicit choice for a "deploy" kind — "", "auto", "fast" or
// "full". "" and "auto" defer to the app's saved deploy configuration (app_deploy_configs.mode)
// and deploy.ChooseTrack; "fast" and "full" are stored as asked, bypassing that decision (the
// caller is taking responsibility for it). rollback and restart never build, so they carry no
// track. QueueDeployment itself still cannot diff a deploy's changed paths against the last
// successful release ahead of time — an "auto" deployment resolves to "full" here — but a "fast"
// deployment's own agent op (app.fast_build) computes that diff on the server, from its cached
// checkout, and aborts back to a full build when it sees a build-affecting path (runner.fastBuild).
func (s *Service) QueueDeployment(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, kind, ref string, target *string, trackOverride string) (db.Deployment, error) {
	if err := refuseImported(a); err != nil {
		return db.Deployment{}, err
	}
	if ref == "" {
		ref = BranchOf(a)
	}
	if kind == "deploy" && !agentproto.ValidRef(ref) {
		return db.Deployment{}, invalid("that is not a valid branch, tag or commit")
	}
	superseded, err := q.SupersedeQueuedDeployments(ctx, db.SupersedeQueuedDeploymentsParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return db.Deployment{}, err
	}
	createdBy := actor.UserID
	if createdBy == "" {
		createdBy = actor.Via
	}
	var track, trackReason *string
	if kind == "deploy" {
		t, reason := s.chooseTrack(ctx, q, teamID, a, trackOverride)
		track, trackReason = &t, &reason
	}
	d, err := q.InsertDeployment(ctx, db.InsertDeploymentParams{ID: newUUID(), TeamID: teamID, AppID: a.ID, ServerID: a.ServerID,
		Ref: ref, Trigger: actor.trigger(), Kind: kind, TargetReleaseID: target, CreatedBy: createdBy, Track: track, TrackReason: trackReason})
	if err != nil {
		return db.Deployment{}, err
	}
	extra := map[string]any{"app": a.Name, "kind": kind, "ref": ref, "number": d.Number}
	if len(superseded) > 0 {
		extra["superseded"] = superseded
	}
	if track != nil {
		extra["track"] = *track
	}
	if err := tenancy.Record(ctx, q, teamID, createdBy, "deployment.queued", "deployment", d.ID, actor.audit(extra)); err != nil {
		return db.Deployment{}, err
	}
	s.Wake()
	s.ops.Broker().Publish(ops.AppTopic(a.ID))
	return d, nil
}

// chooseTrack resolves a "deploy" kind's track: an explicit fast/full override is stored as
// asked; "" and "auto" fall back to the app's saved deploy configuration mode (default "auto")
// fed through ChooseTrack. See QueueDeployment's doc comment for why changed paths are always
// treated as unknown today.
func (s *Service) chooseTrack(ctx context.Context, q *db.Queries, teamID string, a db.App, trackOverride string) (string, string) {
	switch trackOverride {
	case TrackFast:
		return TrackFast, "fast rebuild requested"
	case TrackFull:
		return TrackFull, "full rebuild requested"
	}
	mode := "auto"
	phpActive := false
	if cfg, err := q.GetAppDeployConfig(ctx, db.GetAppDeployConfigParams{AppID: a.ID, TeamID: teamID}); err == nil {
		mode = cfg.Mode
		phpActive = phpConfigActive(cfg.PhpVersion, cfg.PhpIni)
	}
	lastOK := a.CurrentReleaseID != nil
	track, reason := ChooseTrack(mode, nil, lastOK)
	if track == TrackFast && phpActive {
		// MH-576 D2: the fast track reuses the current live image and never calls `railpack
		// prepare` again, so a pinned PHP version or ini override would silently not apply.
		return TrackFull, "PHP version or php.ini overrides configured"
	}
	return track, reason
}

// phpConfigActive reports whether an app_deploy_configs row's php_version/php_ini differ from the
// defaults ("auto", no overrides) — i.e. whether a build needs to actually apply them.
func phpConfigActive(version, iniJSON string) bool {
	if version != "" && version != "auto" {
		return true
	}
	var ini map[string]string
	_ = json.Unmarshal([]byte(iniJSON), &ini)
	return len(ini) > 0
}

// DeploymentByRef resolves a deployment of an app by id or number ("12" or "#12").
func DeploymentByRef(ctx context.Context, q *db.Queries, teamID string, a db.App, ref string) (db.Deployment, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	if n, err := strconv.ParseInt(ref, 10, 64); err == nil {
		rows, err := q.ListDeploymentsOfApp(ctx, db.ListDeploymentsOfAppParams{AppID: a.ID, TeamID: teamID, Limit: 1000})
		if err == nil {
			for _, d := range rows {
				if d.Number == n {
					return d, nil
				}
			}
		}
		return db.Deployment{}, notFound("deployment")
	}
	d, err := q.DeploymentInTeam(ctx, db.DeploymentInTeamParams{ID: ref, TeamID: teamID})
	if err != nil || d.AppID != a.ID {
		return db.Deployment{}, notFound("deployment")
	}
	return d, nil
}

// CancelDeployment cancels a waiting deployment at once, or asks a running one to stop: the step in
// progress is cancelled on the agent and nothing switches.
func (s *Service) CancelDeployment(ctx context.Context, q *db.Queries, teamID string, actor Actor, d db.Deployment) (string, error) {
	var msg string
	switch d.Status {
	case "queued":
		if n, err := q.CancelQueuedDeployment(ctx, db.CancelQueuedDeploymentParams{Reason: ptr("cancelled"), ID: d.ID, TeamID: teamID}); err != nil || n == 0 {
			return "", conflict("that deployment has already started — try again")
		}
		msg = fmt.Sprintf("Deployment #%d cancelled before it started.", d.Number)
	case "running":
		if n, err := q.RequestDeploymentCancel(ctx, db.RequestDeploymentCancelParams{ID: d.ID, TeamID: teamID}); err != nil || n == 0 {
			return "", conflict("that deployment has just finished")
		}
		msg = fmt.Sprintf("Deployment #%d is stopping: the current step is cancelled and traffic does not move.", d.Number)
	default:
		return "", conflict("that deployment has already finished (" + d.Status + ")")
	}
	s.ops.Broker().Publish(ops.DeployTopic(d.ID), ops.AppTopic(d.AppID))
	return msg, tenancy.Record(ctx, q, teamID, actor.UserID, "deployment.cancelled", "deployment", d.ID, actor.audit(map[string]any{"number": d.Number}))
}

// Rollback re-points the app at a kept release: release ("" = the one live before the current, or an
// id, or a version like "3" / "v3").
func (s *Service) Rollback(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, release string) (db.Deployment, db.Release, error) {
	if err := refuseImported(a); err != nil {
		return db.Deployment{}, db.Release{}, err
	}
	var target db.Release
	release = strings.TrimPrefix(strings.TrimSpace(release), "v")
	if release == "" {
		r, err := q.PreviousReleaseOfApp(ctx, a.ID)
		if err != nil || r.TeamID != teamID {
			return db.Deployment{}, db.Release{}, conflict("there is no earlier release to roll back to")
		}
		target = r
	} else {
		rows, err := q.ListReleasesOfApp(ctx, db.ListReleasesOfAppParams{AppID: a.ID, TeamID: teamID, Limit: 200})
		if err != nil {
			return db.Deployment{}, db.Release{}, err
		}
		for _, r := range rows {
			if r.ID == release || strconv.FormatInt(r.Version, 10) == release {
				target = r
			}
		}
		if target.ID == "" {
			return db.Deployment{}, db.Release{}, notFound("release")
		}
	}
	switch target.Status {
	case "live":
		return db.Deployment{}, db.Release{}, conflict(fmt.Sprintf("release v%d is already live", target.Version))
	case "pruned":
		return db.Deployment{}, db.Release{}, conflict(fmt.Sprintf("release v%d's image was pruned (the newest %d are kept) — deploy its commit again", target.Version, agentproto.DefaultKeepImages))
	}
	d, err := s.QueueDeployment(ctx, q, teamID, actor, a, "rollback", fmt.Sprintf("v%d", target.Version), &target.ID, "")
	return d, target, err
}

// Restart starts the live release in fresh containers (new env values take effect) with the same
// health gate and switch as a deploy.
func (s *Service) Restart(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App) (db.Deployment, error) {
	if err := refuseImported(a); err != nil {
		return db.Deployment{}, err
	}
	live, err := q.LiveReleaseOfApp(ctx, a.ID)
	if err != nil || live.TeamID != teamID {
		return db.Deployment{}, conflict("nothing is live to restart — deploy first")
	}
	return s.QueueDeployment(ctx, q, teamID, actor, a, "restart", fmt.Sprintf("v%d", live.Version), nil, "")
}

// ---- env -------------------------------------------------------------------------------------------

const maxEnvValue = 32 << 10

// SetEnv seals and stores a value (applied by the next deploy or restart).
func (s *Service) SetEnv(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, name, value string) error {
	if err := refuseImported(a); err != nil {
		return err
	}
	if !validEnvName(name) {
		return invalid("variable names are letters, digits and _, not starting with a digit")
	}
	if name == "PORT" {
		return invalid("PORT is set by Hosbah to the app's port — change the port instead")
	}
	if len(value) > maxEnvValue || strings.ContainsRune(value, 0) || !utf8.ValidString(value) {
		return invalid("values are UTF-8 text up to 32 KiB")
	}
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return err
	}
	sealed, err := crypto.Seal(dek, teamID, EnvSealContext(a.ID, name), []byte(value))
	if err != nil {
		return err
	}
	if _, err := q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{TeamID: teamID, AppID: a.ID, Name: name, ValueSealed: sealed, UpdatedBy: actor.UserID}); err != nil {
		return err
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "env.set", "app", a.ID, actor.audit(map[string]any{"name": name}))
}

// UnsetEnv removes a variable.
func (s *Service) UnsetEnv(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, name string) error {
	if err := refuseImported(a); err != nil {
		return err
	}
	n, err := q.DeleteEnvVarInApp(ctx, db.DeleteEnvVarInAppParams{AppID: a.ID, TeamID: teamID, Name: name})
	if err != nil {
		return err
	}
	if n == 0 {
		return notFound("variable")
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "env.unset", "app", a.ID, actor.audit(map[string]any{"name": name}))
}

// RevealEnv opens one value — the only path by which a stored value leaves Hosbah toward a person —
// and audits it in the same transaction.
func (s *Service) RevealEnv(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, name string) (string, error) {
	v, err := q.EnvVarInApp(ctx, db.EnvVarInAppParams{AppID: a.ID, TeamID: teamID, Name: name})
	if err != nil {
		return "", notFound("variable")
	}
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return "", err
	}
	pt, err := crypto.Open(dek, teamID, EnvSealContext(a.ID, name), v.ValueSealed)
	if err != nil {
		return "", errors.New("the value could not be opened")
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "env.revealed", "app", a.ID, actor.audit(map[string]any{"name": name})); err != nil {
		return "", err
	}
	return string(pt), nil
}

func validEnvName(s string) bool {
	if s == "" || len(s) > 128 || (s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// ---- domains ---------------------------------------------------------------------------------------

// NormalizeHostname lowercases and drops a trailing dot.
func NormalizeHostname(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

// AddDomain routes a hostname to the app (at once, if a release is live). An imported app is routed
// through a proxy route on its server to where the workload listens: port is the container's
// published port (0 = the first) or, for a Proxmox guest, the port its site listens on (required).
func (s *Service) AddDomain(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, hostname, tls string, port int) (db.Domain, error) {
	upstream := ""
	if IsImported(a) {
		w := WorkloadOfApp(a)
		if _, err := s.check(ctx, q, teamID, w); err != nil {
			return db.Domain{}, err
		}
		up, err := importedUpstream(w, port)
		if err != nil {
			return db.Domain{}, err
		}
		upstream = up
	} else if port != 0 {
		return db.Domain{}, invalid("port applies to imported apps; a Hosbah-built app is routed to its live release")
	}
	hostname = NormalizeHostname(hostname)
	if tls == "" {
		tls = "auto"
	}
	if !agentproto.ValidHostname(hostname) {
		return db.Domain{}, invalid("that is not a valid hostname (for example app.example.com)")
	}
	if !agentproto.ValidTLS(tls) {
		return db.Domain{}, invalid("tls must be auto, internal or off")
	}
	if s.apps.Owns(hostname) {
		return db.Domain{}, invalid("hostnames under " + s.apps.Domain + " are given to apps automatically")
	}
	if upstream != "" && s.hostnameTaken(ctx, hostname) {
		return db.Domain{}, conflict("that hostname is already routed in Hosbah")
	}
	tok := make([]byte, 16)
	_, _ = rand.Read(tok)
	d, err := q.InsertDomain(ctx, db.InsertDomainParams{TeamID: teamID, AppID: a.ID, Hostname: hostname, Tls: tls, VerifyToken: newVerifyToken(tok)})
	if err != nil {
		if strings.Contains(err.Error(), "domains_hostname_key") {
			return db.Domain{}, conflict("that hostname is already routed to an app in Hosbah")
		}
		return db.Domain{}, err
	}
	if upstream != "" {
		if err := q.SetDomainUpstream(ctx, db.SetDomainUpstreamParams{Upstream: &upstream, ID: d.ID, TeamID: teamID}); err != nil {
			return db.Domain{}, err
		}
		d.Upstream = &upstream
	}
	extra := map[string]any{"hostname": hostname, "tls": tls}
	if upstream != "" {
		extra["upstream"] = upstream
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "domain.added", "app", a.ID, actor.audit(extra)); err != nil {
		return db.Domain{}, err
	}
	if s.publishTunnelled(ctx, q, teamID, a.ServerID, hostname) || s.wireTeamDNS(ctx, q, teamID, a, hostname) {
		if err := q.MarkDomainVerified(ctx, db.MarkDomainVerifiedParams{ID: d.ID, TeamID: teamID}); err == nil {
			now := time.Now()
			d.VerifiedAt = &now
		}
	}
	if upstream != "" {
		return d, s.routeImported(ctx, q, teamID, actor, a, d, false)
	}
	return d, s.SyncRoutes(ctx, q, teamID, a)
}

// DomainByRef resolves one of the app's domains by id or hostname.
func DomainByRef(ctx context.Context, q *db.Queries, teamID string, a db.App, ref string) (db.Domain, error) {
	d, err := q.DomainOfApp(ctx, db.DomainOfAppParams{AppID: a.ID, TeamID: teamID, Ref: NormalizeHostname(ref)})
	if err != nil {
		if d, err = q.DomainOfApp(ctx, db.DomainOfAppParams{AppID: a.ID, TeamID: teamID, Ref: strings.TrimSpace(ref)}); err != nil {
			return db.Domain{}, notFound("domain")
		}
	}
	return d, nil
}

// RemoveDomain stops routing a hostname.
func (s *Service) RemoveDomain(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, d db.Domain) error {
	if d.VerifyToken == "platform" {
		return invalid("the app's platform hostname cannot be removed")
	}
	if n, err := q.DeleteDomainInTeam(ctx, db.DeleteDomainInTeamParams{ID: d.ID, TeamID: teamID}); err != nil || n == 0 {
		return notFound("domain")
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "domain.removed", "app", a.ID, actor.audit(map[string]any{"hostname": d.Hostname})); err != nil {
		return err
	}
	s.unwireTeamDNS(ctx, q, teamID, d.Hostname)
	s.unpublishHost(ctx, q, teamID, d.Hostname)
	if d.Upstream != nil {
		return s.routeImported(ctx, q, teamID, actor, a, d, true)
	}
	return s.SyncRoutes(ctx, q, teamID, a)
}

// LookupTXT is overridable in tests.
var LookupTXT = func(ctx context.Context, name string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, name)
}

// VerifyDomain checks the domain's verification TXT record (_hosbah-verify, or _cloudy-verify). Verification gates public certificates (the
// on-demand TLS ask), not routing.
func (s *Service) VerifyDomain(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, d db.Domain) (bool, string, error) {
	if d.VerifiedAt != nil {
		return true, d.Hostname + " is already verified.", nil
	}
	if ok, found := checkVerifyTXT(ctx, d.Hostname, d.VerifyToken); ok {
		if err := q.MarkDomainVerified(ctx, db.MarkDomainVerifiedParams{ID: d.ID, TeamID: teamID}); err != nil {
			return false, "", err
		}
		return true, d.Hostname + " is verified.", tenancy.Record(ctx, q, teamID, actor.UserID, "domain.verified", "app", a.ID,
			actor.audit(map[string]any{"hostname": d.Hostname}))
	} else if found {
		return false, notVerifiedYet("the TXT record does not match", d.Hostname, d.VerifyToken), nil
	}
	return false, notVerifiedYet("no TXT record found", d.Hostname, d.VerifyToken), nil
}

// SyncRoutes points the app's hostnames at its live release now (domains added or removed between
// deploys). Nothing live: nothing to route yet — the next deploy's switch includes them.
func (s *Service) SyncRoutes(ctx context.Context, q *db.Queries, teamID string, a db.App) error {
	if IsImported(a) {
		return nil // an imported app's domains are proxy routes, applied when each domain is added
	}
	live, err := q.LiveReleaseOfApp(ctx, a.ID)
	if err != nil || live.HostPort == nil || live.TeamID != teamID {
		return nil
	}
	domains, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return err
	}
	// route.set is the app's whole route: the realtime path the live release serves rides along, or
	// adding a domain would silently unroute the site's websockets.
	cfg, err := realtimeConfig(ctx, q, a.ID, teamID)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(agentproto.RouteParams{AppID: a.ID, Hosts: HostsOf(domains), Upstream: fmt.Sprintf("127.0.0.1:%d", *live.HostPort),
		Realtime: RealtimeRouteOf(cfg, &live)})
	_, _, err = s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: a.ServerID, Kind: agentproto.KindRouteSet, Params: raw, CreatedBy: "hosbah"})
	return err
}

// ---- server-side helpers -----------------------------------------------------------------------------

// Detect queues app.detect (after the idempotent tool steps it needs) on a server. The returned
// operation's result is an agentproto.DetectResult.
func (s *Service) Detect(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, repoURL, ref string) (db.Operation, error) {
	if err := agentproto.CheckRepoURL(repoURL); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	if ref == "" {
		ref = "main"
	}
	if !agentproto.ValidRef(ref) {
		return db.Operation{}, invalid("that is not a valid branch name")
	}
	if srv.Status != "enrolled" {
		return db.Operation{}, conflict("that server's agent has not enrolled yet")
	}
	arch := "amd64"
	if srv.Arch != nil && *srv.Arch != "" {
		arch = *srv.Arch
	}
	railpack, err := RailpackTool(arch)
	if err != nil {
		return db.Operation{}, err
	}
	for _, p := range []agentproto.ProvisionParams{{Step: agentproto.StepBuildTools}, {Step: agentproto.StepRailpack, Tool: railpack}} {
		raw, _ := json.Marshal(p)
		if _, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: agentproto.KindProvisionStep, Params: raw, CreatedBy: actor.UserID}); err != nil {
			return db.Operation{}, err
		}
	}
	src := agentproto.GitSource{RepoURL: agentproto.NormalizeRepoURL(repoURL), Ref: ref}
	if srv.AgentPublicKey != nil {
		// The team's GitHub App installation, when it covers the repository (a private one then
		// detects too). Without it the clone goes ahead as before.
		if box, err := s.CloneToken(ctx, teamID, src.RepoURL, *srv.AgentPublicKey); err == nil {
			src.Token = box
		} else {
			s.log.Warn("could not mint a GitHub App token for app.detect", "err", err)
		}
	}
	raw, _ := json.Marshal(agentproto.DetectParams{GitSource: src, RailpackFrontend: RailpackFrontend})
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: agentproto.KindAppDetect, Params: raw, CreatedBy: actor.UserID})
	return op, err
}

// RuntimeLogs queues app.logs: the process's recent lines, then following for a while.
func (s *Service) RuntimeLogs(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, process string, tail, follow int) (db.Operation, error) {
	if IsImported(a) { // an imported container's own logs (it has no processes of Hosbah's)
		return s.WorkloadLogs(ctx, q, teamID, actor, WorkloadOfApp(a), tail, "", follow)
	}
	if process == "" {
		process = "web"
	}
	if !agentproto.ValidProcess(process) {
		return db.Operation{}, invalid("process is web or worker:<name>")
	}
	raw, _ := json.Marshal(agentproto.LogsParams{AppID: a.ID, Process: process, Tail: min(max(tail, 0), agentproto.MaxLogTail),
		FollowSeconds: min(max(follow, 0), agentproto.MaxLogFollowSeconds)})
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: a.ServerID, Kind: agentproto.KindAppLogs, Params: raw, CreatedBy: actor.UserID})
	return op, err
}

// LogsSearch queues app.logs.search: the agent filters the app's current containers' logs itself
// and relays back only the matching lines (H4). The caller awaits the result the same way it
// awaits workload.stats — see api.awaitInto.
func (s *Service) LogsSearch(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, p agentproto.LogsSearchParams) (db.Operation, error) {
	if IsImported(a) {
		return db.Operation{}, invalid("a raw container's logs are searched through workload logs, not app logs")
	}
	p.AppID = a.ID
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	raw, _ := json.Marshal(p)
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: a.ServerID, Kind: agentproto.KindAppLogsSearch, Params: raw, CreatedBy: actor.UserID})
	return op, err
}
