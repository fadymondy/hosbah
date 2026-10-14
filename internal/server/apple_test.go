package server

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/fadymondy/hosbah/internal/account"
)

/*
Sign in with Apple (web form_post and native), the phone app's round trip (?app=1 → hosbah://oauth
→ POST /api/auth/oauth/exchange) and self-service account deletion, against the fake IdP in
oauth_test.go extended with a fake Apple: a token endpoint that checks the ES256 client secret and
answers RS256 id_tokens (verified through the same fake JWKS), and a revocation endpoint.
*/

const (
	fakeAppleServices = "com.fadymondy.hosbah.web"
	fakeAppleBundle   = "com.fadymondy.hosbah"
	fakeAppleTeam     = "TEAM123456"
	fakeAppleKeyID    = "KEY1234567"
)

func (f *fakeIdP) appleRoutes(mux *http.ServeMux) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.t.Fatal(err)
	}
	f.appleKey = key
	// secretOK: Hosbah's client secret is a JWT signed with the team's key, for clientID.
	secretOK := func(r *http.Request) bool {
		clientID := r.PostForm.Get("client_id")
		_, err := jwt.Parse(r.PostForm.Get("client_secret"), func(t *jwt.Token) (any, error) {
			if t.Header["kid"] != fakeAppleKeyID {
				return nil, errors.New("kid")
			}
			return &key.PublicKey, nil
		}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithAudience("https://appleid.apple.com"), jwt.WithIssuer(fakeAppleTeam),
			jwt.WithSubject(clientID), jwt.WithExpirationRequired())
		return err == nil
	}
	mux.HandleFunc("/apple/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		clientID := r.PostForm.Get("client_id")
		f.mu.Lock()
		fc, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		f.mu.Unlock()
		web := clientID == fakeAppleServices
		if !secretOK(r) || !ok || fc.provider != "apple" || (!web && clientID != fakeAppleBundle) ||
			web != strings.HasSuffix(r.PostForm.Get("redirect_uri"), "/api/auth/oauth/apple/callback") {
			f.t.Logf("fake Apple refused a token request: known=%v client=%q", ok, clientID)
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		aud := clientID
		if fc.user.Aud != "" {
			aud = fc.user.Aud
		}
		nonce := fc.nonce
		if fc.user.Nonce != "" {
			nonce = fc.user.Nonce
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "a.x", "token_type": "Bearer",
			"id_token": f.appleIDToken(aud, fc.user, nonce), "refresh_token": "rt-" + fc.user.Sub})
	})
	mux.HandleFunc("/apple/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if !secretOK(r) || r.PostForm.Get("token_type_hint") != "refresh_token" {
			http.Error(w, "invalid_client", 400)
			return
		}
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm.Get("client_id")+" "+r.PostForm.Get("token"))
		f.mu.Unlock()
	})
}

func (f *fakeIdP) appleProvider() *account.Provider {
	p := account.NewAppleProvider(fakeAppleServices, account.AppleConfig{TeamID: fakeAppleTeam, KeyID: fakeAppleKeyID, Key: f.appleKey,
		BundleIDs: []string{fakeAppleBundle}, RevokeURL: f.srv.URL + "/apple/revoke"}, f.srv.Client())
	p.AuthURL, p.TokenURL, p.JWKSURL = "https://appleid.test/auth/authorize", f.srv.URL+"/apple/token", f.srv.URL+"/google/certs"
	return p
}

// appleIDToken is an id_token as Apple signs it (email_verified is a string there).
func (f *fakeIdP) appleIDToken(aud string, u fakeUser, nonce string) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://appleid.apple.com", "aud": aud, "sub": u.Sub,
		"email": u.Email, "email_verified": strconv.FormatBool(u.Verified), "nonce": nonce,
		"iat": time.Now().Unix(), "exp": time.Now().Add(10 * time.Minute).Unix()})
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString(f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// appleCode is the authorization code Apple hands the iOS app alongside its id_token.
func (f *fakeIdP) appleCode(u fakeUser, nonce string) string {
	code := "acode-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	f.mu.Lock()
	f.codes[code] = fakeCode{user: u, nonce: nonce, provider: "apple"}
	f.mu.Unlock()
	return code
}

func (f *fakeIdP) revokedTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.revoked...)
}

// appleSignIn is the web round trip: start, approve at Apple, Apple's form_post back (no CSRF token,
// as from Apple's page), then the GET it is turned into. It returns where that GET sent the browser.
func (f *fakeIdP) appleSignIn(t *testing.T, c *client, query string, u fakeUser) *url.URL {
	t.Helper()
	status, auth := location(t, c, "/api/auth/oauth/apple?"+query)
	q := auth.Query()
	if status != http.StatusSeeOther || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("response_mode") != "form_post" ||
		q.Get("scope") != "name email" || q.Get("client_id") != fakeAppleServices || q.Has("code_challenge") {
		t.Fatalf("Apple start -> %d %s", status, auth)
	}
	resp, err := noFollow(c).PostForm(c.base+"/api/auth/oauth/apple/callback", url.Values{"state": {q.Get("state")}, "code": {f.authorize("apple", auth, u)}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	back, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || back == nil || back.Path != "/api/auth/oauth/apple/callback" {
		t.Fatalf("Apple form_post -> %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	status, loc := location(t, c, back.RequestURI())
	if status != http.StatusSeeOther {
		t.Fatalf("Apple callback -> %d", status)
	}
	return loc
}

// plain is a request as the phone app sends it: no cookies, no CSRF token, an optional bearer token.
func plain(t *testing.T, base, method, path, token string, body any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

type sessionOut struct {
	Token string `json:"token"`
	User  struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
}

func bearerMe(t *testing.T, base, token string) (int, meOut) {
	t.Helper()
	status, text := plain(t, base, "GET", "/api/auth/me", token, nil)
	var m meOut
	_ = json.Unmarshal([]byte(text), &m)
	return status, m
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestAppleWebAndNative(t *testing.T) {
	ts, idp, cleanup := bootOAuthServer(t)
	defer cleanup()

	// Web: a new account from Apple's form_post round trip.
	email := uniq("apple-web")
	u := fakeUser{Sub: "a-" + email, Email: email, Verified: true}
	c := newClient(t, ts.URL)
	if loc := idp.appleSignIn(t, c, "locale=en", u); loc.Path != "/en" || loc.Query().Get("oauth_error") != "" {
		t.Fatalf("Apple web sign-in landed on %s", loc)
	}
	_, web := whoami(t, c)
	if web.Email != email {
		t.Fatalf("Apple web sign-in: %+v", web)
	}
	// A forged id_token (another round trip's nonce, another client) is refused.
	wantError(t, idp.appleSignIn(t, newClient(t, ts.URL), "", fakeUser{Sub: u.Sub, Email: email, Verified: true, Nonce: "other"}), "provider_error")
	wantError(t, idp.appleSignIn(t, newClient(t, ts.URL), "", fakeUser{Sub: u.Sub, Email: email, Verified: true, Aud: "com.example.other"}), "provider_error")

	// Native: the same Apple account (Apple's subject is per team, so the app sees the same one).
	raw := "raw-nonce-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hashed := sha256Hex(raw)
	native := func(idToken, nonce, code string) (int, string) {
		return plain(t, ts.URL, "POST", "/api/auth/apple/native", "", map[string]any{"id_token": idToken, "nonce": nonce, "authorization_code": code})
	}
	status, text := native(idp.appleIDToken(fakeAppleBundle, u, hashed), raw, idp.appleCode(u, hashed))
	var s sessionOut
	_ = json.Unmarshal([]byte(text), &s)
	if status != 200 || s.Token == "" || s.User.ID != web.ID {
		t.Fatalf("native Apple sign-in -> %d %s (want user %s)", status, text, web.ID)
	}
	if status, m := bearerMe(t, ts.URL, s.Token); status != 200 || m.Email != email {
		t.Fatalf("the native session: %d %+v", status, m)
	}

	refused := map[string][3]string{
		"the raw nonce does not match":    {idp.appleIDToken(fakeAppleBundle, u, hashed), "another-nonce", idp.appleCode(u, hashed)},
		"an id_token for the web client":  {idp.appleIDToken(fakeAppleServices, u, hashed), raw, idp.appleCode(u, hashed)},
		"another app's id_token":          {idp.appleIDToken("com.example.other", u, hashed), raw, idp.appleCode(u, hashed)},
		"a code for another Apple person": {idp.appleIDToken(fakeAppleBundle, u, hashed), raw, idp.appleCode(fakeUser{Sub: "someone-else", Email: email, Verified: true}, hashed)},
		"a made-up code":                  {idp.appleIDToken(fakeAppleBundle, u, hashed), raw, "not-a-code"},
	}
	for name, in := range refused {
		if status, text := native(in[0], in[1], in[2]); status != 401 || !strings.Contains(text, "provider_error") {
			t.Errorf("%s -> %d %s (want 401 provider_error)", name, status, text)
		}
	}
	// A code works once.
	code := idp.appleCode(u, hashed)
	if status, _ := native(idp.appleIDToken(fakeAppleBundle, u, hashed), raw, code); status != 200 {
		t.Fatalf("fresh code -> %d", status)
	}
	if status, _ := native(idp.appleIDToken(fakeAppleBundle, u, hashed), raw, code); status != 401 {
		t.Fatalf("replayed code -> %d (want 401)", status)
	}
	// Apple's relay addresses are verified; an unverified address never makes an account.
	nv := uniq("apple-unverified")
	nu := fakeUser{Sub: "a-" + nv, Email: nv, Verified: false}
	if status, text := native(idp.appleIDToken(fakeAppleBundle, nu, hashed), raw, idp.appleCode(nu, hashed)); status != 422 || !strings.Contains(text, "oauth_email_unverified") {
		t.Fatalf("unverified native -> %d %s", status, text)
	}
}

func TestOAuthAppHandoff(t *testing.T) {
	ts, idp, cleanup := bootOAuthServer(t)
	defer cleanup()
	email := uniq("app")
	u := fakeUser{Sub: "g-" + email, Email: email, Verified: true}
	verifier := "app-verifier-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-0123456789abcdef"
	appQuery := url.Values{"app": {"1"}, "app_challenge": {sha256Hex(verifier)}, "locale": {"ar"}}.Encode()
	handoff := func(loc *url.URL) string {
		t.Helper()
		if loc.Scheme != "hosbah" || loc.Host != "oauth" || loc.Query().Get("code") == "" {
			t.Fatalf("the app's round trip landed on %s", loc)
		}
		return loc.Query().Get("code")
	}

	// The browser sheet ends at hosbah://oauth?code=…; the browser itself is not signed in.
	browser := newClient(t, ts.URL)
	code := handoff(idp.signIn(t, browser, "google", appQuery, u))
	if status, _ := whoami(t, browser); status == 200 {
		t.Fatal("the app's round trip left a session in the browser")
	}
	// A wrong verifier is refused, and spends the code.
	exchange := func(code, verifier string) (int, string) {
		return plain(t, ts.URL, "POST", "/api/auth/oauth/exchange", "", map[string]any{"code": code, "code_verifier": verifier})
	}
	if status, text := exchange(code, verifier+"-wrong"); status != 400 || !strings.Contains(text, "state_invalid") {
		t.Fatalf("wrong verifier -> %d %s", status, text)
	}
	if status, _ := exchange(code, verifier); status != 400 {
		t.Fatalf("a code survived a wrong verifier -> %d", status)
	}

	// The real thing: the app trades the code for its session, once.
	code = handoff(idp.signIn(t, browser, "google", appQuery, u))
	status, text := exchange(code, verifier)
	var s sessionOut
	_ = json.Unmarshal([]byte(text), &s)
	if status != 200 || s.Token == "" || s.User.Email != email {
		t.Fatalf("exchange -> %d %s", status, text)
	}
	if status, m := bearerMe(t, ts.URL, s.Token); status != 200 || m.Email != email {
		t.Fatalf("the app's session: %d %+v", status, m)
	}
	if status, _ := exchange(code, verifier); status != 400 {
		t.Fatalf("replayed code -> %d", status)
	}

	// Apple through the browser (Android): form_post, then the same hand-off.
	ae := uniq("app-apple")
	code = handoff(idp.appleSignIn(t, newClient(t, ts.URL), appQuery, fakeUser{Sub: "a-" + ae, Email: ae, Verified: true}))
	if status, text := exchange(code, verifier); status != 200 {
		t.Fatalf("Apple app exchange -> %d %s", status, text)
	}

	// Failures land in the app too, never on the web sign-in page.
	appErr := func(loc *url.URL, want string) {
		t.Helper()
		if loc.Scheme != "hosbah" || loc.Query().Get("error") != want {
			t.Fatalf("app failure landed on %s (want error=%s)", loc, want)
		}
	}
	nv := uniq("app-unverified")
	appErr(idp.signIn(t, newClient(t, ts.URL), "google", appQuery, fakeUser{Sub: "g-" + nv, Email: nv, Verified: false}), "oauth_email_unverified")
	_, loc := location(t, browser, "/api/auth/oauth/google?app=1&app_challenge=short")
	appErr(loc, "state_invalid")
	auth := start(t, browser, "google", appQuery)
	_, loc = location(t, browser, callbackPath("google", auth.Query().Get("state"), "")+"&error=access_denied")
	if loc.Scheme != "hosbah" || loc.Query().Get("error") == "" {
		t.Fatalf("a cancelled app round trip landed on %s", loc)
	}

	// The marker does not stick: the next web sign-in in the same browser ends on the web.
	if loc := idp.signIn(t, browser, "google", "", u); loc.Path != "/en" {
		t.Fatalf("a web sign-in after the app's landed on %s", loc)
	}
}

func TestDeleteAccount(t *testing.T) {
	ts, idp, cleanup := bootOAuthServer(t)
	defer cleanup()
	sqlDB := testDB(t)

	// A password account: the password is the proof; a sole-owned team still running a server blocks.
	pwEmail := uniq("del-pw")
	pw := newClient(t, ts.URL)
	pw.register(pwEmail)
	var busy struct{ ID, Slug, Name string }
	pw.mustJSON("POST", "/api/teams", map[string]any{"name": "Busy " + pwEmail[:12]}, 201, &busy)
	pw.mustJSON("POST", "/api/teams/"+busy.ID+"/servers", map[string]any{"name": "del-srv", "provider": "byo"}, 201, nil)
	pw.expectCode("POST", "/api/me/delete", map[string]any{"password": "not it"}, 422, "invalid_credentials")
	pw.expectCode("POST", "/api/me/delete", map[string]any{"confirm": pwEmail}, 422, "invalid_credentials")
	status, text := pw.do("POST", "/api/me/delete", map[string]any{"password": pw.pw})
	if status != 409 || !strings.Contains(text, "team_has_resources") || !strings.Contains(text, busy.ID) {
		t.Fatalf("sole owner of a busy team -> %d %s", status, text)
	}
	if status, _ := whoami(t, pw); status != 200 {
		t.Fatal("a refused deletion signed the account out")
	}
	if status, _ := plain(t, ts.URL, "POST", "/api/me/delete", "", map[string]any{"password": pw.pw}); status != 401 && status != 403 {
		t.Fatalf("deletion without a session -> %d", status)
	}

	// An Apple account from the phone: no password, so the address is typed; its empty team goes
	// with it and Apple is told to forget the grant.
	email := uniq("del-apple")
	u := fakeUser{Sub: "a-" + email, Email: email, Verified: true}
	raw := "raw-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	app := newClient(t, ts.URL)
	var s sessionOut
	app.mustJSON("POST", "/api/auth/apple/native", map[string]any{"id_token": idp.appleIDToken(fakeAppleBundle, u, sha256Hex(raw)), "nonce": raw,
		"authorization_code": idp.appleCode(u, sha256Hex(raw))}, 200, &s)
	var team struct{ ID string }
	status, text = plain(t, ts.URL, "POST", "/api/teams", s.Token, map[string]any{"name": "Empty " + email[:12]})
	if status != 201 || json.Unmarshal([]byte(text), &team) != nil || team.ID == "" {
		t.Fatalf("team over the bearer session -> %d %s", status, text)
	}
	if status, text := plain(t, ts.URL, "POST", "/api/me/delete", s.Token, map[string]any{"confirm": "someone@example.test"}); status != 422 || !strings.Contains(text, "confirm_mismatch") {
		t.Fatalf("wrong confirmation -> %d %s", status, text)
	}
	if status, text := plain(t, ts.URL, "POST", "/api/me/delete", s.Token, map[string]any{"confirm": strings.ToUpper(email)}); status != 200 || !strings.Contains(text, "deleted") {
		t.Fatalf("delete -> %d %s", status, text)
	}
	if status, _ := bearerMe(t, ts.URL, s.Token); status == 200 {
		t.Fatal("the deleted account's session still works")
	}
	if status, _ := whoami(t, app); status == 200 {
		t.Fatal("the deleted account's cookie session still works")
	}
	var n int
	_ = sqlDB.QueryRow(`SELECT count(*) FROM users WHERE id = $1`, s.User.ID).Scan(&n)
	if n != 0 {
		t.Fatal("the user row is still there")
	}
	tx, err := sqlDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tx.Exec(`SELECT set_config('app.platform_admin', 'on', true)`)
	_ = tx.QueryRow(`SELECT count(*) FROM teams WHERE id = $1`, team.ID).Scan(&n)
	_ = tx.Rollback()
	if n != 0 {
		t.Fatal("the deleted account's empty team is still there")
	}
	found := false
	for _, r := range idp.revokedTokens() {
		found = found || r == fakeAppleBundle+" rt-"+u.Sub
	}
	if !found {
		t.Fatalf("Apple was not asked to revoke the grant: %v", idp.revokedTokens())
	}

	// Signing in with the same Apple account again makes a brand-new account.
	raw2 := raw + "-2"
	var again sessionOut
	newClient(t, ts.URL).mustJSON("POST", "/api/auth/apple/native", map[string]any{"id_token": idp.appleIDToken(fakeAppleBundle, u, sha256Hex(raw2)), "nonce": raw2,
		"authorization_code": idp.appleCode(u, sha256Hex(raw2))}, 200, &again)
	if again.User.ID == "" || again.User.ID == s.User.ID {
		t.Fatalf("after deletion the Apple account signed into %+v", again.User)
	}
}
