package server

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/monitors"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// fakeClock is the evaluator's clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// routedTransport connects a hostname to the httptest listener registered for it, whatever the
// port — the evaluator's HTTP transport in this test (it stands in for DNS and for the SSRF dialer,
// which has its own tests).
type routedTransport struct {
	mu     sync.Mutex
	routes map[string]string
}

func (r *routedTransport) route(host string, srv *httptest.Server) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[host] = srv.Listener.Addr().String()
}

func (r *routedTransport) transport() http.RoundTripper {
	return &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // httptest's certificate
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(addr)
			r.mu.Lock()
			to := r.routes[host]
			r.mu.Unlock()
			if to == "" {
				return nil, fmt.Errorf("no route to %s in this test", host)
			}
			return (&net.Dialer{}).DialContext(ctx, network, to)
		}}
}

// site is a monitored target whose answer the test sets.
type site struct {
	mu     sync.Mutex
	status int
	delay  time.Duration
	hits   int
	agents []string
}

func (s *site) set(status int, delay time.Duration) {
	s.mu.Lock()
	s.status, s.delay = status, delay
	s.mu.Unlock()
}
func (s *site) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.hits }
func (s *site) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.hits++
	s.agents = append(s.agents, r.UserAgent())
	status, delay := s.status, s.delay
	s.mu.Unlock()
	time.Sleep(delay)
	if r.URL.Path == "/loop" {
		http.Redirect(w, r, "/loop", http.StatusFound)
		return
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, strings.Repeat("x", 1024))
}

type monitorJSON struct {
	ID                  string     `json:"id"`
	AppID               string     `json:"app_id"`
	Name                string     `json:"name"`
	Kind                string     `json:"kind"`
	Target              string     `json:"target"`
	ExpectedStatus      int        `json:"expected_status"`
	Threshold           float64    `json:"threshold"`
	WindowMinutes       int        `json:"window_minutes"`
	IntervalSeconds     int        `json:"interval_seconds"`
	Enabled             bool       `json:"enabled"`
	NotifyEmails        []string   `json:"notify_emails"`
	NotifyWebhook       string     `json:"notify_webhook"`
	State               string     `json:"state"`
	LastCheckedAt       *time.Time `json:"last_checked_at"`
	LastError           string     `json:"last_error"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	OpenIncident        *struct {
		ID       string    `json:"id"`
		OpenedAt time.Time `json:"opened_at"`
		Reason   string    `json:"reason"`
	} `json:"open_incident"`
}

type incidentJSON struct {
	ID      string `json:"id"`
	Monitor struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"monitor"`
	AppID      string     `json:"app_id"`
	OpenedAt   time.Time  `json:"opened_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	Reason     string     `json:"reason"`
	LastValue  string     `json:"last_value"`
	Notified   bool       `json:"notified"`
}

type incidentsJSON struct {
	Incidents  []incidentJSON `json:"incidents"`
	NextCursor *string        `json:"next_cursor"`
}

/*
"Alerts & monitors" end to end, with the evaluator on a fake clock and httptest targets: an http
monitor going ok → failing → incident on the second failure → resolved; the expected_status and
max-ms rules; the cpu | memory | disk rule over seeded server_metrics (no data = unknown); what is
refused on write (a target that is not the app's hostname, above all); the announcements (team
notification, email, webhook); roles, other teams, MCP, imported apps, and the cascade.
*/
func TestAppMonitors(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ctx := context.Background()

	ev := lastMonitors
	ev.Stop() // the test ticks
	clock := &fakeClock{t: time.Now().UTC().Truncate(time.Second)}
	ev.SetClock(clock.now)
	routes := &routedTransport{routes: map[string]string{}}
	ev.SetTransport(routes.transport())

	owner := newClient(t, ts.URL)
	owner.register("mon-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Mon " + suffix}, 201, &team)
	t.Cleanup(func() { // whatever is left must not be picked up by a later test's evaluator
		_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE monitors SET enabled = false WHERE team_id = $1`, team.ID)
			return err
		})
	})
	var readTok, writeTok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "mon-read", "scopes": []string{"read"}, "expires_in_days": 30}, 201, &readTok)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "mon-write", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &writeTok)

	viewerEmail := "mon-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	stranger := newClient(t, ts.URL)
	stranger.register("mon-stranger-" + suffix + "@example.test")
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, nil)

	srv, _ := enrolWithServicesKey(t, owner, ts.URL, team.ID, "mon-"+short, "203.0.113.20", []agentproto.Service{
		{Kind: "container", Name: "imp-mon-" + short, Image: "nginx:1.27", Status: "running", Ports: []string{"0.0.0.0:8081->80/tcp"}}})
	var app, sibling struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "mon-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "mon2-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &sibling)
	appBase := func(id string) string { return "/api/teams/" + team.ID + "/apps/" + id }
	base := appBase(app.App.ID)
	host := "up-" + suffix + ".example.test"
	siblingHost := "sibling-" + suffix + ".example.test"
	hookHost := "hooks-" + suffix + ".example.test"
	owner.mustJSON("POST", base+"/domains", map[string]any{"hostname": host, "tls": "off"}, 201, nil)
	owner.mustJSON("POST", appBase(sibling.App.ID)+"/domains", map[string]any{"hostname": siblingHost, "tls": "off"}, 201, nil)

	target := &site{status: 200}
	targetSrv := httptest.NewServer(http.HandlerFunc(target.serve))
	defer targetSrv.Close()
	routes.route(host, targetSrv)

	var hookMu sync.Mutex
	var hooks []map[string]any
	var hookAgents []string
	hookSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		hookMu.Lock()
		hooks = append(hooks, body)
		hookAgents = append(hookAgents, r.Method+" "+r.Header.Get("Content-Type")+" "+r.UserAgent())
		hookMu.Unlock()
	}))
	defer hookSrv.Close()
	routes.route(hookHost, hookSrv)
	hookCount := func() int { hookMu.Lock(); defer hookMu.Unlock(); return len(hooks) }

	alertEmail := "alerts-" + suffix + "@example.test"
	tick := func() {
		t.Helper()
		if _, err := ev.Tick(ctx); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	get := func(c *client, id string) monitorJSON {
		t.Helper()
		var m monitorJSON
		c.mustJSON("GET", base+"/monitors/"+id, nil, 200, &m)
		return m
	}
	incidents := func(c *client, query string) incidentsJSON {
		t.Helper()
		var out incidentsJSON
		c.mustJSON("GET", base+"/incidents"+query, nil, 200, &out)
		return out
	}
	notifications := func(kind string) int {
		t.Helper()
		var out struct {
			Notifications []struct {
				Kind        string  `json:"kind"`
				Title       string  `json:"title"`
				SubjectType *string `json:"subject_type"`
				SubjectID   *string `json:"subject_id"`
			} `json:"notifications"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/notifications", nil, 200, &out)
		n := 0
		for _, x := range out.Notifications {
			if x.Kind == kind && x.SubjectID != nil && *x.SubjectID == app.App.ID && x.SubjectType != nil && *x.SubjectType == "app" &&
				strings.Contains(x.Title, app.App.Name) {
				n++
			}
		}
		return n
	}

	t.Run("what a write refuses", func(t *testing.T) {
		httpMon := func(over map[string]any) map[string]any {
			b := map[string]any{"name": "refused", "kind": "http", "target": "https://" + host + "/"}
			for k, v := range over {
				b[k] = v
			}
			return b
		}
		eleven := []string{}
		for i := 0; i < 11; i++ {
			eleven = append(eleven, fmt.Sprintf("a%d@example.test", i))
		}
		for name, body := range map[string]map[string]any{
			"an arbitrary URL":            httpMon(map[string]any{"target": "https://example.com/"}),
			"the metadata service":        httpMon(map[string]any{"target": "http://169.254.169.254/latest/meta-data/"}),
			"loopback":                    httpMon(map[string]any{"target": "http://127.0.0.1:8710/api/health"}),
			"another app's hostname":      httpMon(map[string]any{"target": "https://" + siblingHost + "/"}),
			"a userinfo trick":            httpMon(map[string]any{"target": "https://" + host + "@example.com/"}),
			"a suffix trick":              httpMon(map[string]any{"target": "https://" + host + ".example.com/"}),
			"not http":                    httpMon(map[string]any{"target": "ftp://" + host + "/"}),
			"no target":                   {"name": "refused", "kind": "http"},
			"a status that is not one":    httpMon(map[string]any{"expected_status": 99}),
			"an interval under 30 s":      httpMon(map[string]any{"interval_seconds": 5}),
			"an http webhook":             httpMon(map[string]any{"notify_webhook": "http://" + hookHost + "/x"}),
			"a private webhook":           httpMon(map[string]any{"notify_webhook": "https://192.168.1.1/x"}),
			"an email that is not one":    httpMon(map[string]any{"notify_emails": []string{"nope"}}),
			"eleven emails":               httpMon(map[string]any{"notify_emails": eleven}),
			"a kind that does not exist":  {"name": "refused", "kind": "tcp", "threshold": 1},
			"cpu without a threshold":     {"name": "refused", "kind": "cpu"},
			"cpu over 100 percent":        {"name": "refused", "kind": "cpu", "threshold": 101},
			"cpu with a target":           {"name": "refused", "kind": "cpu", "threshold": 80, "target": "https://" + host + "/"},
			"memory with an hour and one": {"name": "refused", "kind": "memory", "threshold": 80, "window_minutes": 61},
		} {
			if status, text := owner.do("POST", base+"/monitors", body); status != 422 {
				t.Errorf("%s: %d (want 422): %s", name, status, text)
			}
		}
		var list struct{ Monitors []monitorJSON }
		owner.mustJSON("GET", base+"/monitors", nil, 200, &list)
		if list.Monitors == nil || len(list.Monitors) != 0 {
			t.Fatalf("nothing should have been created: %+v", list.Monitors)
		}
	})

	var up monitorJSON
	t.Run("create, roles and other teams", func(t *testing.T) {
		body := map[string]any{"name": "homepage", "kind": "http", "target": "http://" + host + ":8080/health", "notify_emails": []string{alertEmail},
			"notify_webhook": "https://" + hookHost + "/hook"}
		viewer.mustJSON("POST", base+"/monitors", body, 403, nil)
		stranger.mustJSON("POST", base+"/monitors", body, 404, nil)
		owner.mustJSON("POST", base+"/monitors", body, 201, &up)
		if up.ID == "" || up.AppID != app.App.ID || up.Kind != "http" || up.ExpectedStatus != 200 || up.IntervalSeconds != 60 || !up.Enabled ||
			up.State != "unknown" || up.LastCheckedAt != nil || up.OpenIncident != nil || len(up.NotifyEmails) != 1 || up.Threshold != 0 {
			t.Fatalf("created: %+v", up)
		}
		owner.mustJSON("POST", base+"/monitors", body, 409, nil) // the name is taken

		if m := get(viewer, up.ID); m.Name != "homepage" {
			t.Errorf("a viewer reads a monitor: %+v", m)
		}
		if m := get(viewer, "homepage"); m.ID != up.ID {
			t.Errorf("by name: %+v", m)
		}
		var list struct{ Monitors []monitorJSON }
		viewer.mustJSON("GET", base+"/monitors", nil, 200, &list)
		if len(list.Monitors) != 1 || list.Monitors[0].ID != up.ID {
			t.Errorf("list: %+v", list.Monitors)
		}
		viewer.mustJSON("PATCH", base+"/monitors/"+up.ID, map[string]any{"enabled": false}, 403, nil)
		viewer.mustJSON("DELETE", base+"/monitors/"+up.ID, nil, 403, nil)
		viewer.mustJSON("POST", base+"/monitors/"+up.ID+"/check", nil, 403, nil)
		viewer.mustJSON("GET", base+"/incidents", nil, 200, nil)
		for _, call := range [][2]string{{"GET", "/monitors"}, {"GET", "/monitors/" + up.ID}, {"PATCH", "/monitors/" + up.ID}, {"DELETE", "/monitors/" + up.ID},
			{"POST", "/monitors/" + up.ID + "/check"}, {"GET", "/incidents"}} {
			var b any
			if call[0] == "PATCH" {
				b = map[string]any{"enabled": false}
			}
			if status, text := stranger.do(call[0], base+call[1], b); status != 404 {
				t.Errorf("another team: %s %s -> %d: %s", call[0], call[1], status, text)
			}
		}
		// A monitor is addressed through ITS app only.
		owner.mustJSON("GET", appBase(sibling.App.ID)+"/monitors/"+up.ID, nil, 404, nil)
		// A PATCH is held to the same rule as a create.
		owner.mustJSON("PATCH", base+"/monitors/"+up.ID, map[string]any{"target": "https://example.com/"}, 422, nil)
		owner.mustJSON("PATCH", base+"/monitors/"+up.ID, map[string]any{"notify_webhook": "http://" + hookHost + "/x"}, 422, nil)
	})

	t.Run("ok, failing, an incident on the second failure, resolved", func(t *testing.T) {
		mailsBefore := mailbox.count(alertEmail)
		tick()
		m := get(owner, up.ID)
		if m.State != "ok" || m.ConsecutiveFailures != 0 || m.LastError != "" || m.OpenIncident != nil || m.LastCheckedAt == nil || !m.LastCheckedAt.Equal(clock.now()) {
			t.Fatalf("first check: %+v", m)
		}
		if target.count() != 1 || target.agents[0] != "Hosbah-Monitor" {
			t.Fatalf("the target saw %d requests, agents %v", target.count(), target.agents)
		}
		tick() // not due: nothing is fetched
		clock.advance(59 * time.Second)
		tick()
		if target.count() != 1 {
			t.Fatalf("a monitor that is not due was checked (%d requests)", target.count())
		}

		target.set(500, 0)
		clock.advance(2 * time.Second)
		tick()
		m = get(owner, up.ID)
		if m.State != "failing" || m.ConsecutiveFailures != 1 || m.OpenIncident != nil || !strings.Contains(m.LastError, "expected HTTP 200, got HTTP 500") {
			t.Fatalf("first failure: %+v", m)
		}
		if n := len(incidents(owner, "").Incidents); n != 0 || hookCount() != 0 || mailbox.count(alertEmail) != mailsBefore || notifications("monitor.opened") != 0 {
			t.Fatalf("one failure must not open an incident (%d incidents, %d webhooks)", n, hookCount())
		}

		clock.advance(61 * time.Second)
		tick()
		openedAt := clock.now()
		m = get(viewer, up.ID)
		if m.State != "failing" || m.ConsecutiveFailures != 2 || m.OpenIncident == nil || !m.OpenIncident.OpenedAt.Equal(openedAt) ||
			!strings.Contains(m.OpenIncident.Reason, "got HTTP 500") {
			t.Fatalf("second failure: %+v %+v", m, m.OpenIncident)
		}
		feed := incidents(viewer, "")
		if len(feed.Incidents) != 1 || feed.NextCursor != nil {
			t.Fatalf("incidents: %+v", feed)
		}
		if i := feed.Incidents[0]; i.ID != m.OpenIncident.ID || i.Monitor.ID != up.ID || i.Monitor.Name != "homepage" || i.Monitor.Kind != "http" ||
			i.AppID != app.App.ID || i.ResolvedAt != nil || !i.Notified || !strings.HasPrefix(i.LastValue, "HTTP 500 in ") {
			t.Fatalf("incident: %+v", i)
		}
		if n := mailbox.count(alertEmail) - mailsBefore; n != 1 {
			t.Fatalf("%d emails on opening (want 1)", n)
		}
		mailbox.mu.Lock()
		last := mailbox.msgs[len(mailbox.msgs)-1]
		mailbox.mu.Unlock()
		if last.To != alertEmail || !strings.Contains(last.Subject, "homepage is failing") || !strings.Contains(last.Text, "got HTTP 500") || !strings.Contains(last.Text, app.App.Name) {
			t.Errorf("email: %+v", last)
		}
		if notifications("monitor.opened") != 1 {
			t.Errorf("no team notification for the opening")
		}
		hookMu.Lock()
		if len(hooks) != 1 {
			t.Fatalf("webhooks: %v", hooks)
		}
		h, agent := hooks[0], hookAgents[0]
		hookMu.Unlock()
		mon, _ := h["monitor"].(map[string]any)
		ap, _ := h["app"].(map[string]any)
		at, _ := time.Parse(time.RFC3339, fmt.Sprint(h["at"]))
		if h["event"] != "opened" || mon["id"] != up.ID || mon["name"] != "homepage" || mon["kind"] != "http" || ap["id"] != app.App.ID || ap["name"] != app.App.Name ||
			!strings.Contains(fmt.Sprint(h["reason"]), "got HTTP 500") || !strings.HasPrefix(fmt.Sprint(h["value"]), "HTTP 500 in ") || !at.Equal(openedAt) || len(h) != 6 {
			t.Errorf("webhook payload: %v", h)
		}
		if agent != "POST application/json Hosbah-Monitor" {
			t.Errorf("webhook request: %q", agent)
		}

		// Still failing: the same incident, nothing announced again.
		clock.advance(61 * time.Second)
		tick()
		if m = get(owner, up.ID); m.ConsecutiveFailures != 3 || m.OpenIncident == nil || m.OpenIncident.ID != feed.Incidents[0].ID {
			t.Fatalf("third failure: %+v", m)
		}
		if len(incidents(owner, "").Incidents) != 1 || hookCount() != 1 || mailbox.count(alertEmail)-mailsBefore != 1 {
			t.Fatalf("a failing monitor announced itself twice")
		}

		// The first success resolves it.
		target.set(200, 0)
		clock.advance(61 * time.Second)
		tick()
		m = get(owner, up.ID)
		if m.State != "ok" || m.ConsecutiveFailures != 0 || m.OpenIncident != nil || m.LastError != "" {
			t.Fatalf("recovered: %+v", m)
		}
		feed = incidents(owner, "")
		if len(feed.Incidents) != 1 || feed.Incidents[0].ResolvedAt == nil || !feed.Incidents[0].ResolvedAt.Equal(clock.now()) ||
			!strings.HasPrefix(feed.Incidents[0].LastValue, "HTTP 200 in ") || !strings.Contains(feed.Incidents[0].Reason, "got HTTP 500") {
			t.Fatalf("resolved incident: %+v", feed.Incidents)
		}
		hookMu.Lock()
		if len(hooks) != 2 || hooks[1]["event"] != "resolved" {
			t.Errorf("webhooks after recovery: %v", hooks)
		}
		hookMu.Unlock()
		if mailbox.count(alertEmail)-mailsBefore != 2 || notifications("monitor.resolved") != 1 {
			t.Errorf("the recovery was not announced (%d emails)", mailbox.count(alertEmail)-mailsBefore)
		}
	})

	check := func(id string) monitorJSON {
		t.Helper()
		var m monitorJSON
		owner.mustJSON("POST", base+"/monitors/"+id+"/check", nil, 200, &m)
		return m
	}

	t.Run("expected_status, max ms, redirects, and a webhook that is down", func(t *testing.T) {
		var m monitorJSON
		owner.mustJSON("PATCH", base+"/monitors/"+up.ID, map[string]any{"expected_status": 0}, 200, &m)
		if m.ExpectedStatus != 0 || m.State != "unknown" || m.LastCheckedAt != nil {
			t.Fatalf("a changed rule forgets the state: %+v", m)
		}
		target.set(204, 0)
		if m = check(up.ID); m.State != "ok" {
			t.Fatalf("0 accepts any 2xx: %+v", m)
		}
		target.set(404, 0)
		if m = check(up.ID); m.State != "failing" || !strings.Contains(m.LastError, "expected a 2xx or 3xx answer, got HTTP 404") {
			t.Fatalf("0 refuses a 404: %+v", m)
		}
		owner.mustJSON("PATCH", base+"/monitors/"+up.ID, map[string]any{"expected_status": 404}, 200, nil)
		if m = check(up.ID); m.State != "ok" || m.ConsecutiveFailures != 0 {
			t.Fatalf("a monitor may expect a 404: %+v", m)
		}

		owner.mustJSON("PATCH", base+"/monitors/"+up.ID, map[string]any{"expected_status": 200, "threshold": 50}, 200, nil)
		target.set(200, 250*time.Millisecond)
		if m = check(up.ID); m.State != "failing" || !strings.Contains(m.LastError, "slower than 50 ms") {
			t.Fatalf("max ms: %+v", m)
		}
		target.set(200, 0)
		owner.mustJSON("PATCH", base+"/monitors/"+up.ID, map[string]any{"threshold": 5000}, 200, nil)
		if m = check(up.ID); m.State != "ok" {
			t.Fatalf("fast enough: %+v", m)
		}

		// A redirect loop is followed three times, then it is a failure.
		before := target.count()
		owner.mustJSON("PATCH", base+"/monitors/"+up.ID, map[string]any{"target": "http://" + host + "/loop", "expected_status": 0}, 200, nil)
		if m = check(up.ID); m.State != "failing" || !strings.Contains(m.LastError, "more than 3 redirects") || target.count()-before != 4 {
			t.Fatalf("redirect loop: %+v after %d requests", m, target.count()-before)
		}

		// A webhook that is down costs the announcement nothing else: the second failure still opens
		// the incident, mails, and is marked notified.
		hookSrv.CloseClientConnections()
		routes.mu.Lock()
		routes.routes[hookHost] = "127.0.0.1:1"
		routes.mu.Unlock()
		mails := mailbox.count(alertEmail)
		if m = check(up.ID); m.OpenIncident == nil || mailbox.count(alertEmail) != mails+1 {
			t.Fatalf("second failure with the webhook down: %+v", m)
		}
		if f := incidents(owner, "?limit=1"); len(f.Incidents) != 1 || !f.Incidents[0].Notified || f.Incidents[0].ResolvedAt != nil {
			t.Fatalf("incident with the webhook down: %+v", f.Incidents)
		}
		routes.route(hookHost, hookSrv)

		// Switching it off closes the incident quietly; a disabled monitor is neither ticked nor checked.
		hooksBefore := hookCount()
		owner.mustJSON("PATCH", base+"/monitors/"+up.ID, map[string]any{"enabled": false}, 200, &m)
		if m.Enabled || m.OpenIncident != nil || m.State != "unknown" {
			t.Fatalf("disabled: %+v", m)
		}
		owner.mustJSON("POST", base+"/monitors/"+up.ID+"/check", nil, 409, nil)
		before = target.count()
		clock.advance(time.Hour)
		tick()
		if target.count() != before || hookCount() != hooksBefore || mailbox.count(alertEmail) != mails+1 {
			t.Fatalf("a disabled monitor was checked or announced")
		}
	})

	t.Run("the private-address refusal", func(t *testing.T) {
		// The real transport, with a resolver that points the app's hostname at this machine.
		ev.SetTransport(monitors.SafeTransport(func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.0.1")}, nil }))
		defer ev.SetTransport(routes.transport())
		_, port, _ := net.SplitHostPort(targetSrv.Listener.Addr().String())
		owner.mustJSON("PATCH", base+"/monitors/"+up.ID, map[string]any{"enabled": true, "expected_status": 200,
			"target": "http://" + host + ":" + port + "/health"}, 200, nil)
		before := target.count()
		m := check(up.ID)
		if m.State != "failing" || !strings.Contains(m.LastError, "refused") || target.count() != before {
			t.Fatalf("a hostname that resolves to loopback must be refused, unfetched: %+v (%d requests)", m, target.count()-before)
		}
		owner.mustJSON("PATCH", base+"/monitors/"+up.ID, map[string]any{"enabled": false}, 200, nil)
	})

	t.Run("cpu, memory and disk over the server's metrics", func(t *testing.T) {
		var cpu, mem, disk monitorJSON
		owner.mustJSON("POST", base+"/monitors", map[string]any{"name": "cpu", "kind": "cpu", "threshold": 80, "window_minutes": 5, "notify_emails": []string{alertEmail}}, 201, &cpu)
		owner.mustJSON("POST", base+"/monitors", map[string]any{"name": "mem", "kind": "memory", "threshold": 60}, 201, &mem)
		owner.mustJSON("POST", base+"/monitors", map[string]any{"name": "disk", "kind": "disk", "threshold": 20, "window_minutes": 10}, 201, &disk)
		if cpu.Target != "" || cpu.WindowMinutes != 5 || mem.WindowMinutes != 5 || disk.WindowMinutes != 10 {
			t.Fatalf("created: %+v %+v %+v", cpu, mem, disk)
		}

		// No samples in the window: unknown — not failing, no incident.
		tick()
		for _, id := range []string{cpu.ID, mem.ID, disk.ID} {
			if m := get(owner, id); m.State != "unknown" || m.OpenIncident != nil || m.ConsecutiveFailures != 0 || !strings.Contains(m.LastError, "no metrics") {
				t.Fatalf("no data: %+v", m)
			}
		}

		// Five minutes at 95% CPU; memory at 50%, disk at 25% (seedMinutes).
		mails := mailbox.count(alertEmail)
		seedMinutes(t, sqlDB, srv, team.ID, clock.now().Truncate(time.Minute), 5, 95, false)
		clock.advance(61 * time.Second)
		tick()
		c := get(owner, cpu.ID)
		if c.State != "failing" || c.OpenIncident == nil || c.ConsecutiveFailures != 1 || !strings.Contains(c.OpenIncident.Reason, "cpu averaged 95.0% over 5 minutes, above 80%") {
			t.Fatalf("cpu over its threshold opens an incident at once: %+v %+v", c, c.OpenIncident)
		}
		if m := get(owner, mem.ID); m.State != "ok" || m.OpenIncident != nil {
			t.Fatalf("memory at 50%% of a 60%% threshold: %+v", m)
		}
		if m := get(owner, disk.ID); m.State != "failing" || m.OpenIncident == nil || !strings.Contains(m.OpenIncident.Reason, "disk averaged 25.0%") {
			t.Fatalf("disk at 25%% of a 20%% threshold: %+v", m)
		}
		if mailbox.count(alertEmail) != mails+1 {
			t.Errorf("the cpu incident was not mailed")
		}

		// Ten minutes later the window holds calm minutes only.
		clock.advance(10 * time.Minute)
		seedMinutes(t, sqlDB, srv, team.ID, clock.now().Truncate(time.Minute), 5, 10, false)
		tick()
		if m := get(owner, cpu.ID); m.State != "ok" || m.OpenIncident != nil {
			t.Fatalf("cpu recovered: %+v", m)
		}
		// …and an hour later none at all: unknown again, and the disk incident stays as it is.
		clock.advance(time.Hour)
		tick()
		if m := get(owner, disk.ID); m.State != "unknown" || m.OpenIncident == nil {
			t.Fatalf("no data leaves an open incident alone: %+v", m)
		}
	})

	t.Run("the incident feed pages by cursor", func(t *testing.T) {
		all := incidents(viewer, "")
		if len(all.Incidents) < 4 || all.NextCursor != nil {
			t.Fatalf("feed: %+v", all)
		}
		for i := 1; i < len(all.Incidents); i++ {
			if all.Incidents[i].OpenedAt.After(all.Incidents[i-1].OpenedAt) {
				t.Fatalf("not newest first: %+v", all.Incidents)
			}
		}
		var seen []string
		query := "?limit=1"
		for page := 0; page < 10; page++ {
			f := incidents(viewer, query)
			for _, i := range f.Incidents {
				seen = append(seen, i.ID)
			}
			if f.NextCursor == nil {
				break
			}
			query = "?limit=1&before=" + *f.NextCursor
		}
		if len(seen) != len(all.Incidents) {
			t.Fatalf("paged %d of %d incidents", len(seen), len(all.Incidents))
		}
		for i, id := range seen {
			if id != all.Incidents[i].ID {
				t.Fatalf("page order: %v", seen)
			}
		}
		viewer.mustJSON("GET", base+"/incidents?before=nonsense", nil, 422, nil)
	})

	t.Run("MCP", func(t *testing.T) {
		res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "list_app_monitors", map[string]any{"app": app.App.Name})
		if res.IsError || !strings.Contains(res.Text, "homepage") || !strings.Contains(res.Text, "disk") || !strings.Contains(res.Text, "INCIDENT since") {
			t.Fatalf("list_app_monitors: %+v", res)
		}
		if res = mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "list_app_incidents", map[string]any{"app": app.App.ID, "limit": 2}); res.IsError ||
			!strings.Contains(res.Text, "next page: before=") || !strings.Contains(res.Text, "OPEN") {
			t.Fatalf("list_app_incidents: %+v", res)
		}
		create := map[string]any{"app": app.App.ID, "name": "via-mcp", "kind": "http", "target": "https://" + host + "/", "expected_status": 0,
			"interval_seconds": 300, "notify_emails": []string{alertEmail}, "enabled": false}
		if res = mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "create_app_monitor", create); !res.IsError {
			t.Fatalf("a read token created a monitor: %+v", res)
		}
		bad := map[string]any{"app": app.App.ID, "name": "via-mcp", "kind": "http", "target": "http://169.254.169.254/"}
		if res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "create_app_monitor", bad); !res.IsError || !strings.Contains(res.Text, "own hostnames") {
			t.Fatalf("MCP must hold the target to the app's hostnames: %+v", res)
		}
		if res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "create_app_monitor", create); res.IsError || !strings.Contains(res.Text, "via-mcp") {
			t.Fatalf("create_app_monitor: %+v", res)
		}
		m := get(owner, "via-mcp")
		if m.Enabled || m.ExpectedStatus != 0 || m.IntervalSeconds != 300 || len(m.NotifyEmails) != 1 {
			t.Fatalf("created through MCP: %+v", m)
		}
		if res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "update_app_monitor", map[string]any{"app": app.App.ID, "monitor": "via-mcp",
			"name": "renamed", "threshold": 1500, "notify_emails": []string{}}); res.IsError || !strings.Contains(res.Text, "renamed") {
			t.Fatalf("update_app_monitor: %+v", res)
		}
		if m = get(owner, m.ID); m.Name != "renamed" || m.Threshold != 1500 || len(m.NotifyEmails) != 0 || m.IntervalSeconds != 300 {
			t.Fatalf("updated through MCP: %+v", m)
		}
		if res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "delete_app_monitor", map[string]any{"app": app.App.ID, "monitor": "renamed"}); res.IsError {
			t.Fatalf("delete_app_monitor: %+v", res)
		}
		owner.mustJSON("GET", base+"/monitors/"+m.ID, nil, 404, nil)

		var audit struct {
			Entries []struct {
				Action string `json:"action"`
			} `json:"entries"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/audit?limit=200", nil, 200, &audit)
		got := map[string]int{}
		for _, e := range audit.Entries {
			got[e.Action]++
		}
		if got["monitor.created"] < 5 || got["monitor.updated"] < 5 || got["monitor.deleted"] != 1 {
			t.Errorf("audit: %v", got)
		}
	})

	t.Run("an imported app has monitors too", func(t *testing.T) {
		apps, _ := listImported(t, owner, team.ID, srv)
		imp := apps["imp-mon-"+short]
		if imp.ID == "" {
			t.Fatalf("imports: %v", keys(apps))
		}
		impHost := "imported-" + suffix + ".example.test"
		owner.mustJSON("POST", appBase(imp.ID)+"/domains", map[string]any{"hostname": impHost, "tls": "off"}, 201, nil)
		routes.route(impHost, targetSrv)
		target.set(200, 0)
		var m monitorJSON
		owner.mustJSON("POST", appBase(imp.ID)+"/monitors", map[string]any{"name": "imported", "kind": "http", "target": "https://" + host + "/"}, 422, nil)
		owner.mustJSON("POST", appBase(imp.ID)+"/monitors", map[string]any{"name": "imported", "kind": "http", "target": "http://" + impHost + "/"}, 201, &m)
		owner.mustJSON("POST", appBase(imp.ID)+"/monitors/"+m.ID+"/check", nil, 200, &m)
		if m.State != "ok" {
			t.Fatalf("imported app's monitor: %+v", m)
		}
		owner.mustJSON("POST", appBase(imp.ID)+"/monitors", map[string]any{"name": "imported-cpu", "kind": "cpu", "threshold": 90}, 201, nil)
		owner.mustJSON("DELETE", appBase(imp.ID)+"/monitors/"+m.ID, nil, 204, nil)
	})

	t.Run("deleting the app takes its monitors and incidents", func(t *testing.T) {
		count := func() (monitors, incidents int) {
			_ = tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
				_ = tx.QueryRow(`SELECT count(*) FROM monitors WHERE app_id = $1`, app.App.ID).Scan(&monitors)
				return tx.QueryRow(`SELECT count(*) FROM monitor_incidents WHERE app_id = $1`, app.App.ID).Scan(&incidents)
			})
			return
		}
		if m, i := count(); m != 4 || i < 4 {
			t.Fatalf("before: %d monitors, %d incidents", m, i)
		}
		owner.mustJSON("DELETE", base, map[string]any{"confirm": app.App.Name}, 204, nil)
		if m, i := count(); m != 0 || i != 0 {
			t.Fatalf("after deleting the app: %d monitors, %d incidents", m, i)
		}
	})
}
