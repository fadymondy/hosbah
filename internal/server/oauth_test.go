package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/fadymondy/hosbah/internal/account"
	"github.com/fadymondy/hosbah/internal/totp"
)

/*
Sign in with Google and GitHub (internal/account/oauth.go) end to end, against a fake Google (a
token endpoint that signs id_tokens with a test RSA key, and its JWKS) and a fake GitHub (token
endpoint, /user, /user/emails). The browser is a cookie jar that does not follow redirects, so every
hop is checked: the start's redirect to the provider (state, PKCE S256, nonce), and the callback's
redirect back into the web app.
*/

// fakeUser is what a provider says about the account behind an authorization code.
type fakeUser struct {
	Sub      string
	Email    string
	Verified bool
	// Google only: override the id_token's nonce or audience (a forged or foreign token).
	Nonce, Aud string
}

type fakeIdP struct {
	t      *testing.T
	key    *rsa.PrivateKey
	srv    *httptest.Server
	mu     sync.Mutex
	codes  map[string]fakeCode
	tokens map[string]fakeUser // GitHub access token → user
}

type fakeCode struct {
	user      fakeUser
	challenge string // the PKCE challenge the start sent; the code_verifier must hash to it
	nonce     string
	provider  string
}

const (
	fakeGoogleClient = "google-client-id"
	fakeGitHubClient = "github-client-id"
)

func newFakeIdP(t *testing.T) *fakeIdP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{t: t, key: key, codes: map[string]fakeCode{}, tokens: map[string]fakeUser{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/google/certs", func(w http.ResponseWriter, _ *http.Request) {
		e := big.NewInt(int64(key.PublicKey.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)}}})
	})
	mux.HandleFunc("/google/token", func(w http.ResponseWriter, r *http.Request) {
		fc, ok := f.redeem(r, fakeGoogleClient, "google-secret")
		if !ok {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		nonce, aud := fc.nonce, fakeGoogleClient
		if fc.user.Nonce != "" {
			nonce = fc.user.Nonce
		}
		if fc.user.Aud != "" {
			aud = fc.user.Aud
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://accounts.google.com", "aud": aud, "sub": fc.user.Sub,
			"email": fc.user.Email, "email_verified": fc.user.Verified, "nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()})
		tok.Header["kid"] = "k1"
		signed, err := tok.SignedString(key)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "ya29.x", "id_token": signed, "token_type": "Bearer"})
	})
	mux.HandleFunc("/github/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		fc, ok := f.redeem(r, fakeGitHubClient, "github-secret")
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
			return
		}
		at := "gho_" + fc.user.Sub + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
		f.mu.Lock()
		f.tokens[at] = fc.user
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": at, "token_type": "bearer", "scope": "read:user,user:email"})
	})
	user := func(r *http.Request) (fakeUser, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		u, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		return u, ok
	}
	mux.HandleFunc("/github/api/user", func(w http.ResponseWriter, r *http.Request) {
		u, ok := user(r)
		if !ok {
			http.Error(w, "unauthorized", 401)
			return
		}
		id, _ := strconv.ParseInt(u.Sub, 10, 64)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "login": "octo" + u.Sub})
	})
	mux.HandleFunc("/github/api/user/emails", func(w http.ResponseWriter, r *http.Request) {
		u, ok := user(r)
		if !ok {
			http.Error(w, "unauthorized", 401)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"email": "secondary-" + u.Sub + "@example.test", "primary": false, "verified": true},
			{"email": u.Email, "primary": true, "verified": u.Verified},
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// redeem checks a token request the way the providers do: the client's credentials, the redirect
// URI, and the PKCE verifier against the challenge sent at the start. A code works once.
func (f *fakeIdP) redeem(r *http.Request, clientID, secret string) (fakeCode, bool) {
	if err := r.ParseForm(); err != nil {
		return fakeCode{}, false
	}
	f.mu.Lock()
	fc, ok := f.codes[r.PostForm.Get("code")]
	delete(f.codes, r.PostForm.Get("code"))
	f.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || r.PostForm.Get("client_id") != clientID || r.PostForm.Get("client_secret") != secret ||
		!strings.HasSuffix(r.PostForm.Get("redirect_uri"), "/api/auth/oauth/"+fc.provider+"/callback") ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != fc.challenge {
		f.t.Logf("fake IdP refused a token request: known=%v client=%q", ok, r.PostForm.Get("client_id"))
		return fakeCode{}, false
	}
	return fc, true
}

func (f *fakeIdP) providers() map[string]*account.Provider {
	return map[string]*account.Provider{
		"google": {ID: "google", Name: "Google", ClientID: fakeGoogleClient, ClientSecret: "google-secret",
			AuthURL: "https://accounts.google.test/o/oauth2/v2/auth", TokenURL: f.srv.URL + "/google/token",
			JWKSURL: f.srv.URL + "/google/certs", Issuers: []string{"https://accounts.google.com"}, HTTP: f.srv.Client()},
		"github": {ID: "github", Name: "GitHub", ClientID: fakeGitHubClient, ClientSecret: "github-secret",
			AuthURL: "https://github.test/login/oauth/authorize", TokenURL: f.srv.URL + "/github/login/oauth/access_token",
			APIURL: f.srv.URL + "/github/api", HTTP: f.srv.Client()},
	}
}

// bootOAuthServer boots the API with the fake providers.
func bootOAuthServer(t *testing.T) (*httptest.Server, *fakeIdP, func()) {
	idp := newFakeIdP(t)
	configureAccounts = func(s *account.Service) { s.SetProviders(idp.providers(), "http://web.test") }
	ts, cleanup := bootTestServer(t)
	configureAccounts = nil
	return ts, idp, cleanup
}

// noFollow is c's cookie jar in a client that stops at every redirect.
func noFollow(c *client) *http.Client {
	return &http.Client{Jar: c.http.Jar, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func location(t *testing.T, c *client, path string) (int, *url.URL) {
	t.Helper()
	resp, err := noFollow(c).Get(c.base + path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc == nil {
		loc = &url.URL{}
	}
	return resp.StatusCode, loc
}

// start begins a round trip and returns the provider's authorization URL.
func start(t *testing.T, c *client, provider, query string) *url.URL {
	t.Helper()
	path := "/api/auth/oauth/" + provider
	if query != "" {
		path += "?" + query
	}
	status, loc := location(t, c, path)
	if status != http.StatusSeeOther {
		t.Fatalf("start %s -> %d %s", path, status, loc)
	}
	q := loc.Query()
	if q.Get("state") == "" || q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("start %s: the provider URL lacks state or PKCE: %s", path, loc)
	}
	if provider == "google" && q.Get("nonce") == "" {
		t.Fatalf("Google start without a nonce: %s", loc)
	}
	if !strings.HasSuffix(q.Get("redirect_uri"), "/api/auth/oauth/"+provider+"/callback") {
		t.Fatalf("redirect_uri %q", q.Get("redirect_uri"))
	}
	return loc
}

// authorize is the person approving at the provider: the provider mints a code for u.
func (f *fakeIdP) authorize(provider string, auth *url.URL, u fakeUser) string {
	code := fmt.Sprintf("code-%d", time.Now().UnixNano())
	f.mu.Lock()
	f.codes[code] = fakeCode{user: u, challenge: auth.Query().Get("code_challenge"), nonce: auth.Query().Get("nonce"), provider: provider}
	f.mu.Unlock()
	return code
}

func callbackPath(provider, state, code string) string {
	return "/api/auth/oauth/" + provider + "/callback?" + url.Values{"state": {state}, "code": {code}}.Encode()
}

// signIn runs a whole round trip and returns where the callback sent the browser.
func (f *fakeIdP) signIn(t *testing.T, c *client, provider, query string, u fakeUser) *url.URL {
	t.Helper()
	auth := start(t, c, provider, query)
	code := f.authorize(provider, auth, u)
	status, loc := location(t, c, callbackPath(provider, auth.Query().Get("state"), code))
	if status != http.StatusSeeOther {
		t.Fatalf("callback -> %d", status)
	}
	return loc
}

func wantError(t *testing.T, loc *url.URL, code string) {
	t.Helper()
	if got := loc.Query().Get("oauth_error"); got != code {
		t.Fatalf("redirect %s: oauth_error %q, want %q", loc, got, code)
	}
}

type meOut struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

func whoami(t *testing.T, c *client) (int, meOut) {
	t.Helper()
	status, text := c.do("GET", "/api/auth/me", nil)
	var m meOut
	_ = json.Unmarshal([]byte(text), &m)
	return status, m
}

type identitiesOut struct {
	Identities []struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
		Email    string `json:"email"`
	} `json:"identities"`
	HasPassword bool     `json:"has_password"`
	Providers   []string `json:"providers"`
}

func identities(t *testing.T, c *client) identitiesOut {
	t.Helper()
	var out identitiesOut
	c.mustJSON("GET", "/api/me/identities", nil, 200, &out)
	return out
}

func TestOAuthProvidersAndNewUser(t *testing.T) {
	ts, idp, cleanup := bootOAuthServer(t)
	defer cleanup()

	var prov struct {
		Providers []struct{ ID, Name string } `json:"providers"`
	}
	newClient(t, ts.URL).mustJSON("GET", "/api/auth/providers", nil, 200, &prov)
	if len(prov.Providers) != 2 || prov.Providers[0].ID != "google" || prov.Providers[1].ID != "github" {
		t.Fatalf("providers: %+v", prov)
	}

	// A new account from a verified Google address: signed in, verified, no password.
	email := uniq("g-new")
	c := newClient(t, ts.URL)
	loc := idp.signIn(t, c, "google", "locale=ar", fakeUser{Sub: "g-" + email, Email: strings.ToUpper(email), Verified: true})
	if loc.Path != "/ar" || loc.Fragment != "" || loc.Query().Get("oauth_error") != "" {
		t.Fatalf("new user landed on %s", loc)
	}
	status, me := whoami(t, c)
	if status != 200 || me.Email != email {
		t.Fatalf("signed in as %+v (%d), want %s", me, status, email)
	}
	ids := identities(t, c)
	if len(ids.Identities) != 1 || ids.Identities[0].Provider != "google" || ids.Identities[0].Email != email || ids.HasPassword {
		t.Fatalf("identities: %+v", ids)
	}
	// It can use the product (the account is verified): create a team.
	c.mustJSON("POST", "/api/teams", map[string]any{"name": "OAuth " + email[:10]}, 201, nil)

	// The only way in cannot be unlinked.
	c.expectCode("DELETE", "/api/me/identities/"+ids.Identities[0].ID, nil, 409, "last_sign_in_method")

	// GitHub: a new account from the primary, verified address.
	gh := uniq("gh-new")
	c2 := newClient(t, ts.URL)
	sub := strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 10)
	if loc := idp.signIn(t, c2, "github", "", fakeUser{Sub: sub, Email: gh, Verified: true}); loc.Path != "/en" {
		t.Fatalf("GitHub new user landed on %s", loc)
	}
	if _, me := whoami(t, c2); me.Email != gh {
		t.Fatalf("GitHub sign-in: %+v", me)
	}
	// The provider's address changes; the identity (its numeric id) is what counts.
	c3 := newClient(t, ts.URL)
	if loc := idp.signIn(t, c3, "github", "", fakeUser{Sub: sub, Email: uniq("gh-renamed"), Verified: true}); loc.Path != "/en" {
		t.Fatalf("after the email change: %s", loc)
	}
	_, me2 := whoami(t, c2)
	_, me3 := whoami(t, c3)
	if me3.ID == "" || me3.ID != me2.ID || me3.Email != gh {
		t.Fatalf("an email change at GitHub made another account: %+v vs %+v", me3, me2)
	}
}

func TestOAuthVerifiedEmailOnly(t *testing.T) {
	ts, idp, cleanup := bootOAuthServer(t)
	defer cleanup()
	email := uniq("unverified")
	c := newClient(t, ts.URL)
	wantError(t, idp.signIn(t, c, "google", "", fakeUser{Sub: "g-" + email, Email: email, Verified: false}), "oauth_email_unverified")
	wantError(t, idp.signIn(t, c, "github", "", fakeUser{Sub: "91919", Email: email, Verified: false}), "oauth_email_unverified")
	if status, _ := whoami(t, c); status == 200 {
		t.Fatal("an unverified address signed in")
	}
	var n int
	_ = testDB(t).QueryRow(`SELECT count(*) FROM users WHERE lower(email) = $1`, email).Scan(&n)
	if n != 0 {
		t.Fatal("an unverified address created an account")
	}

	// A forged id_token: wrong nonce, or minted for another client.
	wantError(t, idp.signIn(t, c, "google", "", fakeUser{Sub: "g-x", Email: email, Verified: true, Nonce: "not-the-nonce"}), "provider_error")
	wantError(t, idp.signIn(t, c, "google", "", fakeUser{Sub: "g-x", Email: email, Verified: true, Aud: "someone-else"}), "provider_error")
}

func TestOAuthStateAndNext(t *testing.T) {
	ts, idp, cleanup := bootOAuthServer(t)
	defer cleanup()
	email := uniq("state")
	u := fakeUser{Sub: "g-" + email, Email: email, Verified: true}

	// Reuse: the same state (and cookie) a second time is refused.
	c := newClient(t, ts.URL)
	auth := start(t, c, "google", "")
	state := auth.Query().Get("state")
	if _, loc := location(t, c, callbackPath("google", state, idp.authorize("google", auth, u))); loc.Query().Get("oauth_error") != "" {
		t.Fatalf("first use refused: %s", loc)
	}
	c.do("POST", "/api/auth/logout", nil)
	_, loc := location(t, c, callbackPath("google", state, idp.authorize("google", auth, u)))
	wantError(t, loc, "state_invalid")

	// Mismatch: a state started in one browser, completed in another (login CSRF).
	attacker := newClient(t, ts.URL)
	auth = start(t, attacker, "google", "")
	victim := newClient(t, ts.URL)
	_, loc = location(t, victim, callbackPath("google", auth.Query().Get("state"), idp.authorize("google", auth, u)))
	wantError(t, loc, "state_invalid")
	if status, _ := whoami(t, victim); status == 200 {
		t.Fatal("a state from another browser signed the victim in")
	}
	// …and it is spent: the attacker's own browser cannot use it afterwards either.
	_, loc = location(t, attacker, callbackPath("google", auth.Query().Get("state"), idp.authorize("google", auth, u)))
	wantError(t, loc, "state_invalid")

	// A made-up state, and a state for the other provider.
	_, loc = location(t, c, callbackPath("google", "made-up", "x"))
	wantError(t, loc, "state_invalid")
	auth = start(t, c, "github", "")
	_, loc = location(t, c, callbackPath("google", auth.Query().Get("state"), idp.authorize("google", auth, u)))
	wantError(t, loc, "state_invalid")

	// next: a same-origin path is honoured; anything else falls back to the home page.
	for next, want := range map[string]string{
		"/en/t/somewhere?x=1":  "/en/t/somewhere?x=1",
		"//evil.example/x":     "/en",
		"https://evil.example": "/en",
		"/\\evil.example":      "/en",
		"/%2F%2Fevil.example":  "/en",
		"javascript:alert(1)":  "/en",
	} {
		c := newClient(t, ts.URL)
		loc := idp.signIn(t, c, "google", url.Values{"next": {next}}.Encode(), u)
		if got := loc.String(); got != want {
			t.Errorf("next=%q -> %q, want %q", next, got, want)
		}
	}
}

func TestOAuthLinkDisabledAndTwoFactor(t *testing.T) {
	ts, idp, cleanup := bootOAuthServer(t)
	defer cleanup()
	sqlDB := testDB(t)

	// An existing password account: a verified Google address with the same email signs into it
	// and links the identity.
	email := uniq("pw")
	c := newClient(t, ts.URL)
	c.register(email)
	_, before := whoami(t, c)
	g := newClient(t, ts.URL)
	if loc := idp.signIn(t, g, "google", "", fakeUser{Sub: "g-" + email, Email: email, Verified: true}); loc.Path != "/en" {
		t.Fatalf("link by email: %s", loc)
	}
	if _, after := whoami(t, g); after.ID != before.ID {
		t.Fatalf("linked into another account: %+v vs %+v", after, before)
	}
	ids := identities(t, c)
	if len(ids.Identities) != 1 || !ids.HasPassword {
		t.Fatalf("identities after the link: %+v", ids)
	}
	// With a password, the identity can go.
	c.mustJSON("DELETE", "/api/me/identities/"+ids.Identities[0].ID, nil, 200, nil)

	// Link while signed in (GitHub), then a second browser signs in with it.
	sub := strconv.FormatInt(time.Now().UnixNano()%1_000_000_000+7, 10)
	if code, _ := location(t, newClient(t, ts.URL), "/api/auth/oauth/github?link=1"); code != http.StatusUnauthorized {
		t.Fatalf("link without a session -> %d (want 401)", code)
	}
	loc := idp.signIn(t, c, "github", "link=1", fakeUser{Sub: sub, Email: uniq("gh-other-address"), Verified: true})
	if loc.Path != "/en/account" || loc.Query().Get("linked") != "github" {
		t.Fatalf("link landed on %s", loc)
	}
	if ids := identities(t, c); len(ids.Identities) != 1 || ids.Identities[0].Provider != "github" {
		t.Fatalf("identities after linking GitHub: %+v", ids)
	}
	other := newClient(t, ts.URL)
	idp.signIn(t, other, "github", "", fakeUser{Sub: sub, Email: "whatever@example.test", Verified: true})
	if _, m := whoami(t, other); m.ID != before.ID {
		t.Fatalf("the linked GitHub identity signed into %+v", m)
	}

	// A GitHub account already linked to somebody else cannot be linked here.
	someone := newClient(t, ts.URL)
	someone.register(uniq("someone"))
	loc = idp.signIn(t, someone, "github", "link=1", fakeUser{Sub: sub, Email: "x@example.test", Verified: true})
	if loc.Path != "/en/account" {
		t.Fatalf("taken identity redirect: %s", loc)
	}
	wantError(t, loc, "identity_taken")

	// Two-factor: the provider is the first factor only.
	secret, _ := enable2FA(t, c)
	tf := newClient(t, ts.URL)
	loc = idp.signIn(t, tf, "github", url.Values{"next": {"/en/t/x"}}.Encode(), fakeUser{Sub: sub, Email: "x@example.test", Verified: true})
	frag, _ := url.ParseQuery(loc.Fragment)
	if loc.Path != "/en/login" || loc.Query().Get("next") != "/en/t/x" || frag.Get("challenge") == "" {
		t.Fatalf("2FA account: %s", loc)
	}
	if status, _ := whoami(t, tf); status == 200 {
		t.Fatal("a 2FA account got a session from the provider alone")
	}
	code, _ := totp.Code(secret, time.Now().Add(30*time.Second)) // the current step was spent confirming the setup
	tf.mustJSON("POST", "/api/auth/2fa/challenge", map[string]any{"challenge": frag.Get("challenge"), "code": code}, 200, nil)
	if _, m := whoami(t, tf); m.ID != before.ID {
		t.Fatalf("after the challenge: %+v", m)
	}

	// Disabled: refused like a password sign-in.
	if _, err := sqlDB.Exec(`UPDATE users SET disabled_at = now() WHERE id = $1`, before.ID); err != nil {
		t.Fatal(err)
	}
	d := newClient(t, ts.URL)
	wantError(t, idp.signIn(t, d, "github", "", fakeUser{Sub: sub, Email: "x@example.test", Verified: true}), "account_disabled")
	if status, _ := whoami(t, d); status == 200 {
		t.Fatal("a disabled account signed in")
	}

	// An address somebody registered without ever verifying it: the provider proves the inbox,
	// the squatter's password stops working.
	squat := uniq("squat")
	sq := newClient(t, ts.URL)
	sq.mustJSON("POST", "/api/auth/register", map[string]any{"email": squat, "password": sq.pw}, 403, nil)
	owner := newClient(t, ts.URL)
	if loc := idp.signIn(t, owner, "google", "", fakeUser{Sub: "g-" + squat, Email: squat, Verified: true}); loc.Path != "/en" {
		t.Fatalf("claiming an unverified account: %s", loc)
	}
	if status, _ := sq.do("POST", "/api/auth/login", map[string]any{"email": squat, "password": sq.pw}); status == 200 {
		t.Fatal("the squatter's password still signs in after the owner proved the address")
	}
	if ids := identities(t, owner); ids.HasPassword {
		t.Fatalf("the squatter's password was kept: %+v", ids)
	}

	// Every sign-in and link was audited, with no team.
	var n int
	tx, err := sqlDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()                                                   //nolint:errcheck
	_, _ = tx.Exec(`SELECT set_config('app.platform_admin', 'on', true)`) // platform-level entries are read by administration
	_ = tx.QueryRow(`SELECT count(*) FROM activity_log WHERE causer_id = $1 AND action IN ('auth.oauth_login', 'auth.oauth_linked') AND team_id IS NULL`, before.ID).Scan(&n)
	if n < 4 {
		t.Fatalf("audit entries: %d", n)
	}
}
