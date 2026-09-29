// Package deploy is the control plane's deploy pipeline and the app actions the API and the MCP
// share. A deployment is a row in `deployments`; this package's worker claims queued ones (FOR
// UPDATE SKIP LOCKED, at most one running per app) and drives the target server's agent through
// typed, signed operations:
//
//	provision → build → release → start web (health gate) → start workers → switch → drain
//
// Nothing moves traffic before the switch, and the switch is the last step that can fail — if it
// does, the route is put back — so any failure leaves the previous release serving. Rollback and
// restart skip the build: one app.rollback round trip starts a kept image, gates it, switches and
// drains.
//
// Every operation a deployment enqueues has an id derived from (deployment, step), so a control
// plane that restarts mid-deploy picks the same deployment up again (it is still "running"),
// re-attaches to the operations that already exist, and carries on.
package deploy

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
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/apm"
	"github.com/fadymondy/hosbah/internal/app"
	"github.com/fadymondy/hosbah/internal/cloudflare"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Service runs deployments.
type Service struct {
	a      *app.App
	ops    *ops.Service
	log    *slog.Logger
	wake   chan struct{}
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	active map[string]bool

	github GitHubApp
	// ghMu guards github (tests swap it) and repoCache, the GitHub App repositories per team.
	ghMu      sync.Mutex
	repoCache map[string]repoCacheEntry
	// apps is the platform apps domain (HOSBAH_APPS_DOMAIN …); see appsdomain.go.
	apps AppsDomain
	// cfOpts, when set, configures every Cloudflare client (tests).
	cfOpts []cloudflare.Option
	// apmSvc is set by SetApm once internal/apm's Service exists (server.go wires it after both are
	// constructed); nil until then, and in any test that does not need APM env injection.
	apmSvc *apm.Service
}

// SetApm wires the APM service in, for env auto-injection (injectApmEnv). Call once, after both
// services are constructed.
func (s *Service) SetApm(svc *apm.Service) { s.apmSvc = svc }

// New builds the service; Start launches its worker.
func New(a *app.App, o *ops.Service) *Service {
	log := a.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{a: a, ops: o, log: log.With("component", "deploy"), wake: make(chan struct{}, 1), active: map[string]bool{},
		github: GitHubFromEnv(), repoCache: map[string]repoCacheEntry{}, apps: AppsDomainFromEnv()}
}

// Start launches the worker. Deployments this control plane was running when it stopped are
// picked up again first.
func (s *Service) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go s.loop(ctx)
}

// Stop ends the worker and waits for running pipelines to notice.
func (s *Service) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

// Wake nudges the worker (it also polls).
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// App is the application container.
func (s *Service) App() *app.App { return s.a }

// Ops is the operations service underneath.
func (s *Service) Ops() *ops.Service { return s.ops }

// AskURL is Caddy's on-demand TLS permission endpoint, as servers reach the control plane.
func (s *Service) AskURL() string { return s.ops.PublicURL() + "/internal/tls/allow" }

// HookURL is an app's deploy-hook URL for a token.
func (s *Service) HookURL(token string) string { return s.ops.PublicURL() + "/hooks/deploy/" + token }

func (s *Service) loop(ctx context.Context) {
	defer s.wg.Done()
	var running []db.Deployment
	_ = s.sys(ctx, func(q *db.Queries) error {
		var err error
		running, err = q.RunningDeployments(ctx)
		return err
	})
	for _, d := range running {
		s.log.Info("resuming a deployment interrupted by a restart", "deployment", d.ID)
		s.spawn(ctx, d)
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
		for ctx.Err() == nil {
			d, ok, err := s.claim(ctx)
			if err != nil {
				s.log.Warn("claiming a deployment failed", "err", err)
				break
			}
			if !ok {
				break
			}
			s.spawn(ctx, d)
		}
	}
}

func (s *Service) sys(ctx context.Context, fn func(q *db.Queries) error) error {
	return tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error { return fn(q) })
}

func (s *Service) claim(ctx context.Context) (db.Deployment, bool, error) {
	var d db.Deployment
	found := false
	err := s.sys(ctx, func(q *db.Queries) error {
		row, err := q.ClaimQueuedDeployment(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if n, err := q.StartDeployment(ctx, row.ID); err != nil || n == 0 {
			return err
		}
		row.Status = "running"
		d, found = row, true
		return nil
	})
	if found {
		s.ops.Broker().Publish(ops.DeployTopic(d.ID), ops.AppTopic(d.AppID))
	}
	return d, found, err
}

func (s *Service) spawn(ctx context.Context, d db.Deployment) {
	s.mu.Lock()
	if s.active[d.ID] {
		s.mu.Unlock()
		return
	}
	s.active[d.ID] = true
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.active, d.ID)
			s.mu.Unlock()
			s.Wake() // the app's next queued deployment may start now
		}()
		r := &runner{s: s, dep: d}
		r.run(ctx)
	}()
}

// ---- one deployment ----------------------------------------------------------------------------------

var errCancelled = errors.New("cancelled")

type runner struct {
	s        *Service
	dep      db.Deployment
	app      db.App
	server   db.Server
	agentKey string
	dek      []byte
	live     *db.Release
	hosts    []agentproto.Route
	started  time.Time

	mounts       []agentproto.Mount // the app's volumes, read once per deployment (mountsOnce)
	mountsLoaded bool

	realtime       *db.AppRealtime // set when this release has a realtime worker (realtimeOnce)
	realtimeLoaded bool
}

// tx runs in the system scope AND the deployment's team (operations are inserted under the team
// policy; the worker's reads of other tables use the system policies).
func (r *runner) tx(ctx context.Context, fn func(q *db.Queries) error) error {
	return tenancy.Tx(ctx, r.s.a.SQLDB, tenancy.Scope{System: true, TeamID: r.dep.TeamID}, func(q *db.Queries, _ *sql.Tx) error { return fn(q) })
}

func (r *runner) publish() {
	r.s.ops.Broker().Publish(ops.DeployTopic(r.dep.ID), ops.AppTopic(r.dep.AppID))
}

// logf adds one of the pipeline's own lines to the deployment's log.
func (r *runner) logf(stream, format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	err := r.tx(context.Background(), func(q *db.Queries) error {
		return ops.AppendDeployLog(context.Background(), q, r.dek, r.dep.TeamID, r.dep.ServerID, r.dep.ID, nil, nil,
			ops.LogLine{Kind: agentproto.EventLog, Stream: stream, Line: line, Op: "pipeline"})
	})
	if err != nil {
		r.s.log.Warn("could not store a deployment log line", "deployment", r.dep.ID, "err", err)
	}
	r.publish()
}

func (r *runner) step(ctx context.Context, name string) {
	_ = r.tx(ctx, func(q *db.Queries) error {
		return q.SetDeploymentStep(ctx, db.SetDeploymentStepParams{ID: r.dep.ID, Step: &name})
	})
	r.logf("info", "── %s", name)
}

func (r *runner) load(ctx context.Context) error {
	return r.tx(ctx, func(q *db.Queries) error {
		var err error
		if r.app, err = q.AppByID(ctx, r.dep.AppID); err != nil {
			return fmt.Errorf("app: %w", err)
		}
		if r.server, err = q.ServerByID(ctx, r.dep.ServerID); err != nil {
			return fmt.Errorf("server: %w", err)
		}
		k, err := q.ServerTeamKey(ctx, r.dep.ServerID)
		if err != nil {
			return err
		}
		if k.AgentPublicKey == nil {
			return errors.New("the server's agent has not enrolled")
		}
		r.agentKey = *k.AgentPublicKey
		if r.dek, err = r.s.a.Keys.UnwrapDEK(k.TeamID, k.DekWrapped); err != nil {
			return err
		}
		if live, err := q.LiveReleaseOfApp(ctx, r.app.ID); err == nil {
			r.live = &live
		}
		domains, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: r.app.ID, TeamID: r.dep.TeamID})
		if err != nil {
			return err
		}
		r.hosts = HostsOf(domains)
		return nil
	})
}

/*
refreshHosts re-reads the app's domains just before a route is pushed.

load() runs at the top of the pipeline; the switch happens after the build, which can be
minutes later. route.set carries the app's *whole* host list, so pushing the list as it looked
at load() silently unroutes any hostname added in between — last write wins on the agent.

Seen live on 2026-09-18 on demo-1: sample-go and sample-node had a domain added seconds after
their first deploy was queued, the switch pushed `hosts: []` over it, and the dashboard then
showed a domain that Caddy answered 404 for. sample-python and sample-php, whose deploys were
still queued when their domains were added, routed correctly — which is what made it look
intermittent rather than ordered.

A failure here is not worth failing a deploy over: keep the snapshot and carry on.
*/
func (r *runner) refreshHosts(ctx context.Context) {
	err := r.tx(ctx, func(q *db.Queries) error {
		domains, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: r.app.ID, TeamID: r.dep.TeamID})
		if err != nil {
			return err
		}
		r.hosts = HostsOf(domains)
		return nil
	})
	if err != nil {
		r.logf("error", "could not re-read the app's domains before routing (%v) — using the list from the start of this deploy", err)
	}
}

// HostsOf turns an app's domains into Caddy routes.
func HostsOf(domains []db.Domain) []agentproto.Route {
	out := []agentproto.Route{}
	for _, d := range domains {
		out = append(out, agentproto.Route{Hostname: d.Hostname, TLS: d.Tls})
	}
	return out
}

func (r *runner) run(ctx context.Context) {
	r.started = time.Now()
	if err := r.load(ctx); err != nil {
		r.finish(ctx, "failed", "could not load the deployment: "+err.Error(), nil)
		return
	}
	r.logf("info", "%s #%d of %s on %s (%s)", r.dep.Kind, r.dep.Number, r.app.Name, r.server.Name, r.dep.Ref)
	var (
		release *db.Release
		err     error
	)
	switch r.dep.Kind {
	case "deploy":
		release, err = r.deploy(ctx)
	case "rollback", "restart":
		release, err = r.swap(ctx)
	default:
		err = fmt.Errorf("unknown deployment kind %q", r.dep.Kind)
	}
	switch {
	case ctx.Err() != nil:
		// The control plane is shutting down: leave it "running"; the next start resumes it.
		return
	case errors.Is(err, errCancelled):
		r.finish(ctx, "cancelled", "cancelled", nil)
	case err != nil:
		r.finish(ctx, "failed", err.Error(), nil)
	default:
		r.finish(ctx, "succeeded", "", release)
	}
}

// finish records the outcome, the app's state and — on failure — a notification.
func (r *runner) finish(ctx context.Context, status, errText string, rel *db.Release) {
	var relID *string
	if rel != nil {
		relID = &rel.ID
	}
	serving := r.live != nil || rel != nil
	switch status {
	case "succeeded":
		r.logf("info", "✓ %s #%d succeeded in %s — release v%d is live", r.dep.Kind, r.dep.Number, time.Since(r.started).Round(time.Second), rel.Version)
	case "cancelled":
		r.logf("info", "cancelled — nothing switched")
	default:
		if r.live != nil {
			r.logf("error", "✗ %s #%d failed: %s — release v%d keeps serving", r.dep.Kind, r.dep.Number, errText, r.live.Version)
		} else {
			r.logf("error", "✗ %s #%d failed: %s", r.dep.Kind, r.dep.Number, errText)
		}
	}
	err := r.tx(context.Background(), func(q *db.Queries) error {
		var e *string
		if errText != "" && status != "succeeded" {
			e = &errText
		}
		if _, err := q.FinishDeployment(ctx, db.FinishDeploymentParams{Status: status, Error: e, ReleaseID: relID, ID: r.dep.ID}); err != nil {
			return err
		}
		appStatus := "running"
		if !serving {
			appStatus = map[string]string{"failed": "failed", "cancelled": r.app.Status}[status]
			if appStatus == "" || appStatus == "deploying" {
				appStatus = "new"
			}
		}
		if err := q.SetAppState(ctx, db.SetAppStateParams{Status: appStatus, CurrentReleaseID: relID, ID: r.app.ID}); err != nil {
			return err
		}
		if status != "failed" || r.app.ID == "" {
			return nil
		}
		body := errText
		if r.live != nil {
			body += fmt.Sprintf("\nRelease v%d is still serving.", r.live.Version)
		}
		return q.InsertNotification(ctx, db.InsertNotificationParams{TeamID: r.dep.TeamID, Kind: "deploy.failed",
			Title: fmt.Sprintf("%s #%d of %s failed", strings.ToUpper(r.dep.Kind[:1])+r.dep.Kind[1:], r.dep.Number, r.app.Name),
			Body:  &body, SubjectType: ptr("deployment"), SubjectID: &r.dep.ID})
	})
	if err != nil {
		r.s.log.Error("could not finish a deployment", "deployment", r.dep.ID, "err", err)
	}
	r.publish()
}

// ---- operations --------------------------------------------------------------------------------------

// stepOpID derives an operation id from (deployment, step): the same step of the same deployment is
// always the same operation, so a resumed pipeline re-attaches instead of running it twice.
func stepOpID(deploymentID, step string) string {
	// "cloudy-step" is frozen: a new seed would give a deploy resumed across the upgrade new op ids.
	sum := sha256.Sum256([]byte("cloudy-step\n" + deploymentID + "\n" + step))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// op enqueues (or re-attaches to) one agent operation of this deployment and waits for it. A cancel
// requested meanwhile is forwarded to the agent as op.cancel.
func (r *runner) op(ctx context.Context, step, kind string, params any, out any) error {
	id := stepOpID(r.dep.ID, step)
	var queued db.Operation
	err := r.tx(ctx, func(q *db.Queries) error {
		if existing, err := q.OperationInTeam(ctx, db.OperationInTeamParams{ID: id, TeamID: r.dep.TeamID}); err == nil {
			queued = existing
			return nil
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		queued, _, err = r.s.ops.Enqueue(ctx, q, ops.EnqueueSpec{OpID: id, TeamID: r.dep.TeamID, ServerID: r.dep.ServerID,
			Kind: kind, Params: raw, CreatedBy: r.dep.CreatedBy, DeploymentID: r.dep.ID})
		return err
	})
	if err != nil {
		return fmt.Errorf("%s: could not queue: %w", kind, err)
	}
	r.s.ops.Wake()
	if !r.s.ops.Connected(r.dep.ServerID) && queued.Status == "pending" {
		r.logf("info", "waiting for %s's agent to connect…", r.server.Name)
	}
	cancelSent := false
	done, err := r.s.ops.Await(ctx, id, r.dep.ServerID, func() {
		if !cancelSent && r.cancelRequested(ctx) {
			cancelSent = true
			r.logf("info", "cancel requested — stopping %s", kind)
			_ = r.tx(ctx, func(q *db.Queries) error {
				raw, _ := json.Marshal(agentproto.CancelParams{OpID: id})
				_, _, err := r.s.ops.Enqueue(ctx, q, ops.EnqueueSpec{OpID: stepOpID(r.dep.ID, "cancel:"+step), TeamID: r.dep.TeamID,
					ServerID: r.dep.ServerID, Kind: agentproto.KindOpCancel, Params: raw, CreatedBy: r.dep.CreatedBy, DeploymentID: r.dep.ID})
				return err
			})
			r.s.ops.Wake()
		}
	})
	if err != nil {
		return err
	}
	if done.Status != "ok" {
		if cancelSent {
			return errCancelled
		}
		msg := done.Status
		if done.Error != nil {
			msg = *done.Error
		}
		return errors.New(msg)
	}
	if out != nil && done.Result != nil {
		if err := json.Unmarshal([]byte(*done.Result), out); err != nil {
			return fmt.Errorf("%s: unreadable result: %w", kind, err)
		}
	}
	return nil
}

func (r *runner) cancelRequested(ctx context.Context) bool {
	requested := false
	_ = r.tx(ctx, func(q *db.Queries) error {
		d, err := q.DeploymentByID(ctx, r.dep.ID)
		requested = err == nil && d.CancelRequested
		return err
	})
	return requested
}

// EnvSealContext binds a stored env value to its app and name.
func EnvSealContext(appID, name string) string { return "env:" + appID + ":" + name }

// envBox opens the app's env values with the team key and seals them again, to the target agent's
// own key, for one process of this deployment. "" when the app has none.
func (r *runner) envBox(ctx context.Context, process string) (string, error) {
	env := map[string]string{}
	err := r.tx(ctx, func(q *db.Queries) error {
		rows, err := q.ListEnvVarsOfApp(ctx, db.ListEnvVarsOfAppParams{AppID: r.app.ID, TeamID: r.dep.TeamID})
		if err != nil {
			return err
		}
		for _, v := range rows {
			pt, err := crypto.Open(r.dek, r.dep.TeamID, EnvSealContext(r.app.ID, v.Name), v.ValueSealed)
			if err != nil {
				return fmt.Errorf("env %s could not be opened", v.Name)
			}
			env[v.Name] = string(pt)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	r.injectApmEnv(ctx, env)
	r.injectPHPEnv(ctx, env)
	if len(env) == 0 {
		return "", nil
	}
	raw, _ := json.Marshal(env)
	return agentproto.SealBox(r.agentKey, agentproto.EnvContext(r.app.ID, r.dep.ID, process), raw)
}

// injectApmEnv adds the standard OpenTelemetry OTLP/HTTP JSON exporter env vars when APM (MH-575
// H7, internal/apm) is enabled for the app, so its own OTel SDK finds Hosbah's ingest endpoints with
// zero code beyond the SDK itself. Never overrides a var the user already set under the app's own
// environment. Best-effort: a settings-read or key-open failure just skips injection, it never fails
// the deploy.
func (r *runner) injectApmEnv(ctx context.Context, env map[string]string) {
	if r.s.apmSvc == nil {
		return
	}
	var setting db.AppApmSetting
	err := r.tx(ctx, func(q *db.Queries) error {
		var err error
		setting, err = q.AppApmSettingOfApp(ctx, db.AppApmSettingOfAppParams{TeamID: r.dep.TeamID, AppID: r.app.ID})
		return err
	})
	if err != nil || !setting.Enabled || setting.KeySealed == nil {
		return
	}
	plain, err := crypto.Open(r.dek, r.dep.TeamID, apm.EnvSealContext(r.app.ID), *setting.KeySealed)
	if err != nil {
		return
	}
	base := r.s.apmSvc.PublicURL()
	if base == "" {
		return
	}
	defaults := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": base + "/api/otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
		// x-hosbah-key, not "Authorization=Bearer …": the spec percent-decodes header values and a raw
		// space trips some SDKs.
		"OTEL_EXPORTER_OTLP_HEADERS":  "x-hosbah-key=" + string(plain),
		"OTEL_SERVICE_NAME":           r.app.Name,
	}
	for k, v := range defaults {
		if _, set := env[k]; !set {
			env[k] = v
		}
	}
}

// phpConfig (MH-576 D2) reads the app's saved php_version/php_ini, best-effort: a config read
// failure (no row saved yet) just means the defaults ("auto", no overrides) apply.
func (r *runner) phpConfig(ctx context.Context) (version string, ini map[string]string) {
	var cfg db.AppDeployConfig
	err := r.tx(ctx, func(q *db.Queries) error {
		var err error
		cfg, err = q.GetAppDeployConfig(ctx, db.GetAppDeployConfigParams{AppID: r.app.ID, TeamID: r.dep.TeamID})
		return err
	})
	if err != nil {
		return "", nil
	}
	_ = json.Unmarshal([]byte(cfg.PhpIni), &ini)
	return cfg.PhpVersion, ini
}

// buildConfig (MH-579 gap B) reads the app's saved dockerfile_path/build_context/build_env,
// best-effort like phpConfig: a config read failure just means the defaults apply (checkout-root
// Dockerfile, checkout root as context, no build-time env).
func (r *runner) buildConfig(ctx context.Context) (dockerfilePath, buildContext string, buildEnvNames []string) {
	var cfg db.AppDeployConfig
	err := r.tx(ctx, func(q *db.Queries) error {
		var err error
		cfg, err = q.GetAppDeployConfig(ctx, db.GetAppDeployConfigParams{AppID: r.app.ID, TeamID: r.dep.TeamID})
		return err
	})
	if err != nil {
		return "", "", nil
	}
	return cfg.DockerfilePath, cfg.BuildContext, UnmarshalBuildEnv(cfg.BuildEnv)
}

// buildEnvBox (MH-579 gap A) opens the app's env values with the team key and reseals exactly the
// ones named in names — a build-time subset of the app's env, sealed to the target agent's own key
// for this deployment's build step. A name with no matching env var is silently skipped. ""
// when names is empty or none of them are set.
func (r *runner) buildEnvBox(ctx context.Context, names []string) (string, error) {
	if len(names) == 0 {
		return "", nil
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	env := map[string]string{}
	err := r.tx(ctx, func(q *db.Queries) error {
		rows, err := q.ListEnvVarsOfApp(ctx, db.ListEnvVarsOfAppParams{AppID: r.app.ID, TeamID: r.dep.TeamID})
		if err != nil {
			return err
		}
		for _, v := range rows {
			if !want[v.Name] {
				continue
			}
			pt, err := crypto.Open(r.dek, r.dep.TeamID, EnvSealContext(r.app.ID, v.Name), v.ValueSealed)
			if err != nil {
				return fmt.Errorf("env %s could not be opened", v.Name)
			}
			env[v.Name] = string(pt)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(env) == 0 {
		return "", nil
	}
	raw, _ := json.Marshal(env)
	return agentproto.SealBox(r.agentKey, agentproto.EnvContext(r.app.ID, r.dep.ID, "build"), raw)
}

// injectPHPEnv (MH-576 D2) exposes the app's PHP version and ini overrides to the running
// container as HOSBAH_PHP_VERSION / HOSBAH_PHP_INI — informational for a Dockerfile app (it owns
// its image and build; Hosbah cannot write a php.ini into it), redundant but harmless for a
// Railpack one (which already got them at build time). Never overrides a var the app's own
// environment already set.
func (r *runner) injectPHPEnv(ctx context.Context, env map[string]string) {
	version, ini := r.phpConfig(ctx)
	if version != "" && version != "auto" {
		if _, set := env["HOSBAH_PHP_VERSION"]; !set {
			env["HOSBAH_PHP_VERSION"] = version
		}
	}
	if len(ini) > 0 {
		if _, set := env["HOSBAH_PHP_INI"]; !set {
			env["HOSBAH_PHP_INI"] = PHPIniEnvValue(ini)
		}
	}
}

// fastBuild (MH-573 A7) tries the fast track: app.fast_build runs the app's own install/build/
// migrate steps inside its current live image on the target server and commits the result,
// skipping the Dockerfile/Railpack rebuild. It returns false (never an error, unless the op itself
// failed to run) when the fast track cannot apply — no previous release, no deploy script
// configured, or the agent's own diff saw a build-affecting path — so the caller falls back to a
// full build; either way the deployment's stored track is corrected to match what actually ran.
func (r *runner) fastBuild(ctx context.Context, image string, src agentproto.GitSource, built *agentproto.BuildResult) (bool, error) {
	if reason, ok := fastBuildBlockedByRelease(r.live); ok {
		r.downgradeTrack(ctx, reason)
		return false, nil
	}
	var cfg db.AppDeployConfig
	cfgErr := r.tx(ctx, func(q *db.Queries) error {
		var err error
		cfg, err = q.GetAppDeployConfig(ctx, db.GetAppDeployConfigParams{AppID: r.app.ID, TeamID: r.dep.TeamID})
		return err
	})
	mode := FastBuildMode(effectivePreset(cfg), cfg.BuilderImage)
	if mode == agentproto.FastModeDockerfile && (cfg.DockerfilePath != "" || cfg.BuildContext != "") {
		// The dockerfile fast-build mode always builds "<checkout>/Dockerfile" with the checkout
		// root as context (MH-573 A7b) — it has no notion of a custom path or context, so a full
		// build (which does) is the only way to honor either setting.
		r.downgradeTrack(ctx, "custom Dockerfile path or build context configured")
		return false, nil
	}
	var fbSteps []agentproto.FastBuildStep
	if mode != agentproto.FastModeDockerfile {
		// The dockerfile mode needs no install/build/migrate steps at all — it builds the
		// checkout's own Dockerfile — so a missing/unreadable deploy script config only blocks
		// the builder and runtime modes.
		var reason string
		var ok bool
		fbSteps, reason, ok = fastBuildSteps(cfg.Steps, cfgErr)
		if !ok {
			r.downgradeTrack(ctx, reason)
			return false, nil
		}
	}
	var outputs []string
	if mode == agentproto.FastModeBuilder {
		if err := json.Unmarshal([]byte(cfg.Outputs), &outputs); err != nil || len(outputs) == 0 {
			r.downgradeTrack(ctx, "no outputs configured for the builder image")
			return false, nil
		}
	}
	box, err := r.envBox(ctx, "fast_build")
	if err != nil {
		return false, err
	}
	var res agentproto.FastBuildResult
	if err := r.op(ctx, "build", agentproto.KindAppFastBuild, agentproto.FastBuildParams{
		AppID: r.app.ID, App: r.app.Name, DeploymentID: r.dep.ID, GitSource: src,
		Image: image, BaseImage: r.live.Image, Mode: mode, Steps: fbSteps,
		BuilderImage: cfg.BuilderImage, Outputs: outputs, Env: box,
	}, &res); err != nil {
		return false, fmt.Errorf("fast build: %w", err)
	}
	if res.Aborted {
		reason := res.AbortReason
		if reason == "" {
			reason = "a build-affecting file changed"
		}
		r.logf("info", "fast track aborted (%s) — falling back to a full build", reason)
		r.downgradeTrack(ctx, reason)
		return false, nil
	}
	r.notePreset(ctx, res.DetectedFiles)
	*built = agentproto.BuildResult{
		DetectResult: agentproto.DetectResult{Commit: res.Commit, Message: res.Message, Runtime: "fast",
			HosbahToml: res.HosbahToml, TomlFile: res.TomlFile},
		Image: res.Image, ImageID: res.ImageID,
	}
	return true, nil
}

// notePreset (MH-573 A7b) persists the preset DetectPreset sees in the checkout's own files, best
// effort — a fast build's outcome should never fail because the detected preset could not be saved.
func (r *runner) notePreset(ctx context.Context, files []string) {
	if len(files) == 0 {
		return
	}
	preset := DetectPreset(files)
	_ = r.tx(ctx, func(q *db.Queries) error {
		return q.SetAppDeployConfigDetectedPreset(ctx, db.SetAppDeployConfigDetectedPresetParams{
			AppID: r.app.ID, TeamID: r.dep.TeamID, DetectedPreset: preset,
		})
	})
}

// effectivePreset is the preset the app's builds follow: the chosen one, else the one the last
// build detected from the repository (an app created from a repo with a Dockerfile never had to
// choose "dockerfile", and must not fast-build as if it had no Dockerfile).
func effectivePreset(cfg db.AppDeployConfig) string {
	if cfg.Preset != "" {
		return cfg.Preset
	}
	return cfg.DetectedPreset
}

// fastBuildBlockedByRelease reports whether the fast track cannot apply because there is no
// previous successful release to build from (app.fast_build needs a live image as its base).
// Pure so the dispatcher's decision is unit-testable without a database.
func fastBuildBlockedByRelease(live *db.Release) (reason string, blocked bool) {
	if live == nil || live.Image == "" {
		return "no previous successful release", true
	}
	return "", false
}

// fastBuildSteps parses an app_deploy_configs.steps JSON column into the install/build/migrate/
// on_failure steps app.fast_build runs, or reports why the fast track cannot apply: no deploy
// script configuration row (cfgErr, e.g. sql.ErrNoRows) or an unreadable one. Pure so the
// dispatcher's decision is unit-testable without a database.
func fastBuildSteps(stepsJSON string, cfgErr error) (steps []agentproto.FastBuildStep, reason string, ok bool) {
	if cfgErr != nil {
		return nil, "no deploy script configured", false
	}
	var parsed []DeployStep
	if err := json.Unmarshal([]byte(stepsJSON), &parsed); err != nil {
		return nil, "the deploy script configuration is unreadable", false
	}
	for _, s := range parsed {
		if !s.Enabled || strings.TrimSpace(s.Run) == "" {
			continue
		}
		switch s.Key {
		case "install", "build", "migrate", "on_failure":
			steps = append(steps, agentproto.FastBuildStep{Key: s.Key, Name: s.Name, Run: s.Run})
		}
	}
	return steps, "", true
}

// downgradeTrack corrects the deployment's stored track (and reason) once a fast attempt could not
// run, so deployments.track always matches what the pipeline actually did.
func (r *runner) downgradeTrack(ctx context.Context, reason string) {
	full := TrackFull
	_ = r.tx(ctx, func(q *db.Queries) error {
		return q.SetDeploymentTrack(ctx, db.SetDeploymentTrackParams{ID: r.dep.ID, Track: &full, TrackReason: &reason})
	})
	r.dep.Track, r.dep.TrackReason = &full, &reason
}

// ---- deploy ------------------------------------------------------------------------------------------

func (r *runner) deploy(ctx context.Context) (*db.Release, error) {
	arch := "amd64"
	if r.server.Arch != nil && *r.server.Arch != "" {
		arch = *r.server.Arch
	}
	caddy, err := CaddyTool(arch)
	if err != nil {
		return nil, err
	}
	railpack, err := RailpackTool(arch)
	if err != nil {
		return nil, err
	}

	r.step(ctx, "provision")
	for _, p := range []agentproto.ProvisionParams{
		{Step: agentproto.StepDockerInstall},
		{Step: agentproto.StepBuildTools},
		{Step: agentproto.StepCaddy, Tool: caddy, AskURL: r.s.AskURL()},
		{Step: agentproto.StepRailpack, Tool: railpack},
	} {
		if err := r.op(ctx, "provision:"+p.Step, agentproto.KindProvisionStep, p, nil); err != nil {
			return nil, fmt.Errorf("preparing the server (%s): %w", p.Step, err)
		}
	}

	r.step(ctx, "build")
	image := agentproto.ImageRepo(r.app.Name) + ":" + r.dep.ID
	var protect []string
	if r.live != nil {
		protect = append(protect, r.live.Image)
	}
	var built agentproto.BuildResult
	src := agentproto.GitSource{RepoURL: RepoOf(r.app), Ref: r.dep.Ref}
	if box, err := r.s.CloneToken(ctx, r.dep.TeamID, src.RepoURL, r.agentKey); err != nil {
		// A public repository still clones; a private one fails with the reason from git.
		r.logf("info", "could not get a GitHub App token for this repository (%v) — cloning without it", err)
	} else if box != "" {
		src.Token = box
		r.logf("info", "cloning with a fresh token of the team's GitHub App installation")
	}
	fast := r.dep.Track != nil && *r.dep.Track == TrackFast
	buildStep := "build"
	if fast {
		ok, err := r.fastBuild(ctx, image, src, &built)
		if err != nil {
			return nil, err
		}
		fast = ok // aborted or unavailable: falls through to the full build below
		// Operations are idempotent by the op id derived from the step, and app.fast_build already
		// used "build": the fallback must be a step of its own, or it is answered with the aborted
		// fast build's result and never runs.
		buildStep = "build:full"
	}
	if !fast {
		phpVersion, phpIni := r.phpConfig(ctx)
		dockerfilePath, buildContext, buildEnvNames := r.buildConfig(ctx)
		buildEnvBox, err := r.buildEnvBox(ctx, buildEnvNames)
		if err != nil {
			return nil, err
		}
		if err := r.op(ctx, buildStep, agentproto.KindAppBuild, agentproto.BuildParams{
			AppID: r.app.ID, App: r.app.Name, DeploymentID: r.dep.ID,
			GitSource: src,
			Image:     image, Keep: agentproto.DefaultKeepImages, Protect: protect, RailpackFrontend: RailpackFrontend,
			NoCache:        r.dep.TrackReason != nil && *r.dep.TrackReason == "full rebuild requested",
			PHPVersion:     phpVersion, PHPIni: phpIni,
			DockerfilePath: dockerfilePath, BuildContext: buildContext, Env: buildEnvBox,
		}, &built); err != nil {
			return nil, fmt.Errorf("build: %w", err)
		}
		r.notePreset(ctx, built.Files)
	}
	runtime := built.Runtime
	if built.Provider != "" {
		runtime += ":" + built.Provider
	}
	if err := r.tx(ctx, func(q *db.Queries) error {
		msg := clip(built.Message, 500)
		if err := q.SetDeploymentBuild(ctx, db.SetDeploymentBuildParams{CommitSha: nonEmpty(built.Commit), CommitMessage: nonEmpty(msg),
			Image: &image, Runtime: &runtime, ID: r.dep.ID}); err != nil {
			return err
		}
		if err := q.SetAppState(ctx, db.SetAppStateParams{Status: "deploying", Runtime: &runtime, ID: r.app.ID}); err != nil {
			return err
		}
		if len(built.Pruned) > 0 {
			return q.MarkReleasesPruned(ctx, db.MarkReleasesPrunedParams{AppID: r.app.ID, Images: strings.Join(built.Pruned, ",")})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	r.publish()

	cfg, err := ParseConfigFile(built.Toml(), ConfigFileOf(built.DetectResult))
	if err != nil {
		return nil, err
	}
	appRes, appProcRes := appResources(r.app)
	eff := Resolve(cfg, Defaults{Port: int(r.app.Port), HealthPath: r.app.HealthcheckPath, HealthTimeout: int(r.app.HealthcheckTimeout),
		Resources: appRes, ProcessResources: appProcRes})
	if fast && built.Toml() == "" && r.live != nil {
		// An agent from before the fix does not return hosbah.toml from a fast build, so keep the
		// live release's processes and release command rather than silently dropping them.
		var prev Effective
		if err := json.Unmarshal([]byte(r.live.Config), &prev); err == nil && prev.Port != 0 {
			eff = prev
			r.logf("info", "fast track: keeping the processes and release command of release v%d", r.live.Version)
		}
	}
	r.logf("info", "processes: %s · port %d · health check GET %s within %ds", strings.Join(append([]string{"web"}, eff.Workers()...), ", "),
		eff.Port, eff.HealthPath, eff.HealthTimeout)
	if err := r.tx(ctx, func(q *db.Queries) error {
		keep := []string{}
		for name, cmd := range eff.Processes {
			keep = append(keep, name)
			if err := q.UpsertProcessType(ctx, db.UpsertProcessTypeParams{TeamID: r.dep.TeamID, AppID: r.app.ID, Name: name, Command: nonEmpty(cmd)}); err != nil {
				return err
			}
		}
		return q.DeleteProcessTypesExcept(ctx, db.DeleteProcessTypesExceptParams{AppID: r.app.ID, Keep: strings.Join(keep, ",")})
	}); err != nil {
		return nil, err
	}
	if r.cancelRequested(ctx) {
		return nil, errCancelled
	}

	if eff.Release != "" {
		r.step(ctx, "release")
		box, err := r.envBox(ctx, "release")
		if err != nil {
			return nil, err
		}
		if err := r.op(ctx, "release", agentproto.KindAppRelease, agentproto.ReleaseParams{AppID: r.app.ID, App: r.app.Name,
			DeploymentID: r.dep.ID, Image: image, Command: eff.Release, Env: box}, nil); err != nil {
			return nil, fmt.Errorf("release command: %w", err)
		}
		if r.cancelRequested(ctx) {
			return nil, errCancelled
		}
	}

	r.step(ctx, "start")
	var created []string
	cleanup := func() {
		if len(created) == 0 {
			return
		}
		r.logf("info", "removing the new containers — nothing switched")
		_ = r.op(context.Background(), "cleanup", agentproto.KindAppDrain,
			agentproto.DrainParams{AppID: r.app.ID, Only: created, GraceSeconds: 5}, nil)
	}
	webParams, err := r.startParams(ctx, image, "web", eff)
	if err != nil {
		return nil, err
	}
	var web agentproto.StartResult
	if err := r.op(ctx, "start:web", agentproto.KindAppStart, webParams, &web); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	created = append(created, web.Container)
	var workers []string
	var rtRecord *ReleaseRealtime
	var rtRoute *agentproto.RealtimeRoute
	for _, w := range eff.Workers() {
		wp, err := r.startParams(ctx, image, w, eff)
		if err != nil {
			cleanup()
			return nil, err
		}
		var res agentproto.StartResult
		if err := r.op(ctx, "start:"+w, agentproto.KindAppStart, wp, &res); err != nil {
			cleanup()
			return nil, fmt.Errorf("start %s: %w", w, err)
		}
		created = append(created, res.Container)
		workers = append(workers, res.Container)
		if wp.Realtime != nil {
			rtRecord, rtRoute = r.realtimeStarted(res)
		}
	}
	if r.cancelRequested(ctx) {
		cleanup()
		return nil, errCancelled
	}

	r.step(ctx, "switch")
	r.refreshHosts(ctx)
	upstream := fmt.Sprintf("127.0.0.1:%d", web.HostPort)
	// One route.set moves web AND the realtime path: until it, the old release's socket server serves.
	if err := r.op(ctx, "switch", agentproto.KindRouteSet, agentproto.RouteParams{AppID: r.app.ID, Hosts: r.hosts, Upstream: upstream, Realtime: rtRoute}, nil); err != nil {
		if r.live != nil && r.live.HostPort != nil {
			r.logf("error", "switch failed — pointing the route back at release v%d", r.live.Version)
			_ = r.op(context.Background(), "revert", agentproto.KindRouteSet, agentproto.RouteParams{AppID: r.app.ID, Hosts: r.hosts,
				Upstream: fmt.Sprintf("127.0.0.1:%d", *r.live.HostPort), Realtime: r.liveRealtimeRoute(context.Background())}, nil)
		}
		cleanup()
		return nil, fmt.Errorf("switch: %w", err)
	}

	containers := releaseContainers(web.Container, workers, r.mounts, rtRecord)
	config, _ := json.Marshal(eff)
	var rel db.Release
	if err := r.tx(ctx, func(q *db.Queries) error {
		if err := q.RetireLiveRelease(ctx, r.app.ID); err != nil {
			return err
		}
		var err error
		rel, err = q.InsertRelease(ctx, db.InsertReleaseParams{ID: newUUID(), TeamID: r.dep.TeamID, AppID: r.app.ID, DeploymentID: r.dep.ID,
			Image: image, CommitSha: nonEmpty(built.Commit), Port: int64(eff.Port), HostPort: ptr(int64(web.HostPort)),
			Containers: ptr(containers), Config: string(config)})
		if err != nil {
			return err
		}
		return q.SetAppState(ctx, db.SetAppStateParams{Status: "running", CurrentReleaseID: &rel.ID, ID: r.app.ID})
	}); err != nil {
		return nil, err
	}
	r.publish()
	r.s.scanAfterDeploy(r.dep.TeamID, r.app.ID, r.dep.ServerID, rel.ID, rel.Image) // MH-580 J5, best-effort, non-blocking

	r.step(ctx, "drain")
	if err := r.op(ctx, "drain", agentproto.KindAppDrain, agentproto.DrainParams{AppID: r.app.ID, Keep: created, GraceSeconds: 10}, nil); err != nil {
		r.logf("error", "the new release is live, but draining the old containers failed: %v", err)
	}
	return &rel, nil
}

func (r *runner) startParams(ctx context.Context, image, process string, eff Effective) (agentproto.StartParams, error) {
	box, err := r.envBox(ctx, process)
	if err != nil {
		return agentproto.StartParams{}, err
	}
	mounts, err := r.mountsOnce(ctx)
	if err != nil {
		return agentproto.StartParams{}, fmt.Errorf("volumes: %w", err)
	}
	p := agentproto.StartParams{AppID: r.app.ID, App: r.app.Name, DeploymentID: r.dep.ID, Image: image, Process: process,
		Command: eff.Processes[process], Env: box, Mounts: mounts, Resources: eff.ResourcesFor(process)}
	rt, err := r.realtimeOnce(ctx, eff)
	if err != nil {
		return agentproto.StartParams{}, fmt.Errorf("realtime: %w", err)
	}
	if rt != nil && process == agentproto.RealtimeWorker(rt.Process) {
		p.Realtime = RealtimePublishOf(*rt) // this worker only: every other one publishes nothing
	}
	if process == "web" {
		p.Port = eff.Port
		p.Health = &agentproto.HealthSpec{Path: eff.HealthPath, TimeoutSeconds: eff.HealthTimeout}
	}
	return p, nil
}

// ---- rollback / restart ------------------------------------------------------------------------------

// swap starts a kept release in fresh containers and switches to it in one app.rollback round trip.
// rollback: the target release (default: the one live before the current); restart: the live one.
func (r *runner) swap(ctx context.Context) (*db.Release, error) {
	var target db.Release
	err := r.tx(ctx, func(q *db.Queries) error {
		var err error
		switch {
		case r.dep.Kind == "restart":
			if r.live == nil {
				return errors.New("nothing is live to restart — deploy first")
			}
			target = *r.live
		case r.dep.TargetReleaseID != nil:
			target, err = q.ReleaseByID(ctx, *r.dep.TargetReleaseID)
			if err != nil || target.AppID != r.app.ID {
				return errors.New("the release to roll back to does not exist")
			}
		default:
			target, err = q.PreviousReleaseOfApp(ctx, r.app.ID)
			if err != nil {
				return errors.New("there is no earlier release to roll back to")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if target.Status == "pruned" {
		return nil, fmt.Errorf("release v%d's image was pruned (only the newest %d are kept) — deploy its commit again instead", target.Version, agentproto.DefaultKeepImages)
	}
	if r.dep.Kind == "rollback" && r.live != nil && target.ID == r.live.ID {
		return nil, fmt.Errorf("release v%d is already live", target.Version)
	}
	var eff Effective
	if err := json.Unmarshal([]byte(target.Config), &eff); err != nil || eff.Port == 0 {
		return nil, errors.New("the release's configuration is unreadable")
	}
	_ = r.tx(ctx, func(q *db.Queries) error {
		return q.SetDeploymentBuild(ctx, db.SetDeploymentBuildParams{CommitSha: target.CommitSha, Image: &target.Image, ID: r.dep.ID})
	})

	r.step(ctx, r.dep.Kind)
	r.logf("info", "starting release v%d (%s) — no build", target.Version, target.Image)
	web, err := r.startParams(ctx, target.Image, "web", eff)
	if err != nil {
		return nil, err
	}
	r.refreshHosts(ctx)
	params := agentproto.RollbackParams{AppID: r.app.ID, Web: web, Hosts: r.hosts, GraceSeconds: 10}
	for _, w := range eff.Workers() {
		wp, err := r.startParams(ctx, target.Image, w, eff)
		if err != nil {
			return nil, err
		}
		params.Workers = append(params.Workers, wp)
	}
	var res agentproto.RollbackResult
	if err := r.op(ctx, r.dep.Kind, agentproto.KindAppRollback, params, &res); err != nil {
		return nil, fmt.Errorf("%s: %w", r.dep.Kind, err)
	}
	var workers []string
	var rtRecord *ReleaseRealtime
	for i, w := range res.Workers {
		workers = append(workers, w.Container)
		if i < len(params.Workers) && params.Workers[i].Realtime != nil {
			rtRecord, _ = r.realtimeStarted(w) // app.rollback routed it itself, after the workers started
		}
	}
	containers := releaseContainers(res.Web.Container, workers, r.mounts, rtRecord)
	if err := r.tx(ctx, func(q *db.Queries) error {
		if r.dep.Kind == "restart" {
			return q.SetReleaseContainers(ctx, db.SetReleaseContainersParams{HostPort: ptr(int64(res.Web.HostPort)), Containers: ptr(containers), ID: target.ID})
		}
		if err := q.RetireLiveRelease(ctx, r.app.ID); err != nil {
			return err
		}
		if err := q.ActivateRelease(ctx, db.ActivateReleaseParams{HostPort: ptr(int64(res.Web.HostPort)), Containers: ptr(containers), ID: target.ID}); err != nil {
			return err
		}
		return q.SetAppState(ctx, db.SetAppStateParams{Status: "running", CurrentReleaseID: &target.ID, ID: r.app.ID})
	}); err != nil {
		return nil, err
	}
	target.Status = "live"
	return &target, nil
}

// ---- helpers -----------------------------------------------------------------------------------------

func ptr[T any](v T) *T { return &v }

func nonEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
