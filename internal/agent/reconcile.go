package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
Port drift (MH-1331). A release container publishes its port at 127.0.0.1::<port> — a host port
the kernel picks, so an old and a new release run side by side (publishArg). Docker picks again
whenever the container starts: after a host or LXC reboot, a dockerd restart or a plain
`docker restart`, the same containers come back (--restart unless-stopped) on NEW host ports, and
every Caddy route still dials the old ones: 502 on every site of the server until the next deploy.

Pinning a port per app was the other option. It is not taken: a deploy runs the old and the new
release at once, so a pinned port would need two slots per process and a free-port allocator the
agent must keep in step with whatever else binds loopback ports, and it would still not cover a
container restarted by hand or a release started by an older agent. Reconciling against what
Docker actually publishes covers every cause and needs nothing from a redeploy:

  - reconcile reads the running containers once (docker ps), then re-points every app upstream
    (web, and the realtime worker) whose port no container of THAT app publishes at the app's
    newest running container of the same role. Ports are reused across apps after a reboot (they
    all start again from 32768), so "some container answers there" is not enough.
  - proxy hosts dial arbitrary upstreams, so the container behind a loopback dial is learned while
    it answers (proxyEntry.Container/ContainerPort) and followed when its port moves.
  - it runs when the agent starts (retrying until Docker and Caddy answer), on every container
    start Docker reports (debounced), and from the health loop below; route.set gets the same
    correction (correctUpstream), because the control plane's releases.host_port is just as stale.
  - every change is reported to the control plane (TypeRoutes), which moves the live release's
    host_port; a session also starts with a snapshot, so a report lost to a disconnect converges.

The health loop dials every loopback upstream every healthEvery. One that refuses connections for
healthGrace is reconciled again (and then once per healthGrace while it stays down), and a WARN
goes to the control plane once per outage.
*/

// Variables so tests can shorten them.
var (
	healthEvery       = 15 * time.Second
	healthGrace       = 60 * time.Second
	reconcileDebounce = 2 * time.Second
	reconcileRetry    = 5 * time.Second
)

// runningContainer is one running container as reconcile needs it.
type runningContainer struct {
	Name    string
	AppID   string // hosbah.app (or cloudy.app)
	Process string // hosbah.process (or cloudy.process)
	Created time.Time
	// Ports maps a container port to the 127.0.0.1 host port it is published at (tcp only).
	Ports map[int]int
}

// hostPorts is the container's loopback host ports, ascending.
func (c runningContainer) hostPorts() []int {
	out := make([]int, 0, len(c.Ports))
	for _, h := range c.Ports {
		out = append(out, h)
	}
	sort.Ints(out)
	return out
}

func (c runningContainer) publishes(hostPort int) bool {
	for _, h := range c.Ports {
		if h == hostPort {
			return true
		}
	}
	return false
}

// listRunning reads every running container: names, the app/process labels (either generation)
// and the loopback ports each publishes.
func listRunning(ctx context.Context, run runFunc) ([]runningContainer, error) {
	out, err := run(ctx, nil, "docker", "ps", "--no-trunc", "--format", "{{json .}}")
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	return parseRunning(string(out)), nil
}

// parseRunning reads `docker ps --format '{{json .}}'` lines; a line it cannot read is skipped.
func parseRunning(out string) []runningContainer {
	var cs []runningContainer
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row struct {
			Names, Labels, Ports, CreatedAt string
		}
		if json.Unmarshal([]byte(line), &row) != nil || row.Names == "" {
			continue
		}
		labels := parseLabels(row.Labels)
		c := runningContainer{Name: strings.Split(row.Names, ",")[0], Ports: parsePorts(row.Ports)}
		for _, l := range labelSets {
			if c.AppID == "" {
				c.AppID = labels[l(agentproto.LabelApp)]
			}
			if c.Process == "" {
				c.Process = labels[l(agentproto.LabelProcess)]
			}
		}
		// "2026-10-18 16:50:01 +0000 UTC"; an unreadable date sorts oldest.
		if t, err := time.Parse("2006-01-02 15:04:05 -0700 MST", row.CreatedAt); err == nil {
			c.Created = t
		}
		cs = append(cs, c)
	}
	return cs
}

func parseLabels(s string) map[string]string {
	m := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[strings.TrimSpace(k)] = v
		}
	}
	return m
}

var loopbackPortRE = regexp.MustCompile(`^127\.0\.0\.1:([0-9]{1,5})->([0-9]{1,5})/tcp$`)

// parsePorts reads docker ps' Ports column ("127.0.0.1:32768->3000/tcp, 50051/tcp"); only single
// tcp ports published on 127.0.0.1 count.
func parsePorts(s string) map[int]int {
	m := map[int]int{}
	for _, p := range strings.Split(s, ",") {
		if sm := loopbackPortRE.FindStringSubmatch(strings.TrimSpace(p)); sm != nil {
			h, _ := strconv.Atoi(sm[1])
			c, _ := strconv.Atoi(sm[2])
			m[c] = h
		}
	}
	return m
}

// loopbackPort is <port> of "127.0.0.1:<port>", or 0.
func loopbackPort(upstream string) int {
	rest, ok := strings.CutPrefix(upstream, "127.0.0.1:")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 || n > 65535 {
		return 0
	}
	return n
}

func loopback(port int) string { return "127.0.0.1:" + strconv.Itoa(port) }

// appIndex groups the running containers by app.
type appIndex struct {
	byApp  map[string][]runningContainer
	byName map[string]runningContainer
	all    []runningContainer
}

func indexRunning(cs []runningContainer) appIndex {
	ix := appIndex{byApp: map[string][]runningContainer{}, byName: map[string]runningContainer{}, all: cs}
	for _, c := range cs {
		ix.byName[c.Name] = c
		if c.AppID != "" {
			ix.byApp[c.AppID] = append(ix.byApp[c.AppID], c)
		}
	}
	for id := range ix.byApp { // newest first
		sort.SliceStable(ix.byApp[id], func(i, j int) bool { return ix.byApp[id][i].Created.After(ix.byApp[id][j].Created) })
	}
	return ix
}

// owner is the container of app (web, or a worker when realtime) that publishes hostPort.
func (ix appIndex) owner(app string, realtime bool, hostPort int) (runningContainer, bool) {
	for _, c := range ix.byApp[app] {
		if (c.Process == "web") != realtime && c.publishes(hostPort) {
			return c, true
		}
	}
	return runningContainer{}, false
}

// replacement is the app's newest running container of the role that publishes exactly one
// loopback port: web for the app's upstream, a worker for its realtime route (only the realtime
// worker publishes a port).
func (ix appIndex) replacement(app string, realtime bool) (runningContainer, int, bool) {
	for _, c := range ix.byApp[app] {
		if (c.Process == "web") == realtime {
			continue
		}
		if hp := c.hostPorts(); len(hp) == 1 {
			return c, hp[0], true
		}
	}
	return runningContainer{}, 0, false
}

// correct is the upstream an app's route should dial: unchanged while a container of the app
// publishes it, else its replacement (ok=false when there is none: left as it is).
func (ix appIndex) correct(app string, realtime bool, upstream string) (to string, ctr string, changed bool) {
	p := loopbackPort(upstream)
	if p == 0 {
		return upstream, "", false
	}
	if c, ok := ix.owner(app, realtime, p); ok {
		return upstream, c.Name, false
	}
	if c, hp, ok := ix.replacement(app, realtime); ok {
		return loopback(hp), c.Name, true
	}
	return upstream, "", false
}

// reconcileTable re-points t at the ports its containers publish now. reload reports a change
// Caddy must load; learned, a proxy binding learned (only routes.json changes).
func reconcileTable(t *routeTable, ix appIndex) (reports []agentproto.RouteReport, reload, learned bool) {
	ids := make([]string, 0, len(t.Apps))
	for id := range t.Apps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := t.Apps[id]
		r := agentproto.RouteReport{AppID: id, Level: agentproto.RouteReportInfo}
		var msgs []string
		if to, ctr, ok := ix.correct(id, false, e.Upstream); ok {
			r.Container, r.Previous, r.Upstream = ctr, e.Upstream, to
			msgs = append(msgs, fmt.Sprintf("%s moved to %s (%s)", e.Upstream, to, ctr))
			e.Upstream = to
		}
		if e.Realtime != nil {
			rt := *e.Realtime
			if to, ctr, ok := ix.correct(id, true, rt.Upstream); ok {
				r.RealtimeContainer, r.RealtimePrevious, r.RealtimeUpstream = ctr, rt.Upstream, to
				msgs = append(msgs, fmt.Sprintf("realtime %s moved to %s (%s)", rt.Upstream, to, ctr))
				rt.Upstream = to
				e.Realtime = &rt
			}
		}
		if len(msgs) > 0 {
			r.Message = "a restart moved the published port: " + strings.Join(msgs, "; ")
			t.Apps[id] = e
			reports = append(reports, r)
			reload = true
		}
	}
	pids := make([]string, 0, len(t.Proxies))
	for id := range t.Proxies {
		pids = append(pids, id)
	}
	sort.Strings(pids)
	for _, id := range pids {
		p := t.Proxies[id]
		port := loopbackPort(p.Dial)
		if port == 0 {
			continue
		}
		if p.Container == "" { // learn who answers there, while it does
			for _, c := range ix.all {
				for cp, hp := range c.Ports {
					if hp == port {
						p.Container, p.ContainerPort = c.Name, cp
						learned = true
					}
				}
			}
			t.Proxies[id] = p
			continue
		}
		c, ok := ix.byName[p.Container]
		if !ok {
			continue // not running: nothing to follow (the health loop warns)
		}
		hp, ok := c.Ports[p.ContainerPort]
		if !ok || hp == port {
			continue
		}
		reports = append(reports, agentproto.RouteReport{ProxyID: id, Container: c.Name, Previous: p.Dial, Upstream: loopback(hp),
			Level: agentproto.RouteReportInfo, Message: fmt.Sprintf("a restart moved the published port of %s (%s): %s moved to %s", p.Host.Hostname, c.Name, p.Dial, loopback(hp))})
		if p.Requested == "" {
			p.Requested = p.Dial
		}
		p.Dial = loopback(hp)
		t.Proxies[id] = p
		reload = true
	}
	return reports, reload, learned
}

// routeSnapshot is the table as reports, for the control plane to converge on at the start of a
// session: every app's upstream with the container behind it (none when nothing answers there).
func routeSnapshot(t routeTable, ix appIndex) []agentproto.RouteReport {
	var out []agentproto.RouteReport
	for id, e := range t.Apps {
		c, ok := ix.owner(id, false, loopbackPort(e.Upstream))
		if !ok {
			continue
		}
		r := agentproto.RouteReport{AppID: id, Container: c.Name, Upstream: e.Upstream, Level: agentproto.RouteReportInfo}
		if e.Realtime != nil {
			if rc, ok := ix.owner(id, true, loopbackPort(e.Realtime.Upstream)); ok {
				r.RealtimeContainer, r.RealtimeUpstream = rc.Name, e.Realtime.Upstream
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppID < out[j].AppID })
	return out
}

// reconcileRoutes is one pass: read Docker, fix the table, and (only when a route moved) load the
// result into Caddy before persisting it, as applyRoutes does.
func reconcileRoutes(oc *OpContext, run runFunc) ([]agentproto.RouteReport, error) {
	cs, err := listRunning(oc.Ctx, run)
	if err != nil {
		return nil, err
	}
	ix := indexRunning(cs)
	routesMu.Lock()
	defer routesMu.Unlock()
	t, err := loadRoutes()
	if err != nil {
		return nil, err
	}
	reports, reload, learned := reconcileTable(&t, ix)
	if reload {
		t.Edge = wantEdge(oc)
		cfg := renderCaddy(t, wantAccessLogging(oc))
		if err := caddyLoad(oc.Ctx, cfg); err != nil {
			return nil, err
		}
		if err := saveRoutes(t); err != nil {
			return nil, err
		}
		return reports, writeCaddyFile(cfg)
	}
	if learned {
		return reports, saveRoutes(t)
	}
	return reports, nil
}

// correctUpstream fixes a route.set whose upstream no container of the app publishes (the control
// plane's host_port is from before a restart) the same way reconcile does. A listing that fails
// leaves the params as they are.
func correctUpstream(oc *OpContext, p *agentproto.RouteParams) *agentproto.RouteReport {
	if p.Upstream == "" {
		return nil
	}
	cs, err := listRunning(oc.Ctx, appDocker)
	if err != nil {
		return nil
	}
	ix := indexRunning(cs)
	r := agentproto.RouteReport{AppID: p.AppID, Level: agentproto.RouteReportInfo}
	changed := false
	if to, ctr, ok := ix.correct(p.AppID, false, p.Upstream); ok {
		oc.Info("%s is not published by any running container of this app (a restart moved it): routing to %s (%s) instead", p.Upstream, to, ctr)
		r.Container, r.Previous, r.Upstream = ctr, p.Upstream, to
		p.Upstream, changed = to, true
	}
	if p.Realtime != nil {
		rt := *p.Realtime
		if to, ctr, ok := ix.correct(p.AppID, true, rt.Upstream); ok {
			oc.Info("realtime %s is not published by any running container of this app: routing to %s (%s) instead", rt.Upstream, to, ctr)
			r.RealtimeContainer, r.RealtimePrevious, r.RealtimeUpstream = ctr, rt.Upstream, to
			rt.Upstream = to
			p.Realtime, changed = &rt, true
		}
	}
	if !changed {
		return nil
	}
	r.Message = "route.set named a port from before a restart; routed to the port the container publishes now"
	return &r
}

// reportRoutes hands reports to the control plane connection (set by the Agent; nil in tests and
// when no agent runs).
var reportRoutes func([]agentproto.RouteReport)

func sendRouteReports(rs []agentproto.RouteReport) {
	if f := reportRoutes; f != nil && len(rs) > 0 {
		f(rs)
	}
}

// backgroundOp is an OpContext for work no operation asked for: its lines go to the agent's log.
func backgroundOp(ctx context.Context, log *slog.Logger) *OpContext {
	return &OpContext{Ctx: ctx,
		log:      func(_, line string) { log.Info(line) },
		progress: func(int, int) {},
		notify:   func(line string, _ json.RawMessage) { log.Info(line) },
	}
}

// reconciler runs reconcileRoutes when the agent starts, when Docker starts a container, and when
// the health loop finds an upstream down.
type reconciler struct {
	run     runFunc
	log     *slog.Logger
	report  func([]agentproto.RouteReport)
	dial    func(ctx context.Context, addr string) error
	events  func(ctx context.Context, onStart func()) error // blocks while Docker's event stream is up
	now     func() time.Time
	trigger chan struct{}

	mu     sync.Mutex
	down   map[string]time.Time // health key -> first refusal
	warned map[string]bool
	tried  map[string]time.Time // health key -> last re-reconcile
	// learnTried: proxy dials a learn pass already ran for.
	learnTried map[string]bool
}

func newReconciler(log *slog.Logger, report func([]agentproto.RouteReport)) *reconciler {
	return &reconciler{run: appDocker, log: log, report: report, dial: dialTCP, events: dockerStartEvents, now: time.Now,
		trigger: make(chan struct{}, 1), down: map[string]time.Time{}, warned: map[string]bool{}, tried: map[string]time.Time{},
		learnTried: map[string]bool{}}
}

func dialTCP(ctx context.Context, addr string) error {
	c, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

// kick asks for a pass (coalesced).
func (r *reconciler) kick() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

// Run blocks until ctx ends.
func (r *reconciler) Run(ctx context.Context) {
	go r.watchDocker(ctx)
	go r.healthLoop(ctx)
	r.kick() // the agent just started: the host may have rebooted under its routes
	var retry <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.trigger:
		case <-retry:
		}
		retry = nil
		// Containers come up one after another; let a burst of starts settle into one pass.
		if !sleepCtx(ctx, reconcileDebounce) {
			return
		}
		select {
		case <-r.trigger:
		default:
		}
		if err := r.pass(ctx); err != nil {
			r.log.Warn("route reconcile failed; retrying", "err", err, "retry_in", reconcileRetry)
			retry = time.After(reconcileRetry)
		}
	}
}

func (r *reconciler) pass(ctx context.Context) error {
	reports, err := reconcileRoutes(backgroundOp(ctx, r.log), r.run)
	if err != nil {
		return err
	}
	for _, rep := range reports {
		r.log.Warn("route reconciled", "app", rep.AppID, "proxy", rep.ProxyID, "msg", rep.Message)
	}
	if len(reports) > 0 && r.report != nil {
		r.report(reports)
	}
	return nil
}

// watchDocker re-reconciles on every container start, and whenever the event stream (re)connects:
// dockerd restarting restarts its containers too.
func (r *reconciler) watchDocker(ctx context.Context) {
	backoff := reconcileRetry
	for ctx.Err() == nil {
		started := r.now()
		err := r.events(ctx, r.kick)
		if ctx.Err() != nil {
			return
		}
		if r.now().Sub(started) > time.Minute {
			backoff = reconcileRetry
		}
		r.log.Warn("docker event stream ended; reconnecting", "err", err, "retry_in", backoff)
		if !sleepCtx(ctx, backoff) {
			return
		}
		if backoff *= 2; backoff > time.Minute {
			backoff = time.Minute
		}
		r.kick()
	}
}

// dockerStartEvents follows `docker events` for container starts until it ends.
func dockerStartEvents(ctx context.Context, onStart func()) error {
	// No --format: the default output is one line per event, and the fields a template may name
	// differ between Docker versions (Docker 29 has no .ID: the stream would end at once).
	cmd := exec.CommandContext(ctx, "docker", "events", "--filter", "type=container", "--filter", "event=start") //#nosec G204 -- constant args
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		onStart()
	}
	if err := errors.Join(sc.Err(), cmd.Wait()); err != nil {
		return fmt.Errorf("docker events: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// healthTarget is one loopback upstream the health loop dials.
type healthTarget struct {
	key, what, addr string
	app, proxy      string
}

func healthTargets(t routeTable) []healthTarget {
	var out []healthTarget
	for id, e := range t.Apps {
		if loopbackPort(e.Upstream) != 0 {
			out = append(out, healthTarget{key: "app:" + id + "@" + e.Upstream, what: "upstream", addr: e.Upstream, app: id})
		}
		if e.Realtime != nil && loopbackPort(e.Realtime.Upstream) != 0 {
			out = append(out, healthTarget{key: "rt:" + id + "@" + e.Realtime.Upstream, what: "realtime upstream", addr: e.Realtime.Upstream, app: id})
		}
	}
	for id, p := range t.Proxies {
		if loopbackPort(p.Dial) != 0 {
			out = append(out, healthTarget{key: "proxy:" + id + "@" + p.Dial, what: "proxy upstream of " + p.Host.Hostname, addr: p.Dial, proxy: id})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

func (r *reconciler) healthLoop(ctx context.Context) {
	tk := time.NewTicker(healthEvery)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			r.healthCheck(ctx)
		}
	}
}

// healthCheck dials every loopback upstream once. A target down for healthGrace is reconciled
// (again every healthGrace while it stays down) and WARNed about once per outage.
func (r *reconciler) healthCheck(ctx context.Context) {
	routesMu.Lock()
	t, err := loadRoutes()
	routesMu.Unlock()
	if err != nil {
		return
	}
	targets := healthTargets(t)
	now := r.now()
	var due []healthTarget
	live := map[string]bool{}
	learn := false
	for _, tg := range targets {
		live[tg.key] = true
		if r.dial(ctx, tg.addr) == nil {
			// A proxy host answering for a container not learned yet: learn it now, before a restart
			// moves the port (and another container may take the old one).
			// Once per dial: a host service (no container behind it) stays unlearned.
			if tg.proxy != "" && t.Proxies[tg.proxy].Container == "" {
				r.mu.Lock()
				if !r.learnTried[tg.key] {
					r.learnTried[tg.key], learn = true, true
				}
				r.mu.Unlock()
			}
			r.mu.Lock()
			delete(r.down, tg.key)
			delete(r.warned, tg.key)
			delete(r.tried, tg.key)
			r.mu.Unlock()
			continue
		}
		r.mu.Lock()
		first, ok := r.down[tg.key]
		if !ok {
			r.down[tg.key] = now
			first = now
		}
		if now.Sub(first) >= healthGrace && now.Sub(r.tried[tg.key]) >= healthGrace {
			r.tried[tg.key] = now
			due = append(due, tg)
		}
		r.mu.Unlock()
	}
	r.mu.Lock() // forget targets that left the table (a deploy moved the route)
	for k := range r.down {
		if !live[k] {
			delete(r.down, k)
			delete(r.warned, k)
			delete(r.tried, k)
		}
	}
	r.mu.Unlock()
	if len(due) == 0 {
		if learn {
			if _, err := reconcileRoutes(backgroundOp(ctx, r.log), r.run); err != nil {
				r.log.Debug("learning proxy containers failed", "err", err)
			}
		}
		return
	}
	reports, err := reconcileRoutes(backgroundOp(ctx, r.log), r.run)
	if err != nil {
		r.log.Warn("route reconcile after a failed health check did not run", "err", err)
	}
	moved := map[string]agentproto.RouteReport{}
	for _, rep := range reports {
		moved[rep.AppID+"|"+rep.ProxyID] = rep
	}
	var out []agentproto.RouteReport
	for _, tg := range due {
		r.mu.Lock()
		warned := r.warned[tg.key]
		r.warned[tg.key] = true
		r.mu.Unlock()
		rep, ok := moved[tg.app+"|"+tg.proxy]
		if ok {
			delete(moved, tg.app+"|"+tg.proxy)
			rep.Level = agentproto.RouteReportWarn
			rep.Message = fmt.Sprintf("%s %s refused connections for %s; %s", tg.what, tg.addr, healthGrace, rep.Message)
			out = append(out, rep)
			r.log.Warn(rep.Message, "app", tg.app, "proxy", tg.proxy)
			continue
		}
		if warned {
			continue
		}
		msg := fmt.Sprintf("%s %s refused connections for %s and no running container publishes a replacement port — is the app stopped?", tg.what, tg.addr, healthGrace)
		r.log.Warn(msg, "app", tg.app, "proxy", tg.proxy)
		out = append(out, agentproto.RouteReport{AppID: tg.app, ProxyID: tg.proxy, Previous: tg.addr, Level: agentproto.RouteReportWarn, Message: msg})
	}
	for _, rep := range moved { // fixed in the same pass without being due yet
		out = append(out, rep)
	}
	if len(out) > 0 && r.report != nil {
		r.report(out)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
