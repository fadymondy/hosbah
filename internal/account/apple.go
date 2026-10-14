package account

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"

	"github.com/fadymondy/hosbah/internal/brand"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/oauthstate"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Sign in with Apple.

Settings (HOSBAH_…, each also CLOUDY_…):

	APPLE_TEAM_ID                 the Apple Developer team id
	APPLE_KEY_ID                  the id of a "Sign in with Apple" key …
	APPLE_PRIVATE_KEY(_FILE)      … and its .p8 (PEM; "\n" escapes are accepted in the env value)
	APPLE_SERVICES_ID             the web: the Services ID (client_id of the browser round trip),
	                              Return URL <origin>/api/auth/oauth/apple/callback
	APPLE_BUNDLE_IDS              the iOS app: its bundle id(s), comma-separated (the identity
	                              token's audience, and the client_id its code is redeemed with)

Apple has no static client secret: every token request carries an ES256 JWT signed with the key
(clientSecret), valid for five minutes.

The web round trip asks for the name and the address, which obliges response_mode=form_post: Apple's
answer is a cross-site POST to the callback. A SameSite=Lax cookie is not sent on a cross-site POST,
so that request cannot be tied to the browser that started the trip. Two ways out were possible:

 1. accept the POST on the server-side state alone (the row, its nonce, single use);
 2. keep what Apple POSTed on the state's row and answer 303 to a GET of the callback with only the
    state in the URL. That GET is a top-level navigation, which does carry the Lax cookie, and it goes
    through oauthstate.Finish exactly like Google's and GitHub's callbacks.

This is 2. With 1, anybody could start a trip in their own browser, sign in at Apple as themselves
and get a victim's browser to submit the POST (an auto-submitting form on any page): the victim
would be signed in to the attacker's account (login CSRF), which the browser binding exists to
prevent. With 2 the code is never put in a URL (it stays on the server-side row), the state is still
single use, and only the browser holding the cookie can finish.

Apple's id_token says email_verified (Apple only hands out addresses it verified; sometimes as the
string "true") and is_private_email for a "Hide My Email" relay address. The user's name arrives
only on the first consent (the form's "user" field, or fullName from the iOS SDK): it is kept on the
identity, since Apple never sends it again.

The refresh token Apple returns is sealed (HOSBAH_KEK) on the identity and revoked
(https://appleid.apple.com/auth/revoke) when the account is deleted or Apple is disconnected — App
Store guideline 5.1.1(v).
*/

func appleFromEnv(client *http.Client) *Provider {
	team, keyID := strings.TrimSpace(brand.Getenv("APPLE_TEAM_ID")), strings.TrimSpace(brand.Getenv("APPLE_KEY_ID"))
	services := strings.TrimSpace(brand.Getenv("APPLE_SERVICES_ID"))
	bundles := nonEmpty(strings.Split(brand.Getenv("APPLE_BUNDLE_IDS"), ",")...)
	if team == "" || keyID == "" || (services == "" && len(bundles) == 0) {
		return nil
	}
	raw := brand.Getenv("APPLE_PRIVATE_KEY")
	if f := strings.TrimSpace(brand.Getenv("APPLE_PRIVATE_KEY_FILE")); raw == "" && f != "" {
		b, err := os.ReadFile(f) //#nosec G304 -- an operator-configured path
		if err != nil {
			return nil
		}
		raw = string(b)
	}
	key, err := ParseAppleKey(raw)
	if err != nil {
		return nil
	}
	return NewAppleProvider(services, bundles, team, keyID, key, "https://appleid.apple.com", client)
}

// NewAppleProvider builds the Apple provider against base (https://appleid.apple.com; tests: a fake).
func NewAppleProvider(servicesID string, bundleIDs []string, teamID, keyID string, key *ecdsa.PrivateKey, base string, client *http.Client) *Provider {
	base = strings.TrimRight(base, "/")
	return &Provider{ID: ProviderApple, Name: "Apple", ClientID: servicesID, NativeAudiences: bundleIDs,
		TeamID: teamID, KeyID: keyID, SigningKey: key,
		AuthURL: base + "/auth/authorize", TokenURL: base + "/auth/token", RevokeURL: base + "/auth/revoke",
		JWKSURL: base + "/auth/keys", Issuers: []string{"https://appleid.apple.com"}, HTTP: client}
}

// ParseAppleKey reads the .p8 (PKCS#8 PEM, EC P-256). Literal "\n" sequences (a key pasted into a
// one-line env value) are turned back into newlines.
func ParseAppleKey(raw string) (*ecdsa.PrivateKey, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, `\n`, "\n"))
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("apple: the private key is not PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apple: %w", err)
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("apple: the private key is not an EC key")
	}
	return ec, nil
}

// clientSecret is Apple's client secret for clientID (the Services ID, or a bundle id): an ES256
// JWT by the team's key, five minutes.
func (s *Service) clientSecret(p *Provider, clientID string) (string, error) {
	if p.SigningKey == nil {
		return "", errors.New("apple: no signing key")
	}
	now := s.Now().UTC()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": p.TeamID, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"aud": "https://appleid.apple.com", "sub": clientID,
	})
	tok.Header["kid"] = p.KeyID
	return tok.SignedString(p.SigningKey)
}

type appleTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Error        string `json:"error"`
}

// appleRedeem trades an authorization code for Apple's tokens. redirectURI is the web callback for
// the browser round trip and "" for a code from the iOS SDK.
func (s *Service) appleRedeem(ctx context.Context, p *Provider, clientID, code, redirectURI string) (appleTokens, error) {
	secret, err := s.clientSecret(p, clientID)
	if err != nil {
		return appleTokens{}, err
	}
	form := url.Values{"client_id": {clientID}, "client_secret": {secret}, "code": {code}, "grant_type": {"authorization_code"}}
	if redirectURI != "" {
		form.Set("redirect_uri", redirectURI)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return appleTokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var tok appleTokens
	if err := doJSON(p.HTTP, req, &tok); err != nil {
		return appleTokens{}, err
	}
	if tok.Error != "" {
		return appleTokens{}, fmt.Errorf("apple token endpoint: %s", tok.Error)
	}
	return tok, nil
}

// appleUser is the "user" field of Apple's first form_post: the name and address the person shared.
type appleUser struct {
	Name struct {
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
	} `json:"name"`
}

func joinName(first, last string) string {
	return strings.TrimSpace(strings.TrimSpace(first) + " " + strings.TrimSpace(last))
}

// appleWebProfile redeems the browser round trip's code (Services ID, our redirect URI) and verifies
// the id_token it returns (audience the Services ID, the nonce this trip stored).
func (s *Service) appleWebProfile(ctx context.Context, p *Provider, code string, rec oauthstate.Record) (profile, error) {
	tok, err := s.appleRedeem(ctx, p, p.ClientID, code, s.redirectURI(p.ID))
	if err != nil {
		return profile{}, err
	}
	if rec.Nonce == "" {
		return profile{}, errors.New("id_token: no nonce stored")
	}
	claims, err := s.verifyIDToken(ctx, p, tok.IDToken, []string{p.ClientID}, rec.Nonce, 0)
	if err != nil {
		return profile{}, err
	}
	prof := claimsProfile(claims)
	if rec.ProviderUser != "" {
		var u appleUser
		if json.Unmarshal([]byte(rec.ProviderUser), &u) == nil {
			prof.Name = joinName(u.Name.FirstName, u.Name.LastName)
		}
	}
	prof.RefreshToken, prof.TokenClient = tok.RefreshToken, p.ClientID
	return prof, nil
}

// appleNativeProfile checks what the iOS SDK (expo-apple-authentication) returned: the identity
// token (audience one of our bundle ids, ten minutes old at most, the nonce if the app sent one),
// and its authorization code, redeemed here with that bundle id. The redemption is what proves the
// sign-in is fresh (a code works once, for five minutes) and gives the refresh token to revoke at
// deletion; its id_token must name the same Apple account.
func (s *Service) appleNativeProfile(ctx context.Context, p *Provider, identityToken, code, nonce, name string) (profile, error) {
	claims, err := s.verifyIDToken(ctx, p, identityToken, p.NativeAudiences, nonce, nativeMaxAge)
	if err != nil {
		return profile{}, err
	}
	aud, _ := claims.GetAudience()
	if len(aud) != 1 {
		return profile{}, errors.New("identity token: audience")
	}
	if code == "" || len(code) > 2048 {
		return profile{}, errors.New("no authorization code")
	}
	tok, err := s.appleRedeem(ctx, p, aud[0], code, "")
	if err != nil {
		return profile{}, err
	}
	redeemed, err := s.verifyIDToken(ctx, p, tok.IDToken, aud, "", 0)
	if err != nil {
		return profile{}, err
	}
	prof := claimsProfile(claims)
	if sub, _ := redeemed["sub"].(string); sub != prof.Subject {
		return profile{}, errors.New("the authorization code is for another Apple account")
	}
	prof.Name = name
	prof.RefreshToken, prof.TokenClient = tok.RefreshToken, aud[0]
	return prof, nil
}

// POST /api/auth/oauth/{provider}/callback — Apple's form_post. See the package comment above: what
// Apple sent is kept on the state's row and the browser is sent on to the GET of the same callback,
// which carries the browser-binding cookie and finishes the trip.
func (s *Service) handleOAuthFormPost(w http.ResponseWriter, r *http.Request) {
	fallback := localeOf(r, "")
	if !s.oauthTries.allow("oauth:" + s.clientIP(r)) {
		failTo(w, r, fallback, oauthstate.PurposeLogin, "", OAuthRateLimited)
		return
	}
	p := s.provider(chi.URLParam(r, "provider"))
	if p == nil || p.ID != ProviderApple {
		failTo(w, r, fallback, oauthstate.PurposeLogin, "", OAuthUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		failTo(w, r, fallback, oauthstate.PurposeLogin, "", OAuthProviderError)
		return
	}
	state := r.PostForm.Get("state")
	next := url.Values{"state": {state}}
	if e := r.PostForm.Get("error"); e != "" {
		next.Set("error", "denied")
	} else if !oauthstate.Stash(r.Context(), s.DB, state, p.ID, r.PostForm.Get("code"), r.PostForm.Get("user")) {
		failTo(w, r, fallback, oauthstate.PurposeLogin, "", OAuthStateInvalid)
		return
	}
	oauthRedirect(w, r, "/api/auth/oauth/"+p.ID+"/callback?"+next.Encode())
}

/* ------------------------------------------------------------ revocation */

type sealedToken struct {
	Provider string
	Sealed   string
}

// revokeProviderTokens ends the grants behind sealed provider tokens of userID (Apple's refresh
// tokens). Best effort, a few seconds at most: the account deletion or disconnection has already
// happened, and Apple's own "Stop using Apple ID" still works for whatever fails here (logged).
func (s *Service) revokeProviderTokens(ctx context.Context, userID string, tokens []sealedToken) {
	for _, t := range tokens {
		if t.Sealed == "" || t.Provider != ProviderApple || s.Keys == nil {
			continue
		}
		p := s.provider(ProviderApple)
		if p == nil {
			s.Log.Warn("account: an Apple token could not be revoked: Apple is not configured")
			continue
		}
		raw, err := s.Keys.OpenPlatform(tokenContext(userID), t.Sealed)
		var pt providerToken
		if err != nil || json.Unmarshal(raw, &pt) != nil || pt.RefreshToken == "" {
			s.Log.Warn("account: an Apple token could not be opened", "error", err)
			continue
		}
		if err := s.appleRevoke(ctx, p, pt); err != nil {
			s.Log.Warn("account: Apple token revocation failed", "error", err)
		}
	}
}

func (s *Service) appleRevoke(ctx context.Context, p *Provider, pt providerToken) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	secret, err := s.clientSecret(p, pt.Client)
	if err != nil {
		return err
	}
	form := url.Values{"client_id": {pt.Client}, "client_secret": {secret}, "token": {pt.RefreshToken}, "token_type_hint": {"refresh_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.RevokeURL, strings.NewReader(form.Encode()))
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
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("apple revoke answered %d", resp.StatusCode)
	}
	return nil
}

// identityTokens reads userID's sealed provider tokens (before the identities are deleted).
func (s *Service) identityTokens(ctx context.Context, userID string) []sealedToken {
	var out []sealedToken
	_ = tenancy.Tx(ctx, s.DB, tenancy.Scope{UserID: userID}, func(q *db.Queries, _ *sql.Tx) error {
		rows, err := q.IdentityTokensOfUser(ctx, userID)
		for _, r := range rows {
			out = append(out, sealedToken{Provider: r.Provider, Sealed: r.ProviderToken})
		}
		return err
	})
	return out
}
