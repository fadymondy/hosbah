package dbsvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
App browsers: a headless Chromium of a site, on the site's own server, that the site drives over
CDP/WebSocket (agent ops browser.create / browser.remove / browser.status).

How a site's container reaches it: exactly the way it reaches a data service — the server's private
address (privateBind: the Docker bridge gateway 172.17.0.1 unless HOSBAH_DB_BIND names a private
NIC) and a port the control plane hands out (from agentproto.BrowserPortBase). Release containers
run on Docker's default bridge, from which the host's 127.0.0.1 is NOT reachable — so Chromium's own
port is published on 127.0.0.1 only, and what listens on the private address is the agent's gateway,
which checks the token before anything reaches Chromium.

How the token travels, like a bucket key's secret: generated HERE (40 alphanumerics), sealed with the
team DEK bound to the browser for the row, and sealed again to the target agent's own key (a box,
context BrowserTokenContext) for the operation — operations.params holds a box and no plaintext. The
agent keeps its SHA-256 only. The app receives it as the sealed env vars <PREFIX>_WS_ENDPOINT (the
token is in its query string, so the whole value is sealed) and <PREFIX>_TOKEN.
*/

const (
	// DefaultBrowserPrefix is the env prefix when the caller gives none.
	DefaultBrowserPrefix = "BROWSER"

	BrowserCreating = "creating"
	BrowserReady    = "ready"
	BrowserFailed   = "failed"
	BrowserRemoving = "removing"

	// browserBusyFor: a creating/removing browser younger than this is left alone (a first image pull
	// takes minutes); older, the worker that owned it is taken to be gone and a removal may take over.
	browserBusyFor = 25 * time.Minute
	// BrowserStatusWait is how long a listing waits for browser.status.
	BrowserStatusWait = 8 * time.Second
	// BrowserRemoveWait is how long a DELETE waits for the agent.
	BrowserRemoveWait = 90 * time.Second
)

func browserSealContext(browserID string) string { return "appbrowser:" + browserID + ":token" }

// BrowserEnvNames are the variables a browser sets on its app, in a fixed order.
func BrowserEnvNames(prefix string) []string {
	return []string{prefix + "_WS_ENDPOINT", prefix + "_TOKEN"}
}

// Browser is an app browser as the API shows it. The token is never part of it, and neither is the
// endpoint (it carries the token). Kind is "headless" (the default, driven over CDP by the app) or
// "desktop" (a full interactive KasmVNC/Chromium session at Hostname, opened by a team member).
type Browser struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Kind               string    `json:"kind" enum:"headless,desktop"`
	Image              string    `json:"image" doc:"The browser image, fixed by Hosbah for the chosen kind."`
	MemoryMB           int64     `json:"memory_mb"`
	CPUs               float64   `json:"cpus"`
	MaxSessions        int64     `json:"max_sessions" doc:"Headless only."`
	IdleTimeoutSeconds int64     `json:"idle_timeout_seconds" doc:"Headless only."`
	EnvPrefix          string    `json:"env_prefix" doc:"Headless only."`
	Env                []string  `json:"env" doc:"The variables this browser set on the app. Empty for a desktop browser."`
	Hostname           *string   `json:"hostname" doc:"Desktop only: where a team member opens it."`
	Status             string    `json:"status" enum:"creating,ready,failed,removing"`
	Error              *string   `json:"error"`
	Running            *bool     `json:"running" doc:"Whether its container runs; null when the server did not answer."`
	Restarts           *int64    `json:"restarts" doc:"How often Docker restarted it; null when the server did not answer."`
	MemoryBytes        *int64    `json:"memory_bytes" doc:"Memory in use as docker stats reports it; null when unknown."`
	CreatedAt          time.Time `json:"created_at"`
}

func browserView(b db.AppBrowser) Browser {
	env := []string{}
	if b.Kind != agentproto.BrowserKindDesktop {
		env = BrowserEnvNames(b.EnvPrefix)
	}
	return Browser{ID: b.ID, Name: b.Name, Kind: b.Kind, Image: b.Image, MemoryMB: b.MemoryMb, CPUs: b.Cpus, MaxSessions: b.MaxSessions,
		IdleTimeoutSeconds: b.IdleTimeoutSeconds, EnvPrefix: b.EnvPrefix, Env: env, Hostname: b.Hostname, Status: b.Status,
		Error: b.Error, CreatedAt: b.CreatedAt}
}

// BrowserSpec is what a caller may choose. Zero values take the defaults. The image is not in it.
// Kind "" reads as headless. A desktop browser ignores EnvPrefix (it sets no app env vars).
type BrowserSpec struct {
	Name               string
	Kind               string
	MemoryMB           int64
	CPUs               float64
	MaxSessions        int64
	IdleTimeoutSeconds int64
	EnvPrefix          string
}

// ListBrowsers is the app's browsers (team scope).
func ListBrowsers(ctx context.Context, q *db.Queries, teamID string, a db.App) ([]Browser, error) {
	rows, err := q.ListBrowsersOfApp(ctx, db.ListBrowsersOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	out := make([]Browser, 0, len(rows))
	for _, b := range rows {
		out = append(out, browserView(b))
	}
	return out, nil
}

// FindBrowser resolves one browser of an app by id or name.
func FindBrowser(ctx context.Context, q *db.Queries, teamID string, a db.App, ref string) (db.AppBrowser, error) {
	b, err := q.BrowserOfApp(ctx, db.BrowserOfAppParams{AppID: a.ID, TeamID: teamID, Ref: strings.TrimSpace(ref)})
	if err != nil {
		return db.AppBrowser{}, notFound("browser")
	}
	return b, nil
}

// QueueBrowserStatus asks the app's server how the browsers are doing. Best effort: a zero
// operation when there is nothing to ask or nobody to ask.
func (s *Service) QueueBrowserStatus(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, browsers []Browser) db.Operation {
	if len(browsers) == 0 || deploy.IsImported(a) {
		return db.Operation{}
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil || srv.AgentPublicKey == nil || !s.ops.Connected(srv.ID) {
		return db.Operation{}
	}
	p := agentproto.BrowserStatusParams{AppID: a.ID}
	for _, b := range browsers {
		p.BrowserIDs = append(p.BrowserIDs, b.ID)
	}
	if p.Validate() != nil {
		return db.Operation{}
	}
	sp := opsSpec(teamID, srv.ID, agentproto.KindBrowserStatus, mustJSON(p), actor)
	sp.QueueTTL = time.Minute
	op, _, err := s.ops.Enqueue(ctx, q, sp)
	if err != nil {
		return db.Operation{}
	}
	return op
}

// WithBrowserStatus copies what the server answered into the browsers.
func WithBrowserStatus(browsers []Browser, res agentproto.BrowserStatusResult) {
	for i := range browsers {
		for _, st := range res.Browsers {
			if st.BrowserID == browsers[i].ID {
				running, restarts := st.Running, int64(st.Restarts)
				browsers[i].Running, browsers[i].Restarts, browsers[i].MemoryBytes = &running, &restarts, st.MemoryBytes
			}
		}
	}
}

func between[T int64 | float64](v, def, lo, hi T, what string) (T, error) {
	if v == 0 {
		return def, nil
	}
	if v < lo || v > hi {
		return 0, invalid(fmt.Sprintf("%s is %v to %v", what, lo, hi))
	}
	return v, nil
}

// browserServer is the app's server, with an enrolled agent.
func browserServer(ctx context.Context, q *db.Queries, teamID string, a db.App) (db.Server, error) {
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil {
		return db.Server{}, notFound("server")
	}
	if srv.AgentPublicKey == nil || *srv.AgentPublicKey == "" {
		return db.Server{}, conflict("the app's server has no enrolled agent")
	}
	return srv, nil
}

// CreateBrowser records the browser, sets its two sealed variables on the app and queues
// browser.create — all in the caller's transaction, so a refused request leaves nothing. The browser
// is 'creating' until the agent answers (FollowBrowser, after the commit). The variables apply from
// the app's next deploy or restart. A variable that already exists is never overwritten: 409.
func (s *Service) CreateBrowser(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, in BrowserSpec) (Browser, db.Operation, error) {
	none := db.Operation{}
	if deploy.IsImported(a) {
		return Browser{}, none, invalid("browsers belong to a site Hosbah deploys — " + a.Name + " is an imported app")
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = agentproto.BrowserKindHeadless
	}
	if !agentproto.ValidBrowserKind(kind) {
		return Browser{}, none, invalid("kind must be headless or desktop")
	}
	desktop := kind == agentproto.BrowserKindDesktop
	name := strings.TrimSpace(in.Name)
	if !agentproto.ValidBrowserName(name) {
		return Browser{}, none, invalid("a browser name is a lowercase letter, then up to 30 lowercase letters, digits and dashes")
	}
	prefix := strings.TrimSpace(in.EnvPrefix)
	if prefix == "" {
		prefix = DefaultBrowserPrefix
	}
	if !desktop && !bucketPrefixRE.MatchString(prefix) {
		return Browser{}, none, invalid("env_prefix is an upper-case letter, then up to 20 upper-case letters, digits and _")
	}
	var err error
	defMemory, minMemory, maxMemory := agentproto.BrowserDefaultMemoryMB, agentproto.BrowserMinMemoryMB, agentproto.BrowserMaxMemoryMB
	if desktop {
		defMemory, minMemory, maxMemory = agentproto.DesktopDefaultMemoryMB, agentproto.DesktopMinMemoryMB, agentproto.DesktopMaxMemoryMB
	}
	if in.MemoryMB, err = between(in.MemoryMB, int64(defMemory), int64(minMemory), int64(maxMemory), "memory_mb"); err != nil {
		return Browser{}, none, err
	}
	if in.CPUs, err = between(in.CPUs, agentproto.BrowserDefaultCPUs, agentproto.BrowserMinCPUs, agentproto.BrowserMaxCPUs, "cpus"); err != nil {
		return Browser{}, none, err
	}
	if in.MaxSessions, err = between(in.MaxSessions, agentproto.BrowserDefaultSessions, agentproto.BrowserMinSessions, agentproto.BrowserMaxSessions, "max_sessions"); err != nil {
		return Browser{}, none, err
	}
	if in.IdleTimeoutSeconds, err = between(in.IdleTimeoutSeconds, agentproto.BrowserDefaultIdle, agentproto.BrowserMinIdle, agentproto.BrowserMaxIdle, "idle_timeout_seconds"); err != nil {
		return Browser{}, none, err
	}
	srv, err := browserServer(ctx, q, teamID, a)
	if err != nil {
		return Browser{}, none, err
	}
	existing, err := q.ListBrowsersOfApp(ctx, db.ListBrowsersOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return Browser{}, none, err
	}
	if len(existing) >= agentproto.MaxAppBrowsers {
		return Browser{}, none, conflict(fmt.Sprintf("an app has at most %d browsers", agentproto.MaxAppBrowsers))
	}
	for _, e := range existing {
		if e.Name == name {
			return Browser{}, none, conflict("browser " + name + " already exists on " + a.Name)
		}
	}
	names := BrowserEnvNames(prefix)
	if !desktop {
		for _, n := range names {
			if _, err := q.EnvVarInApp(ctx, db.EnvVarInAppParams{AppID: a.ID, TeamID: teamID, Name: n}); err == nil {
				return Browser{}, none, conflict(n + " is already set on " + a.Name + " — choose another env_prefix (a browser never overwrites a variable)")
			}
		}
	}
	taken, err := q.BrowserPortsOfServer(ctx, db.BrowserPortsOfServerParams{ServerID: srv.ID, TeamID: teamID})
	if err != nil {
		return Browser{}, none, err
	}
	used := map[int]bool{}
	for _, p := range taken {
		used[int(derefInt(p))] = true
	}
	// A headless gateway listens on the server's private address (reached by a release container,
	// the way a data service is). A desktop browser is dialed by Caddy on the SAME host, so it
	// listens on loopback only — never reachable except through the forward_auth'd hostname.
	bind, port := privateBind(srv.ID), nextFreePort(agentproto.BrowserPortBase, used)
	image := agentproto.BrowserImage
	if desktop {
		bind, image = "127.0.0.1", agentproto.DesktopImage
		var hostname string
		var teamOwned bool
		hostname, teamOwned = s.deploy.DesktopBrowserHostname(ctx, q, teamID, name)
		if hostname == "" {
			return Browser{}, none, conflict("connect a domain (team Cloudflare, or wait for the apps domain) before creating a desktop browser")
		}
		return s.createDesktopBrowser(ctx, q, teamID, actor, a, srv, in, name, kind, image, bind, port, hostname, teamOwned)
	}

	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return Browser{}, none, err
	}
	id, token := newUUID(), randPassword(40)
	sealed, err := crypto.Seal(dek, teamID, browserSealContext(id), []byte(token))
	if err != nil {
		return Browser{}, none, err
	}
	row, err := q.InsertAppBrowser(ctx, db.InsertAppBrowserParams{ID: id, TeamID: teamID, AppID: a.ID, Name: name, Image: image,
		MemoryMb: in.MemoryMB, Cpus: in.CPUs, MaxSessions: in.MaxSessions, IdleTimeoutSeconds: in.IdleTimeoutSeconds, EnvPrefix: prefix,
		TokenSealed: sealed, HostPort: int64ptr(int64(port)), Kind: kind})
	if err != nil {
		return Browser{}, none, conflict("browser " + name + " or env_prefix " + prefix + " is already in use")
	}
	endpoint := fmt.Sprintf("ws://%s:%d?token=%s", bind, port, token)
	for i, v := range []string{endpoint, token} {
		if err := s.deploy.SetEnv(ctx, q, teamID, actor, a, names[i], v); err != nil {
			return Browser{}, none, err
		}
	}
	box, err := agentproto.SealBox(*srv.AgentPublicKey, agentproto.BrowserTokenContext(id), mustJSON(map[string]string{"token": token}))
	if err != nil {
		return Browser{}, none, err
	}
	p := agentproto.BrowserCreateParams{BrowserID: id, AppID: a.ID, Image: image, Bind: bind, Port: port,
		MemoryMB: int(in.MemoryMB), CPUs: in.CPUs, MaxSessions: int(in.MaxSessions), IdleTimeoutSeconds: int(in.IdleTimeoutSeconds), Token: box}
	if err := p.Validate(); err != nil {
		return Browser{}, none, invalid(err.Error())
	}
	op, _, err := s.ops.Enqueue(ctx, q, opsSpecWithID(opID(id, "browser-create"), teamID, srv.ID, agentproto.KindBrowserCreate, mustJSON(p), actor))
	if err != nil {
		return Browser{}, none, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "browser.created", "app", a.ID, auditExtra(actor, map[string]any{
		"browser": id, "name": name, "image": image, "env_prefix": prefix, "memory_mb": in.MemoryMB, "cpus": in.CPUs,
		"max_sessions": in.MaxSessions, "idle_timeout_seconds": in.IdleTimeoutSeconds, "operation": op.ID})); err != nil {
		return Browser{}, none, err
	}
	return browserView(row), op, nil
}

// desktopEnvPrefixPlaceholder turns a browser id (a UUID: hex digits and dashes) into a value that
// satisfies app_browsers_env_prefix_check and the (app_id, env_prefix) unique index, for a desktop
// browser, which has no real env prefix. Never surfaced to a user or an app.
func desktopEnvPrefixPlaceholder(id string) string {
	p := "D" + strings.ToUpper(strings.ReplaceAll(id, "-", ""))
	if len(p) > 21 {
		p = p[:21]
	}
	return p
}

// createDesktopBrowser is CreateBrowser's desktop-kind branch: no CDP token, no app env vars, its
// own hostname behind forward_auth instead. Called with the caller's basic validation already done.
func (s *Service) createDesktopBrowser(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, srv db.Server,
	in BrowserSpec, name, kind, image, bind string, port int, hostname string, teamOwned bool) (Browser, db.Operation, error) {
	none := db.Operation{}
	id := newUUID()
	// token_sealed is NOT NULL; a desktop browser has no token, so an empty seal is its placeholder.
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return Browser{}, none, err
	}
	sealed, err := crypto.Seal(dek, teamID, browserSealContext(id), []byte{})
	if err != nil {
		return Browser{}, none, err
	}
	// env_prefix is NOT NULL, matches ^[A-Z][A-Z0-9_]{0,20}$ and is unique per app — a desktop browser
	// sets no env vars, so this is never read by an app, only a placeholder derived from the browser's
	// own id so two desktop browsers on the same app never collide.
	row, err := q.InsertAppBrowser(ctx, db.InsertAppBrowserParams{ID: id, TeamID: teamID, AppID: a.ID, Name: name, Image: image,
		MemoryMb: in.MemoryMB, Cpus: in.CPUs, MaxSessions: in.MaxSessions, IdleTimeoutSeconds: in.IdleTimeoutSeconds, EnvPrefix: desktopEnvPrefixPlaceholder(id),
		TokenSealed: sealed, HostPort: int64ptr(int64(port)), Kind: kind})
	if err != nil {
		return Browser{}, none, conflict("browser " + name + " is already in use")
	}
	if n, err := q.SetAppBrowserHostname(ctx, db.SetAppBrowserHostnameParams{Hostname: &hostname, ID: id, TeamID: teamID}); err != nil || n == 0 {
		return Browser{}, none, conflict("could not record the browser's hostname — try again")
	}
	row.Hostname = &hostname
	p := agentproto.BrowserCreateParams{BrowserID: id, AppID: a.ID, Kind: kind, Image: image, Bind: bind, Port: port,
		MemoryMB: int(in.MemoryMB), CPUs: in.CPUs, MaxSessions: int(in.MaxSessions), IdleTimeoutSeconds: int(in.IdleTimeoutSeconds)}
	if err := p.Validate(); err != nil {
		return Browser{}, none, invalid(err.Error())
	}
	op, _, err := s.ops.Enqueue(ctx, q, opsSpecWithID(opID(id, "browser-create"), teamID, srv.ID, agentproto.KindBrowserCreate, mustJSON(p), actor))
	if err != nil {
		return Browser{}, none, err
	}
	upstream := fmt.Sprintf("%s:%d", bind, port)
	if _, err := s.deploy.CreateDesktopBrowserProxyHost(ctx, q, teamID, actor, srv, id, hostname, upstream, teamOwned); err != nil {
		return Browser{}, none, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "browser.created", "app", a.ID, auditExtra(actor, map[string]any{
		"browser": id, "name": name, "kind": kind, "image": image, "hostname": hostname, "memory_mb": in.MemoryMB, "cpus": in.CPUs,
		"operation": op.ID})); err != nil {
		return Browser{}, none, err
	}
	return browserView(row), op, nil
}

// OpenDesktopBrowser mints "Open browser"'s one-time handoff: a token bound to this browser and the
// caller, exchanged at the browser's own hostname (<hostname>/.hosbah/auth?token=...) for a session
// cookie — see internal/deploy/browserauth.go. Headless or not-yet-hostnamed browsers have nothing to
// open. Not team-audited: no data moved, and a browser's own forward_auth check is the real gate.
func (s *Service) OpenDesktopBrowser(ctx context.Context, q *db.Queries, teamID string, actor Actor, b db.AppBrowser) (string, error) {
	if b.Kind != agentproto.BrowserKindDesktop || b.Hostname == nil || *b.Hostname == "" {
		return "", invalid("this is not a desktop browser with a hostname yet")
	}
	if b.Status != BrowserReady {
		return "", conflict("the browser is " + b.Status + " — try again once it is ready")
	}
	token := randPassword(40)
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	if _, err := q.InsertBrowserAuthToken(ctx, db.InsertBrowserAuthTokenParams{
		ID: newUUID(), TeamID: teamID, BrowserID: b.ID, UserID: actor.UserID, TokenHash: hash,
		ExpiresAt: time.Now().Add(deploy.BrowserAuthTokenTTL),
	}); err != nil {
		return "", err
	}
	return "https://" + *b.Hostname + "/.hosbah/auth?token=" + token, nil
}

// FollowBrowser waits, in the background, for a browser.create queued by CreateBrowser and records
// how it ended. Call it after the request's transaction committed.
func (s *Service) FollowBrowser(teamID, browserID string, op db.Operation) {
	s.spawn(func(ctx context.Context) {
		done, err := s.awaitVisible(ctx, op)
		status, errStr := BrowserReady, (*string)(nil)
		switch {
		case err != nil:
			status, errStr = BrowserFailed, strptr(err.Error())
		case done.Status != "ok":
			status, errStr = BrowserFailed, strptr(opError(done))
		}
		_ = s.sys(ctx, teamID, func(q *db.Queries) error {
			_, err := q.SetAppBrowserStatus(ctx, db.SetAppBrowserStatusParams{ID: browserID, TeamID: teamID, Status: status, Error: errStr, From: BrowserCreating})
			return err
		})
	})
}

// BrowserRemoval is a removal that was begun: what FinishBrowserRemoval needs once the agent answered.
type BrowserRemoval struct {
	Browser   db.AppBrowser
	Operation db.Operation // zero: nothing to wait for
}

// BeginBrowserRemoval checks the request, marks the browser 'removing' and queues browser.remove in
// the caller's transaction. The caller commits, waits (AwaitBrowserRemoval) and then calls
// FinishBrowserRemoval in a new transaction. Nothing of the app changes before the agent answered.
func (s *Service) BeginBrowserRemoval(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, b db.AppBrowser, confirm string) (BrowserRemoval, error) {
	rm := BrowserRemoval{Browser: b}
	if deploy.IsImported(a) {
		return rm, invalid("browsers belong to a site Hosbah deploys — " + a.Name + " is an imported app")
	}
	if confirm != b.Name {
		return rm, invalid("confirm must be the browser's name: " + b.Name)
	}
	if (b.Status == BrowserCreating || b.Status == BrowserRemoving) && time.Since(b.UpdatedAt) < browserBusyFor {
		return rm, conflict("the browser is still " + b.Status + " — try again when that has finished")
	}
	srv, err := browserServer(ctx, q, teamID, a)
	if err != nil {
		return rm, err
	}
	if !s.ops.Connected(srv.ID) && !ops.Online(srv.LastSeenAt, time.Now()) {
		return rm, conflict(srv.Name + " is offline — the browser can be removed once its agent is connected")
	}
	p := agentproto.BrowserRemoveParams{BrowserID: b.ID, AppID: a.ID}
	if err := p.Validate(); err != nil {
		return rm, invalid(err.Error())
	}
	if n, err := q.SetAppBrowserStatus(ctx, db.SetAppBrowserStatusParams{ID: b.ID, TeamID: teamID, Status: BrowserRemoving, From: b.Status}); err != nil || n == 0 {
		return rm, conflict("the browser changed meanwhile — try again")
	}
	sp := opsSpec(teamID, srv.ID, agentproto.KindBrowserRemove, mustJSON(p), actor)
	sp.QueueTTL = 2 * time.Minute // a removal nobody waits for any more must not run an hour later
	rm.Operation, _, err = s.ops.Enqueue(ctx, q, sp)
	return rm, err
}

// AwaitBrowserRemoval waits for the removal's operation. "" means it succeeded.
func (s *Service) AwaitBrowserRemoval(ctx context.Context, rm BrowserRemoval) (failure string) {
	if rm.Operation.ID == "" {
		return ""
	}
	wctx, cancel := context.WithTimeout(ctx, BrowserRemoveWait)
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

// FinishBrowserRemoval ends a removal in a new team transaction. failure "": exactly the variables
// the browser set are unset, the record goes, and it is audited. Otherwise the browser is as it was.
func (s *Service) FinishBrowserRemoval(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, rm BrowserRemoval, failure string) error {
	b := rm.Browser
	if failure != "" {
		back := b.Status
		if back != BrowserFailed {
			back = BrowserReady
		}
		_, err := q.SetAppBrowserStatus(ctx, db.SetAppBrowserStatusParams{ID: b.ID, TeamID: teamID, Status: back, Error: strptr(failure), From: BrowserRemoving})
		return err // the caller answers with BrowserRemovalRefused AFTER this transaction committed
	}
	if b.Kind == agentproto.BrowserKindDesktop {
		if p, err := q.ProxyHostOfBrowser(ctx, db.ProxyHostOfBrowserParams{BrowserID: &b.ID, TeamID: teamID}); err == nil {
			if err := s.deploy.DeleteDesktopBrowserProxyHost(ctx, q, teamID, actor, p); err != nil {
				return err
			}
		}
		if err := q.DeleteBrowserAuthTokensOfBrowser(ctx, db.DeleteBrowserAuthTokensOfBrowserParams{BrowserID: b.ID, TeamID: teamID}); err != nil {
			return err
		}
	} else {
		for _, n := range BrowserEnvNames(b.EnvPrefix) {
			if _, err := q.DeleteEnvVarInApp(ctx, db.DeleteEnvVarInAppParams{AppID: a.ID, TeamID: teamID, Name: n}); err != nil {
				return err
			}
		}
	}
	if _, err := q.DeleteAppBrowserInTeam(ctx, db.DeleteAppBrowserInTeamParams{ID: b.ID, TeamID: teamID}); err != nil {
		return err
	}
	extra := map[string]any{"browser": b.ID, "name": b.Name, "env_prefix": b.EnvPrefix}
	if rm.Operation.ID != "" {
		extra["operation"] = rm.Operation.ID
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "browser.removed", "app", a.ID, auditExtra(actor, extra))
}

// BrowserRemovalRefused is the 409 of a removal the agent did not carry out.
func BrowserRemovalRefused(failure string) error {
	return conflict("the browser was not removed: " + failure + " — try again")
}

// RefuseImportedBrowsers is the 422 of every browser write on an imported app.
func RefuseImportedBrowsers(a db.App) error {
	if deploy.IsImported(a) {
		return invalid("browsers belong to a site Hosbah deploys — " + a.Name + " is an imported app")
	}
	return nil
}
