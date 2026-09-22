package account

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/togo-framework/auth"
	"golang.org/x/crypto/bcrypt"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/mail"
	"github.com/fadymondy/hosbah/internal/platform"
)

/*
Email verification, password recovery and sign-in by emailed code.

  - POST /api/auth/register still creates the account through the plugin, after the server's
    accountGate has applied the sign-up mode. The session the plugin minted is withheld, the
    account is marked unverified (users.email_unverified_at), a 6-digit code is mailed, and the
    answer is 403 {code:"email_unverified", email}. A sign-up carrying a valid invitation_token
    for the same address is the exception: the invitation reached that inbox, so the address is
    proved and the plugin's session passes through.
  - POST /api/auth/login is checked by the plugin first (a wrong password is the plugin's 401,
    so nothing here tells a stranger anything about an address). With the password right: a
    disabled account gets 403 account_disabled, an unverified one 403 email_unverified and a
    fresh code, and one with two-factor on 401 2fa_required with a challenge.
  - verify-email, password/forgot, password/reset, code/request and code/verify: see Mount.

Accounts that existed before verification did have email_unverified_at NULL, so they count as
verified and nobody is locked out.

Codes: 6 digits from crypto/rand, bcrypt-hashed, one live code per (address, purpose), 15
minutes, dead after five wrong tries, re-sent at most once a minute and at most 6 times an hour
per address. Guesses: 15 per 10 minutes per client and per address.
*/

const (
	purposeVerify = "verify"
	purposeReset  = "reset"
	purposeLogin  = "login"
	codeTTL       = 15 * time.Minute
	maxAttempts   = 5
	resendEvery   = time.Minute
)

var errBadCode = errors.New("that code is wrong or has expired — request a new one")

func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// issueCode stores a fresh code and mails it. With force=false nothing happens when a code went
// out less than a minute ago. Past 6 mails an hour to one address, nothing is sent either.
func (s *Service) issueCode(ctx context.Context, email, purpose, locale string, force bool) error {
	now := s.Now().UTC()
	q := s.q()
	if !force {
		sent, err := q.AccountCodeSentAt(ctx, db.AccountCodeSentAtParams{Email: email, Purpose: purpose})
		if err == nil && now.Sub(sent) < resendEvery {
			return nil
		}
	}
	if !s.emailSend.allow("send:" + email) {
		s.Log.Warn("account: code not sent, hourly limit for this address reached", "purpose", purpose)
		return nil
	}
	code, err := newCode()
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := q.UpsertAccountCode(ctx, db.UpsertAccountCodeParams{
		Email: email, Purpose: purpose, CodeHash: string(hash), ExpiresAt: now.Add(codeTTL), SentAt: now,
	}); err != nil {
		return err
	}
	subject, text, body := mail.CodeEmail(locale, purpose, code)
	msg := mail.Message{To: email, Subject: subject, Text: text, HTML: body}
	// Sent in the background: an SMTP round trip must not hold the request, and the answer must
	// not take longer for an address that has an account than for one that does not.
	send := s.Send
	log := s.Log
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := send(ctx, msg); err != nil {
			log.Warn("account: could not send the code email", "purpose", purpose, "error", err)
		}
	}()
	return nil
}

// checkCode consumes a code: right → deleted; wrong → counted; expired or exhausted → deleted.
func (s *Service) checkCode(ctx context.Context, email, purpose, code string) error {
	q := s.q()
	key := db.AccountCodeParams{Email: email, Purpose: purpose}
	row, err := q.AccountCode(ctx, key)
	if err != nil {
		return errBadCode
	}
	if row.Attempts >= maxAttempts || s.Now().After(row.ExpiresAt) {
		_, _ = q.DeleteAccountCode(ctx, db.DeleteAccountCodeParams(key))
		return errBadCode
	}
	if bcrypt.CompareHashAndPassword([]byte(row.CodeHash), []byte(strings.TrimSpace(code))) != nil {
		_ = q.CountAccountCodeAttempt(ctx, db.CountAccountCodeAttemptParams(key))
		return errBadCode
	}
	// Only the request that deletes the row wins, so a code cannot be spent twice concurrently.
	if n, err := q.DeleteAccountCode(ctx, db.DeleteAccountCodeParams(key)); err != nil || n != 1 {
		return errBadCode
	}
	return nil
}

/* ------------------------------------------------------------ register / login wrapper */

// Middleware wraps the plugin's POST /api/auth/register and /api/auth/login.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		switch strings.TrimRight(r.URL.Path, "/") {
		case "/api/auth/register":
			s.wrapRegister(next, w, r)
		case "/api/auth/login":
			s.wrapLogin(next, w, r)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

type credBody struct {
	Email           string `json:"email"`
	Locale          string `json:"locale"`
	InvitationToken string `json:"invitation_token"`
}

func readBody(r *http.Request) (credBody, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var c credBody
	_ = json.Unmarshal(raw, &c)
	return c, err
}

// recorder holds the plugin's answer (and its Set-Cookie) until it is passed on or dropped.
type recorder struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func newRecorder() *recorder                    { return &recorder{header: http.Header{}, code: http.StatusOK} }
func (c *recorder) Header() http.Header         { return c.header }
func (c *recorder) WriteHeader(code int)        { c.code = code }
func (c *recorder) Write(b []byte) (int, error) { return c.body.Write(b) }
func (c *recorder) flush(w http.ResponseWriter) {
	for k, vs := range c.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(c.code)
	_, _ = w.Write(c.body.Bytes())
}

func (c *recorder) userID() string {
	var out struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	_ = json.Unmarshal(c.body.Bytes(), &out)
	return out.User.ID
}

func unverified(w http.ResponseWriter, email, locale string) {
	msg := "Check your inbox: we sent a 6-digit code to " + email + ". Enter it to activate your account."
	if locale == "ar" {
		msg = "تحقق من بريدك: أرسلنا رمزًا من 6 أرقام إلى " + email + ". أدخله لتفعيل حسابك."
	}
	fail(w, http.StatusForbidden, CodeEmailUnverified, msg, "email", email)
}

func (s *Service) wrapRegister(next http.Handler, w http.ResponseWriter, r *http.Request) {
	if !s.signupIP.allow("register:" + s.clientIP(r)) {
		tooMany(w)
		return
	}
	c, err := readBody(r)
	if err != nil {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "invalid body")
		return
	}
	rec := newRecorder()
	next.ServeHTTP(rec, r)
	id := rec.userID()
	if rec.code != http.StatusCreated || id == "" {
		rec.flush(w) // not created: the plugin's answer as it was
		return
	}
	email := normEmail(c.Email)
	ctx := r.Context()
	// The invitation was mailed to this address: it proves the inbox.
	if platform.InvitationValidFor(ctx, s.DB, email, c.InvitationToken) {
		rec.flush(w)
		return
	}
	if err := s.q().MarkEmailUnverified(ctx, id); err != nil {
		// Never hand out the session of an account that should be unverified.
		s.Log.Error("account: could not mark the new account unverified", "error", err)
		fail(w, http.StatusInternalServerError, CodeInternal, "registration failed")
		return
	}
	locale := localeOf(r, c.Locale)
	if err := s.issueCode(ctx, email, purposeVerify, locale, true); err != nil {
		s.Log.Warn("account: could not issue the verification code", "error", err)
	}
	clearSession(w, r)
	unverified(w, email, locale)
}

func (s *Service) wrapLogin(next http.Handler, w http.ResponseWriter, r *http.Request) {
	c, err := readBody(r)
	if err != nil {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "invalid body")
		return
	}
	rec := newRecorder()
	next.ServeHTTP(rec, r)
	id := rec.userID()
	if rec.code != http.StatusOK || id == "" {
		rec.flush(w)
		return
	}
	// The password was right. The plugin's session reaches the client only if nothing below
	// objects.
	ctx := r.Context()
	u, err := s.userByID(ctx, id)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "sign-in failed")
		return
	}
	switch {
	case u.Disabled:
		clearSession(w, r)
		disabled(w)
	case u.Unverified:
		email := normEmail(u.Email)
		locale := localeOf(r, c.Locale)
		if err := s.issueCode(ctx, email, purposeVerify, locale, false); err != nil {
			s.Log.Warn("account: could not issue the verification code", "error", err)
		}
		clearSession(w, r)
		unverified(w, email, locale)
	case s.twoFactorOn(ctx, u.ID):
		clearSession(w, r)
		s.challenge(w, r, u)
	default:
		rec.flush(w)
	}
}

/* ------------------------------------------------------------ endpoints */

// Mount adds the account endpoints. The /api/auth/* ones are public (except the ?link=1 start of a
// Google / GitHub round trip); /api/me/* are for any signed-in member (the authorization tiers in
// internal/server).
func (s *Service) Mount(post, get, del func(pattern string, h http.HandlerFunc)) {
	get("/api/auth/providers", s.handleProviders)
	get("/api/auth/oauth/{provider}", s.handleOAuthStart)
	get("/api/auth/oauth/{provider}/callback", s.handleOAuthCallback)
	get("/api/me/identities", s.handleIdentities)
	del("/api/me/identities/{id}", s.handleUnlink)

	post("/api/auth/verify-email", s.handleVerify)
	post("/api/auth/verify-email/resend", s.handleResend)
	post("/api/auth/password/forgot", s.handleForgot)
	post("/api/auth/password/reset", s.handleReset)
	post("/api/auth/code/request", s.handleCodeRequest)
	post("/api/auth/code/verify", s.handleCodeVerify)
	post("/api/auth/2fa/challenge", s.handleChallenge)

	get("/api/me/2fa", s.handleStatus)
	post("/api/me/2fa/enroll", s.handleEnroll)
	post("/api/me/2fa/confirm", s.handleConfirm)
	post("/api/me/2fa/disable", s.handleDisable)
	post("/api/me/2fa/recovery", s.handleRecovery)
}

type codeBody struct {
	Email    string `json:"email"`
	Code     string `json:"code"`
	Password string `json:"password"`
	Locale   string `json:"locale"`
}

func decodeCode(r *http.Request) (codeBody, bool) {
	var b codeBody
	err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&b)
	b.Email = normEmail(b.Email)
	return b, err == nil && validEmail(b.Email)
}

// guessLimited applies the per-client and per-address limit on code guesses.
func (s *Service) guessLimited(w http.ResponseWriter, r *http.Request, email string) bool {
	ipOK := s.tries.allow("ip:" + s.clientIP(r))
	emailOK := s.tries.allow("email:" + email)
	if !ipOK || !emailOK {
		tooMany(w)
		return true
	}
	return false
}

// sendAllowed applies the per-client limit on "send me a code" requests. Over it, nothing is
// sent but the answer is the same 200: these endpoints never say anything about an address.
func (s *Service) sendAllowed(r *http.Request) bool {
	return s.signupIP.allow("send:" + s.clientIP(r))
}

func sent(w http.ResponseWriter) { writeJSON(w, http.StatusOK, map[string]string{"status": "sent"}) }

func badCode(w http.ResponseWriter) {
	fail(w, http.StatusUnprocessableEntity, CodeInvalidCode, errBadCode.Error())
}

// POST /api/auth/verify-email {email, code} → a session, or 2fa_required.
func (s *Service) handleVerify(w http.ResponseWriter, r *http.Request) {
	b, ok := decodeCode(r)
	if !ok || b.Code == "" {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "email and code are required")
		return
	}
	if s.guessLimited(w, r, b.Email) {
		return
	}
	ctx := r.Context()
	if err := s.checkCode(ctx, b.Email, purposeVerify, b.Code); err != nil {
		badCode(w)
		return
	}
	u, err := s.userByEmail(ctx, b.Email)
	if err != nil {
		badCode(w)
		return
	}
	if err := s.q().MarkEmailVerified(ctx, u.ID); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not verify the address")
		return
	}
	u.Unverified = false
	s.complete(w, r, u, map[string]any{"status": "verified"})
}

// POST /api/auth/verify-email/resend {email, locale?} → always 200 {status:"sent"}.
func (s *Service) handleResend(w http.ResponseWriter, r *http.Request) {
	b, ok := decodeCode(r)
	if !ok {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "email is required")
		return
	}
	if s.sendAllowed(r) {
		if u, err := s.userByEmail(r.Context(), b.Email); err == nil && u.Unverified {
			if err := s.issueCode(r.Context(), b.Email, purposeVerify, localeOf(r, b.Locale), false); err != nil {
				s.Log.Warn("account: could not resend the verification code", "error", err)
			}
		}
	}
	sent(w)
}

// POST /api/auth/password/forgot {email, locale?} → always 200 {status:"sent"}.
func (s *Service) handleForgot(w http.ResponseWriter, r *http.Request) {
	b, ok := decodeCode(r)
	if !ok {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "email is required")
		return
	}
	if s.sendAllowed(r) {
		if _, err := s.userByEmail(r.Context(), b.Email); err == nil {
			if err := s.issueCode(r.Context(), b.Email, purposeReset, localeOf(r, b.Locale), false); err != nil {
				s.Log.Warn("account: could not send the reset code", "error", err)
			}
		}
	}
	sent(w)
}

func minPassword() int {
	if v, err := strconv.Atoi(os.Getenv("AUTH_MIN_PASSWORD")); err == nil && v > 0 {
		return v
	}
	return 8
}

// POST /api/auth/password/reset {email, code, password} → 200 {status:"reset"}.
func (s *Service) handleReset(w http.ResponseWriter, r *http.Request) {
	b, ok := decodeCode(r)
	if !ok || b.Code == "" || b.Password == "" {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "email, code and password are required")
		return
	}
	if len(b.Password) < minPassword() || len(b.Password) > 72 {
		fail(w, http.StatusUnprocessableEntity, CodeWeakPassword, fmt.Sprintf("the password must be %d to 72 characters", minPassword()))
		return
	}
	if s.guessLimited(w, r, b.Email) {
		return
	}
	ctx := r.Context()
	if err := s.checkCode(ctx, b.Email, purposeReset, b.Code); err != nil {
		badCode(w)
		return
	}
	u, err := s.userByEmail(ctx, b.Email)
	if err != nil {
		badCode(w)
		return
	}
	// The plugin's hashing: bcrypt at DefaultCost.
	hash, err := bcrypt.GenerateFromPassword([]byte(b.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not set the password")
		return
	}
	/*
		Every session issued before now stops working (activeAccount refuses a token whose iat is
		not after sessions_revoked_at, compared in whole seconds). The instant stored is the end of
		the previous second, so signing in with the new password straight away — in this same
		second — is not refused; only a session minted in this very second before the reset would
		survive, which fadymondy.com-v2 accepts too.
	*/
	revokedAt := s.Now().UTC().Truncate(time.Second).Add(-time.Millisecond)
	if err := s.q().ResetPassword(ctx, db.ResetPasswordParams{ID: u.ID, PasswordHash: string(hash), RevokedAt: revokedAt}); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not set the password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

// POST /api/auth/code/request {email, locale?} → always 200 {status:"sent"}.
func (s *Service) handleCodeRequest(w http.ResponseWriter, r *http.Request) {
	b, ok := decodeCode(r)
	if !ok {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "email is required")
		return
	}
	if s.sendAllowed(r) {
		if _, err := s.userByEmail(r.Context(), b.Email); err == nil {
			if err := s.issueCode(r.Context(), b.Email, purposeLogin, localeOf(r, b.Locale), false); err != nil {
				s.Log.Warn("account: could not send the sign-in code", "error", err)
			}
		}
	}
	sent(w)
}

// POST /api/auth/code/verify {email, code} → a session, or 2fa_required. The code also proves
// the inbox, so it verifies an unverified account.
func (s *Service) handleCodeVerify(w http.ResponseWriter, r *http.Request) {
	b, ok := decodeCode(r)
	if !ok || b.Code == "" {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "email and code are required")
		return
	}
	if s.guessLimited(w, r, b.Email) {
		return
	}
	ctx := r.Context()
	if err := s.checkCode(ctx, b.Email, purposeLogin, b.Code); err != nil {
		badCode(w)
		return
	}
	u, err := s.userByEmail(ctx, b.Email)
	if err != nil {
		badCode(w)
		return
	}
	if u.Unverified {
		if err := s.q().MarkEmailVerified(ctx, u.ID); err != nil {
			fail(w, http.StatusInternalServerError, CodeInternal, "could not sign in")
			return
		}
		u.Unverified = false
	}
	s.complete(w, r, u, nil)
}

/* ------------------------------------------------------------ finishing a sign-in */

// complete signs u in, or asks for the second factor first. Every session this package creates
// goes through here or through handleChallenge, and both refuse a disabled account.
func (s *Service) complete(w http.ResponseWriter, r *http.Request, u *user, extra map[string]any) {
	if u.Disabled {
		disabled(w)
		return
	}
	if s.twoFactorOn(r.Context(), u.ID) {
		clearSession(w, r)
		s.challenge(w, r, u)
		return
	}
	s.issue(w, u, extra)
}

func (s *Service) issue(w http.ResponseWriter, u *user, extra map[string]any) {
	token, id, err := s.startSession(w, u)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not sign in")
		return
	}
	body := map[string]any{"token": token, "user": id}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, http.StatusOK, body)
}

// startSession mints u's session through the auth plugin (the cookie is set on w). Every session
// this package creates comes from here.
func (s *Service) startSession(w http.ResponseWriter, u *user) (string, auth.Identity, error) {
	id := auth.Identity{ID: u.ID, Email: u.Email, Roles: splitCSV(u.Roles), Permissions: splitCSV(u.Permissions), Guard: "api"}
	if s.Auth == nil {
		return "", id, errors.New("sign-in is unavailable")
	}
	token, err := s.Auth.IssueSession(w, id)
	return token, id, err
}
