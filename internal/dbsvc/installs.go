package dbsvc

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/catalog"
	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/pgtext"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Installed recipes: an "app" recipe of the built-in catalog (package catalog), run as one container
next to a site, on the site's server (agent ops recipe.install / recipe.remove / recipe.status).

What travels: the recipe's id and version, and the image only so the agent can compare it with its
own catalog. The agent runs ITS copy of the recipe. Generated values and a data service's connection
string travel in a box sealed to the agent's key; the generated values are also sealed with the team
DEK, bound to the install, in app_installs.secrets. No route returns them.

How the site reaches an install. Its HTTP port is published on the server's 127.0.0.1 only, and a
site's containers run on Docker's bridge, from which the host's loopback is not reachable. So in this
step the one address a site has for an install is the hostname the caller may give: a normal proxy
host (deploy.CreateProxyHost — same hostname rules, DNS wiring, verification and TLS as any other)
whose upstream is the install's loopback port. {host} in a recipe's `exposes` is that hostname and
{port} is 443. Without a hostname, the `exposes` entries that use {host} or {port} are not set — the
answer lists them as skipped — while everything else (inject_as, templates without an address) is.

The proxy host is created, disabled, in the request's transaction — so a taken hostname is a 409
before anything runs — and is pointed at the port and enabled once the agent has answered. Docker
picks a new loopback port whenever the container restarts; recipe.status reports the current one and
ReconcileInstallPorts moves the row and the proxy host after it.
*/

const (
	InstallCreating = "creating"
	InstallReady    = "ready"
	InstallFailed   = "failed"
	InstallRemoving = "removing"

	// installBusyFor: like browserBusyFor, but an install's pull and health wait may take longer.
	installBusyFor    = 35 * time.Minute
	InstallStatusWait = 8 * time.Second
	InstallRemoveWait = 90 * time.Second

	// installPlaceholderUpstream is the upstream of an install's proxy host until its port is known.
	// The proxy host is disabled for that long, so nothing is ever routed there.
	installPlaceholderUpstream = "http://127.0.0.1:9"
	// installSitePort is {port}: the site reaches an install through its proxy host, over HTTPS.
	installSitePort = "443"
)

func installSealContext(installID string) string { return "appinstall:" + installID + ":secrets" }

// Install is an installed recipe as the API shows it. Never a secret.
type AppInstall struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Recipe        string    `json:"recipe" doc:"The recipe's id in the catalog."`
	RecipeName    string    `json:"recipe_name"`
	RecipeVersion string    `json:"recipe_version"`
	Image         string    `json:"image" doc:"The image of that recipe in this control plane's catalog; empty when the recipe left the catalog."`
	HasWebUI      bool      `json:"has_web_ui"`
	HostPort      *int64    `json:"host_port" doc:"The port on the server's 127.0.0.1; null until the server answered, and for a headless recipe."`
	ProxyHostID   *string   `json:"proxy_host_id" doc:"The proxy host this install created; removed with it."`
	Hostname      *string   `json:"hostname"`
	EnvInjected   []string  `json:"env_injected" doc:"Exactly the variables this install set on the site."`
	Status        string    `json:"status" enum:"creating,ready,failed,removing"`
	Error         *string   `json:"error"`
	Running       *bool     `json:"running" doc:"Whether its container runs; null when the server did not answer."`
	Restarts      *int64    `json:"restarts"`
	MemoryBytes   *int64    `json:"memory_bytes"`
	ExtraHostPort *int64    `json:"extra_host_port" doc:"The recipe's extra_port (e.g. an admin console) on the server's 127.0.0.1; null until the server answered, and for a recipe without one. Not proxied automatically."`
	CreatedAt     time.Time `json:"created_at"`
}

func installView(ctx context.Context, q *db.Queries, teamID string, row db.AppInstall) AppInstall {
	v := AppInstall{ID: row.ID, Name: row.Name, Recipe: row.RecipeID, RecipeName: row.RecipeID, RecipeVersion: row.RecipeVersion,
		HostPort: row.HostPort, ProxyHostID: row.ProxyHostID, EnvInjected: append([]string{}, row.EnvInjected...),
		Status: row.Status, Error: row.Error, CreatedAt: row.CreatedAt}
	if c, err := catalog.Builtin(); err == nil {
		if r, ok := c.Get(row.RecipeID); ok {
			v.RecipeName, v.Image, v.HasWebUI = r.Name, r.Image, r.HasWebUI
		}
	}
	if row.ProxyHostID != nil {
		if p, err := q.ProxyHostInTeam(ctx, db.ProxyHostInTeamParams{TeamID: teamID, Ref: *row.ProxyHostID}); err == nil {
			v.Hostname = &p.Hostname
		}
	}
	return v
}

// InstallSpec is what a caller chooses.
type InstallSpec struct {
	Recipe   string
	Name     string
	Postgres string // a managed Postgres on the same server, when the recipe needs one
	MySQL    string // a managed MySQL/MariaDB on the same server, when the recipe needs one
	Redis    string
	Hostname string // optional: a proxy host to the install's web UI
}

// InstallCreated is the answer of CreateInstall.
type InstallCreated struct {
	Install   AppInstall
	Skipped   []string // exposes that were not set because no hostname was given
	Operation db.Operation
}

// ListInstalls is the app's installs (team scope).
func ListInstalls(ctx context.Context, q *db.Queries, teamID string, a db.App) ([]AppInstall, error) {
	rows, err := q.ListInstallsOfApp(ctx, db.ListInstallsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	out := make([]AppInstall, 0, len(rows))
	for _, r := range rows {
		out = append(out, installView(ctx, q, teamID, r))
	}
	return out, nil
}

// FindInstall resolves one install of an app by id or name.
func FindInstall(ctx context.Context, q *db.Queries, teamID string, a db.App, ref string) (db.AppInstall, error) {
	row, err := q.InstallOfApp(ctx, db.InstallOfAppParams{AppID: a.ID, TeamID: teamID, Ref: strings.TrimSpace(ref)})
	if err != nil {
		return db.AppInstall{}, notFound("install")
	}
	return row, nil
}

// RefuseImportedInstalls is the 422 of every install write on an imported app.
func RefuseImportedInstalls(a db.App) error {
	if deploy.IsImported(a) {
		return invalid("recipes are installed next to a site Hosbah deploys — " + a.Name + " is an imported app")
	}
	return nil
}

// QueueInstallStatus asks the app's server how the installs are doing. Best effort.
func (s *Service) QueueInstallStatus(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, installs []AppInstall) db.Operation {
	if len(installs) == 0 || deploy.IsImported(a) {
		return db.Operation{}
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil || srv.AgentPublicKey == nil || !s.ops.Connected(srv.ID) {
		return db.Operation{}
	}
	p := agentproto.RecipeStatusParams{AppID: a.ID}
	for _, in := range installs {
		p.InstallIDs, p.RecipeIDs = append(p.InstallIDs, in.ID), append(p.RecipeIDs, in.Recipe)
	}
	if p.Validate() != nil {
		return db.Operation{}
	}
	sp := opsSpec(teamID, srv.ID, agentproto.KindRecipeStatus, mustJSON(p), actor)
	sp.QueueTTL = time.Minute
	op, _, err := s.ops.Enqueue(ctx, q, sp)
	if err != nil {
		return db.Operation{}
	}
	return op
}

// WithInstallStatus copies what the server answered into the installs.
func WithInstallStatus(installs []AppInstall, res agentproto.RecipeStatusResult) {
	for i := range installs {
		for _, st := range res.Installs {
			if st.InstallID == installs[i].ID {
				running, restarts := st.Running, int64(st.Restarts)
				installs[i].Running, installs[i].Restarts, installs[i].MemoryBytes = &running, &restarts, st.MemoryBytes
				if st.ExtraHostPort > 0 {
					extra := int64(st.ExtraHostPort)
					installs[i].ExtraHostPort = &extra
				}
			}
		}
	}
}

// ReconcileInstallPorts follows a loopback port that moved (Docker picks a new one when a container
// restarts): the row, and the upstream of the proxy host the install created. System + team scope —
// the listing that noticed may be a viewer's. Ready installs only. Best effort.
func (s *Service) ReconcileInstallPorts(ctx context.Context, teamID string, a db.App, installs []AppInstall, res agentproto.RecipeStatusResult) {
	for i := range installs {
		in := &installs[i]
		for _, st := range res.Installs {
			if st.InstallID != in.ID || !st.Running || st.HostPort <= 0 || in.Status != InstallReady ||
				(in.HostPort != nil && *in.HostPort == int64(st.HostPort)) {
				continue
			}
			port := int64(st.HostPort)
			err := s.sys(ctx, teamID, func(q *db.Queries) error {
				row, err := q.InstallOfApp(ctx, db.InstallOfAppParams{AppID: a.ID, TeamID: teamID, Ref: in.ID})
				if err != nil || row.Status != InstallReady {
					return err
				}
				return s.pointInstall(ctx, q, teamID, Actor{Via: "system"}, row, port)
			})
			if err != nil {
				s.log.Warn("an install's port moved and could not be followed", "install", in.ID, "err", err)
				continue
			}
			in.HostPort = &port
		}
	}
}

// pointInstall records the install's loopback port and points (and enables) its proxy host at it.
func (s *Service) pointInstall(ctx context.Context, q *db.Queries, teamID string, actor Actor, row db.AppInstall, port int64) error {
	if _, err := q.SetAppInstallHostPort(ctx, db.SetAppInstallHostPortParams{ID: row.ID, TeamID: teamID, HostPort: &port}); err != nil {
		return err
	}
	if row.ProxyHostID == nil {
		return nil
	}
	p, err := q.ProxyHostInTeam(ctx, db.ProxyHostInTeamParams{TeamID: teamID, Ref: *row.ProxyHostID})
	if err != nil {
		return nil // somebody deleted it by hand
	}
	on := true
	_, err = s.deploy.UpdateProxyHost(ctx, q, teamID, actor, p, deploy.ProxyHostInput{Upstream: fmt.Sprintf("http://127.0.0.1:%d", port), Enabled: &on})
	return err
}

func randFrom(alphabet string, n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func generateValue(g catalog.RecipeGenerate) string {
	if g.Charset == catalog.CharsetHex {
		return randFrom("0123456789abcdef", g.Length)
	}
	return randFrom(pwAlphabet, g.Length)
}

// resolvedService resolves the data service a recipe needs: the caller's choice, of the right kind,
// running, on the app's server. Step 1 never creates one. A zero db.DataService (ID == "") means the
// recipe does not need this kind.
func (s *Service) resolvedService(ctx context.Context, q *db.Queries, teamID string, a db.App, needed bool, kind, ref string) (db.DataService, error) {
	ref = strings.TrimSpace(ref)
	if !needed {
		if ref != "" {
			return db.DataService{}, invalid("this recipe does not use a " + kind + " service")
		}
		return db.DataService{}, nil
	}
	if ref == "" {
		return db.DataService{}, invalid("this recipe needs a managed " + kind + " service on the app's server: give its name or id as " + kind)
	}
	svc, err := ServiceByRef(ctx, q, teamID, ref)
	if err != nil {
		return db.DataService{}, err
	}
	if err := usable(svc); err != nil {
		return db.DataService{}, err
	}
	switch {
	case svc.Kind != kind:
		return db.DataService{}, invalid(svc.Name + " is a " + svc.Kind + " service, not " + kind)
	case svc.ServerID != a.ServerID:
		return db.DataService{}, invalid(svc.Name + " is on another server — an install uses a data service on the app's own server")
	case svc.Status != "running" || svc.Host == nil || svc.Port == nil:
		return db.DataService{}, conflict("the service is not running yet")
	}
	return svc, nil
}

// serviceParts is a data service's connection string, and its pieces, for a recipe that takes a
// connection apart (env: from: postgres_host, mysql_user, …) instead of one URL.
type serviceParts struct{ URL, Host, Port, HostPort, Database, User, Password string }

// connParts resolves a service's connection into serviceParts. Zero db.DataService (ID == "") yields
// a zero serviceParts — used when a recipe does not need that kind.
func (s *Service) connParts(ctx context.Context, q *db.Queries, teamID string, svc db.DataService) (serviceParts, error) {
	if svc.ID == "" {
		return serviceParts{}, nil
	}
	host, port := derefStr(svc.Host), int(derefInt(svc.Port))
	hostPort := fmt.Sprintf("%s:%d", host, port)
	if svc.Kind == "redis" {
		return serviceParts{URL: fmt.Sprintf("redis://%s:%d", host, port), Host: host, Port: strconv.Itoa(port), HostPort: hostPort}, nil
	}
	users, err := q.ListDatabaseUsersOfService(ctx, db.ListDatabaseUsersOfServiceParams{DataServiceID: svc.ID, TeamID: teamID})
	if err != nil || len(users) == 0 {
		return serviceParts{}, conflict("create a database and user first")
	}
	u := users[0]
	dbName := u.Name
	if u.DatabaseID != nil {
		if d, err := q.DatabaseInTeam(ctx, db.DatabaseInTeamParams{ID: *u.DatabaseID, TeamID: teamID}); err == nil {
			dbName = d.Name
		}
	}
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return serviceParts{}, err
	}
	pt, err := crypto.Open(dek, teamID, userContext(svc.ID, u.Name), u.PasswordSealed)
	if err != nil {
		return serviceParts{}, errors.New("the user password could not be opened")
	}
	scheme := "postgres"
	if svc.Kind == "mysql" {
		scheme = "mysql"
	}
	ui := url.UserPassword(u.Name, string(pt))
	return serviceParts{
		URL:  fmt.Sprintf("%s://%s@%s:%d/%s", scheme, ui.String(), host, port, dbName),
		Host: host, Port: strconv.Itoa(port), HostPort: hostPort, Database: dbName, User: u.Name, Password: string(pt),
	}, nil
}

// CreateInstall records the install, reserves its hostname, sets its variables on the site and
// queues recipe.install — all in the caller's transaction, so a refused request leaves nothing. The
// install is 'creating' until the agent answers (FollowInstall, after the commit). A variable that
// already exists on the site is never overwritten: 409.
func (s *Service) CreateInstall(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, in InstallSpec) (InstallCreated, error) {
	var out InstallCreated
	if err := RefuseImportedInstalls(a); err != nil {
		return out, err
	}
	cat, err := catalog.Builtin()
	if err != nil {
		return out, err
	}
	r, ok := cat.Get(strings.TrimSpace(in.Recipe))
	if !ok {
		return out, notFound("recipe")
	}
	if r.Kind != catalog.KindApp {
		return out, invalid(r.Name + " is a stack: it prefills a new app and installs nothing")
	}
	name := strings.TrimSpace(in.Name)
	if !agentproto.ValidInstallName(name) {
		return out, invalid("an install name is a lowercase letter, then up to 30 lowercase letters, digits and dashes")
	}
	hostname := deploy.NormalizeHostname(in.Hostname)
	if strings.TrimSpace(in.Hostname) != "" && !r.HasWebUI {
		return out, invalid(r.Name + " has no web port to put a hostname in front of")
	}
	srv, err := browserServer(ctx, q, teamID, a)
	if err != nil {
		return out, err
	}
	existing, err := q.ListInstallsOfApp(ctx, db.ListInstallsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return out, err
	}
	if len(existing) >= agentproto.MaxAppInstalls {
		return out, conflict(fmt.Sprintf("an app has at most %d installs", agentproto.MaxAppInstalls))
	}
	for _, e := range existing {
		if e.Name == name {
			return out, conflict("install " + name + " already exists on " + a.Name)
		}
	}
	pgSvc, err := s.resolvedService(ctx, q, teamID, a, r.Needs.Postgres, "postgres", in.Postgres)
	if err != nil {
		return out, err
	}
	mysqlSvc, err := s.resolvedService(ctx, q, teamID, a, r.Needs.MySQL, "mysql", in.MySQL)
	if err != nil {
		return out, err
	}
	redisSvc, err := s.resolvedService(ctx, q, teamID, a, r.Needs.Redis, "redis", in.Redis)
	if err != nil {
		return out, err
	}
	pgParts, err := s.connParts(ctx, q, teamID, pgSvc)
	if err != nil {
		return out, err
	}
	mysqlParts, err := s.connParts(ctx, q, teamID, mysqlSvc)
	if err != nil {
		return out, err
	}
	redisParts, err := s.connParts(ctx, q, teamID, redisSvc)
	if err != nil {
		return out, err
	}
	fromValue := map[string]string{
		catalog.FromPostgresURL: pgParts.URL, catalog.FromPostgresHost: pgParts.Host, catalog.FromPostgresPort: pgParts.Port,
		catalog.FromPostgresHostPort: pgParts.HostPort,
		catalog.FromPostgresDatabase: pgParts.Database, catalog.FromPostgresUser: pgParts.User, catalog.FromPostgresPassword: pgParts.Password,
		catalog.FromMySQLURL: mysqlParts.URL, catalog.FromMySQLHost: mysqlParts.Host, catalog.FromMySQLPort: mysqlParts.Port,
		catalog.FromMySQLHostPort: mysqlParts.HostPort,
		catalog.FromMySQLDatabase: mysqlParts.Database, catalog.FromMySQLUser: mysqlParts.User, catalog.FromMySQLPassword: mysqlParts.Password,
		catalog.FromRedisURL: redisParts.URL,
	}

	// The container's secret env (the box) and the generated values (sealed at rest).
	generated, boxed := map[string]string{}, map[string]string{}
	for _, e := range r.Env {
		switch {
		case e.Generate != nil:
			generated[e.Name] = generateValue(*e.Generate)
			boxed[e.Name] = generated[e.Name]
		case e.From != "":
			boxed[e.Name] = fromValue[e.From]
		}
	}
	// What the site gets. Nothing that needs an address when there is none.
	vars := map[string]string{}
	for k, v := range generated {
		vars[k] = v
	}
	if hostname != "" {
		vars["host"], vars["port"] = hostname, installSitePort
	}
	type siteVar struct{ name, value string }
	var site []siteVar
	for _, e := range r.Env {
		if e.InjectAs == "" {
			continue
		}
		v := generated[e.Name]
		if e.Value != nil {
			if v, err = catalog.Render(*e.Value, generated); err != nil {
				return out, err
			}
		}
		site = append(site, siteVar{e.InjectAs, v})
	}
	for _, x := range r.Exposes {
		if catalog.UsesAddress(x.Template) && hostname == "" {
			out.Skipped = append(out.Skipped, x.Env)
			continue
		}
		v, err := catalog.Render(x.Template, vars)
		if err != nil {
			return out, err
		}
		site = append(site, siteVar{x.Env, v})
	}
	injected := pgtext.Array{}
	for _, v := range site {
		if _, err := q.EnvVarInApp(ctx, db.EnvVarInAppParams{AppID: a.ID, TeamID: teamID, Name: v.name}); err == nil {
			return out, conflict(v.name + " is already set on " + a.Name + " — remove or rename it first (an install never overwrites a variable)")
		}
		injected = append(injected, v.name)
	}

	id := newUUID()
	var proxyID *string
	if hostname != "" {
		off := false
		p, err := s.deploy.CreateProxyHost(ctx, q, teamID, actor, deploy.ProxyHostInput{Server: srv.ID, Hostname: hostname,
			Upstream: installPlaceholderUpstream, Enabled: &off})
		if err != nil {
			return out, err
		}
		proxyID = &p.ID
	}
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return out, err
	}
	sealed, err := crypto.Seal(dek, teamID, installSealContext(id), mustJSON(generated))
	if err != nil {
		return out, err
	}
	row, err := q.InsertAppInstall(ctx, db.InsertAppInstallParams{ID: id, TeamID: teamID, AppID: a.ID, RecipeID: r.ID, RecipeVersion: r.Version,
		Name: name, ServerID: srv.ID, ProxyHostID: proxyID, Secrets: sealed, EnvInjected: injected})
	if err != nil {
		return out, conflict("install " + name + " already exists on " + a.Name)
	}
	for _, v := range site {
		if err := s.deploy.SetEnv(ctx, q, teamID, actor, a, v.name, v.value); err != nil {
			return out, err
		}
	}
	p := agentproto.RecipeInstallParams{InstallID: id, AppID: a.ID, RecipeID: r.ID, RecipeVersion: r.Version, Image: r.Image}
	if len(boxed) > 0 {
		sec := agentproto.InstallSecrets{Env: boxed}
		if err := sec.Validate(); err != nil {
			return out, invalid(err.Error())
		}
		if p.Secrets, err = agentproto.SealBox(*srv.AgentPublicKey, agentproto.InstallSecretsContext(id), mustJSON(sec)); err != nil {
			return out, err
		}
	}
	if err := p.Validate(); err != nil {
		return out, invalid(err.Error())
	}
	out.Operation, _, err = s.ops.Enqueue(ctx, q, opsSpecWithID(opID(id, "recipe-install"), teamID, srv.ID, agentproto.KindRecipeInstall, mustJSON(p), actor))
	if err != nil {
		return out, err
	}
	extra := map[string]any{"install": id, "name": name, "recipe": r.ID, "recipe_version": r.Version, "image": r.Image,
		"env_injected": []string(injected), "operation": out.Operation.ID}
	if hostname != "" {
		extra["hostname"] = hostname
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "install.created", "app", a.ID, auditExtra(actor, extra)); err != nil {
		return out, err
	}
	out.Install = installView(ctx, q, teamID, row)
	return out, nil
}

// FollowInstall waits, in the background, for the recipe.install queued by CreateInstall and records
// how it ended: ready, with its port and its proxy host pointed at it — or failed, and the hostname
// it reserved given back. Call it after the request's transaction committed.
func (s *Service) FollowInstall(teamID string, actor Actor, a db.App, installID string, op db.Operation) {
	s.spawn(func(ctx context.Context) {
		done, err := s.awaitVisible(ctx, op)
		failure := ""
		var res agentproto.RecipeInstallResult
		switch {
		case err != nil:
			failure = err.Error()
		case done.Status != "ok":
			failure = opError(done)
		case done.Result != nil:
			_ = json.Unmarshal([]byte(*done.Result), &res)
		}
		if err := s.sys(ctx, teamID, func(q *db.Queries) error {
			row, err := q.InstallOfApp(ctx, db.InstallOfAppParams{AppID: a.ID, TeamID: teamID, Ref: installID})
			if err != nil || row.Status != InstallCreating {
				return err
			}
			if failure == "" && res.HostPort > 0 {
				if err := s.pointInstall(ctx, q, teamID, actor, row, int64(res.HostPort)); err != nil {
					failure = "it runs, but its hostname could not be routed: " + err.Error()
				}
			}
			status, errStr := InstallReady, (*string)(nil)
			if failure != "" {
				status, errStr = InstallFailed, strptr(failure)
				s.dropInstallProxy(ctx, q, teamID, actor, row)
			}
			_, err = q.SetAppInstallStatus(ctx, db.SetAppInstallStatusParams{ID: installID, TeamID: teamID, Status: status, Error: errStr, From: InstallCreating})
			return err
		}); err != nil {
			s.log.Warn("an install's outcome could not be recorded", "install", installID, "err", err)
		}
	})
}

// dropInstallProxy deletes the proxy host an install created (the FK then clears proxy_host_id).
func (s *Service) dropInstallProxy(ctx context.Context, q *db.Queries, teamID string, actor Actor, row db.AppInstall) {
	if row.ProxyHostID == nil {
		return
	}
	if p, err := q.ProxyHostInTeam(ctx, db.ProxyHostInTeamParams{TeamID: teamID, Ref: *row.ProxyHostID}); err == nil {
		if err := s.deploy.DeleteProxyHost(ctx, q, teamID, actor, p); err != nil {
			s.log.Warn("an install's proxy host could not be deleted", "install", row.ID, "err", err)
		}
	}
}

// InstallRemoval is a removal that was begun.
type InstallRemoval struct {
	Install    db.AppInstall
	DeleteData bool
	Operation  db.Operation
}

// BeginInstallRemoval checks the request, marks the install 'removing' and queues recipe.remove in
// the caller's transaction. The caller commits, waits (AwaitInstallRemoval) and then calls
// FinishInstallRemoval in a new transaction. Nothing of the site changes before the agent answered.
func (s *Service) BeginInstallRemoval(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, row db.AppInstall, confirm string, deleteData bool) (InstallRemoval, error) {
	rm := InstallRemoval{Install: row, DeleteData: deleteData}
	if err := RefuseImportedInstalls(a); err != nil {
		return rm, err
	}
	if confirm != row.Name {
		return rm, invalid("confirm must be the install's name: " + row.Name)
	}
	if (row.Status == InstallCreating || row.Status == InstallRemoving) && time.Since(row.UpdatedAt) < installBusyFor {
		return rm, conflict("the install is still " + row.Status + " — try again when that has finished")
	}
	srv, err := browserServer(ctx, q, teamID, a)
	if err != nil {
		return rm, err
	}
	if !s.ops.Connected(srv.ID) && !ops.Online(srv.LastSeenAt, time.Now()) {
		return rm, conflict(srv.Name + " is offline — the install can be removed once its agent is connected")
	}
	p := agentproto.RecipeRemoveParams{InstallID: row.ID, AppID: a.ID, DeleteData: deleteData}
	if err := p.Validate(); err != nil {
		return rm, invalid(err.Error())
	}
	if n, err := q.SetAppInstallStatus(ctx, db.SetAppInstallStatusParams{ID: row.ID, TeamID: teamID, Status: InstallRemoving, From: row.Status}); err != nil || n == 0 {
		return rm, conflict("the install changed meanwhile — try again")
	}
	sp := opsSpec(teamID, srv.ID, agentproto.KindRecipeRemove, mustJSON(p), actor)
	sp.QueueTTL = 2 * time.Minute
	rm.Operation, _, err = s.ops.Enqueue(ctx, q, sp)
	return rm, err
}

// AwaitInstallRemoval waits for the removal's operation. "" means it succeeded.
func (s *Service) AwaitInstallRemoval(ctx context.Context, rm InstallRemoval) (failure string) {
	if rm.Operation.ID == "" {
		return ""
	}
	wctx, cancel := context.WithTimeout(ctx, InstallRemoveWait)
	defer cancel()
	done, err := s.awaitVisible(wctx, rm.Operation)
	switch {
	case err != nil:
		return "the server did not answer in time (operation " + rm.Operation.ID + ")"
	case done.Status != "ok":
		return opError(done)
	}
	return ""
}

// FinishInstallRemoval ends a removal in a new team transaction. failure "": exactly the variables
// the install set are unset, the proxy host it created goes, the record goes, and it is audited.
// Otherwise the install is as it was.
func (s *Service) FinishInstallRemoval(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, rm InstallRemoval, failure string) error {
	row := rm.Install
	if failure != "" {
		back := row.Status
		if back != InstallFailed {
			back = InstallReady
		}
		_, err := q.SetAppInstallStatus(ctx, db.SetAppInstallStatusParams{ID: row.ID, TeamID: teamID, Status: back, Error: strptr(failure), From: InstallRemoving})
		return err // the caller answers with InstallRemovalRefused AFTER this transaction committed
	}
	for _, n := range row.EnvInjected {
		if _, err := q.DeleteEnvVarInApp(ctx, db.DeleteEnvVarInAppParams{AppID: a.ID, TeamID: teamID, Name: n}); err != nil {
			return err
		}
	}
	extra := map[string]any{"install": row.ID, "name": row.Name, "recipe": row.RecipeID, "recipe_version": row.RecipeVersion,
		"env_unset": []string(row.EnvInjected), "delete_data": rm.DeleteData}
	if row.ProxyHostID != nil {
		if p, err := q.ProxyHostInTeam(ctx, db.ProxyHostInTeamParams{TeamID: teamID, Ref: *row.ProxyHostID}); err == nil {
			if err := s.deploy.DeleteProxyHost(ctx, q, teamID, actor, p); err != nil {
				return err
			}
			extra["hostname"] = p.Hostname
		}
	}
	if _, err := q.DeleteAppInstallInTeam(ctx, db.DeleteAppInstallInTeamParams{ID: row.ID, TeamID: teamID}); err != nil {
		return err
	}
	if rm.Operation.ID != "" {
		extra["operation"] = rm.Operation.ID
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "install.removed", "app", a.ID, auditExtra(actor, extra))
}

// InstallRemovalRefused is the 409 of a removal the agent did not carry out.
func InstallRemovalRefused(failure string) error {
	return conflict("the install was not removed: " + failure + " — try again")
}
