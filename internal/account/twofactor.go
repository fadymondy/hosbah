package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/togo-framework/auth"
	"rsc.io/qr"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/totp"
)

/*
Authenticator-app two-factor.

  - Every sign-in this package controls — password (wrapLogin), emailed code, and the code after
    sign-up — ends in complete(): with two-factor on, the answer is 401 {code:"2fa_required",
    challenge} instead of a session. POST /api/auth/2fa/challenge {challenge, code |
    recovery_code} finishes it. A challenge lives 5 minutes and allows 5 tries.
  - /api/me/2fa (signed in): status, enrol (secret + otpauth URL + QR), confirm (turns it on and
    returns ten recovery codes, once), disable, and a new set of recovery codes.

The secret is sealed under the deployment KEK (crypto.Keyring.SealPlatform, bound to
"totp:<user_id>"). A code is accepted for the current 30-second step and one either side, and
never twice: last_step only moves forward. Recovery codes ("xxxx-xxxx") are SHA-256 hashed and
single-use.

This replaces the plugin's /api/auth/2fa/enroll and /verify (blocked in internal/server): they
stored the secret in plain text and nothing checked it at sign-in. ImportPluginTOTP carries over
the secrets of accounts that had turned it on there.
*/

const (
	challengeTTL  = 5 * time.Minute
	recoveryCount = 10
	issuer        = "Hosbah"
)

func totpContext(userID string) string { return "totp:" + userID }

func otpauthURL(email, secret string) string {
	return "otpauth://totp/" + url.PathEscape(issuer+":"+email) +
		"?secret=" + secret + "&issuer=" + url.QueryEscape(issuer) + "&algorithm=SHA1&digits=6&period=30"
}

func qrDataURL(text string) (string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	code.Scale = 6
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG()), nil
}

func hashRecovery(code string) string {
	c := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	sum := sha256.Sum256([]byte(c))
	return hex.EncodeToString(sum[:])
}

var recoveryEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newRecoveryCode() (string, error) {
	raw := make([]byte, 5)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	s := strings.ToLower(recoveryEncoding.EncodeToString(raw)) // 8 characters
	return s[:4] + "-" + s[4:8], nil
}

func newChallengeID() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

/* ------------------------------------------------------------ storage */

type totpState struct {
	Secret   string
	Enabled  bool
	LastStep int64
}

// totpOf returns the account's authenticator state, or nil when it has none.
func (s *Service) totpOf(ctx context.Context, userID string) (*totpState, error) {
	row, err := s.q().AccountTotp(ctx, userID)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.Keys == nil {
		return nil, errNoKeys
	}
	secret, err := s.Keys.OpenPlatform(totpContext(userID), row.SecretSealed)
	if err != nil {
		return nil, err
	}
	return &totpState{Secret: string(secret), Enabled: row.Enabled, LastStep: row.LastStep}, nil
}

type keysError struct{}

func (keysError) Error() string { return "two-factor needs CLOUDY_KEK on the server" }

var errNoKeys error = keysError{}

// twoFactorOn fails closed: an unreadable row still counts as on, so a database hiccup never
// turns a password into a full sign-in.
func (s *Service) twoFactorOn(ctx context.Context, userID string) bool {
	row, err := s.q().AccountTotp(ctx, userID)
	if isNoRows(err) {
		return false
	}
	return err != nil || row.Enabled
}

// secondFactor checks a TOTP or a recovery code and consumes it.
func (s *Service) secondFactor(ctx context.Context, userID, code, recovery string) bool {
	q := s.q()
	if strings.TrimSpace(recovery) != "" {
		n, err := q.UseRecoveryCode(ctx, db.UseRecoveryCodeParams{UserID: userID, CodeHash: hashRecovery(recovery)})
		return err == nil && n == 1
	}
	st, err := s.totpOf(ctx, userID)
	if err != nil || st == nil || !st.Enabled {
		return false
	}
	step := totp.Match(st.Secret, code, s.Now(), st.LastStep)
	if step == 0 {
		return false
	}
	n, err := q.AdvanceTotpStep(ctx, db.AdvanceTotpStepParams{UserID: userID, LastStep: step})
	return err == nil && n == 1
}

// replaceRecoveryCodes deletes the account's recovery codes and makes ten new ones inside tx.
func replaceRecoveryCodes(ctx context.Context, q *db.Queries, userID string) ([]string, error) {
	if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
		return nil, err
	}
	codes := make([]string, 0, recoveryCount)
	seen := map[string]bool{}
	for len(codes) < recoveryCount {
		c, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		if err := q.InsertRecoveryCode(ctx, db.InsertRecoveryCodeParams{UserID: userID, CodeHash: hashRecovery(c)}); err != nil {
			return nil, err
		}
		codes = append(codes, c)
	}
	return codes, nil
}

func (s *Service) inTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

/* ------------------------------------------------------------ the challenge */

// challenge answers 401 2fa_required with a fresh challenge for u.
func (s *Service) challenge(w http.ResponseWriter, r *http.Request, u *user) {
	ctx := r.Context()
	id, err := newChallengeID()
	if err == nil {
		err = s.q().CreateChallenge(ctx, db.CreateChallengeParams{ID: id, UserID: u.ID, ExpiresAt: s.Now().UTC().Add(challengeTTL)})
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not start the second step")
		return
	}
	_ = s.q().DeleteExpiredChallenges(ctx)
	fail(w, http.StatusUnauthorized, Code2FARequired, "Enter the code from your authenticator app.", "challenge", id)
}

func challengeExpired(w http.ResponseWriter) {
	fail(w, http.StatusUnauthorized, CodeChallengeExpired, "this sign-in has expired — start again")
}

func invalid2FA(w http.ResponseWriter) {
	fail(w, http.StatusUnprocessableEntity, Code2FAInvalid, "that code is not right")
}

// POST /api/auth/2fa/challenge {challenge, code | recovery_code}
// → 200 {token, user, extra:{recovery_codes_left}}.
func (s *Service) handleChallenge(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Challenge    string `json:"challenge"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&b) != nil || b.Challenge == "" || (b.Code == "" && b.RecoveryCode == "") {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "challenge and code are required")
		return
	}
	if !s.tries.allow("2fa:" + s.clientIP(r)) {
		tooMany(w)
		return
	}
	ctx := r.Context()
	q := s.q()
	ch, err := q.Challenge(ctx, b.Challenge)
	if err != nil || s.Now().After(ch.ExpiresAt) || ch.Attempts >= maxAttempts {
		_, _ = q.DeleteChallenge(ctx, b.Challenge)
		challengeExpired(w)
		return
	}
	if !s.secondFactor(ctx, ch.UserID, b.Code, b.RecoveryCode) {
		_ = q.CountChallengeAttempt(ctx, b.Challenge)
		invalid2FA(w)
		return
	}
	// One sign-in per challenge: only the request that deletes it goes on.
	if n, err := q.DeleteChallenge(ctx, b.Challenge); err != nil || n != 1 {
		challengeExpired(w)
		return
	}
	u, err := s.userByID(ctx, ch.UserID)
	if err != nil {
		challengeExpired(w)
		return
	}
	if u.Disabled {
		disabled(w)
		return
	}
	left, _ := q.RecoveryCodesLeft(ctx, u.ID)
	s.issue(w, u, map[string]any{"extra": map[string]any{"recovery_codes_left": left}})
}

/* ------------------------------------------------------------ /api/me/2fa */

// me returns the signed-in account. The member tier (internal/server) has already required a
// session, an active account and the CSRF token.
func me(w http.ResponseWriter, r *http.Request) (*auth.Identity, bool) {
	id, ok := auth.IdentityFrom(r.Context())
	if !ok || id == nil || id.ID == "" || id.Guard == "pat" {
		fail(w, http.StatusUnauthorized, CodeUnauthorized, "sign in first")
		return nil, false
	}
	return id, true
}

type answer struct {
	Code         string `json:"code"`
	RecoveryCode string `json:"recovery_code"`
}

func decodeAnswer(r *http.Request) answer {
	var b answer
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&b)
	return b
}

// GET /api/me/2fa → {enabled, recovery_codes_left, available}.
func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := me(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	enabled := false
	if row, err := s.q().AccountTotp(ctx, id.ID); err == nil {
		enabled = row.Enabled
	} else if !isNoRows(err) {
		fail(w, http.StatusInternalServerError, CodeInternal, "lookup failed")
		return
	}
	left, _ := s.q().RecoveryCodesLeft(ctx, id.ID)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "recovery_codes_left": left, "available": s.Keys != nil})
}

// POST /api/me/2fa/enroll → {secret, otpauth_url, qr}. 409 when it is already on.
func (s *Service) handleEnroll(w http.ResponseWriter, r *http.Request) {
	id, ok := me(w, r)
	if !ok {
		return
	}
	if s.Keys == nil {
		fail(w, http.StatusServiceUnavailable, "2fa_unavailable", errNoKeys.Error())
		return
	}
	ctx := r.Context()
	if s.twoFactorOn(ctx, id.ID) {
		fail(w, http.StatusConflict, Code2FAEnabled, "two-factor is already on — turn it off first to set it up again")
		return
	}
	secret, err := totp.NewSecret()
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not create a secret")
		return
	}
	sealed, err := s.Keys.SealPlatform(totpContext(id.ID), []byte(secret))
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not seal the secret")
		return
	}
	if err := s.q().UpsertPendingTotp(ctx, db.UpsertPendingTotpParams{UserID: id.ID, SecretSealed: sealed}); err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not save the secret")
		return
	}
	email := id.Email
	if u, err := s.userByID(ctx, id.ID); err == nil {
		email = u.Email
	}
	link := otpauthURL(email, secret)
	img, err := qrDataURL(link)
	if err != nil {
		img = ""
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauth_url": link, "qr": img})
}

// POST /api/me/2fa/confirm {code} → {enabled:true, recovery_codes:[…]}.
func (s *Service) handleConfirm(w http.ResponseWriter, r *http.Request) {
	id, ok := me(w, r)
	if !ok {
		return
	}
	b := decodeAnswer(r)
	if !s.tries.allow("2fa-confirm:" + id.ID) {
		tooMany(w)
		return
	}
	ctx := r.Context()
	st, err := s.totpOf(ctx, id.ID)
	if err != nil || st == nil {
		fail(w, http.StatusConflict, Code2FANotStarted, "start the setup first")
		return
	}
	if st.Enabled {
		fail(w, http.StatusConflict, Code2FAEnabled, "two-factor is already on")
		return
	}
	step := totp.Match(st.Secret, b.Code, s.Now(), 0)
	if step == 0 {
		fail(w, http.StatusUnprocessableEntity, Code2FAInvalid, "that code is not right — check the time on your phone")
		return
	}
	var codes []string
	err = s.inTx(ctx, func(q *db.Queries) error {
		n, err := q.EnableTotp(ctx, db.EnableTotpParams{UserID: id.ID, LastStep: step})
		if err != nil {
			return err
		}
		if n != 1 {
			return errAlreadyOn
		}
		codes, err = replaceRecoveryCodes(ctx, q, id.ID)
		return err
	})
	if err == errAlreadyOn {
		fail(w, http.StatusConflict, Code2FAEnabled, "two-factor is already on")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not turn two-factor on")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "recovery_codes": codes})
}

type alreadyOn struct{}

func (alreadyOn) Error() string { return "already on" }

var errAlreadyOn error = alreadyOn{}

// POST /api/me/2fa/disable {code} or {recovery_code} → {enabled:false}.
func (s *Service) handleDisable(w http.ResponseWriter, r *http.Request) {
	id, ok := me(w, r)
	if !ok {
		return
	}
	b := decodeAnswer(r)
	if !s.tries.allow("2fa-disable:" + id.ID) {
		tooMany(w)
		return
	}
	ctx := r.Context()
	if s.twoFactorOn(ctx, id.ID) && !s.secondFactor(ctx, id.ID, b.Code, b.RecoveryCode) {
		invalid2FA(w)
		return
	}
	err := s.inTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteTotp(ctx, id.ID); err != nil {
			return err
		}
		return q.DeleteRecoveryCodes(ctx, id.ID)
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not turn two-factor off")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

// POST /api/me/2fa/recovery {code} → {recovery_codes:[…]} (the old ones stop working).
func (s *Service) handleRecovery(w http.ResponseWriter, r *http.Request) {
	id, ok := me(w, r)
	if !ok {
		return
	}
	b := decodeAnswer(r)
	if !s.tries.allow("2fa-recovery:" + id.ID) {
		tooMany(w)
		return
	}
	ctx := r.Context()
	if !s.twoFactorOn(ctx, id.ID) {
		fail(w, http.StatusConflict, Code2FANotEnabled, "two-factor is not on")
		return
	}
	if !s.secondFactor(ctx, id.ID, b.Code, "") {
		invalid2FA(w)
		return
	}
	var codes []string
	err := s.inTx(ctx, func(q *db.Queries) error {
		var err error
		codes, err = replaceRecoveryCodes(ctx, q, id.ID)
		return err
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not create recovery codes")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

/* ------------------------------------------------------------ carrying over the plugin's secrets */

/*
ImportPluginTOTP moves the plugin's auth_totp secrets (plain text) into account_totp, sealed.

Runs at every boot and is idempotent: an account that turned two-factor on through the plugin
keeps it, with the same authenticator entry. A secret already set up through Hosbah is never
overwritten. Rows are deleted from auth_totp once handled, so no plain-text secret stays behind;
a row that was never enabled (an abandoned plugin enrolment) is simply deleted. It needs the KEK
(SQL migrations cannot seal), which is why it is not a migration.
*/
func (s *Service) ImportPluginTOTP(ctx context.Context) (imported int, err error) {
	if s.Keys == nil {
		return 0, errNoKeys
	}
	q := s.q()
	rows, err := q.PluginTotpSecrets(ctx)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if row.Enabled == "true" {
			if _, cerr := totp.Code(row.Secret, time.Now()); cerr != nil {
				// Left in auth_totp (not deleted) so an operator can look at it.
				s.Log.Warn("account: skipping an unreadable plugin TOTP secret", "user", row.Subject)
				continue
			}
			sealed, err := s.Keys.SealPlatform(totpContext(row.Subject), []byte(strings.ToUpper(strings.TrimSpace(row.Secret))))
			if err != nil {
				return imported, err
			}
			n, err := q.ImportTotp(ctx, db.ImportTotpParams{UserID: row.Subject, SecretSealed: sealed})
			if err != nil {
				return imported, err
			}
			imported += int(n)
		}
		if err := q.DeletePluginTotp(ctx, row.Subject); err != nil {
			return imported, err
		}
	}
	return imported, nil
}
