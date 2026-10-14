package account

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/platform"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Self-service account deletion (App Store guideline 5.1.1(v): an app that lets people create an
account must let them delete it, in the app).

  POST   /api/me/delete/code   {locale?}   mail a 6-digit code to the account's address (15 minutes)
  DELETE /api/me               one proof of who you are, then the account is gone:
                                 {password}                        an account with a password
                                 {code}                            the mailed code (any account)
                                 {provider, id_token, authorization_code?, nonce?}
                                                                   a fresh native Google / Apple
                                                                   sign-in to an identity of this
                                                                   account (the phone app; Apple
                                                                   relay addresses may not receive
                                                                   mail from this server)
                               → 200 {status:"deleted"}, the session cookie cleared
                               → 403 reauth_required {has_password} with no proof
                               → 409 sole_owner | team_has_servers | last_admin (platform.DeleteOwnAccount)

A session alone is not enough: deletion cannot be undone, and a session (or a phone left unlocked)
is easier to come by than the password or the inbox. What goes, and which teams block it, is
platform.DeleteOwnAccount. Afterwards Apple's refresh tokens are revoked (apple.go).
*/

const purposeDelete = "delete"

// CodeReauthRequired: DELETE /api/me without a proof.
const CodeReauthRequired = "reauth_required"

// POST /api/me/delete/code → 200 {status:"sent"}.
func (s *Service) handleDeleteCode(w http.ResponseWriter, r *http.Request) {
	id, ok := me(w, r)
	if !ok {
		return
	}
	var b struct {
		Locale string `json:"locale"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&b)
	if !s.sendAllowed(r) {
		tooMany(w)
		return
	}
	u, err := s.userByID(r.Context(), id.ID)
	if err != nil {
		fail(w, http.StatusUnauthorized, CodeUnauthorized, "sign in first")
		return
	}
	if err := s.issueCode(r.Context(), normEmail(u.Email), purposeDelete, localeOf(r, b.Locale), false); err != nil {
		s.Log.Warn("account: could not issue the deletion code", "error", err)
		fail(w, http.StatusInternalServerError, CodeInternal, "could not send the code")
		return
	}
	sent(w)
}

type deleteBody struct {
	Password string `json:"password"`
	Code     string `json:"code"`
	Provider string `json:"provider"`
	nativeBody
}

// DELETE /api/me
func (s *Service) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := me(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var b deleteBody
	_ = json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&b)
	u, err := s.userByID(ctx, id.ID)
	if err != nil {
		fail(w, http.StatusUnauthorized, CodeUnauthorized, "sign in first")
		return
	}
	hasPassword := false
	if hash, err := s.q().UserPasswordHash(ctx, u.ID); err == nil {
		hasPassword = strings.HasPrefix(hash, "$2")
	}
	if b.Password == "" && strings.TrimSpace(b.Code) == "" && b.IDToken == "" {
		fail(w, http.StatusForbidden, CodeReauthRequired, "confirm it is you: your password, a code from your email, or a fresh sign-in",
			"has_password", hasPassword)
		return
	}
	if !s.tries.allow("delete:" + u.ID) {
		tooMany(w)
		return
	}
	var fresh profile
	switch {
	case b.Password != "":
		hash, err := s.q().UserPasswordHash(ctx, u.ID)
		if err != nil || !strings.HasPrefix(hash, "$2") || bcrypt.CompareHashAndPassword([]byte(hash), []byte(b.Password)) != nil {
			fail(w, http.StatusForbidden, "invalid_password", "that password is not right")
			return
		}
	case strings.TrimSpace(b.Code) != "":
		if s.checkCode(ctx, normEmail(u.Email), purposeDelete, b.Code) != nil {
			badCode(w)
			return
		}
	default:
		p := s.provider(b.Provider)
		if p == nil || !p.native() {
			oauthJSONError(w, OAuthUnavailable)
			return
		}
		prof, err := s.nativeProfile(r, p, b.nativeBody)
		if err == nil {
			err = s.sys(ctx, func(q *db.Queries) error {
				row, err := q.OAuthIdentityBySubject(ctx, db.OAuthIdentityBySubjectParams{Provider: p.ID, Subject: prof.Subject})
				if err == nil && row.UserID != u.ID {
					return errors.New("another account's identity")
				}
				return err
			})
		}
		if err != nil {
			s.Log.Warn("account: deletion re-authentication refused", "provider", b.Provider, "error", err)
			oauthJSONError(w, OAuthProviderError)
			return
		}
		fresh = prof
	}

	tokens := s.identityTokens(ctx, u.ID)
	err = tenancy.Tx(ctx, s.DB, tenancy.Scope{UserID: u.ID, PlatformAdmin: true}, func(q *db.Queries, tx *sql.Tx) error {
		return platform.DeleteOwnAccount(ctx, q, tx, u.ID)
	})
	var pe *platform.Error
	switch {
	case errors.As(err, &pe):
		code := pe.Code
		if code == "" {
			code = CodeInternal
			if pe.Status < 500 {
				code = "refused"
			}
		}
		fail(w, pe.Status, code, pe.Msg)
		return
	case err != nil:
		s.Log.Error("account: self-deletion failed", "error", err)
		fail(w, http.StatusInternalServerError, CodeInternal, "could not delete the account")
		return
	}
	s.revokeProviderTokens(ctx, u.ID, tokens)
	if fresh.RefreshToken != "" {
		if p := s.provider(ProviderApple); p != nil {
			if err := s.appleRevoke(ctx, p, providerToken{Client: fresh.TokenClient, RefreshToken: fresh.RefreshToken}); err != nil {
				s.Log.Warn("account: Apple token revocation failed", "error", err)
			}
		}
	}
	s.Log.Info("account: an account deleted itself", "user", u.ID)
	clearSession(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
