package account

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/brand"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/oauthstate"
	"github.com/fadymondy/hosbah/internal/platform"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

/*
Sign in with Google and GitHub, built here rather than with the togo auth-oauth plugin: that plugin
finds or creates an account by whatever email the provider returns, verified or not, and hands out
a session directly — around Hosbah's two-factor, disabled-account and email-verification rules.

  GET /api/auth/providers                    public: which buttons to show
  GET /api/auth/oauth/{provider}             public: redirect to the provider (state, PKCE S256,
                                             and a nonce for Google). ?next= a same-origin path,
                                             ?locale=, ?invitation= (invite-only sign-up)
  GET /api/auth/oauth/{provider}?link=1      signed in (member tier): link that provider to you
  GET /api/auth/oauth/{provider}/callback    public: the provider sends the browser back here
  GET /api/me/identities                     your linked accounts, and whether you have a password
  DELETE /api/me/identities/{id}             unlink (never the last way to sign in)

The callback accepts only a state it issued, once, within ten minutes, in the browser that started
it (internal/oauthstate). Only a provider-verified address counts: Google's id_token is verified
(RS256 against Google's published keys, iss, aud = our client id, exp, nonce) and must say
email_verified; GitHub's /user/emails must have a primary, verified address. The account is found
by (provider, subject) first — the provider's stable user id, so a changed address at the provider
changes nothing — then by the verified address (the identity is linked to it), and only then
created (already verified, no password; the platform's sign-up mode applies). Then exactly what a
password sign-in does: a disabled account is refused, one with two-factor gets the second step,
and everyone else gets the auth plugin's session. The answers are redirects to the web app:
/{locale}/login?oauth_error=<code> on failure, /{locale}/login#challenge=<id> for the second step
(a fragment never reaches a server log or a Referer), and next (or the home page) on success.
*/

// Providers.
const (
	ProviderGoogle = "google"
	ProviderGitHub = "github"
)

// OAuth error codes, carried to the web app as ?oauth_error=.
const (
	OAuthUnavailable     = "provider_unavailable"
	OAuthStateInvalid    = "state_invalid"
	OAuthDenied          = "denied"
	OAuthProviderError   = "provider_error"
	OAuthEmailUnverified = "oauth_email_unverified"
	OAuthSignupClosed    = "signup_closed"
	OAuthInviteOnly      = "invite_only"
	OAuthIdentityTaken   = "identity_taken"
	OAuthRateLimited     = "rate_limited"
	OAuthInternal        = "internal"
	CodeLastSignIn       = "last_sign_in_method"
	CodeNotFound         = "not_found"
)

const oauthCookie = "hosbah_oauth"

// Provider is one sign-in provider's client and endpoints. The endpoints are fields so tests point
// them at a fake.
type Provider struct {
	ID           string
	Name         string
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	// Google: the id_token's signing keys and accepted issuers.
	JWKSURL string
	Issuers []string
	// GitHub: the REST API (for /user and /user/emails).
	APIURL string
	HTTP   *http.Client

	jwks jwksCache
}

// ProvidersFromEnv reads HOSBAH_GOOGLE_CLIENT_ID/_SECRET and HOSBAH_GITHUB_CLIENT_ID/_SECRET (each
// also as CLOUDY_…). A provider without both is not offered.
func ProvidersFromEnv() map[string]*Provider {
	out := map[string]*Provider{}
	client := &http.Client{Timeout: 15 * time.Second}
	if id, secret := strings.TrimSpace(brand.Getenv("GOOGLE_CLIENT_ID")), strings.TrimSpace(brand.Getenv("GOOGLE_CLIENT_SECRET")); id != "" && secret != "" {
		out[ProviderGoogle] = &Provider{ID: ProviderGoogle, Name: "Google", ClientID: id, ClientSecret: secret,
			AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token",
			JWKSURL: "https://www.googleapis.com/oauth2/v3/certs", Issuers: []string{"https://accounts.google.com", "accounts.google.com"}, HTTP: client}
	}
	if id, secret := strings.TrimSpace(brand.Getenv("GITHUB_CLIENT_ID")), strings.TrimSpace(brand.Getenv("GITHUB_CLIENT_SECRET")); id != "" && secret != "" {
		out[ProviderGitHub] = &Provider{ID: ProviderGitHub, Name: "GitHub", ClientID: id, ClientSecret: secret,
			AuthURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token",
			APIURL: "https://api.github.com", HTTP: client}
	}
	return out
}

// PublicOrigin is where the web app (and, through its proxy, /api) is reached: HOSBAH_WEB_ORIGIN,
// else APP_URL. The providers' redirect URI is <origin>/api/auth/oauth/<provider>/callback.
func PublicOrigin() string {
	if v := strings.TrimSpace(brand.Getenv("WEB_ORIGIN")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return strings.TrimRight(strings.TrimSpace(os.Getenv("APP_URL")), "/")
}

func (s *Service) provider(id string) *Provider {
	s.oauthMu.RLock()
	defer s.oauthMu.RUnlock()
	return s.providers[id]
}

// SetProviders replaces the sign-in providers (tests point them at fakes).
func (s *Service) SetProviders(p map[string]*Provider, origin string) {
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	s.providers, s.origin = p, strings.TrimRight(origin, "/")
}

func (s *Service) redirectURI(provider string) string {
	s.oauthMu.RLock()
	defer s.oauthMu.RUnlock()
	return s.origin + "/api/auth/oauth/" + provider + "/callback"
}

/* ------------------------------------------------------------ routes */

// GET /api/auth/providers → {providers:[{id, name}]}.
func (s *Service) handleProviders(w http.ResponseWriter, _ *http.Request) {
	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := []item{}
	for _, id := range []string{ProviderGoogle, ProviderGitHub} {
		if p := s.provider(id); p != nil {
			out = append(out, item{ID: p.ID, Name: p.Name})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// oauthRedirect sends the browser back to the web app.
func oauthRedirect(w http.ResponseWriter, r *http.Request, target string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// failTo is where a failed round trip lands: the sign-in page, or the account settings for a link.
func failTo(w http.ResponseWriter, r *http.Request, locale, purpose, next, code string) {
	q := url.Values{"oauth_error": {code}}
	page := "/" + locale + "/login"
	if purpose == oauthstate.PurposeLink {
		page = "/" + locale + "/account"
	} else if next != "" {
		q.Set("next", next)
	}
	oauthRedirect(w, r, page+"?"+q.Encode())
}

func pkcePair() (verifier, challenge string, err error) {
	verifier, err = oauthstate.Random(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// GET /api/auth/oauth/{provider}
func (s *Service) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	qs := r.URL.Query()
	locale := localeOf(r, qs.Get("locale"))
	next := oauthstate.SafeNext(qs.Get("next"))
	link := qs.Has("link")
	purpose := oauthstate.PurposeLogin
	if link {
		purpose = oauthstate.PurposeLink
	}
	if !s.oauthTries.allow("oauth:" + s.clientIP(r)) {
		failTo(w, r, locale, purpose, next, OAuthRateLimited)
		return
	}
	p := s.provider(chi.URLParam(r, "provider"))
	if p == nil {
		failTo(w, r, locale, purpose, next, OAuthUnavailable)
		return
	}
	rec := oauthstate.Record{Purpose: purpose, Provider: p.ID, Next: next, Locale: locale}
	if link {
		// The member tier (internal/server) already required a live session for ?link=1.
		id, ok := auth.IdentityFrom(ctx)
		if !ok || id == nil || id.ID == "" || id.Guard == "pat" {
			failTo(w, r, locale, oauthstate.PurposeLogin, "", OAuthStateInvalid)
			return
		}
		rec.UserID, rec.Next = id.ID, ""
	} else if inv := strings.TrimSpace(qs.Get("invitation")); inv != "" && len(inv) < 200 {
		rec.InvitationHash = tokens.Hash(inv)
	}
	verifier, challenge, err := pkcePair()
	if err != nil {
		failTo(w, r, locale, purpose, next, OAuthInternal)
		return
	}
	rec.CodeVerifier = verifier
	if p.ID == ProviderGoogle {
		if rec.Nonce, err = oauthstate.Random(24); err != nil {
			failTo(w, r, locale, purpose, next, OAuthInternal)
			return
		}
	}
	state, browser, err := oauthstate.Begin(ctx, s.DB, rec, s.Now())
	if err != nil {
		s.Log.Error("account: could not store an OAuth state", "error", err)
		failTo(w, r, locale, purpose, next, OAuthInternal)
		return
	}
	http.SetCookie(w, oauthstate.BrowserCookie(oauthCookie, browser, oauthstate.Secure(r.TLS != nil))) //#nosec G124 -- HttpOnly+SameSite
	v := url.Values{
		"client_id":             {p.ClientID},
		"redirect_uri":          {s.redirectURI(p.ID)},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	switch p.ID {
	case ProviderGoogle:
		v.Set("response_type", "code")
		v.Set("scope", "openid email profile")
		v.Set("nonce", rec.Nonce)
		v.Set("prompt", "select_account")
	case ProviderGitHub:
		v.Set("scope", "read:user user:email")
	}
	oauthRedirect(w, r, p.AuthURL+"?"+v.Encode())
}

// GET /api/auth/oauth/{provider}/callback
func (s *Service) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	qs := r.URL.Query()
	fallback := localeOf(r, "")
	if !s.oauthTries.allow("oauth:" + s.clientIP(r)) {
		failTo(w, r, fallback, oauthstate.PurposeLogin, "", OAuthRateLimited)
		return
	}
	// A GitHub App installation, not a sign-in: with "Request user authorization (OAuth) during
	// installation" on, GitHub sends the browser here instead of to the Setup URL. Hand it over
	// untouched; /api/github/setup checks its own state and confirms the installation with GitHub.
	if chi.URLParam(r, "provider") == "github" && qs.Get("installation_id") != "" && qs.Get("setup_action") != "" {
		fwd := url.Values{}
		for _, k := range []string{"installation_id", "setup_action", "state"} {
			if v := qs.Get(k); v != "" {
				fwd.Set(k, v)
			}
		}
		http.Redirect(w, r, "/api/github/setup?"+fwd.Encode(), http.StatusSeeOther)
		return
	}
	// The state is consumed before anything else, whatever the provider said.
	browser := ""
	if c, err := r.Cookie(oauthCookie); err == nil {
		browser = c.Value
	}
	http.SetCookie(w, oauthstate.ClearCookie(oauthCookie, oauthstate.Secure(r.TLS != nil))) //#nosec G124 -- clearing
	rec, err := oauthstate.Finish(ctx, s.DB, qs.Get("state"), browser, s.Now(), oauthstate.PurposeLogin, oauthstate.PurposeLink)
	if err != nil {
		failTo(w, r, fallback, oauthstate.PurposeLogin, "", OAuthStateInvalid)
		return
	}
	p := s.provider(chi.URLParam(r, "provider"))
	if p == nil || p.ID != rec.Provider {
		failTo(w, r, rec.Locale, rec.Purpose, rec.Next, OAuthStateInvalid)
		return
	}
	if qs.Get("error") != "" {
		failTo(w, r, rec.Locale, rec.Purpose, rec.Next, OAuthDenied)
		return
	}
	code := qs.Get("code")
	if code == "" || len(code) > 2048 {
		failTo(w, r, rec.Locale, rec.Purpose, rec.Next, OAuthProviderError)
		return
	}
	prof, err := s.exchange(ctx, p, code, rec)
	if err != nil {
		s.Log.Warn("account: OAuth exchange failed", "provider", p.ID, "error", err)
		failTo(w, r, rec.Locale, rec.Purpose, rec.Next, OAuthProviderError)
		return
	}
	if rec.Purpose == oauthstate.PurposeLink {
		s.finishLink(w, r, p, rec, prof)
		return
	}
	s.finishLogin(w, r, p, rec, prof)
}

/* ------------------------------------------------------------ resolving the account */

// profile is what a provider vouched for.
type profile struct {
	Subject       string
	Email         string // lower-cased
	EmailVerified bool
}

type oauthError struct{ code string }

func (e *oauthError) Error() string { return e.code }

func oauthFail(code string) error { return &oauthError{code: code} }

func (s *Service) sys(ctx context.Context, fn func(q *db.Queries) error) error {
	return tenancy.Tx(ctx, s.DB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error { return fn(q) })
}

func (s *Service) finishLogin(w http.ResponseWriter, r *http.Request, p *Provider, rec oauthstate.Record, prof profile) {
	ctx := r.Context()
	u, linked, err := s.resolveOAuthUser(ctx, p, rec, prof)
	if err != nil {
		var oe *oauthError
		code := OAuthInternal
		if errors.As(err, &oe) {
			code = oe.code
		} else {
			s.Log.Error("account: OAuth sign-in failed", "provider", p.ID, "error", err)
		}
		failTo(w, r, rec.Locale, rec.Purpose, rec.Next, code)
		return
	}
	if linked {
		s.audit(ctx, u.ID, "auth.oauth_linked", map[string]any{"provider": p.ID, "email": prof.Email})
	}
	// Exactly the password sign-in's rules (complete()).
	if u.Disabled {
		clearSession(w, r)
		failTo(w, r, rec.Locale, rec.Purpose, rec.Next, CodeAccountDisabled)
		return
	}
	if s.twoFactorOn(ctx, u.ID) {
		clearSession(w, r)
		id, err := s.newChallenge(ctx, u)
		if err != nil {
			failTo(w, r, rec.Locale, rec.Purpose, rec.Next, OAuthInternal)
			return
		}
		s.audit(ctx, u.ID, "auth.oauth_login", map[string]any{"provider": p.ID, "second_factor": true})
		target := "/" + rec.Locale + "/login"
		if rec.Next != "" {
			target += "?" + url.Values{"next": {rec.Next}}.Encode()
		}
		oauthRedirect(w, r, target+"#challenge="+url.QueryEscape(id))
		return
	}
	if _, _, err := s.startSession(w, u); err != nil {
		failTo(w, r, rec.Locale, rec.Purpose, rec.Next, OAuthInternal)
		return
	}
	s.audit(ctx, u.ID, "auth.oauth_login", map[string]any{"provider": p.ID})
	target := rec.Next
	if target == "" {
		target = "/" + rec.Locale
	}
	oauthRedirect(w, r, target)
}

// resolveOAuthUser: the identity (provider, subject) → its user; else the user with the verified
// address → the identity is linked to it; else a new user. linked reports a new identity.
func (s *Service) resolveOAuthUser(ctx context.Context, p *Provider, rec oauthstate.Record, prof profile) (*user, bool, error) {
	var ident *db.UserIdentity
	err := s.sys(ctx, func(q *db.Queries) error {
		row, err := q.OAuthIdentityBySubject(ctx, db.OAuthIdentityBySubjectParams{Provider: p.ID, Subject: prof.Subject})
		if isNoRows(err) {
			return nil
		}
		if err != nil {
			return err
		}
		ident = &row
		return q.TouchUserIdentity(ctx, row.ID)
	})
	if err != nil {
		return nil, false, err
	}
	if ident != nil {
		u, err := s.userByID(ctx, ident.UserID)
		return u, false, err
	}
	// From here the address is what finds or makes the account, so the provider must vouch for it.
	if !prof.EmailVerified || !validEmail(prof.Email) {
		return nil, false, oauthFail(OAuthEmailUnverified)
	}
	u, err := s.userByEmail(ctx, prof.Email)
	switch {
	case err == nil:
		if u.Unverified {
			// The provider proved the inbox. The password was chosen by whoever registered the
			// address without proving it, so it goes, and so does every session.
			revokedAt := s.Now().UTC().Truncate(time.Second).Add(-time.Millisecond)
			if err := s.q().ClaimUnverifiedAccount(ctx, db.ClaimUnverifiedAccountParams{ID: u.ID, RevokedAt: revokedAt}); err != nil {
				return nil, false, err
			}
			u.Unverified = false
		}
	case isNoRows(err):
		if err := platform.CheckSignupHashed(ctx, s.DB, prof.Email, rec.InvitationHash); err != nil {
			var pe *platform.Error
			if errors.As(err, &pe) {
				if strings.Contains(pe.Msg, "invitation") {
					return nil, false, oauthFail(OAuthInviteOnly)
				}
				return nil, false, oauthFail(OAuthSignupClosed)
			}
			return nil, false, err
		}
		id := newUserID()
		if err := s.q().CreateOAuthUser(ctx, db.CreateOAuthUserParams{ID: id, Email: prof.Email, CreatedAt: s.Now().UTC().Format(time.RFC3339)}); err != nil {
			return nil, false, err
		}
		s.audit(ctx, id, "auth.oauth_registered", map[string]any{"provider": p.ID, "email": prof.Email})
		if u, err = s.userByID(ctx, id); err != nil {
			return nil, false, err
		}
	default:
		return nil, false, err
	}
	err = s.sys(ctx, func(q *db.Queries) error {
		_, err := q.InsertUserIdentity(ctx, db.InsertUserIdentityParams{UserID: u.ID, Provider: p.ID, Subject: prof.Subject, EmailAtLink: prof.Email})
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return u, true, nil
}

func newUserID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Service) finishLink(w http.ResponseWriter, r *http.Request, p *Provider, rec oauthstate.Record, prof profile) {
	ctx := r.Context()
	u, err := s.userByID(ctx, rec.UserID)
	if err != nil || u.Disabled {
		failTo(w, r, rec.Locale, rec.Purpose, "", CodeAccountDisabled)
		return
	}
	// The browser still carries a session of somebody else: do not attach to the account that
	// started the round trip behind its back.
	if c, err := r.Cookie(auth.SessionCookie); err == nil && s.Auth != nil {
		if id, err := s.Auth.Verify(c.Value); err == nil && id.ID != "" && id.ID != u.ID {
			failTo(w, r, rec.Locale, rec.Purpose, "", OAuthStateInvalid)
			return
		}
	}
	if !prof.EmailVerified || !validEmail(prof.Email) {
		failTo(w, r, rec.Locale, rec.Purpose, "", OAuthEmailUnverified)
		return
	}
	code := ""
	err = s.sys(ctx, func(q *db.Queries) error {
		row, err := q.OAuthIdentityBySubject(ctx, db.OAuthIdentityBySubjectParams{Provider: p.ID, Subject: prof.Subject})
		switch {
		case err == nil && row.UserID == u.ID:
			code = "already"
			return q.TouchUserIdentity(ctx, row.ID)
		case err == nil:
			code = OAuthIdentityTaken
			return nil
		case !isNoRows(err):
			return err
		}
		_, err = q.InsertUserIdentity(ctx, db.InsertUserIdentityParams{UserID: u.ID, Provider: p.ID, Subject: prof.Subject, EmailAtLink: prof.Email})
		return err
	})
	switch {
	case err != nil:
		s.Log.Error("account: could not link an identity", "provider", p.ID, "error", err)
		failTo(w, r, rec.Locale, rec.Purpose, "", OAuthInternal)
		return
	case code == OAuthIdentityTaken:
		failTo(w, r, rec.Locale, rec.Purpose, "", OAuthIdentityTaken)
		return
	case code == "":
		s.audit(ctx, u.ID, "auth.oauth_linked", map[string]any{"provider": p.ID, "email": prof.Email})
	}
	oauthRedirect(w, r, "/"+rec.Locale+"/account?"+url.Values{"linked": {p.ID}}.Encode())
}

// audit writes a platform-level entry (no team) in the activity log.
func (s *Service) audit(ctx context.Context, userID, action string, changes map[string]any) {
	body, _ := json.Marshal(changes)
	meta := tenancy.MetaFrom(ctx)
	if err := s.q().InsertActivity(ctx, db.InsertActivityParams{ID: newUserID(), Action: action, SubjectType: "user", SubjectID: userID,
		CauserID: userID, Changes: string(body), Ip: meta.IP, UserAgent: meta.UserAgent, CreatedAt: s.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		s.Log.Warn("account: could not write an audit entry", "action", action, "error", err)
	}
}

/* ------------------------------------------------------------ provider exchanges */

func (s *Service) exchange(ctx context.Context, p *Provider, code string, rec oauthstate.Record) (profile, error) {
	form := url.Values{
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
		"code":          {code},
		"redirect_uri":  {s.redirectURI(p.ID)},
		"code_verifier": {rec.CodeVerifier},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return profile{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		Error       string `json:"error"`
	}
	if err := doJSON(p.HTTP, req, &tok); err != nil {
		return profile{}, err
	}
	if tok.Error != "" {
		return profile{}, fmt.Errorf("token endpoint: %s", tok.Error)
	}
	switch p.ID {
	case ProviderGoogle:
		return s.googleProfile(ctx, p, tok.IDToken, rec.Nonce)
	case ProviderGitHub:
		return githubProfile(ctx, p, tok.AccessToken)
	}
	return profile{}, errors.New("unknown provider")
}

func doJSON(client *http.Client, req *http.Request, out any) error {
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s answered %d", req.URL.Host, resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

// googleProfile verifies the id_token: RS256 by one of Google's published keys, issued by Google,
// for our client id, unexpired, carrying the nonce this round trip stored.
func (s *Service) googleProfile(ctx context.Context, p *Provider, idToken, nonce string) (profile, error) {
	if idToken == "" {
		return profile{}, errors.New("no id_token")
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(idToken, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return p.jwks.key(ctx, p.HTTP, p.JWKSURL, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(p.ClientID), jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.Now), jwt.WithLeeway(time.Minute))
	if err != nil {
		return profile{}, fmt.Errorf("id_token: %w", err)
	}
	iss, _ := claims["iss"].(string)
	okIss := false
	for _, want := range p.Issuers {
		okIss = okIss || iss == want
	}
	if !okIss {
		return profile{}, fmt.Errorf("id_token: issuer %q", iss)
	}
	if got, _ := claims["nonce"].(string); nonce == "" || got != nonce {
		return profile{}, errors.New("id_token: nonce mismatch")
	}
	sub, _ := claims["sub"].(string)
	if sub == "" || len(sub) > 255 {
		return profile{}, errors.New("id_token: no subject")
	}
	email, _ := claims["email"].(string)
	verified := false
	switch v := claims["email_verified"].(type) {
	case bool:
		verified = v
	case string:
		verified = v == "true"
	}
	return profile{Subject: sub, Email: normEmail(email), EmailVerified: verified && email != ""}, nil
}

// githubProfile reads the account (its numeric id is the subject) and its primary, verified address.
func githubProfile(ctx context.Context, p *Provider, token string) (profile, error) {
	if token == "" {
		return profile{}, errors.New("no access token")
	}
	get := func(path string, out any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.APIURL, "/")+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		return doJSON(p.HTTP, req, out)
	}
	var u struct {
		ID int64 `json:"id"`
	}
	if err := get("/user", &u); err != nil {
		return profile{}, err
	}
	if u.ID <= 0 {
		return profile{}, errors.New("GitHub returned no user id")
	}
	prof := profile{Subject: strconv.FormatInt(u.ID, 10)}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	// Without access to the addresses (a missing scope or permission), there is no verified one.
	if err := get("/user/emails", &emails); err == nil {
		for _, e := range emails {
			if e.Primary && e.Verified && e.Email != "" {
				prof.Email, prof.EmailVerified = normEmail(e.Email), true
			}
		}
	}
	return prof, nil
}

/* ------------------------------------------------------------ Google's keys */

type jwksCache struct {
	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

// key returns Google's key kid, fetching the set when it is older than an hour or does not have
// it (at most once a minute, so a stream of bogus kids cannot make us hammer Google).
func (c *jwksCache) key(ctx context.Context, client *http.Client, jwksURL, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[kid]; ok && time.Since(c.fetched) < time.Hour {
		return k, nil
	}
	if time.Since(c.fetched) > time.Minute || c.keys == nil {
		keys, err := fetchJWKS(ctx, client, jwksURL)
		if err != nil {
			return nil, err
		}
		c.keys, c.fetched = keys, time.Now()
	}
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown signing key %q", kid)
}

func fetchJWKS(ctx context.Context, client *http.Client, jwksURL string) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, err
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := doJSON(client, req, &set); err != nil {
		return nil, err
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		exp := 0
		for _, b := range e {
			exp = exp<<8 | int(b)
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}
	}
	if len(out) == 0 {
		return nil, errors.New("no usable keys in the JWKS")
	}
	return out, nil
}

/* ------------------------------------------------------------ /api/me/identities */

type identityOut struct {
	ID         string     `json:"id"`
	Provider   string     `json:"provider"`
	Email      string     `json:"email"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// GET /api/me/identities → {identities, has_password, providers}.
func (s *Service) handleIdentities(w http.ResponseWriter, r *http.Request) {
	id, ok := me(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var (
		rows []db.UserIdentity
		pw   bool
	)
	err := tenancy.Tx(ctx, s.DB, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		if rows, err = q.ListUserIdentities(ctx, id.ID); err != nil {
			return err
		}
		pw, err = q.UserHasPassword(ctx, id.ID)
		return err
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "lookup failed")
		return
	}
	out := make([]identityOut, 0, len(rows))
	for _, row := range rows {
		out = append(out, identityOut{ID: row.ID, Provider: row.Provider, Email: row.EmailAtLink, CreatedAt: row.CreatedAt, LastUsedAt: row.LastUsedAt})
	}
	providers := []string{}
	for _, p := range []string{ProviderGoogle, ProviderGitHub} {
		if s.provider(p) != nil {
			providers = append(providers, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": out, "has_password": pw, "providers": providers})
}

// DELETE /api/me/identities/{id} → 200 {status:"unlinked"}; 409 last_sign_in_method when it is the
// account's only way in (no password and no other identity).
func (s *Service) handleUnlink(w http.ResponseWriter, r *http.Request) {
	id, ok := me(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	identID := chi.URLParam(r, "id")
	status, code := http.StatusOK, ""
	var provider string
	err := tenancy.Tx(ctx, s.DB, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, _ *sql.Tx) error {
		row, err := q.UserIdentityOfUser(ctx, db.UserIdentityOfUserParams{ID: identID, UserID: id.ID})
		if isNoRows(err) {
			status, code = http.StatusNotFound, CodeNotFound
			return nil
		}
		if err != nil {
			return err
		}
		provider = row.Provider
		all, err := q.ListUserIdentities(ctx, id.ID)
		if err != nil {
			return err
		}
		pw, err := q.UserHasPassword(ctx, id.ID)
		if err != nil {
			return err
		}
		if !pw && len(all) <= 1 {
			status, code = http.StatusConflict, CodeLastSignIn
			return nil
		}
		_, err = q.DeleteUserIdentity(ctx, db.DeleteUserIdentityParams{ID: identID, UserID: id.ID})
		return err
	})
	switch {
	case err != nil:
		fail(w, http.StatusInternalServerError, CodeInternal, "could not unlink")
	case code == CodeNotFound:
		fail(w, status, code, "not found")
	case code == CodeLastSignIn:
		fail(w, status, code, "this is the only way into your account — set a password (Forgot password) or connect another account first")
	default:
		s.audit(ctx, id.ID, "auth.oauth_unlinked", map[string]any{"provider": provider})
		writeJSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
	}
}
