package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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
Sign in with Google, Apple and GitHub (internal/account/oauth.go, apple.go, native.go) end to end,
against a fake Google (a token endpoint that signs id_tokens with a test RSA key, and its JWKS), a
fake Apple (token endpoint that checks the ES256 client secret, JWKS, revoke) and a fake GitHub
(token endpoint, /user, /user/emails). The browser is a cookie jar that does not follow redirects, so every
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
	// Apple: a "Hide My Email" relay address.
	Private bool
}

type fakeIdP struct {
	t      *testing.T
	key    *rsa.PrivateKey
	ec     *ecdsa.PrivateKey // the Apple key that signs our client secrets
	srv    *httptest.Server
	mu     sync.Mutex
	codes  map[string]fakeCode
	tokens map[string]fakeUser // GitHub access token → user
	// Apple refresh tokens revoked through /apple/auth/revoke.
	revoked []string
}

type fakeCode struct {
	user      fakeUser
	challenge string // the PKCE challenge the start sent; the code_verifier must hash to it
	nonce     string
	provider  string
	client    string // Apple: the client id the code was issued to (Services ID or bundle id)
}

const (
	fakeGoogleClient    = "google-client-id"
	fakeGoogleIOSClient = "google-ios-client-id"
	fakeGitHubClient    = "github-client-id"
	fakeAppleServices   = "com.example.hosbah.web"
	fakeAppleBundle     = "com.example.hosbah"
	fakeAppleTeam       = "TEAM123456"
	fakeAppleKeyID      = "KEY1234567"
)

func newFakeIdP(t *testing.T) *fakeIdP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{t: t, key: key, ec: ec, codes: map[string]fakeCode{}, tokens: map[string]fakeUser{}}
	mux := http.NewServeMux()
	jwks := func(w http.ResponseWriter, _ *http.Request) {
		e := big.NewInt(int64(key.PublicKey.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)}}})
	}
	mux.HandleFunc("/google/certs", jwks)
	mux.HandleFunc("/apple/auth/keys", jwks)
	mux.HandleFunc("/apple/auth/token", func(w http.ResponseWriter, r *http.Request) {
		fc, ok := f.redeemApple(r)
		if !ok {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "a.x", "token_type": "Bearer", "expires_in": "3600",
			"refresh_token": "apple-refresh-" + fc.user.Sub, "id_token": f.appleToken(fc.client, fc.user, fc.nonce, time.Now())})
	})
	mux.HandleFunc("/apple/auth/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if !f.appleSecretOK(r.PostForm.Get("client_secret"), r.PostForm.Get("client_id")) {
			http.Error(w, `{"error":"invalid_client"}`, 400)
			return
		}
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		f.mu.Unlock()
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

// appleSecretOK checks a client secret the way Apple does: ES256 by the team's key (kid), issued by
// the team, for appleid.apple.com, about clientID, unexpired.
func (f *fakeIdP) appleSecretOK(secret, clientID string) bool {
	claims := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(secret, claims, func(t *jwt.Token) (any, error) { return &f.ec.PublicKey, nil },
		jwt.WithValidMethods([]string{"ES256"}), jwt.WithAudience("https://appleid.apple.com"), jwt.WithIssuer(fakeAppleTeam),
		jwt.WithSubject(clientID), jwt.WithExpirationRequired())
	return err == nil && tok.Header["kid"] == fakeAppleKeyID
}

func (f *fakeIdP) redeemApple(r *http.Request) (fakeCode, bool) {
	if err := r.ParseForm(); err != nil {
		return fakeCode{}, false
	}
	f.mu.Lock()
	fc, ok := f.codes[r.PostForm.Get("code")]
	delete(f.codes, r.PostForm.Get("code"))
	f.mu.Unlock()
	web := r.PostForm.Get("redirect_uri") != ""
	if !ok || fc.provider != "apple" || r.PostForm.Get("client_id") != fc.client || !f.appleSecretOK(r.PostForm.Get("client_secret"), fc.client) ||
		(web && !strings.HasSuffix(r.PostForm.Get("redirect_uri"), "/api/auth/oauth/apple/callback")) || (web != (fc.client == fakeAppleServices)) {
		f.t.Logf("fake Apple refused a token request: known=%v client=%q", ok, r.PostForm.Get("client_id"))
		return fakeCode{}, false
	}
	return fc, true
}

// appleToken is an Apple id_token: email_verified and is_private_email as strings, as Apple sends them.
func (f *fakeIdP) appleToken(aud string, u fakeUser, nonce string, iat time.Time) string {
	c := jwt.MapClaims{"iss": "https://appleid.apple.com", "aud": aud, "sub": u.Sub, "email": u.Email,
		"email_verified": strconv.FormatBool(u.Verified), "is_private_email": strconv.FormatBool(u.Private),
		"iat": iat.Unix(), "exp": iat.Add(10 * time.Minute).Unix()}
	if nonce != "" {
		c["nonce"] = nonce
	}
	return f.sign(c)
}

// googleToken is an id_token the phone app's Google SDK would hand over.
func (f *fakeIdP) googleToken(aud string, u fakeUser, nonce string, iat time.Time) string {
	c := jwt.MapClaims{"iss": "https://accounts.google.com", "aud": aud, "sub": u.Sub, "email": u.Email,
		"email_verified": u.Verified, "iat": iat.Unix(), "exp": iat.Add(time.Hour).Unix()}
	if nonce != "" {
		c["nonce"] = nonce
	}
	return f.sign(c)
}

func (f *fakeIdP) sign(c jwt.MapClaims) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString(f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// nativeAppleCode is the authorization code the iOS SDK hands over with its identity token.
func (f *fakeIdP) nativeAppleCode(u fakeUser) string {
	code := fmt.Sprintf("apple-native-%d", time.Now().UnixNano())
	f.mu.Lock()
	f.codes[code] = fakeCode{user: u, provider: "apple", client: fakeAppleBundle}
	f.mu.Unlock()
	return code
}

func (f *fakeIdP) providers() map[string]*account.Provider {
	return map[string]*account.Provider{
		"google": {ID: "google", Name: "Google", ClientID: fakeGoogleClient, ClientSecret: "google-secret",
			NativeAudiences: []string{fakeGoogleIOSClient, fakeGoogleClient},
			AuthURL: "https://accounts.google.test/o/oauth2/v2/auth", TokenURL: f.srv.URL + "/google/token",
			JWKSURL: f.srv.URL + "/google/certs", Issuers: []string{"https://accounts.google.com"}, HTTP: f.srv.Client()},
		"apple": account.NewAppleProvider(fakeAppleServices, []string{fakeAppleBundle}, fakeAppleTeam, fakeAppleKeyID, f.ec, f.srv.URL+"/apple", f.srv.Client()),
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
	if q.Get("state") == "" || (provider != "apple" && (q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256")) {
		t.Fatalf("start %s: the provider URL lacks state or PKCE: %s", path, loc)
	}
	if provider == "apple" && (q.Get("response_mode") != "form_post" || q.Get("scope") != "name email" || q.Get("client_id") != fakeAppleServices) {
		t.Fatalf("Apple start: %s", loc)
	}
	if (provider == "google" || provider == "apple") && q.Get("nonce") == "" {
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
	f.codes[code] = fakeCode{user: u, challenge: auth.Query().Get("code_challenge"), nonce: auth.Query().Get("nonce"), provider: provider,
		client: auth.Query().Get("client_id")}
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
	if len(prov.Providers) != 3 || prov.Providers[0].ID != "google" || prov.Providers[1].ID != "apple" || prov.Providers[2].ID != "github" {
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

/* ------------------------------------------------------------ Apple, the phone app, deletion */

// postForm is Apple's form_post: a cross-site POST, so it carries none of the browser's cookies.
func postForm(t *testing.T, base, path string, form url.Values) (int, *url.URL) {
	t.Helper()
	c := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.PostForm(base+path, form)
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

// appleSignIn runs the web round trip with Apple: start, consent, Apple's POST (no cookies), then the
// browser follows the redirect to the GET of the callback (with its cookies).
func (f *fakeIdP) appleSignIn(t *testing.T, c *client, query string, u fakeUser, user string) *url.URL {
	t.Helper()
	auth := start(t, c, "apple", query)
	code := f.authorize("apple", auth, u)
	status, hop := postForm(t, c.base, "/api/auth/oauth/apple/callback", url.Values{"state": {auth.Query().Get("state")}, "code": {code}, "user": {user}})
	if status != http.StatusSeeOther || hop.Path != "/api/auth/oauth/apple/callback" || hop.Query().Get("code") != "" {
		t.Fatalf("Apple form_post -> %d %s", status, hop)
	}
	status, loc := location(t, c, hop.RequestURI())
	if status != http.StatusSeeOther {
		t.Fatalf("Apple callback GET -> %d", status)
	}
	return loc
}

type identityRow struct {
	Provider, Name, Token string
}

func identityRows(t *testing.T, userID string) []identityRow {
	t.Helper()
	tx, err := testDB(t).Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck
	_, _ = tx.Exec(`SELECT set_config('app.system', 'on', true)`)
	rows, err := tx.Query(`SELECT provider, display_name, provider_token FROM user_identities WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []identityRow
	for rows.Next() {
		var r identityRow
		_ = rows.Scan(&r.Provider, &r.Name, &r.Token)
		out = append(out, r)
	}
	return out
}

func TestOAuthAppleWeb(t *testing.T) {
	ts, idp, cleanup := bootOAuthServer(t)
	defer cleanup()

	// First consent: Apple POSTs the code and the name; the name is kept, the refresh token sealed.
	email := uniq("apple-web")
	sub := "001234." + strconv.FormatInt(time.Now().UnixNano(), 16)
	c := newClient(t, ts.URL)
	loc := idp.appleSignIn(t, c, "locale=ar", fakeUser{Sub: sub, Email: email, Verified: true}, `{"name":{"firstName":"Fady","lastName":"Mondy"},"email":"`+email+`"}`)
	if loc.Path != "/ar" || loc.Query().Get("oauth_error") != "" {
		t.Fatalf("Apple sign-in landed on %s", loc)
	}
	_, me := whoami(t, c)
	if me.Email != email {
		t.Fatalf("signed in as %+v", me)
	}
	rows := identityRows(t, me.ID)
	if len(rows) != 1 || rows[0].Provider != "apple" || rows[0].Name != "Fady Mondy" || rows[0].Token == "" || strings.Contains(rows[0].Token, "apple-refresh") {
		t.Fatalf("Apple identity: %+v", rows)
	}
	// Later sign-ins send no name; the first one stays.
	c2 := newClient(t, ts.URL)
	if loc := idp.appleSignIn(t, c2, "", fakeUser{Sub: sub, Email: email, Verified: true}, ""); loc.Path != "/en" {
		t.Fatalf("second Apple sign-in: %s", loc)
	}
	if rows := identityRows(t, me.ID); rows[0].Name != "Fady Mondy" {
		t.Fatalf("the name was lost: %+v", rows)
	}

	// The POST alone signs nobody in, and a browser without the start's cookie cannot finish it
	// (login CSRF through an auto-submitted form).
	attacker := newClient(t, ts.URL)
	auth := start(t, attacker, "apple", "")
	code := idp.authorize("apple", auth, fakeUser{Sub: sub, Email: email, Verified: true})
	_, hop := postForm(t, ts.URL, "/api/auth/oauth/apple/callback", url.Values{"state": {auth.Query().Get("state")}, "code": {code}})
	victim := newClient(t, ts.URL)
	_, loc = location(t, victim, hop.RequestURI())
	wantError(t, loc, "state_invalid")
	if status, _ := whoami(t, victim); status == 200 {
		t.Fatal("Apple's POST signed another browser in")
	}
	// A second POST for the same state cannot replace the code, and a code in the GET is ignored.
	auth = start(t, c2, "apple", "")
	st := auth.Query().Get("state")
	good := idp.authorize("apple", auth, fakeUser{Sub: sub, Email: email, Verified: true})
	if s, _ := postForm(t, ts.URL, "/api/auth/oauth/apple/callback", url.Values{"state": {st}, "code": {good}}); s != http.StatusSeeOther {
		t.Fatal("first POST refused")
	}
	if _, l := postForm(t, ts.URL, "/api/auth/oauth/apple/callback", url.Values{"state": {st}, "code": {"injected"}}); l.Query().Get("oauth_error") != "state_invalid" {
		t.Fatalf("a second POST was accepted: %s", l)
	}
	_, loc = location(t, c2, "/api/auth/oauth/apple/callback?"+url.Values{"state": {st}, "code": {"injected"}}.Encode())
	if loc.Path != "/en" {
		t.Fatalf("the stashed code did not finish the trip: %s", loc)
	}
	// Cancelled at Apple.
	auth = start(t, c2, "apple", "")
	_, hop = postForm(t, ts.URL, "/api/auth/oauth/apple/callback", url.Values{"state": {auth.Query().Get("state")}, "error": {"user_cancelled_authorize"}})
	_, loc = location(t, c2, hop.RequestURI())
	wantError(t, loc, "denied")

	// Hide My Email: a relay address is its own account. It never matches the person's real address.
	real := uniq("apple-real")
	owner := newClient(t, ts.URL)
	owner.register(real)
	relay := strings.TrimSuffix(strings.Split(real, "@")[0], "") + "@privaterelay.appleid.com"
	r := newClient(t, ts.URL)
	idp.appleSignIn(t, r, "", fakeUser{Sub: "relay-" + sub, Email: relay, Verified: true, Private: true}, "")
	_, ownerMe := whoami(t, owner)
	_, relayMe := whoami(t, r)
	if relayMe.ID == "" || relayMe.ID == ownerMe.ID || relayMe.Email != relay {
		t.Fatalf("relay address matched another account: %+v vs %+v", relayMe, ownerMe)
	}

	// Connected accounts offer Apple, and the identity shows its name.
	ids := identities(t, c)
	if len(ids.Providers) != 3 || ids.Identities[0].Provider != "apple" {
		t.Fatalf("identities: %+v", ids)
	}
}

type loginOut struct {
	Token string `json:"token"`
	User  struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
	Code      string `json:"code"`
	Challenge string `json:"challenge"`
}

func postJSON(t *testing.T, base, path string, body any, bearer string) (int, loginOut, string) {
	t.Helper()
	h := map[string]string{}
	if bearer != "" {
		h["Authorization"] = "Bearer " + bearer
	}
	status, text := rawPost(t, base+path, body, h)
	var out loginOut
	_ = json.Unmarshal([]byte(text), &out)
	return status, out, text
}

func bearerDo(t *testing.T, base, method, path, token string, body any) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, base+path, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := new(strings.Builder)
	_, _ = io.Copy(out, resp.Body)
	return resp.StatusCode, out.String()
}

func TestOAuthNative(t *testing.T) {
	ts, idp, cleanup := bootOAuthServer(t)
	defer cleanup()
	now := time.Now()

	var prov struct {
		Providers []struct {
			ID       string `json:"id"`
			Web      bool   `json:"web"`
			Native   bool   `json:"native"`
			ClientID string `json:"client_id"`
		} `json:"providers"`
	}
	newClient(t, ts.URL).mustJSON("GET", "/api/auth/providers", nil, 200, &prov)
	if !prov.Providers[0].Native || prov.Providers[0].ClientID != fakeGoogleClient || !prov.Providers[1].Native || prov.Providers[2].Native || !prov.Providers[2].Web {
		t.Fatalf("providers: %+v", prov)
	}

	// Google: an id_token for the iOS client, with the app's nonce.
	email := uniq("native-g")
	u := fakeUser{Sub: "gn-" + email, Email: email, Verified: true}
	status, out, text := postJSON(t, ts.URL, "/api/auth/oauth/google/native", map[string]any{"id_token": idp.googleToken(fakeGoogleIOSClient, u, "n0nce", now), "nonce": "n0nce"}, "")
	if status != 200 || out.Token == "" || out.User.Email != email {
		t.Fatalf("Google native -> %d %s", status, text)
	}
	// The session works like a password sign-in's: the app registers the device with it.
	if s, body := bearerDo(t, ts.URL, "POST", "/api/me/devices", out.Token, map[string]any{"name": "Pixel", "platform": "android", "app": "mobile"}); s != 201 {
		t.Fatalf("register device -> %d %s", s, body)
	}
	for name, tok := range map[string]string{
		"another client":  idp.googleToken("someone-else", u, "", now),
		"too old":         idp.googleToken(fakeGoogleIOSClient, u, "", now.Add(-20*time.Minute)),
		"wrong nonce":     idp.googleToken(fakeGoogleIOSClient, u, "other", now),
		"not even a JWT":  "abc.def.ghi",
	} {
		body := map[string]any{"id_token": tok}
		if name == "wrong nonce" {
			body["nonce"] = "n0nce"
		}
		if s, out, _ := postJSON(t, ts.URL, "/api/auth/oauth/google/native", body, ""); s != 401 || out.Code != "provider_error" {
			t.Errorf("%s: %d %+v", name, s, out)
		}
	}
	unv := fakeUser{Sub: "gn-unv", Email: uniq("native-unv"), Verified: false}
	if s, out, _ := postJSON(t, ts.URL, "/api/auth/oauth/google/native", map[string]any{"id_token": idp.googleToken(fakeGoogleClient, unv, "", now)}, ""); s != 403 || out.Code != "oauth_email_unverified" {
		t.Fatalf("unverified Google address: %d %+v", s, out)
	}
	if s, out, _ := postJSON(t, ts.URL, "/api/auth/oauth/github/native", map[string]any{"id_token": "x"}, ""); s != 404 || out.Code != "provider_unavailable" {
		t.Fatalf("GitHub has no native sign-in: %d %+v", s, out)
	}

	// Apple: the identity token (audience the bundle id, nonce hashed by the app) and its code,
	// redeemed at Apple; the name from the SDK is kept with the refresh token.
	aemail := uniq("native-a")
	au := fakeUser{Sub: "an-" + strconv.FormatInt(now.UnixNano(), 16), Email: aemail, Verified: true}
	sum := sha256.Sum256([]byte("raw-nonce"))
	idTok := idp.appleToken(fakeAppleBundle, au, fmt.Sprintf("%x", sum), now)
	status, out, text = postJSON(t, ts.URL, "/api/auth/oauth/apple/native", map[string]any{"id_token": idTok, "authorization_code": idp.nativeAppleCode(au),
		"nonce": "raw-nonce", "full_name": map[string]string{"given_name": "Nour", "family_name": "Hassan"}}, "")
	if status != 200 || out.User.Email != aemail {
		t.Fatalf("Apple native -> %d %s", status, text)
	}
	if rows := identityRows(t, out.User.ID); len(rows) != 1 || rows[0].Name != "Nour Hassan" || rows[0].Token == "" {
		t.Fatalf("Apple native identity: %+v", rows)
	}
	// Without a code Apple redeems (a replayed identity token), nothing.
	if s, out, _ := postJSON(t, ts.URL, "/api/auth/oauth/apple/native", map[string]any{"id_token": idTok, "authorization_code": "spent", "nonce": "raw-nonce"}, ""); s != 401 {
		t.Fatalf("Apple native with a bad code: %d %+v", s, out)
	}
	// An identity token for the web Services ID is not the app's.
	if s, _, _ := postJSON(t, ts.URL, "/api/auth/oauth/apple/native", map[string]any{"id_token": idp.appleToken(fakeAppleServices, au, "", now), "authorization_code": idp.nativeAppleCode(au)}, ""); s != 401 {
		t.Fatalf("Apple native for the web client -> %d", s)
	}

	// Two-factor: the native token is the first factor only.
	pw := newClient(t, ts.URL)
	tfEmail := uniq("native-2fa")
	pw.register(tfEmail)
	secret, _ := enable2FA(t, pw)
	tfu := fakeUser{Sub: "gn-2fa-" + tfEmail, Email: tfEmail, Verified: true}
	status, out, text = postJSON(t, ts.URL, "/api/auth/oauth/google/native", map[string]any{"id_token": idp.googleToken(fakeGoogleClient, tfu, "", now)}, "")
	if status != 401 || out.Code != "2fa_required" || out.Challenge == "" || out.Token != "" {
		t.Fatalf("2FA account via native Google: %d %s", status, text)
	}
	totpCode, _ := totp.Code(secret, time.Now().Add(30*time.Second))
	if s, out, text := postJSON(t, ts.URL, "/api/auth/2fa/challenge", map[string]any{"challenge": out.Challenge, "code": totpCode}, ""); s != 200 || out.Token == "" {
		t.Fatalf("challenge: %d %s", s, text)
	}
}

func pkce() (verifier, challenge string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// appTrip runs the phone app's browser round trip and returns the deep link the callback answered.
func (f *fakeIdP) appTrip(t *testing.T, c *client, provider, challenge string, u fakeUser) *url.URL {
	t.Helper()
	q := url.Values{"app": {"1"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}.Encode()
	if provider == "apple" {
		return f.appleSignIn(t, c, q, u, "")
	}
	return f.signIn(t, c, provider, q, u)
}

func TestOAuthAppPKCE(t *testing.T) {
	ts, idp, cleanup := bootOAuthServer(t)
	defer cleanup()
	email := uniq("app-gh")
	sub := strconv.FormatInt(time.Now().UnixNano()%1_000_000_000+11, 10)
	u := fakeUser{Sub: sub, Email: email, Verified: true}

	// The start insists on the app's S256 challenge.
	c := newClient(t, ts.URL)
	if _, loc := location(t, c, "/api/auth/oauth/github?app=1&code_challenge=short&code_challenge_method=S256"); loc.Scheme != "hosbah" || loc.Query().Get("error") != "state_invalid" {
		t.Fatalf("bad challenge -> %s", loc)
	}

	verifier, challenge := pkce()
	loc := idp.appTrip(t, c, "github", challenge, u)
	if loc.String()[:len("hosbah://auth/callback?")] != "hosbah://auth/callback?" || loc.Query().Get("code") == "" {
		t.Fatalf("app trip ended at %s", loc)
	}
	// The browser got no session: the app does, by redeeming the code.
	if status, _ := whoami(t, c); status == 200 {
		t.Fatal("the in-app browser was signed in")
	}
	// The wrong verifier spends the code.
	other, _ := pkce()
	if s, out, _ := postJSON(t, ts.URL, "/api/auth/app/token", map[string]any{"code": loc.Query().Get("code"), "code_verifier": other}, ""); s != 401 || out.Code != "state_invalid" {
		t.Fatalf("wrong verifier: %d %+v", s, out)
	}
	if s, _, _ := postJSON(t, ts.URL, "/api/auth/app/token", map[string]any{"code": loc.Query().Get("code"), "code_verifier": verifier}, ""); s != 401 {
		t.Fatalf("a spent code worked: %d", s)
	}
	verifier, challenge = pkce()
	loc = idp.appTrip(t, newClient(t, ts.URL), "github", challenge, u)
	status, out, text := postJSON(t, ts.URL, "/api/auth/app/token", map[string]any{"code": loc.Query().Get("code"), "code_verifier": verifier}, "")
	if status != 200 || out.Token == "" || out.User.Email != email {
		t.Fatalf("redeem -> %d %s", status, text)
	}
	if s, _, _ := postJSON(t, ts.URL, "/api/auth/app/token", map[string]any{"code": loc.Query().Get("code"), "code_verifier": verifier}, ""); s != 401 {
		t.Fatalf("a code worked twice: %d", s)
	}

	// Apple on Android: the same trip through Apple's form_post.
	verifier, challenge = pkce()
	au := fakeUser{Sub: "app-apple-" + sub, Email: uniq("app-apple"), Verified: true}
	loc = idp.appTrip(t, newClient(t, ts.URL), "apple", challenge, au)
	if s, out, text := postJSON(t, ts.URL, "/api/auth/app/token", map[string]any{"code": loc.Query().Get("code"), "code_verifier": verifier}, ""); s != 200 || out.User.Email != au.Email {
		t.Fatalf("Apple app trip: %d %s", s, text)
	}

	// A refusal reaches the app as ?error=.
	_, challenge = pkce()
	loc = idp.appTrip(t, newClient(t, ts.URL), "github", challenge, fakeUser{Sub: "424242", Email: uniq("unv"), Verified: false})
	if loc.Scheme != "hosbah" || loc.Query().Get("error") != "oauth_email_unverified" {
		t.Fatalf("refusal -> %s", loc)
	}
}

func TestDeleteOwnAccount(t *testing.T) {
	ts, idp, cleanup := bootOAuthServer(t)
	defer cleanup()
	sqlDB := testDB(t)
	userGone := func(id string) bool {
		var n int
		_ = sqlDB.QueryRow(`SELECT count(*) FROM users WHERE id = $1`, id).Scan(&n)
		return n == 0
	}

	// A password account that owns a team with another member.
	email := uniq("del-pw")
	c := newClient(t, ts.URL)
	c.register(email)
	_, me := whoami(t, c)
	var team struct{ ID string }
	c.mustJSON("POST", "/api/teams", map[string]any{"name": "Del " + email[:12]}, 201, &team)
	mate := newClient(t, ts.URL)
	mateEmail := uniq("del-mate")
	mate.register(mateEmail)
	_, mateMe := whoami(t, mate)
	exec := func(q string, args ...any) {
		t.Helper()
		tx, err := sqlDB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = tx.Exec(`SELECT set_config('app.platform_admin', 'on', true), set_config('app.team_id', $1, true)`, team.ID)
		if _, err := tx.Exec(q, args...); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'viewer')`, team.ID, mateMe.ID)

	e := c.expectCode("DELETE", "/api/me", map[string]any{}, 403, "reauth_required")
	_ = e
	c.expectCode("DELETE", "/api/me", map[string]any{"password": "wrong password"}, 403, "invalid_password")
	c.expectCode("DELETE", "/api/me", map[string]any{"password": c.pw}, 409, "sole_owner")

	// Only member, but the team still manages a server.
	exec(`DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`, team.ID, mateMe.ID)
	c.mustJSON("POST", "/api/teams/"+team.ID+"/servers", map[string]any{"name": "box", "provider": "byo"}, 201, nil)
	c.expectCode("DELETE", "/api/me", map[string]any{"password": c.pw}, 409, "team_has_servers")
	exec(`DELETE FROM servers WHERE team_id = $1`, team.ID)


	// Now it goes, with the empty team; the session stops working.
	c.mustJSON("DELETE", "/api/me", map[string]any{"password": c.pw}, 200, nil)
	if !userGone(me.ID) {
		t.Fatal("the account is still there")
	}
	var teams int
	tx, _ := sqlDB.Begin()
	_, _ = tx.Exec(`SELECT set_config('app.platform_admin', 'on', true)`)
	_ = tx.QueryRow(`SELECT count(*) FROM teams WHERE id = $1`, team.ID).Scan(&teams)
	_ = tx.Rollback()
	if teams != 0 {
		t.Fatal("the team only the deleted account was in is still there")
	}
	if status, _ := whoami(t, c); status == 200 {
		t.Fatal("the deleted account's session still works")
	}
	if s, _ := c.do("POST", "/api/auth/login", map[string]any{"email": email, "password": c.pw}); s == 200 {
		t.Fatal("the deleted account can sign in")
	}

	// An Apple account from the phone app: no password, so a mailed code (or a fresh Apple sign-in);
	// its Apple grant is revoked.
	now := time.Now()
	au := fakeUser{Sub: "del-apple-" + strconv.FormatInt(now.UnixNano(), 16), Email: uniq("del-apple"), Verified: true}
	status, out, text := postJSON(t, ts.URL, "/api/auth/oauth/apple/native", map[string]any{"id_token": idp.appleToken(fakeAppleBundle, au, "", now), "authorization_code": idp.nativeAppleCode(au)}, "")
	if status != 200 {
		t.Fatalf("Apple native: %d %s", status, text)
	}
	if s, body := bearerDo(t, ts.URL, "DELETE", "/api/me", out.Token, map[string]any{}); s != 403 || !strings.Contains(body, `"has_password":false`) {
		t.Fatalf("no proof: %d %s", s, body)
	}
	// A fresh Google sign-in is not a proof for an account it is not linked to.
	g := fakeUser{Sub: "del-g-" + au.Sub, Email: uniq("del-g"), Verified: true}
	if s, _ := bearerDo(t, ts.URL, "DELETE", "/api/me", out.Token, map[string]any{"provider": "google", "id_token": idp.googleToken(fakeGoogleClient, g, "", now)}); s != 401 {
		t.Fatalf("someone else's Google token as proof -> %d", s)
	}
	before := mailbox.count(au.Email)
	if s, body := bearerDo(t, ts.URL, "POST", "/api/me/delete/code", out.Token, map[string]any{"locale": "en"}); s != 200 {
		t.Fatalf("delete code -> %d %s", s, body)
	}
	code := mailbox.wait(t, au.Email, before)
	if s, _ := bearerDo(t, ts.URL, "DELETE", "/api/me", out.Token, map[string]any{"code": "000000"}); s == 200 {
		t.Fatal("a wrong code deleted the account")
	}
	if s, body := bearerDo(t, ts.URL, "DELETE", "/api/me", out.Token, map[string]any{"code": code}); s != 200 {
		t.Fatalf("delete with the code -> %d %s", s, body)
	}
	if !userGone(out.User.ID) {
		t.Fatal("the Apple account is still there")
	}
	idp.mu.Lock()
	revoked := strings.Join(idp.revoked, ",")
	idp.mu.Unlock()
	if !strings.Contains(revoked, "apple-refresh-"+au.Sub) {
		t.Fatalf("Apple token not revoked: %q", revoked)
	}

	// Re-authenticating with a fresh Apple sign-in to the same Apple account.
	au2 := fakeUser{Sub: "del-apple2-" + strconv.FormatInt(now.UnixNano(), 16), Email: uniq("del-apple2"), Verified: true}
	_, out, _ = postJSON(t, ts.URL, "/api/auth/oauth/apple/native", map[string]any{"id_token": idp.appleToken(fakeAppleBundle, au2, "", now), "authorization_code": idp.nativeAppleCode(au2)}, "")
	if s, body := bearerDo(t, ts.URL, "DELETE", "/api/me", out.Token, map[string]any{"provider": "apple", "id_token": idp.appleToken(fakeAppleBundle, au2, "", time.Now()),
		"authorization_code": idp.nativeAppleCode(au2)}); s != 200 {
		t.Fatalf("delete with a fresh Apple sign-in -> %d %s", s, body)
	}
	if !userGone(out.User.ID) {
		t.Fatal("the second Apple account is still there")
	}
}
