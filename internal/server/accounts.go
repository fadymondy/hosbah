package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/platform"
)

/*
activeAccount refuses a session whose account a platform admin has disabled or deleted, from
the very next request.

Sessions are stateless JWTs (SESSION_DRIVER=cookie): nothing server-side can be deleted to end
one, and the token stays valid until it expires. So every signed-in request re-reads the
account: disabled → 401 account_disabled; gone → 401; and a token issued at or before
users.sessions_revoked_at (stamped when the account was disabled) → 401 even after the account
is re-enabled, so re-enabling never revives a session that was out there before.

It runs after auth.Service.Middleware has verified the token, so the token read here is already
authenticated; ParseUnverified only reads its iat claim.
*/
func activeAccount(a *app.App) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := auth.IdentityFrom(r.Context())
			if !ok || id == nil || id.ID == "" || a == nil || a.SQLDB == nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			st, err := db.New(a.SQLDB).UserAccountState(r.Context(), id.ID)
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if st.DisabledAt != nil {
				writeJSONError(w, http.StatusUnauthorized, "account_disabled")
				return
			}
			if st.SessionsRevokedAt != nil {
				iat, ok := issuedAt(r)
				if !ok || !iat.After(st.SessionsRevokedAt.Truncate(time.Second)) {
					writeJSONError(w, http.StatusUnauthorized, "session_revoked")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// issuedAt reads the iat of the request's (already verified) session token: the bearer header,
// else the stateless session cookie.
func issuedAt(r *http.Request) (time.Time, bool) {
	tok := ""
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		tok = strings.TrimSpace(h[7:])
	} else if c, err := r.Cookie(auth.SessionCookie); err == nil {
		tok = c.Value
	}
	if tok == "" {
		return time.Time{}, false
	}
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(tok, claims); err != nil {
		return time.Time{}, false
	}
	at, err := claims.GetIssuedAt()
	if err != nil || at == nil {
		return time.Time{}, false
	}
	return at.Time, true
}

/*
accountGate applies the platform's sign-up mode to the auth plugin's POST /api/auth/register:
403 with a clear message when sign-up is closed, or invite-only without a valid
`invitation_token` for the same email. It is the outer layer; internal/account (inside it) then
withholds the session until the address is verified.

The disabled-account refusal on sign-in (403 account_disabled) lives in internal/account with the
other sign-in rules, because every path that creates a session — password, emailed code,
verification code, two-factor challenge — has to apply it.
*/
func accountGate(a *app.App) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/auth/register" || a == nil || a.SQLDB == nil {
				next.ServeHTTP(w, r)
				return
			}
			raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid body")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var body struct {
				Email           string `json:"email"`
				InvitationToken string `json:"invitation_token"`
			}
			_ = json.Unmarshal(raw, &body)
			if err := platform.CheckSignup(r.Context(), a.SQLDB, body.Email, body.InvitationToken); err != nil {
				var pe *platform.Error
				if errors.As(err, &pe) {
					writeJSONError(w, pe.Status, pe.Msg)
				} else {
					writeJSONError(w, http.StatusInternalServerError, "registration failed")
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
