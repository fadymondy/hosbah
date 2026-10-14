package account

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/fadymondy/hosbah/internal/brand"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/oauthstate"
	"github.com/fadymondy/hosbah/internal/tokens"
)

/*
Sign in with Apple, on the web and natively in the iOS app.

  Web (and the Android app, through the browser): the same round trip as Google — authorize at
  Apple with scope "name email", response_mode=form_post and a nonce; Apple POSTs the code back
  to /api/auth/oauth/apple/callback, which turns the POST into a GET on the same URL (the
  SameSite=Lax cookie binding the state to the browser only travels on a top-level GET) and the
  shared callback carries on. Apple has no client secret: it is an ES256 JWT signed with the
  team's Sign in with Apple key, minted per request.

  Native (iOS, expo-apple-authentication): POST /api/auth/apple/native {id_token, nonce,
  authorization_code, locale?, invitation_token?}. The app passed SHA-256(nonce) to Apple, so the
  id_token's nonce claim must be the hex SHA-256 of the nonce posted here; its audience must be one
  of the app's bundle ids. The authorization code is then redeemed at Apple (client id = the
  bundle id): Apple accepts it once, within five minutes, so a replayed id_token without a fresh
  code gets nowhere — and the refresh token it yields is what account deletion revokes.

Either way the account is resolved exactly like Google's (resolveOAuthUser: identity by subject,
then a verified address, then a new account), and Apple's refresh token is kept sealed on the
identity (provider_token_sealed) so that deleting the account can revoke it at Apple.

Env (each also as CLOUDY_…): HOSBAH_APPLE_TEAM_ID, HOSBAH_APPLE_KEY_ID, HOSBAH_APPLE_PRIVATE_KEY
(the .p8 contents; literal \n accepted) or HOSBAH_APPLE_PRIVATE_KEY_FILE, HOSBAH_APPLE_SERVICES_ID
(the web client id; without it only the native sign-in is offered) and HOSBAH_APPLE_BUNDLE_IDS
(comma-separated; default com.fadymondy.hosbah).
*/

// ProviderApple is Sign in with Apple.
const ProviderApple = "apple"

const (
	appleIssuer       = "https://appleid.apple.com"
	appleTokenContext = "hosbah/apple-token/v1"
	defaultBundleID   = "com.fadymondy.hosbah"
)

// AppleConfig is what signs Apple's client secrets and which app ids the native sign-in accepts.
type AppleConfig struct {
	TeamID    string
	KeyID     string
	Key       *ecdsa.PrivateKey
	BundleIDs []string
	RevokeURL string
}

// appleFromEnv returns the Apple provider, or nil when the key is not configured.
func appleFromEnv(client *http.Client) *Provider {
	team, kid := strings.TrimSpace(brand.Getenv("APPLE_TEAM_ID")), strings.TrimSpace(brand.Getenv("APPLE_KEY_ID"))
	pemText := brand.Getenv("APPLE_PRIVATE_KEY")
	if f := strings.TrimSpace(brand.Getenv("APPLE_PRIVATE_KEY_FILE")); pemText == "" && f != "" {
		b, err := os.ReadFile(f) //#nosec G304 -- an operator-configured path
		if err == nil {
			pemText = string(b)
		}
	}
	if team == "" || kid == "" || strings.TrimSpace(pemText) == "" {
		return nil
	}
	key, err := ParseAppleKey(pemText)
	if err != nil {
		return nil
	}
	bundles := []string{}
	for _, b := range strings.Split(brand.Getenv("APPLE_BUNDLE_IDS"), ",") {
		if b = strings.TrimSpace(b); b != "" {
			bundles = append(bundles, b)
		}
	}
	if len(bundles) == 0 {
		bundles = []string{defaultBundleID}
	}
	return NewAppleProvider(strings.TrimSpace(brand.Getenv("APPLE_SERVICES_ID")), AppleConfig{
		TeamID: team, KeyID: kid, Key: key, BundleIDs: bundles,
	}, client)
}

// NewAppleProvider builds the Apple provider against Apple's real endpoints (tests then point the
// URLs at a fake). servicesID is the web client id ("" = native sign-in only).
func NewAppleProvider(servicesID string, cfg AppleConfig, client *http.Client) *Provider {
	if cfg.RevokeURL == "" {
		cfg.RevokeURL = appleIssuer + "/auth/revoke"
	}
	return &Provider{ID: ProviderApple, Name: "Apple", ClientID: servicesID,
		AuthURL: appleIssuer + "/auth/authorize", TokenURL: appleIssuer + "/auth/token",
		JWKSURL: appleIssuer + "/auth/keys", Issuers: []string{appleIssuer}, HTTP: client, Apple: &cfg}
}

// ParseAppleKey reads a .p8 (PKCS#8 PEM, P-256) key. A key pasted into an env var with its line
// breaks written as \n is accepted.
func ParseAppleKey(pemText string) (*ecdsa.PrivateKey, error) {
	pemText = strings.ReplaceAll(strings.TrimSpace(pemText), `\n`, "\n")
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("apple key: not PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apple key: %w", err)
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("apple key: not an EC key")
	}
	return ec, nil
}

// webReady: the browser round trip needs the Services ID; native needs only the key.
func (p *Provider) webReady() bool { return p != nil && p.ClientID != "" }

func (p *Provider) nativeReady() bool {
	return p != nil && p.Apple != nil && len(p.Apple.BundleIDs) > 0
}

// clientSecret is Apple's client secret for clientID: an ES256 JWT signed with the team's key.
func (p *Provider) clientSecret(clientID string, now time.Time) (string, error) {
	if p.Apple == nil {
		return p.ClientSecret, nil
	}
	t := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": p.Apple.TeamID, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"aud": appleIssuer, "sub": clientID,
	})
	t.Header["kid"] = p.Apple.KeyID
	return t.SignedString(p.Apple.Key)
}

// idTokenClaims verifies an OpenID id_token: RS256 by one of the provider's published keys, one of
// its issuers, unexpired, and an audience in audiences.
func (s *Service) idTokenClaims(ctx context.Context, p *Provider, idToken string, audiences []string) (jwt.MapClaims, error) {
	if idToken == "" {
		return nil, errors.New("no id_token")
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(idToken, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return p.jwks.key(ctx, p.HTTP, p.JWKSURL, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithExpirationRequired(), jwt.WithTimeFunc(s.Now), jwt.WithLeeway(time.Minute))
	if err != nil {
		return nil, fmt.Errorf("id_token: %w", err)
	}
	iss, _ := claims["iss"].(string)
	okIss := false
	for _, want := range p.Issuers {
		okIss = okIss || iss == want
	}
	if !okIss {
		return nil, fmt.Errorf("id_token: issuer %q", iss)
	}
	aud, _ := claims.GetAudience()
	okAud := false
	for _, a := range aud {
		for _, want := range audiences {
			okAud = okAud || (a != "" && a == want)
		}
	}
	if !okAud {
		return nil, errors.New("id_token: audience")
	}
	return claims, nil
}

// profileOf reads subject and address from verified claims (email_verified is a bool at Google, a
// bool or "true" at Apple).
func profileOf(claims jwt.MapClaims) (profile, error) {
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

// appleToken redeems an authorization code at Apple for clientID (the Services ID on the web, the
// bundle id natively).
func (s *Service) appleToken(ctx context.Context, p *Provider, clientID, code, redirectURI string) (idToken, refresh string, err error) {
	secret, err := p.clientSecret(clientID, s.Now())
	if err != nil {
		return "", "", err
	}
	form := url.Values{"client_id": {clientID}, "client_secret": {secret}, "code": {code}, "grant_type": {"authorization_code"}}
	if redirectURI != "" {
		form.Set("redirect_uri", redirectURI)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var tok struct {
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
	}
	if err := doJSON(p.HTTP, req, &tok); err != nil {
		return "", "", err
	}
	if tok.Error != "" {
		return "", "", fmt.Errorf("token endpoint: %s", tok.Error)
	}
	return tok.IDToken, tok.RefreshToken, nil
}

// appleWebProfile: the browser round trip's code, redeemed for the Services ID.
func (s *Service) appleWebProfile(ctx context.Context, p *Provider, code string, rec oauthstate.Record) (profile, error) {
	idToken, refresh, err := s.appleToken(ctx, p, p.ClientID, code, s.redirectURI(p.ID))
	if err != nil {
		return profile{}, err
	}
	claims, err := s.idTokenClaims(ctx, p, idToken, []string{p.ClientID})
	if err != nil {
		return profile{}, err
	}
	if got, _ := claims["nonce"].(string); rec.Nonce == "" || got != rec.Nonce {
		return profile{}, errors.New("id_token: nonce mismatch")
	}
	prof, err := profileOf(claims)
	prof.Token, prof.TokenClient = refresh, p.ClientID
	return prof, err
}

// keepProviderToken seals Apple's refresh token onto the identity, for revocation at deletion.
func (s *Service) keepProviderToken(ctx context.Context, p *Provider, prof profile) {
	if p.ID != ProviderApple || prof.Token == "" || s.Keys == nil {
		return
	}
	body, _ := json.Marshal(map[string]string{"client_id": prof.TokenClient, "refresh_token": prof.Token})
	sealed, err := s.Keys.SealPlatform(appleTokenContext, body)
	if err == nil {
		err = s.sys(ctx, func(q *db.Queries) error {
			return q.SetIdentityProviderToken(ctx, db.SetIdentityProviderTokenParams{Sealed: sealed, Provider: p.ID, Subject: prof.Subject})
		})
	}
	if err != nil {
		s.Log.Warn("account: could not keep the Apple token", "error", err)
	}
}

// revokeProviderTokens tells Apple to forget the app's grant for each Apple identity (App Store
// review guideline 5.1.1(v)). Best effort: the account is gone either way.
func (s *Service) revokeProviderTokens(ctx context.Context, idents []db.UserIdentity) {
	p := s.provider(ProviderApple)
	for _, id := range idents {
		if id.Provider != ProviderApple || id.ProviderTokenSealed == "" || p == nil || s.Keys == nil {
			continue
		}
		plain, err := s.Keys.OpenPlatform(appleTokenContext, id.ProviderTokenSealed)
		if err != nil {
			continue
		}
		var tok struct {
			ClientID     string `json:"client_id"`
			RefreshToken string `json:"refresh_token"`
		}
		if json.Unmarshal(plain, &tok) != nil || tok.ClientID == "" || tok.RefreshToken == "" {
			continue
		}
		if err := s.appleRevoke(ctx, p, tok.ClientID, tok.RefreshToken); err != nil {
			s.Log.Warn("account: Apple token revocation failed", "error", err)
		}
	}
}

func (s *Service) appleRevoke(ctx context.Context, p *Provider, clientID, token string) error {
	secret, err := p.clientSecret(clientID, s.Now())
	if err != nil {
		return err
	}
	form := url.Values{"client_id": {clientID}, "client_secret": {secret}, "token": {token}, "token_type_hint": {"refresh_token"}}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Apple.RevokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("revoke answered %d", resp.StatusCode)
	}
	return nil
}

/* ------------------------------------------------------------ routes */

// POST /api/auth/oauth/apple/callback: Apple's form_post. Re-issued as a GET on the same callback so
// the browser sends the SameSite=Lax cookie that binds the state to it.
func (s *Service) handleOAuthFormPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || len(r.PostForm.Encode()) > 16<<10 {
		failTo(w, r, localeOf(r, ""), oauthstate.PurposeLogin, "", OAuthProviderError)
		return
	}
	q := url.Values{}
	for _, k := range []string{"code", "state", "error"} {
		if v := r.PostForm.Get(k); v != "" {
			q.Set(k, v)
		}
	}
	oauthRedirect(w, r, "/api/auth/oauth/"+ProviderApple+"/callback?"+q.Encode())
}

// POST /api/auth/apple/native {id_token, nonce, authorization_code, locale?, invitation_token?} →
// a session ({token, user}), 2fa_required, or an error code (provider_unavailable, provider_error,
// oauth_email_unverified, signup_closed, invite_only, account_disabled).
func (s *Service) handleAppleNative(w http.ResponseWriter, r *http.Request) {
	if !s.oauthTries.allow("oauth:" + s.clientIP(r)) {
		tooMany(w)
		return
	}
	var in struct {
		IDToken           string `json:"id_token"`
		Nonce             string `json:"nonce"`
		AuthorizationCode string `json:"authorization_code"`
		Locale            string `json:"locale"`
		InvitationToken   string `json:"invitation_token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil ||
		in.IDToken == "" || in.Nonce == "" || in.AuthorizationCode == "" || len(in.AuthorizationCode) > 2048 {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "id_token, nonce and authorization_code are required")
		return
	}
	p := s.provider(ProviderApple)
	if !p.nativeReady() {
		fail(w, http.StatusNotFound, OAuthUnavailable, "Sign in with Apple is not available")
		return
	}
	ctx := r.Context()
	claims, err := s.idTokenClaims(ctx, p, in.IDToken, p.Apple.BundleIDs)
	sum := sha256.Sum256([]byte(in.Nonce))
	if err == nil {
		if got, _ := claims["nonce"].(string); got != hex.EncodeToString(sum[:]) {
			err = errors.New("id_token: nonce mismatch")
		}
	}
	var prof profile
	if err == nil {
		prof, err = profileOf(claims)
	}
	if err == nil {
		// The code proves the sign-in is fresh (single use, minutes old) and yields the refresh
		// token deletion revokes. Its id_token must be the same Apple account.
		aud, _ := claims.GetAudience()
		var idToken string
		idToken, prof.Token, err = s.appleToken(ctx, p, aud[0], in.AuthorizationCode, "")
		prof.TokenClient = aud[0]
		if err == nil {
			var c2 jwt.MapClaims
			if c2, err = s.idTokenClaims(ctx, p, idToken, p.Apple.BundleIDs); err == nil {
				if sub, _ := c2["sub"].(string); sub != prof.Subject {
					err = errors.New("authorization code is for another account")
				}
			}
		}
	}
	if err != nil {
		s.Log.Warn("account: native Apple sign-in refused", "error", err)
		fail(w, http.StatusUnauthorized, OAuthProviderError, "Apple could not confirm this sign-in — try again")
		return
	}
	rec := oauthstate.Record{Purpose: oauthstate.PurposeLogin, Provider: p.ID, Locale: localeOf(r, in.Locale)}
	if inv := strings.TrimSpace(in.InvitationToken); inv != "" && len(inv) < 200 {
		rec.InvitationHash = tokens.Hash(inv)
	}
	u, linked, err := s.resolveOAuthUser(ctx, p, rec, prof)
	if err != nil {
		s.oauthJSONError(w, p, err)
		return
	}
	s.keepProviderToken(ctx, p, prof)
	if linked {
		s.audit(ctx, u.ID, "auth.oauth_linked", map[string]any{"provider": p.ID, "email": prof.Email})
	}
	if !u.Disabled {
		s.audit(ctx, u.ID, "auth.oauth_login", map[string]any{"provider": p.ID, "native": true})
	}
	s.complete(w, r, u, nil)
}

// oauthJSONError answers a failed account resolution as JSON (the app's endpoints).
func (s *Service) oauthJSONError(w http.ResponseWriter, p *Provider, err error) {
	var oe *oauthError
	if !errors.As(err, &oe) {
		s.Log.Error("account: OAuth sign-in failed", "provider", p.ID, "error", err)
		fail(w, http.StatusInternalServerError, OAuthInternal, "sign-in failed")
		return
	}
	status := http.StatusForbidden
	if oe.code == OAuthEmailUnverified {
		status = http.StatusUnprocessableEntity
	}
	fail(w, status, oe.code, oauthMessage(oe.code))
}

func oauthMessage(code string) string {
	switch code {
	case OAuthEmailUnverified:
		return "the provider did not confirm an email address for this account"
	case OAuthSignupClosed:
		return "sign-up is closed"
	case OAuthInviteOnly:
		return "sign-up is by invitation only"
	}
	return "sign-in failed"
}
