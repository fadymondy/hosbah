package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/fadymondy/hosbah/internal/mail"
	"github.com/fadymondy/hosbah/internal/totp"
)

/*
The account cycle (internal/account) end to end: verification, password reset, sign-in by code,
two-factor, the disabled-account refusal, and the limits. Email goes to mailbox (bootTestServer
sets it as the server's sender) instead of SMTP.
*/

type capturedMail struct {
	To, Subject, Text string
}

type testMailbox struct {
	mu   sync.Mutex
	msgs []capturedMail
}

var mailbox = &testMailbox{}

func (m *testMailbox) send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, capturedMail{To: strings.ToLower(msg.To), Subject: msg.Subject, Text: msg.Text})
	return nil
}

func (m *testMailbox) count(email string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, msg := range m.msgs {
		if msg.To == strings.ToLower(email) {
			n++
		}
	}
	return n
}

var spacedCode = regexp.MustCompile(`\b(\d) (\d) (\d) (\d) (\d) (\d)\b`)

// wait returns the code in the newest message to email once more than `after` have arrived
// (sending is asynchronous).
func (m *testMailbox) wait(t *testing.T, email string, after int) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		var mine []capturedMail
		for _, msg := range m.msgs {
			if msg.To == strings.ToLower(email) {
				mine = append(mine, msg)
			}
		}
		m.mu.Unlock()
		if len(mine) > after {
			g := spacedCode.FindStringSubmatch(mine[len(mine)-1].Text)
			if g == nil {
				t.Fatalf("no code in %q", mine[len(mine)-1].Text)
			}
			return strings.Join(g[1:], "")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no email to %s (had %d)", email, after)
	return ""
}

// settle gives background sends a moment, for assertions that nothing was sent.
func settle() { time.Sleep(150 * time.Millisecond) }

type apiErr struct {
	Error     string `json:"error"`
	Code      string `json:"code"`
	Email     string `json:"email"`
	Challenge string `json:"challenge"`
}

func (c *client) expect(method, path string, body any, want int) apiErr {
	c.t.Helper()
	status, text := c.do(method, path, body)
	if status != want {
		c.t.Fatalf("%s %s -> %d (want %d): %s", method, path, status, want, text)
	}
	var e apiErr
	_ = json.Unmarshal([]byte(text), &e)
	return e
}

func (c *client) expectCode(method, path string, body any, want int, code string) apiErr {
	c.t.Helper()
	e := c.expect(method, path, body, want)
	if e.Code != code {
		c.t.Fatalf("%s %s: code %q, want %q (%s)", method, path, e.Code, code, e.Error)
	}
	return e
}

// rawPost posts without cookies, with extra headers.
func rawPost(t *testing.T, url string, body any, headers map[string]string) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func uniq(prefix string) string {
	return fmt.Sprintf("%s-%d@example.test", prefix, time.Now().UnixNano())
}

func TestRegisterVerifyAndUnverifiedLogin(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	email := uniq("verify")
	c := newClient(t, ts.URL)

	before := mailbox.count(email)
	e := c.expectCode("POST", "/api/auth/register", map[string]any{"email": email, "password": c.pw, "locale": "ar"}, 403, "email_unverified")
	if e.Email != email {
		t.Fatalf("403 should carry the email: %+v", e)
	}
	code := mailbox.wait(t, email, before)
	// The plugin's session was withheld.
	c.expect("GET", "/api/me", nil, 401)

	// Sign-in while unverified: the password is checked first, then 403 and a code — but not a
	// second one within a minute.
	c.expect("POST", "/api/auth/login", map[string]any{"email": email, "password": "wrong password!"}, 401)
	c.expectCode("POST", "/api/auth/login", map[string]any{"email": email, "password": c.pw}, 403, "email_unverified")
	c.expect("GET", "/api/me", nil, 401)
	settle()
	if n := mailbox.count(email); n != before+1 {
		t.Fatalf("a second code went out within the minute: %d mails", n-before)
	}
	// Resend: always 200, and also held to once a minute.
	c.expect("POST", "/api/auth/verify-email/resend", map[string]any{"email": email}, 200)
	c.expect("POST", "/api/auth/verify-email/resend", map[string]any{"email": uniq("nobody")}, 200)

	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	c.expectCode("POST", "/api/auth/verify-email", map[string]any{"email": email, "code": wrong}, 422, "invalid_code")
	var ok struct {
		Token  string `json:"token"`
		Status string `json:"status"`
		User   struct{ ID, Email string }
	}
	c.mustJSON("POST", "/api/auth/verify-email", map[string]any{"email": email, "code": code}, 200, &ok)
	if ok.Token == "" || ok.Status != "verified" || ok.User.ID == "" {
		t.Fatalf("verify answer: %+v", ok)
	}
	c.expect("GET", "/api/me", nil, 200) // the cookie was set
	// A code is spent once.
	newClient(t, ts.URL).expectCode("POST", "/api/auth/verify-email", map[string]any{"email": email, "code": code}, 422, "invalid_code")
	// Verified now: password sign-in works.
	c2 := newClient(t, ts.URL)
	c2.login(email)
	c2.expect("GET", "/api/me", nil, 200)
}

func TestExistingAndInvitedUsersAreVerified(t *testing.T) {
	sqlDB := testDB(t)
	ts, cleanup := bootTestServer(t)
	defer cleanup()

	// An account as it existed before verification: email_unverified_at NULL.
	email := uniq("legacy")
	c := newClient(t, ts.URL)
	hash, _ := bcrypt.GenerateFromPassword([]byte(c.pw), bcrypt.MinCost)
	if _, err := sqlDB.Exec(`INSERT INTO users (id, email, password_hash, roles, permissions, created_at) VALUES ($1,$2,$3,'','',$4)`,
		fmt.Sprintf("legacy%d", time.Now().UnixNano()), email, string(hash), time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	c.login(email)
	c.expect("GET", "/api/me", nil, 200)

	// Signing up with an invitation for the same address: the invitation proved the inbox.
	var team struct{ ID string }
	c.mustJSON("POST", "/api/teams", map[string]any{"name": "Legacy " + fmt.Sprint(time.Now().UnixNano())}, 201, &team)
	invited := uniq("invited")
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	c.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": invited, "role": "viewer"}, 201, &inv)
	token := inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]
	before := mailbox.count(invited)
	ic := newClient(t, ts.URL)
	ic.expect("POST", "/api/auth/register", map[string]any{"email": invited, "password": ic.pw, "invitation_token": token}, 201)
	ic.expect("GET", "/api/me", nil, 200)
	settle()
	if mailbox.count(invited) != before {
		t.Fatal("an invitation sign-up was sent a verification code")
	}
	// A token for another address does not skip verification.
	other := uniq("other")
	oc := newClient(t, ts.URL)
	oc.expectCode("POST", "/api/auth/register", map[string]any{"email": other, "password": oc.pw, "invitation_token": token}, 403, "email_unverified")
}

func TestForgotAndResetPassword(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	email := uniq("reset")
	old := newClient(t, ts.URL)
	old.register(email)
	old.expect("GET", "/api/me", nil, 200)
	// Sessions are compared in whole seconds; the old one must be from an earlier second.
	time.Sleep(1100 * time.Millisecond)

	anon := newClient(t, ts.URL)
	nobody := uniq("nobody")
	anon.expect("POST", "/api/auth/password/forgot", map[string]any{"email": nobody}, 200)
	settle()
	if mailbox.count(nobody) != 0 {
		t.Fatal("a reset code went to an address without an account")
	}
	before := mailbox.count(email)
	anon.expect("POST", "/api/auth/password/forgot", map[string]any{"email": email, "locale": "en"}, 200)
	code := mailbox.wait(t, email, before)

	newPW := "a brand new passphrase"
	anon.expectCode("POST", "/api/auth/password/reset", map[string]any{"email": email, "code": code, "password": "short"}, 422, "invalid_password")
	anon.expect("POST", "/api/auth/password/reset", map[string]any{"email": email, "code": code, "password": newPW}, 200)

	// Every earlier session is signed out; the old password is gone; the new one works at once.
	old.expect("GET", "/api/me", nil, 401)
	anon.expect("POST", "/api/auth/login", map[string]any{"email": email, "password": old.pw}, 401)
	fresh := newClient(t, ts.URL)
	fresh.pw = newPW
	fresh.login(email)
	fresh.expect("GET", "/api/me", nil, 200)
	// The code was spent.
	anon.expectCode("POST", "/api/auth/password/reset", map[string]any{"email": email, "code": code, "password": newPW}, 422, "invalid_code")
}

func TestEmailCodeSignIn(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	email := uniq("codein")
	newClient(t, ts.URL).register(email)

	c := newClient(t, ts.URL)
	nobody := uniq("nobody")
	c.expect("POST", "/api/auth/code/request", map[string]any{"email": nobody}, 200)
	before := mailbox.count(email)
	c.expect("POST", "/api/auth/code/request", map[string]any{"email": email}, 200)
	code := mailbox.wait(t, email, before)
	settle()
	if mailbox.count(nobody) != 0 {
		t.Fatal("a sign-in code went to an address without an account")
	}
	var ok struct {
		Token string
		User  struct{ Email string }
	}
	c.mustJSON("POST", "/api/auth/code/verify", map[string]any{"email": email, "code": code}, 200, &ok)
	if ok.Token == "" || !strings.EqualFold(ok.User.Email, email) {
		t.Fatalf("code sign-in answer: %+v", ok)
	}
	c.expect("GET", "/api/me", nil, 200)
}

type enrolled struct {
	Secret     string `json:"secret"`
	OtpauthURL string `json:"otpauth_url"`
	QR         string `json:"qr"`
}

func enable2FA(t *testing.T, c *client) (secret string, recovery []string) {
	t.Helper()
	var en enrolled
	c.mustJSON("POST", "/api/me/2fa/enroll", map[string]any{}, 200, &en)
	if en.Secret == "" || !strings.HasPrefix(en.QR, "data:image/png;base64,") || !strings.Contains(en.OtpauthURL, "issuer=Hosbah") {
		t.Fatalf("enroll answer: %+v", en)
	}
	c.expectCode("POST", "/api/me/2fa/confirm", map[string]any{"code": "000000"}, 422, "2fa_invalid")
	code, _ := totp.Code(en.Secret, time.Now())
	var conf struct {
		Enabled       bool     `json:"enabled"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
	c.mustJSON("POST", "/api/me/2fa/confirm", map[string]any{"code": code}, 200, &conf)
	if !conf.Enabled || len(conf.RecoveryCodes) != 10 {
		t.Fatalf("confirm answer: %+v", conf)
	}
	for _, rc := range conf.RecoveryCodes {
		if !regexp.MustCompile(`^[a-z2-7]{4}-[a-z2-7]{4}$`).MatchString(rc) {
			t.Fatalf("recovery code format: %q", rc)
		}
	}
	return en.Secret, conf.RecoveryCodes
}

func TestTwoFactorEnrolLoginChallenge(t *testing.T) {
	sqlDB := testDB(t)
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	email := uniq("twofa")
	c := newClient(t, ts.URL)
	c.register(email)

	var st struct {
		Enabled           bool `json:"enabled"`
		RecoveryCodesLeft int  `json:"recovery_codes_left"`
		Available         bool `json:"available"`
	}
	c.mustJSON("GET", "/api/me/2fa", nil, 200, &st)
	if st.Enabled || !st.Available {
		t.Fatalf("status before: %+v", st)
	}
	secret, recovery := enable2FA(t, c)
	c.mustJSON("GET", "/api/me/2fa", nil, 200, &st)
	if !st.Enabled || st.RecoveryCodesLeft != 10 {
		t.Fatalf("status after: %+v", st)
	}
	c.expectCode("POST", "/api/me/2fa/enroll", map[string]any{}, 409, "2fa_already_enabled")

	// The secret is sealed, not stored in the clear.
	var sealed string
	if err := sqlDB.QueryRow(`SELECT secret_sealed FROM account_totp t JOIN users u ON u.id = t.user_id WHERE u.email = $1`, email).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, secret) || !strings.HasPrefix(sealed, "cpv1:") {
		t.Fatalf("secret not sealed: %q", sealed)
	}

	login := func() (*client, string) {
		l := newClient(t, ts.URL)
		e := l.expectCode("POST", "/api/auth/login", map[string]any{"email": email, "password": c.pw}, 401, "2fa_required")
		if e.Challenge == "" {
			t.Fatal("no challenge")
		}
		l.expect("GET", "/api/me", nil, 401) // no session before the second factor
		return l, e.Challenge
	}

	// TOTP. The confirm used the current step, so the next one (within the ±1 window) signs in.
	next, _ := totp.Code(secret, time.Now().Add(totp.Period*time.Second))
	l, ch := login()
	var signed struct {
		Token string
		Extra struct {
			RecoveryCodesLeft int `json:"recovery_codes_left"`
		} `json:"extra"`
	}
	l.mustJSON("POST", "/api/auth/2fa/challenge", map[string]any{"challenge": ch, "code": next}, 200, &signed)
	if signed.Token == "" || signed.Extra.RecoveryCodesLeft != 10 {
		t.Fatalf("challenge answer: %+v", signed)
	}
	l.expect("GET", "/api/me", nil, 200)
	// The challenge is spent.
	l.expectCode("POST", "/api/auth/2fa/challenge", map[string]any{"challenge": ch, "code": next}, 401, "challenge_expired")

	// The same TOTP again: refused.
	l2, ch2 := login()
	l2.expectCode("POST", "/api/auth/2fa/challenge", map[string]any{"challenge": ch2, "code": next}, 422, "2fa_invalid")
	// A recovery code works once.
	l2.mustJSON("POST", "/api/auth/2fa/challenge", map[string]any{"challenge": ch2, "recovery_code": strings.ToUpper(recovery[0])}, 200, &signed)
	if signed.Extra.RecoveryCodesLeft != 9 {
		t.Fatalf("recovery codes left: %+v", signed)
	}
	l3, ch3 := login()
	l3.expectCode("POST", "/api/auth/2fa/challenge", map[string]any{"challenge": ch3, "recovery_code": recovery[0]}, 422, "2fa_invalid")
	// Five wrong answers and the challenge is dead, even for a right one.
	for i := 0; i < 4; i++ {
		l3.expectCode("POST", "/api/auth/2fa/challenge", map[string]any{"challenge": ch3, "code": "000000"}, 422, "2fa_invalid")
	}
	l3.expectCode("POST", "/api/auth/2fa/challenge", map[string]any{"challenge": ch3, "recovery_code": recovery[1]}, 401, "challenge_expired")

	// Sign-in by emailed code also asks for the second factor.
	before := mailbox.count(email)
	cc := newClient(t, ts.URL)
	cc.expect("POST", "/api/auth/code/request", map[string]any{"email": email}, 200)
	code := mailbox.wait(t, email, before)
	cc.expectCode("POST", "/api/auth/code/verify", map[string]any{"email": email, "code": code}, 401, "2fa_required")
	cc.expect("GET", "/api/me", nil, 401)

	// New recovery codes need a (fresh) TOTP; then disabling with a recovery code turns it off.
	c.expectCode("POST", "/api/me/2fa/recovery", map[string]any{"code": next}, 422, "2fa_invalid")
	var fresh struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	// Every code in the ±1 window has been used by now; wind last_step back rather than wait for
	// the clock.
	if _, err := sqlDB.Exec(`UPDATE account_totp SET last_step = $2 FROM users u WHERE u.id = account_totp.user_id AND u.email = $1`,
		email, time.Now().Unix()/totp.Period-2); err != nil {
		t.Fatal(err)
	}
	now, _ := totp.Code(secret, time.Now())
	c.mustJSON("POST", "/api/me/2fa/recovery", map[string]any{"code": now}, 200, &fresh)
	if len(fresh.RecoveryCodes) != 10 {
		t.Fatalf("new recovery codes: %+v", fresh)
	}
	c.expectCode("POST", "/api/me/2fa/disable", map[string]any{"recovery_code": recovery[2]}, 422, "2fa_invalid") // old set
	var off struct {
		Enabled bool `json:"enabled"`
	}
	c.mustJSON("POST", "/api/me/2fa/disable", map[string]any{"recovery_code": fresh.RecoveryCodes[0]}, 200, &off)
	if off.Enabled {
		t.Fatal("still enabled")
	}
	newClient(t, ts.URL).login(email)
}

func TestPluginTOTPIsCarriedOver(t *testing.T) {
	sqlDB := testDB(t)
	// An account with two-factor enabled through the plugin, before the server boots.
	ts0, cleanup0 := bootTestServer(t)
	email := uniq("plugin2fa")
	c := newClient(t, ts0.URL)
	c.register(email)
	cleanup0()
	secret, _ := totp.NewSecret()
	var id string
	if err := sqlDB.QueryRow(`SELECT id FROM users WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO auth_totp (subject, secret, enabled) VALUES ($1,$2,'true')`, id, secret); err != nil {
		t.Fatal(err)
	}

	ts, cleanup := bootTestServer(t)
	defer cleanup()
	var n int
	_ = sqlDB.QueryRow(`SELECT count(*) FROM auth_totp WHERE subject = $1`, id).Scan(&n)
	if n != 0 {
		t.Fatal("the plain-text secret is still in auth_totp")
	}
	l := newClient(t, ts.URL)
	e := l.expectCode("POST", "/api/auth/login", map[string]any{"email": email, "password": c.pw}, 401, "2fa_required")
	code, _ := totp.Code(secret, time.Now())
	l.expect("POST", "/api/auth/2fa/challenge", map[string]any{"challenge": e.Challenge, "code": code}, 200)
	// The plugin's own 2FA routes are closed.
	l.expect("POST", "/api/auth/2fa/enroll", map[string]any{}, 404)
}

func TestDisabledAccountRefusedOnEverySessionPath(t *testing.T) {
	sqlDB := testDB(t)
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	disable := func(email string) {
		if _, err := sqlDB.Exec(`UPDATE users SET disabled_at = now() WHERE email = $1`, email); err != nil {
			t.Fatal(err)
		}
	}

	// Password sign-in and emailed-code sign-in.
	email := uniq("disabled")
	c := newClient(t, ts.URL)
	c.register(email)
	secret, _ := enable2FA(t, c)
	// A challenge obtained while still enabled…
	l := newClient(t, ts.URL)
	e := l.expectCode("POST", "/api/auth/login", map[string]any{"email": email, "password": c.pw}, 401, "2fa_required")
	disable(email)
	// …is refused once the account is disabled.
	next, _ := totp.Code(secret, time.Now().Add(totp.Period*time.Second))
	l.expectCode("POST", "/api/auth/2fa/challenge", map[string]any{"challenge": e.Challenge, "code": next}, 403, "account_disabled")
	l.expect("GET", "/api/me", nil, 401)
	l.expectCode("POST", "/api/auth/login", map[string]any{"email": email, "password": c.pw}, 403, "account_disabled")
	before := mailbox.count(email)
	l.expect("POST", "/api/auth/code/request", map[string]any{"email": email}, 200)
	code := mailbox.wait(t, email, before)
	l.expectCode("POST", "/api/auth/code/verify", map[string]any{"email": email, "code": code}, 403, "account_disabled")
	l.expect("GET", "/api/me", nil, 401)

	// Verification of a disabled, unverified account.
	u := uniq("disabled-unverified")
	v := newClient(t, ts.URL)
	before = mailbox.count(u)
	v.expectCode("POST", "/api/auth/register", map[string]any{"email": u, "password": v.pw}, 403, "email_unverified")
	code = mailbox.wait(t, u, before)
	disable(u)
	v.expectCode("POST", "/api/auth/verify-email", map[string]any{"email": u, "code": code}, 403, "account_disabled")
	v.expect("GET", "/api/me", nil, 401)
}

func TestCodeAttemptsExpiryAndRateLimits(t *testing.T) {
	sqlDB := testDB(t)
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	email := uniq("limits")
	newClient(t, ts.URL).register(email)
	c := newClient(t, ts.URL)

	// Five wrong tries kill a code.
	before := mailbox.count(email)
	c.expect("POST", "/api/auth/code/request", map[string]any{"email": email}, 200)
	code := mailbox.wait(t, email, before)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := 0; i < 5; i++ {
		c.expectCode("POST", "/api/auth/code/verify", map[string]any{"email": email, "code": wrong}, 422, "invalid_code")
	}
	c.expectCode("POST", "/api/auth/code/verify", map[string]any{"email": email, "code": code}, 422, "invalid_code")

	// An expired code is refused: back-dated by an hour, not a second, because the app checks expiry
	// with its own clock and the database's can run ahead of it. (The resend window is a minute, so force a new code by clearing
	// the old row's sent_at.)
	if _, err := sqlDB.Exec(`DELETE FROM account_codes WHERE email = $1`, email); err != nil {
		t.Fatal(err)
	}
	before = mailbox.count(email)
	c.expect("POST", "/api/auth/code/request", map[string]any{"email": email}, 200)
	code = mailbox.wait(t, email, before)
	if _, err := sqlDB.Exec(`UPDATE account_codes SET expires_at = now() - interval '1 hour' WHERE email = $1`, email); err != nil {
		t.Fatal(err)
	}
	c.expectCode("POST", "/api/auth/code/verify", map[string]any{"email": email, "code": code}, 422, "invalid_code")

	// Guesses: 15 per 10 minutes per client and per address. This client has made 8 (7 above, and
	// the verify-email inside register), so 7 more are allowed and the next is refused.
	for i := 0; i < 7; i++ {
		c.expect("POST", "/api/auth/code/verify", map[string]any{"email": email, "code": wrong}, 422)
	}
	c.expectCode("POST", "/api/auth/code/verify", map[string]any{"email": email, "code": wrong}, 429, "rate_limited")

	// Sign-up: 20 per 10 minutes per client. X-Forwarded-For changes the plugin's own bucket but
	// not Hosbah's, which does not trust the header from an untrusted peer.
	csrf := newClient(t, ts.URL)
	token := csrf.csrf()
	post := func(i int) int {
		status, _ := rawPost(t, ts.URL+"/api/auth/register", map[string]any{"email": uniq(fmt.Sprintf("flood%d", i)), "password": "correct horse battery"},
			map[string]string{"X-CSRF-Token": token, "Cookie": "togo_csrf=" + token, "X-Forwarded-For": fmt.Sprintf("198.51.100.%d", i)})
		return status
	}
	// Sign-ups already made from this client in this server: the one register above.
	for i := 0; i < 19; i++ {
		if s := post(i); s != 403 {
			t.Fatalf("sign-up %d -> %d (want 403 email_unverified)", i, s)
		}
	}
	if s := post(99); s != 429 {
		t.Fatalf("sign-up over the limit -> %d (want 429)", s)
	}
}
