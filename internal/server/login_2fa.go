package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/fadymondy/hosbah/internal/app"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/totp"
)

/*
requireSecondFactor makes two-factor sign-in real.

The togo auth plugin (v0.8.0) enrolls and verifies TOTP at /api/auth/2fa/*, but its
POST /api/auth/login never consults auth_totp: an account with 2FA "enabled" still signed in
with a password alone. togo auth-mfa v0.1.0 has a challenge flow but is unusable (it takes
user_id from the request body on unauthenticated routes, and its magic-link endpoint echoes a
login token — FM-81), so it is not installed.

This wraps the plugin's login: the password is checked by the plugin first (so the response
never reveals whether an account has 2FA before the password is right), into a recorder. If
the password was right and the account has TOTP enabled, the session the plugin minted is
discarded — its Set-Cookie never reaches the client — unless the body also carried a valid
`code`. The answer is then 401 {"error":"totp_required"}, which the sign-in page uses to ask
for the code.
*/
func requireSecondFactor(a *app.App) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/auth/login" {
				next.ServeHTTP(w, r)
				return
			}
			raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid body")
				return
			}
			var body struct {
				Email string `json:"email"`
				Code  string `json:"code"`
			}
			_ = json.Unmarshal(raw, &body)
			r.Body = io.NopCloser(bytes.NewReader(raw))

			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r)

			if rec.Code == http.StatusOK && body.Email != "" && a.SQLDB != nil {
				q := db.New(a.SQLDB)
				if u, err := q.UserByEmail(r.Context(), body.Email); err == nil {
					if t, err := q.TotpForSubject(r.Context(), u.ID); err == nil && t.Enabled == "true" {
						if body.Code == "" || !totp.Valid(t.Secret, body.Code, time.Now()) {
							writeJSONError(w, http.StatusUnauthorized, "totp_required")
							return
						}
					}
				}
			}

			for k, vs := range rec.Header() {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
		})
	}
}
