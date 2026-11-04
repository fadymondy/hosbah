package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	circlexo "github.com/circlexo/circlexo-go"
	"github.com/circlexo/circlexo-go/circlexotest"
	"github.com/circlexo/circlexo-go/oidc"
	"github.com/circlexo/circlexo-go/webhooks"

	hub "github.com/fadymondy/hosbah/internal/circlexo"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/quota"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// The CircleXO integration against circlexotest, the SDK's fake hub. The secrets below are
// throwaway values made up for this file: they are not, and never were, a real credential.
const (
	testHubApp        = "app-hosbah-test"
	testWebhookSecret = "whsec_" + "aG9zYmFoLXRlc3Qtd2ViaG9vay1zZWNyZXQtMDEyMzQ1Ng=="
	otherWebhook      = "whsec_" + "c29tZS1vdGhlci1zZWNyZXQtbm90LXRoZS1yZWFsLW9uZQ=="
)

type hubStack struct {
	t    *testing.T
	hub  *circlexotest.Hub
	base string
	sql  *sql.DB
	svc  *hub.Service
}

// bootHub starts the fake hub and a Hosbah stack configured for it.
func bootHub(t *testing.T) *hubStack {
	t.Helper()
	h := circlexotest.New(t, testHubApp)
	t.Setenv("CIRCLEXO_ISSUER", h.URL)
	t.Setenv("CIRCLEXO_APP_ID", testHubApp)
	t.Setenv("CIRCLEXO_CLIENT_ID", h.ClientID)
	t.Setenv("CIRCLEXO_CLIENT_SECRET", h.ClientSecret)
	t.Setenv("CIRCLEXO_APP_KEY", h.AppKey)
	t.Setenv("CIRCLEXO_WEBHOOK_SECRET", testWebhookSecret)
	t.Setenv("CIRCLEXO_SESSION_KEY", "test-session-key-not-a-secret")
	t.Setenv("HOSBAH_WEB_ORIGIN", "http://hosbah.test")
	ts, stop := bootTestServer(t)
	t.Cleanup(func() {
		stop()
		hub.Install(nil) // a later test in this process boots without the hub
	})
	svc := hub.Current()
	if svc == nil {
		t.Fatal("the integration did not start")
	}
	return &hubStack{t: t, hub: h, base: ts.URL, sql: testDB(t), svc: svc}
}

func rnd() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

func hubOrg() string { return "org-" + rnd() }

// event POSTs a signed webhook and returns the status.
func (s *hubStack) event(typ, org string, data any, id string) int {
	s.t.Helper()
	body, _ := json.Marshal(map[string]any{"id": id, "type": typ, "version": 1, "occurred_at": time.Now().UTC(), "org_id": org, "data": data})
	return s.post(hub.EventsPath, body, id, testWebhookSecret)
}

func (s *hubStack) post(path string, body []byte, id, secret string) int {
	s.t.Helper()
	ts := time.Now()
	req, _ := http.NewRequest(http.MethodPost, s.base+path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(ts.Unix(), 10))
	if secret != "" {
		sig, err := webhooks.Sign(secret, id, ts, body)
		if err != nil {
			s.t.Fatal(err)
		}
		req.Header.Set("webhook-signature", sig)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

func (s *hubStack) provision(org string) (int, map[string]string) {
	s.t.Helper()
	body, _ := json.Marshal(map[string]string{"org_id": org})
	req, _ := http.NewRequest(http.MethodPost, s.base+hub.ProvisionPath, strings.NewReader(string(body)))
	ts := time.Now()
	id := "prov-" + rnd()
	sig, _ := webhooks.Sign(testWebhookSecret, id, ts, body)
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(ts.Unix(), 10))
	req.Header.Set("webhook-signature", sig)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	out := map[string]string{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// install registers an org at the fake hub with these members (hub id, role; the e-mail is derived).
func (s *hubStack) install(org string, members ...[2]string) []circlexo.Member {
	s.hub.Install(org)
	var ms []circlexo.Member
	for _, m := range members {
		ms = append(ms, circlexo.Member{UserID: m[0], Email: m[0] + "@example.test", DisplayName: m[0], Role: m[1]})
	}
	s.hub.Lock()
	s.hub.Members[org] = ms
	s.hub.Unlock()
	return ms
}

// inTeam runs fn in the team's own scope (FORCE row-level security applies, as in production).
func (s *hubStack) inTeam(teamID string, fn func(q *db.Queries, tx *sql.Tx) error) {
	s.t.Helper()
	if err := tenancy.Tx(context.Background(), s.svc.DB, tenancy.Scope{TeamID: teamID}, fn); err != nil {
		s.t.Fatal(err)
	}
}

func (s *hubStack) roleOf(team, email string) string {
	s.t.Helper()
	role := ""
	s.inTeam(team, func(_ *db.Queries, tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT m.role FROM team_members m JOIN users u ON u.id = m.user_id
			WHERE m.team_id = $1 AND lower(u.email) = lower($2)`, team, email).Scan(&role)
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	})
	return role
}

func (s *hubStack) memberCount(team string) int {
	s.t.Helper()
	n := 0
	s.inTeam(team, func(_ *db.Queries, tx *sql.Tx) error {
		return tx.QueryRow(`SELECT count(*) FROM team_members WHERE team_id = $1`, team).Scan(&n)
	})
	return n
}

func (s *hubStack) teamOf(org string) (id string, active bool) {
	s.t.Helper()
	err := s.sql.QueryRow(`SELECT team_id, status = 'active' FROM circlexo_org_links WHERE org_id = $1`, org).Scan(&id, &active)
	if err != nil {
		s.t.Fatalf("no team linked to org %s: %v", org, err)
	}
	return
}

func (s *hubStack) count(q string, args ...any) int {
	s.t.Helper()
	var n int
	if err := s.sql.QueryRow(q, args...).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

// signup registers a password account and confirms its address (so the hub may link to it).
func (s *hubStack) signup(prefix string) (uid, email string) {
	s.t.Helper()
	email = uniq(prefix)
	c := newClient(s.t, s.base)
	c.register(email)
	if err := s.sql.QueryRow(`SELECT id FROM users WHERE lower(email) = $1`, email).Scan(&uid); err != nil {
		s.t.Fatal(err)
	}
	return uid, email
}

// login is a completed hub sign-in as claims say, through the same code the callback runs.
func (s *hubStack) login(sub, email string, verified bool, org, role, sid string) (*http.Response, string, *hub.LoginError) {
	s.t.Helper()
	id := &circlexo.Claims{Subject: sub, SessionID: sid, Raw: map[string]any{"email": email, "email_verified": verified}}
	at := &circlexo.Claims{Subject: sub, OrgID: org, OrgRole: role, SessionID: sid}
	w := httptest.NewRecorder()
	to, err := s.svc.FinishLogin(context.Background(), w, &oidc.Login{IDClaims: id, AccessClaims: at})
	return w.Result(), to, err
}

func sessionCookie(res *http.Response) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == "togo_session" && c.Value != "" {
			return c
		}
	}
	return nil
}

func (s *hubStack) me(c *http.Cookie) int {
	s.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, s.base+"/api/auth/me", nil)
	req.AddCookie(c)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func (s *hubStack) get(path string) (int, []byte) {
	s.t.Helper()
	res, err := http.Get(s.base + path)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

// roundTrip is the whole browser flow for one hub user: /auth/circlexo/login, the hub authorize
// endpoint (which signs the fake user in), then /auth/circlexo/callback. It returns where the
// callback sent the browser and the cookie jar the flow ran in.
func (s *hubStack) roundTrip(u circlexotest.User, launch string) (string, *http.Client) {
	s.t.Helper()
	s.hub.Lock()
	s.hub.User = u
	s.hub.Unlock()
	noFollow := func() *http.Client {
		return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	browser := noFollow()
	browser.Jar = newClient(s.t, s.base).http.Jar
	step := func(c *http.Client, u string) *http.Response {
		res, err := c.Get(u)
		if err != nil {
			s.t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	start := hub.LoginPath
	if launch != "" {
		start = hub.LaunchPath + "?org=" + url.QueryEscape(launch)
	}
	res := step(browser, s.base+start)
	if launch != "" { // the launch URL bounces to the login route
		res = step(browser, s.base+res.Header.Get("Location"))
	}
	to := res.Header.Get("Location")
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(to, s.hub.URL) {
		s.t.Fatalf("login should redirect to the hub, got %d %q", res.StatusCode, to)
	}
	res = step(noFollow(), to) // the hub, signed in as u
	cb, err := url.Parse(res.Header.Get("Location"))
	if err != nil || cb.Path != hub.CallbackPath {
		s.t.Fatalf("the hub sent us to %q", res.Header.Get("Location"))
	}
	res = step(browser, s.base+cb.Path+"?"+cb.RawQuery)
	if res.StatusCode != http.StatusFound {
		s.t.Fatalf("callback: %d", res.StatusCode)
	}
	return res.Header.Get("Location"), browser
}

func (s *hubStack) signedIn(b *http.Client) bool {
	s.t.Helper()
	res, err := b.Get(s.base + "/api/auth/me")
	if err != nil {
		s.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode == 200
}

func TestCircleXOLoginRoundTrip(t *testing.T) {
	s := bootHub(t)
	if code, b := s.get(hub.ConfigPath); code != 200 || !strings.Contains(string(b), `"enabled":true`) {
		t.Fatalf("config: %d %s", code, b)
	}

	t.Run("new user is created, linked and signed in", func(t *testing.T) {
		email, sub := "roundtrip-"+rnd()+"@example.test", "sub-"+rnd()
		to, browser := s.roundTrip(circlexotest.User{ID: sub, Email: email, Name: "Round Trip"}, "")
		if to != "/" || !s.signedIn(browser) {
			t.Fatalf("landing %q, signed in %v", to, s.signedIn(browser))
		}
		if n := s.count(`SELECT count(*) FROM circlexo_user_links l JOIN users u ON u.id = l.user_id WHERE l.subject = $1 AND lower(u.email) = $2 AND l.linked_via = 'created'`, sub, email); n != 1 {
			t.Fatalf("expected one created link, got %d", n)
		}
		before := s.count(`SELECT count(*) FROM users`)
		if _, b2 := s.roundTrip(circlexotest.User{ID: sub, Email: email}, ""); !s.signedIn(b2) || s.count(`SELECT count(*) FROM users`) != before {
			t.Fatal("a second sign-in did not reuse the account")
		}
	})

	t.Run("linked by verified e-mail to an existing account", func(t *testing.T) {
		uid, email := s.signup("rtlink")
		sub := "sub-" + rnd()
		before := s.count(`SELECT count(*) FROM users`)
		_, browser := s.roundTrip(circlexotest.User{ID: sub, Email: email}, "")
		if !s.signedIn(browser) || s.count(`SELECT count(*) FROM users`) != before {
			t.Fatal("expected to sign in to the existing account")
		}
		var linked, via string
		if e := s.sql.QueryRow(`SELECT user_id, linked_via FROM circlexo_user_links WHERE subject = $1`, sub).Scan(&linked, &via); e != nil || linked != uid || via != "email" {
			t.Fatalf("expected an email link to %s, got %s %s %v", uid, linked, via, e)
		}
	})

	t.Run("an unverified address is refused and creates nothing", func(t *testing.T) {
		email := "unverified-" + rnd() + "@example.test"
		to, browser := s.roundTrip(circlexotest.User{ID: "sub-" + rnd(), Email: email, EmailUnverified: true}, "")
		if to != "/login?circlexo_error=email_unverified" || s.signedIn(browser) {
			t.Fatalf("got %q (signed in %v)", to, s.signedIn(browser))
		}
		if n := s.count(`SELECT count(*) FROM users WHERE lower(email) = $1`, email); n != 0 {
			t.Fatalf("a refused sign-in created %d account(s)", n)
		}
	})

	t.Run("launch URL lands in the team of the org", func(t *testing.T) {
		org := hubOrg()
		s.install(org, [2]string{"own-" + rnd(), "owner"})
		if code, _ := s.provision(org); code != 200 {
			t.Fatal("provision")
		}
		team, _ := s.teamOf(org)
		var slug string
		if err := s.sql.QueryRow(`SELECT org_slug FROM circlexo_org_links WHERE org_id = $1`, org).Scan(&slug); err != nil {
			t.Fatal(err)
		}
		to, browser := s.roundTrip(circlexotest.User{ID: "sub-" + rnd(), Email: "launch-" + rnd() + "@example.test", OrgID: org, OrgRole: "member"}, slug)
		if to != "/en/t/"+team || !s.signedIn(browser) {
			t.Fatalf("launch landed on %q", to)
		}
	})
}

func TestCircleXOLinking(t *testing.T) {
	s := bootHub(t)

	t.Run("new user is created, linked and signed in", func(t *testing.T) {
		email := "new-" + rnd() + "@example.test"
		sub := "sub-" + rnd()
		res, to, err := s.login(sub, email, true, "", "", "sid-"+rnd())
		if err != nil {
			t.Fatal(err)
		}
		if to != "/" {
			t.Fatalf("with no org the user lands on the home page, got %q", to)
		}
		if ck := sessionCookie(res); ck == nil || s.me(ck) != 200 {
			t.Fatal("the session cookie does not sign in")
		}
		before := s.count(`SELECT count(*) FROM users`)
		if _, _, err := s.login(sub, "other-"+rnd()+"@example.test", true, "", "", "sid-"+rnd()); err != nil {
			t.Fatal(err)
		}
		if s.count(`SELECT count(*) FROM users`) != before {
			t.Fatal("a second sign-in created another account")
		}
	})

	t.Run("an unconfirmed password account is never taken over", func(t *testing.T) {
		email := uniq("unconf")
		c := newClient(t, s.base)
		c.mustJSON("POST", "/api/auth/register", map[string]any{"email": email, "password": c.pw}, 403, nil)
		if _, _, err := s.login("sub-"+rnd(), email, true, "", "", "sid-"+rnd()); err == nil || err.Code() != "account_conflict" {
			t.Fatalf("an unconfirmed password account must not be linked, got %v", err)
		}
	})

	t.Run("linked by verified e-mail, no second subject, password still works", func(t *testing.T) {
		uid, email := s.signup("link")
		sub := "sub-" + rnd()
		res, _, err := s.login(sub, strings.ToUpper(email), true, "", "", "sid-"+rnd())
		if err != nil || sessionCookie(res) == nil {
			t.Fatalf("link: %v", err)
		}
		var linked string
		if e := s.sql.QueryRow(`SELECT user_id FROM circlexo_user_links WHERE subject = $1`, sub).Scan(&linked); e != nil || linked != uid {
			t.Fatalf("expected a link to %s, got %s %v", uid, linked, e)
		}
		if _, _, err := s.login("sub-"+rnd(), email, true, "", "", "sid-"+rnd()); err == nil || err.Code() != "account_conflict" {
			t.Fatalf("a second hub subject must not link to a linked account, got %v", err)
		}
		newClient(t, s.base).login(email) // dual login
	})

	t.Run("an unverified hub address is refused", func(t *testing.T) {
		email := "unv-" + rnd() + "@example.test"
		if _, _, err := s.login("sub-"+rnd(), email, false, "", "", "sid-"+rnd()); err == nil || err.Code() != "email_unverified" {
			t.Fatalf("got %v", err)
		}
		if n := s.count(`SELECT count(*) FROM users WHERE lower(email) = $1`, email); n != 0 {
			t.Fatal("an account was created")
		}
	})

	t.Run("a disabled account is refused", func(t *testing.T) {
		uid, email := s.signup("susp")
		if _, err := s.sql.Exec(`UPDATE users SET disabled_at = now() WHERE id = $1`, uid); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.login("sub-"+rnd(), email, true, "", "", "sid-"+rnd()); err == nil || err.Code() != "suspended" {
			t.Fatalf("got %v", err)
		}
	})
}

func TestCircleXOProvision(t *testing.T) {
	s := bootHub(t)
	org := hubOrg()
	ms := s.install(org, [2]string{"own-" + rnd(), "owner"}, [2]string{"adm-" + rnd(), "admin"}, [2]string{"mem-" + rnd(), "member"}, [2]string{"bil-" + rnd(), "billing"})

	if code := s.post(hub.ProvisionPath, []byte(`{"org_id":"`+org+`"}`), "x", ""); code != http.StatusUnauthorized {
		t.Fatalf("an unsigned provision call must be 401, got %d", code)
	}
	if code := s.post(hub.ProvisionPath, []byte(`{"org_id":"`+org+`"}`), "x", otherWebhook); code != http.StatusUnauthorized {
		t.Fatalf("a wrongly signed provision call must be 401, got %d", code)
	}
	if n := s.count(`SELECT count(*) FROM circlexo_org_links WHERE org_id = $1`, org); n != 0 {
		t.Fatal("a refused call provisioned")
	}

	code, first := s.provision(org)
	if code != 200 || first["product_tenant_id"] == "" || first["slug"] == "" {
		t.Fatalf("provision: %d %v", code, first)
	}
	code, second := s.provision(org)
	if code != 200 || second["product_tenant_id"] != first["product_tenant_id"] {
		t.Fatalf("provision must be idempotent: %v then %v", first, second)
	}
	if n := s.count(`SELECT count(*) FROM circlexo_org_links WHERE org_id = $1`, org); n != 1 {
		t.Fatalf("%d links", n)
	}
	team, active := s.teamOf(org)
	if !active || team != first["product_tenant_id"] {
		t.Fatalf("link does not match the answer: %s %v", team, active)
	}
	s.hub.Lock()
	tn := s.hub.Tenants[org]
	s.hub.Unlock()
	if tn.ProductTenantID != team || tn.Status != "active" {
		t.Fatalf("the hub was not told about the tenant: %+v", tn)
	}

	// owner and admin keep their rank; billing reads, member deploys.
	want := map[string]string{ms[0].Email: "owner", ms[1].Email: "admin", ms[2].Email: "deployer", ms[3].Email: "viewer"}
	for email, role := range want {
		if got := s.roleOf(team, email); got != role {
			t.Fatalf("%s: got %q want %q", email, got, role)
		}
	}
	if n := s.memberCount(team); n != 4 {
		t.Fatalf("%d members", n)
	}

	if code := s.event(webhooks.AppInstalled, org, map[string]any{"slug": "org", "name": "Org"}, "evt-"+rnd()); code != http.StatusNoContent {
		t.Fatalf("install webhook: %d", code)
	}
	if again, _ := s.teamOf(org); again != team {
		t.Fatal("the webhook made a second team")
	}
	other := hubOrg()
	s.install(other, [2]string{"o-" + rnd(), "owner"})
	body, _ := json.Marshal(map[string]any{"id": "evt-bad", "type": webhooks.AppInstalled, "org_id": other, "data": map[string]any{}})
	if code := s.post(hub.EventsPath, body, "evt-bad", otherWebhook); code != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", code)
	}
	if code := s.post(hub.EventsPath, body, "evt-none", ""); code != http.StatusUnauthorized {
		t.Fatalf("unsigned webhook: %d", code)
	}
	if n := s.count(`SELECT count(*) FROM circlexo_org_links WHERE org_id = $1`, other); n != 0 {
		t.Fatal("a badly signed install provisioned")
	}
}

func TestCircleXOMemberSync(t *testing.T) {
	s := bootHub(t)
	org := hubOrg()
	owner, mem := "own-"+rnd(), "mem-"+rnd()
	ms := s.install(org, [2]string{owner, "owner"}, [2]string{mem, "member"})
	if code, _ := s.provision(org); code != 200 {
		t.Fatal("provision")
	}
	team, _ := s.teamOf(org)
	person := func(id string) map[string]any {
		return map[string]any{"id": id, "email": id + "@example.test", "display_name": id}
	}

	s.event(webhooks.MemberRoleChanged, org, map[string]any{"user": person(mem), "role": "admin", "previous_role": "member"}, "e1-"+rnd())
	if r := s.roleOf(team, ms[1].Email); r != "admin" {
		t.Fatalf("after promotion: %s", r)
	}
	newbie := "new-" + rnd()
	s.event(webhooks.MemberAdded, org, map[string]any{"user": person(newbie), "role": "member"}, "e3-"+rnd())
	if r := s.roleOf(team, newbie+"@example.test"); r != "deployer" {
		t.Fatalf("added member: %s", r)
	}
	s.event(webhooks.MemberRemoved, org, map[string]any{"user": person(newbie)}, "e4-"+rnd())
	if r := s.roleOf(team, newbie+"@example.test"); r != "" {
		t.Fatalf("removed member still in the team as %s", r)
	}
	s.event(webhooks.MemberAdded, org, map[string]any{"user": person(newbie), "role": "member"}, "e5-"+rnd())
	if r := s.roleOf(team, newbie+"@example.test"); r != "deployer" {
		t.Fatalf("re-added member: %s", r)
	}
	// The last owner is never removed or demoted by the hub.
	s.event(webhooks.MemberRemoved, org, map[string]any{"user": person(owner)}, "e6-"+rnd())
	s.event(webhooks.MemberRoleChanged, org, map[string]any{"user": person(owner), "role": "member"}, "e7-"+rnd())
	if r := s.roleOf(team, ms[0].Email); r != "owner" {
		t.Fatalf("the last owner changed: %s", r)
	}
	if code := s.event(webhooks.MemberAdded, hubOrg(), map[string]any{"user": person("x-" + rnd()), "role": "owner"}, "e8-"+rnd()); code != http.StatusNoContent {
		t.Fatalf("unknown org: %d", code)
	}

	members := s.memberCount(team)
	if code := s.event(webhooks.AppRemoved, org, map[string]any{}, "e9-"+rnd()); code != http.StatusNoContent {
		t.Fatalf("app_removed: %d", code)
	}
	if _, active := s.teamOf(org); active {
		t.Fatal("the link is still active")
	}
	if s.memberCount(team) != members {
		t.Fatal("deprovision deleted members")
	}
	if code, out := s.provision(org); code != 200 || out["product_tenant_id"] != team {
		t.Fatalf("re-provision: %d %v", code, out)
	}
	if _, active := s.teamOf(org); !active {
		t.Fatal("not reactivated")
	}
}

func TestCircleXOLandingTeam(t *testing.T) {
	s := bootHub(t)
	org := hubOrg()
	sub := "sub-" + rnd()
	s.install(org, [2]string{"own-" + rnd(), "owner"})
	email := "land-" + rnd() + "@example.test"

	res, to, err := s.login(sub, email, true, org, "admin", "sid-"+rnd())
	if err != nil {
		t.Fatal(err)
	}
	team, _ := s.teamOf(org)
	if to != "/en/t/"+team {
		t.Fatalf("landing: %q want /en/t/%s", to, team)
	}
	if r := s.roleOf(team, email); r != "admin" {
		t.Fatalf("hub admin should be a team admin: %q", r)
	}
	if sessionCookie(res) == nil {
		t.Fatal("no session")
	}
	other := hubOrg()
	if _, to, err := s.login(sub, email, true, other, "owner", "sid-"+rnd()); err != nil || to != "/" {
		t.Fatalf("unknown org: %q %v", to, err)
	}
	if s.count(`SELECT count(*) FROM circlexo_org_links WHERE org_id = $1`, other) != 0 {
		t.Fatal("an unknown org was provisioned")
	}
}

// Hosbah is free and open source (2026-10-15): the hub's plan no longer decides a team's limits. A
// linked team keeps the platform admin's quotas (unlimited by default) whatever the hub says: a tight
// plan, a hub outage, or no entitlements at all.
func TestCircleXOEntitlements(t *testing.T) {
	s := bootHub(t)
	org := hubOrg()
	s.install(org, [2]string{"own-" + rnd(), "owner"})
	if code, _ := s.provision(org); code != 200 {
		t.Fatal("provision")
	}
	team, _ := s.teamOf(org)
	ctx := context.Background()
	lim := func(n int64) *int64 { return &n }
	get := func(id string) quota.Quota {
		t.Helper()
		var q quota.Quota
		s.inTeam(id, func(qq *db.Queries, _ *sql.Tx) error {
			var err error
			q, err = quota.Get(ctx, qq, id)
			return err
		})
		return q
	}
	free := func(what string) {
		t.Helper()
		q := get(team)
		if q.Hub || q.ReadOnly || q.MaxServers != nil || q.MaxApps != nil || q.MaxDataServices != nil || q.MaxMembers != nil {
			t.Fatalf("%s: the hub must not limit the team: %+v", what, q)
		}
		var err error
		s.inTeam(team, func(qq *db.Queries, _ *sql.Tx) error { err = quota.CheckMembers(ctx, qq, team); return nil })
		if err != nil {
			t.Fatalf("%s: adding a member was refused: %v", what, err)
		}
	}

	s.hub.Lock()
	s.hub.Entitlements[org] = circlexo.Entitlements{OrgID: org, AppID: testHubApp, Active: true, Plan: "pro", Status: "active", Version: 2, Features: []circlexo.Entitlement{
		{Key: "servers", Kind: "limit", Enabled: true, Limit: lim(0)},
		{Key: "members", Kind: "limit", Enabled: true, Limit: lim(1)},
	}}
	s.hub.Unlock()
	s.svc.Ents.Invalidate(org, 0)
	free("a plan with 0 servers and 1 member")

	s.hub.Lock()
	s.hub.Fail = http.StatusInternalServerError
	s.hub.Unlock()
	s.svc.Ents.Invalidate(org, 0)
	free("hub down")
	s.hub.Lock()
	s.hub.Fail = 0
	delete(s.hub.Entitlements, org)
	s.hub.Unlock()
	s.svc.Ents.Invalidate(org, 0)
	free("no entitlements")
}

func TestCircleXOSessionRevoked(t *testing.T) {
	s := bootHub(t)
	_, email := s.signup("sess")
	sub := "sub-" + rnd()
	res1, _, err := s.login(sub, email, true, "", "", "sid-a-"+rnd())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // the marker is (user, iat) at one-second resolution
	sidB := "sid-b-" + rnd()
	res2, _, err := s.login(sub, email, true, "", "", sidB)
	if err != nil {
		t.Fatal(err)
	}
	a, b := sessionCookie(res1), sessionCookie(res2)
	if s.me(a) != 200 || s.me(b) != 200 {
		t.Fatal("both sessions should work")
	}
	time.Sleep(1100 * time.Millisecond)
	pw := newClient(t, s.base)
	pw.login(email)

	if code := s.event(webhooks.SessionRevoked, "", map[string]any{"user": map[string]any{"id": sub}, "sid": sidB}, "sr-"+rnd()); code != http.StatusNoContent {
		t.Fatalf("session.revoked: %d", code)
	}
	if s.me(b) != http.StatusUnauthorized {
		t.Fatal("the revoked session still works")
	}
	if s.me(a) != 200 {
		t.Fatal("another hub session was revoked too")
	}
	pw.mustJSON("GET", "/api/auth/me", nil, 200, nil) // a password session of the same user is untouched
}

func TestCircleXOMCP(t *testing.T) {
	s := bootHub(t)
	org := hubOrg()
	ownerID := "own-" + rnd()
	s.install(org, [2]string{ownerID, "owner"})
	if code, _ := s.provision(org); code != 200 {
		t.Fatal("provision")
	}
	team, _ := s.teamOf(org)
	s.hub.Lock()
	s.hub.Entitlements[org] = circlexo.Entitlements{OrgID: org, AppID: testHubApp, Active: true, Plan: "pro", Status: "active", Version: 1, Features: []circlexo.Entitlement{
		{Key: "servers", Kind: "limit", Enabled: true}, {Key: "members", Kind: "limit", Enabled: true},
	}}
	s.hub.Unlock()
	sub := "sub-" + rnd()
	if _, _, err := s.login(sub, ownerID+"@example.test", true, org, "owner", "sid-"+rnd()); err != nil {
		t.Fatal(err)
	}
	call := func(token, tool string, args map[string]any) mcpResult {
		return mcpCall(t, s.base, "/api/mcp", token, tool, args)
	}

	// A token naming a tool scope gets exactly that scope.
	readTok := s.hub.AccessToken(map[string]any{"sub": sub, "org_id": org, "scope": "openid read"})
	if r := call(readTok, "list_members", nil); r.IsError {
		t.Fatalf("an allowed tool: %s", r.Text)
	}
	if r := call(readTok, "invite_member", map[string]any{"email": "nope-" + rnd() + "@example.test", "role": "viewer"}); !r.IsError {
		t.Fatalf("a tool outside the token's scopes must be refused: %s", r.Text)
	}

	// A gateway token (act chain) gets read, write and deploy, and the actor is recorded.
	agentTok := s.hub.AccessToken(map[string]any{"sub": sub, "org_id": org, "act": map[string]any{"sub": "gateway-1", "agent_id": "agent-7"}})
	invitee := "hub-" + rnd() + "@example.test"
	if r := call(agentTok, "invite_member", map[string]any{"email": invitee, "role": "viewer"}); r.IsError {
		t.Fatalf("agent invite_member: %s", r.Text)
	}
	var changes string
	s.inTeam(team, func(_ *db.Queries, tx *sql.Tx) error {
		return tx.QueryRow(`SELECT changes FROM activity_log WHERE team_id = $1 AND action = 'invitation.created' ORDER BY created_at DESC LIMIT 1`, team).Scan(&changes)
	})
	if !strings.Contains(changes, "gateway-1") {
		t.Fatalf("the actor chain was not recorded: %q", changes)
	}
	// It never gets destructive.
	if r := call(agentTok, "remove_member", map[string]any{"user_id": "x"}); !r.IsError {
		t.Fatalf("an agent token must not delete: %s", r.Text)
	}

	deny := func(what, token string, wantHTTP int) {
		t.Helper()
		if got := mcpStatus(s.base, "/api/mcp", token); got != wantHTTP {
			t.Fatalf("%s: got %d want %d", what, got, wantHTTP)
		}
	}
	deny("unknown org", s.hub.AccessToken(map[string]any{"sub": sub, "org_id": "org-unknown-" + rnd(), "scope": "read"}), http.StatusForbidden)
	deny("unlinked user", s.hub.AccessToken(map[string]any{"sub": "nobody-" + rnd(), "org_id": org, "scope": "read"}), http.StatusForbidden)
	deny("no org", s.hub.AccessToken(map[string]any{"sub": sub, "org_id": nil, "scope": "read"}), http.StatusForbidden)
	deny("wrong audience", s.hub.AccessToken(map[string]any{"sub": sub, "org_id": org, "aud": "some-other-app"}), http.StatusUnauthorized)
	deny("garbage", "not-a-token", http.StatusUnauthorized)
	deny("service token", s.hub.AccessToken(map[string]any{"sub": s.hub.ClientID, "org_id": org}), http.StatusUnauthorized)

	s.event(webhooks.AppRemoved, org, map[string]any{}, "mcp-rm-"+rnd())
	deny("deprovisioned org", readTok, http.StatusForbidden)
}

// With no CIRCLEXO_ISSUER the integration is off: the config says so and the routes are absent.
func TestCircleXODisabled(t *testing.T) {
	t.Setenv("CIRCLEXO_ISSUER", "")
	ts, stop := bootTestServer(t)
	defer stop()
	res, err := http.Get(ts.URL + hub.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"enabled":false`) {
		t.Fatalf("config: %d %s", res.StatusCode, b)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{hub.LoginPath, hub.LaunchPath} {
		r, err := client.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode == http.StatusFound {
			t.Fatalf("%s must not redirect with the integration off", p)
		}
	}
	if hub.Current() != nil {
		t.Fatal("Current must be nil when off")
	}
}

// TestCircleXOIntegrationTokens: the integrations API (MH-1153) takes a CircleXO hub token issued
// to an integration (an act chain), mapped through the user and org links like /api/mcp. It lists
// only the apps that person can manage and mints hba_ tokens for them; a token with no act chain,
// or an actor outside HOSBAH_INTEGRATION_ACTORS, is refused.
func TestCircleXOIntegrationTokens(t *testing.T) {
	s := bootHub(t)
	org := hubOrg()
	ownerID, viewerID := "own-"+rnd(), "view-"+rnd()
	s.install(org, [2]string{ownerID, "owner"}, [2]string{viewerID, "member"})
	if code, _ := s.provision(org); code != 200 {
		t.Fatal("provision")
	}
	team, _ := s.teamOf(org)
	s.hub.Lock()
	s.hub.Entitlements[org] = circlexo.Entitlements{OrgID: org, AppID: testHubApp, Active: true, Plan: "pro", Status: "active", Version: 1, Features: []circlexo.Entitlement{
		{Key: "servers", Kind: "limit", Enabled: true}, {Key: "members", Kind: "limit", Enabled: true}, {Key: "apps", Kind: "limit", Enabled: true},
	}}
	s.hub.Unlock()
	sub, viewerSub := "sub-own-"+rnd(), "sub-dep-"+rnd()
	res, _, err := s.login(sub, ownerID+"@example.test", true, org, "owner", "sid-"+rnd())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.login(viewerSub, viewerID+"@example.test", true, org, "member", "sid-"+rnd()); err != nil {
		t.Fatal(err)
	}

	// The owner's browser session makes a server and an app in the hub team.
	owner := newClient(t, s.base)
	u, _ := url.Parse(s.base)
	owner.http.Jar.SetCookies(u, []*http.Cookie{sessionCookie(res)})
	srv, _ := enrolServer(t, owner, s.base, team, "hubint-"+rnd())
	var created struct {
		App struct{ ID string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team+"/apps", map[string]any{"name": "hubint-" + rnd(), "server": srv,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &created)

	agent := func(subject string) string {
		return s.hub.AccessToken(map[string]any{"sub": subject, "org_id": org, "act": map[string]any{"sub": "mahaam", "agent_id": "mahaam-1"}})
	}
	var list struct {
		Apps []struct{ ID string } `json:"apps"`
	}
	keyJSON(t, s.base, agent(sub), "GET", "/api/integrations/apps", nil, 200, &list)
	if len(list.Apps) != 1 || list.Apps[0].ID != created.App.ID {
		t.Fatalf("hub owner lists %+v, want the one app", list.Apps)
	}
	var minted struct {
		Token string `json:"token"`
		App   struct{ ID string }
	}
	keyJSON(t, s.base, agent(sub), "POST", "/api/integrations/apps/"+created.App.ID+"/tokens", map[string]any{"label": "Mahaam"}, 201, &minted)
	if !strings.HasPrefix(minted.Token, "hba_") || minted.App.ID != created.App.ID {
		t.Fatalf("minted %+v", minted)
	}
	keyJSON(t, s.base, minted.Token, "GET", "/api/integrations/app", nil, 200, nil)

	// A deployer (hub "member") sees nothing and cannot mint.
	keyJSON(t, s.base, agent(viewerSub), "GET", "/api/integrations/apps", nil, 200, &list)
	if len(list.Apps) != 0 {
		t.Errorf("a deployer lists %d apps, want 0", len(list.Apps))
	}
	if code, text := keyDo(t, s.base, agent(viewerSub), "POST", "/api/integrations/apps/"+created.App.ID+"/tokens", map[string]any{"label": "x"}); code != 403 {
		t.Errorf("deployer minting -> %d, want 403: %s", code, text)
	}

	refuse := func(what, token string, want int) {
		t.Helper()
		if code, text := keyDo(t, s.base, token, "GET", "/api/integrations/apps", nil); code != want {
			t.Errorf("%s -> %d, want %d: %s", what, code, want, text)
		}
	}
	refuse("no act chain", s.hub.AccessToken(map[string]any{"sub": sub, "org_id": org, "scope": "read write"}), http.StatusForbidden)
	refuse("unlinked user", agent("nobody-"+rnd()), http.StatusForbidden)
	refuse("wrong audience", s.hub.AccessToken(map[string]any{"sub": sub, "org_id": org, "aud": "other", "act": map[string]any{"sub": "mahaam"}}), http.StatusUnauthorized)
	refuse("garbage", "not-a-token", http.StatusUnauthorized)
	t.Setenv("HOSBAH_INTEGRATION_ACTORS", "someone-else, another")
	refuse("actor not allowed", agent(sub), http.StatusForbidden)
	t.Setenv("HOSBAH_INTEGRATION_ACTORS", "someone-else,mahaam")
	refuse("actor allowed", agent(sub), http.StatusOK)
}
