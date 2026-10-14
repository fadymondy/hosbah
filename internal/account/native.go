package account

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/hosbah/internal/brand"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/oauthstate"
	"github.com/fadymondy/hosbah/internal/tokens"
)

/*
Social sign-in for the phone app (apps/mobile). Every way in ends exactly like POST /api/auth/login:
200 {token, user}, 401 2fa_required {challenge}, 403 account_disabled, or an {error, code} refusal;
the app then registers the device (POST /api/me/devices) as after a password sign-in. The account is
found or made by the same resolveOAuthUser as the web round trip (identity first, then the verified
address, then a new account under the platform's sign-up mode).

  - Google and Apple on iOS — the provider's own SDK, then
    POST /api/auth/oauth/{provider}/native
      google: {id_token, nonce?}            audience: HOSBAH_GOOGLE_IOS_CLIENT_ID,
                                            HOSBAH_GOOGLE_ANDROID_CLIENT_ID or the web client id
      apple:  {id_token, authorization_code, nonce?, full_name?: {given_name, family_name}}
                                            audience: HOSBAH_APPLE_BUNDLE_IDS; the code is
                                            redeemed at Apple (apple.go)
    Native tokens must be at most ten minutes old (nativeMaxAge).

  - GitHub, and Apple on Android — the web round trip in the system browser
    (expo-web-browser's openAuthSessionAsync) with the app's own PKCE:
      GET  /api/auth/oauth/{provider}?app=1&code_challenge=<S256>&code_challenge_method=S256
      …    the provider, then its usual callback on https (one OAuth app, one callback URL)
      →    hosbah://auth/callback?code=<one-time code>   (or ?error=<code>)
      POST /api/auth/app/token {code, code_verifier}
    The code is good once, for two minutes, and only with the verifier whose challenge started the
    trip: another app that registered the hosbah:// scheme and caught the redirect cannot use it.
*/

// nativeMaxAge: how old a native SDK's id_token may be.
const nativeMaxAge = 10 * time.Minute

// appCodeTTL: how long the phone app has to redeem its one-time code.
const appCodeTTL = 2 * time.Minute

// AppRedirectURI is where a round trip the phone app started ends: HOSBAH_APP_REDIRECT_URI, else
// hosbah://auth/callback (the app's scheme, apps/mobile/app.config.js).
func AppRedirectURI() string {
	if v := strings.TrimSpace(brand.Getenv("APP_REDIRECT_URI")); v != "" {
		return v
	}
	return "hosbah://auth/callback"
}

// appRedirect sends the in-app browser to the app's deep link, which closes it.
func appRedirect(w http.ResponseWriter, r *http.Request, q url.Values) {
	oauthRedirect(w, r, AppRedirectURI()+"?"+q.Encode())
}

// validChallenge: a PKCE S256 challenge is 43 base64url characters.
func validChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil
}

// finishApp ends a round trip the phone app started: the account is resolved now (so a refusal
// such as signup_closed reaches the app at once), and the app gets a one-time code for it. Disabled
// accounts and two-factor are handled when the code is redeemed (complete()).
func (s *Service) finishApp(w http.ResponseWriter, r *http.Request, p *Provider, rec oauthstate.Record, prof profile) {
	ctx := r.Context()
	u, linked, err := s.resolveOAuthUser(ctx, p, rec, prof)
	if err != nil {
		var oe *oauthError
		code := OAuthInternal
		if errors.As(err, &oe) {
			code = oe.code
		} else {
			s.Log.Error("account: app OAuth sign-in failed", "provider", p.ID, "error", err)
		}
		appRedirect(w, r, url.Values{"error": {code}})
		return
	}
	if linked {
		s.audit(ctx, u.ID, "auth.oauth_linked", map[string]any{"provider": p.ID, "email": prof.Email})
	}
	code, err := oauthstate.Random(32)
	if err == nil {
		q := s.q()
		_ = q.DeleteExpiredOAuthAppCodes(ctx)
		err = q.CreateOAuthAppCode(ctx, db.CreateOAuthAppCodeParams{ID: tokens.Hash(code), UserID: u.ID, Provider: p.ID,
			CodeChallenge: rec.AppChallenge, ExpiresAt: s.Now().UTC().Add(appCodeTTL)})
	}
	if err != nil {
		s.Log.Error("account: could not store an app code", "error", err)
		appRedirect(w, r, url.Values{"error": {OAuthInternal}})
		return
	}
	appRedirect(w, r, url.Values{"code": {code}})
}

// oauthStatus is the HTTP status of an OAuth refusal answered as JSON.
func oauthStatus(code string) int {
	switch code {
	case OAuthUnavailable, CodeNotFound:
		return http.StatusNotFound
	case OAuthRateLimited:
		return http.StatusTooManyRequests
	case OAuthInternal:
		return http.StatusInternalServerError
	case OAuthProviderError, OAuthStateInvalid:
		return http.StatusUnauthorized
	default: // unverified address, sign-up closed / invite-only, identity taken
		return http.StatusForbidden
	}
}

var oauthMessages = map[string]string{
	OAuthUnavailable:     "signing in with that provider is not set up on this server",
	OAuthStateInvalid:    "that sign-in expired or was already used — start again",
	OAuthProviderError:   "the provider could not confirm the sign-in — try again",
	OAuthEmailUnverified: "that account has no verified email address at the provider",
	OAuthSignupClosed:    "sign-up is closed, and no account uses that email address",
	OAuthInviteOnly:      "sign-up is by invitation — open the invitation sent to that address first",
	OAuthIdentityTaken:   "that account is already connected to another user",
	OAuthInternal:        "something went wrong — try again",
}

func oauthJSONError(w http.ResponseWriter, code string) {
	if code == OAuthRateLimited {
		tooMany(w)
		return
	}
	msg := oauthMessages[code]
	if msg == "" {
		msg = "the sign-in did not work"
	}
	fail(w, oauthStatus(code), code, msg)
}

type nativeBody struct {
	IDToken           string `json:"id_token"`
	AuthorizationCode string `json:"authorization_code"`
	Nonce             string `json:"nonce"`
	FullName          *struct {
		GivenName  string `json:"given_name"`
		FamilyName string `json:"family_name"`
	} `json:"full_name"`
	Invitation string `json:"invitation"`
}

// nativeProfile verifies what a native SDK returned for provider p.
func (s *Service) nativeProfile(r *http.Request, p *Provider, b nativeBody) (profile, error) {
	switch p.ID {
	case ProviderGoogle:
		claims, err := s.verifyIDToken(r.Context(), p, b.IDToken, p.NativeAudiences, b.Nonce, nativeMaxAge)
		if err != nil {
			return profile{}, err
		}
		return claimsProfile(claims), nil
	case ProviderApple:
		name := ""
		if b.FullName != nil {
			name = joinName(b.FullName.GivenName, b.FullName.FamilyName)
		}
		return s.appleNativeProfile(r.Context(), p, b.IDToken, b.AuthorizationCode, b.Nonce, name)
	}
	return profile{}, errors.New("no native sign-in for this provider")
}

// POST /api/auth/oauth/{provider}/native
func (s *Service) handleNative(w http.ResponseWriter, r *http.Request) {
	if !s.oauthTries.allow("oauth:" + s.clientIP(r)) {
		tooMany(w)
		return
	}
	p := s.provider(chi.URLParam(r, "provider"))
	if p == nil || !p.native() {
		oauthJSONError(w, OAuthUnavailable)
		return
	}
	var b nativeBody
	if json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&b) != nil || b.IDToken == "" {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "id_token is required")
		return
	}
	ctx := r.Context()
	prof, err := s.nativeProfile(r, p, b)
	if err != nil {
		s.Log.Warn("account: native sign-in refused", "provider", p.ID, "error", err)
		oauthJSONError(w, OAuthProviderError)
		return
	}
	rec := oauthstate.Record{Purpose: oauthstate.PurposeLogin, Provider: p.ID}
	if inv := strings.TrimSpace(b.Invitation); inv != "" && len(inv) < 200 {
		rec.InvitationHash = tokens.Hash(inv)
	}
	u, linked, err := s.resolveOAuthUser(ctx, p, rec, prof)
	if err != nil {
		var oe *oauthError
		if errors.As(err, &oe) {
			oauthJSONError(w, oe.code)
			return
		}
		s.Log.Error("account: native sign-in failed", "provider", p.ID, "error", err)
		oauthJSONError(w, OAuthInternal)
		return
	}
	if linked {
		s.audit(ctx, u.ID, "auth.oauth_linked", map[string]any{"provider": p.ID, "email": prof.Email})
	}
	s.audit(ctx, u.ID, "auth.oauth_login", map[string]any{"provider": p.ID, "app": true})
	s.complete(w, r, u, nil)
}

// POST /api/auth/app/token {code, code_verifier}
func (s *Service) handleAppToken(w http.ResponseWriter, r *http.Request) {
	if !s.tries.allow("app-code:" + s.clientIP(r)) {
		tooMany(w)
		return
	}
	var b struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&b) != nil || b.Code == "" || len(b.Code) > 200 ||
		len(b.CodeVerifier) < 43 || len(b.CodeVerifier) > 128 {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "code and code_verifier are required")
		return
	}
	ctx := r.Context()
	row, err := s.q().ConsumeOAuthAppCode(ctx, tokens.Hash(b.Code))
	sum := sha256.Sum256([]byte(b.CodeVerifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if err != nil || !s.Now().Before(row.ExpiresAt) || subtle.ConstantTimeCompare([]byte(challenge), []byte(row.CodeChallenge)) != 1 {
		oauthJSONError(w, OAuthStateInvalid)
		return
	}
	u, err := s.userByID(ctx, row.UserID)
	if err != nil {
		oauthJSONError(w, OAuthStateInvalid)
		return
	}
	s.audit(ctx, u.ID, "auth.oauth_login", map[string]any{"provider": row.Provider, "app": true})
	s.complete(w, r, u, nil)
}
