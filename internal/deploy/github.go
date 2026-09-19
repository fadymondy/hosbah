package deploy

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

/*
The GitHub App integration, behind an interface so Hosbah runs without it.

What the owner must supply (nothing is configured yet):
  CLOUDY_GITHUB_APP_ID                the App's numeric id
  CLOUDY_GITHUB_APP_SLUG              its URL name (for the "Install" link)
  CLOUDY_GITHUB_APP_PRIVATE_KEY_FILE  the App's private key (.pem), readable only by the service
  CLOUDY_GITHUB_WEBHOOK_SECRET        the webhook secret set on the App

With them, Hosbah mints a short-lived app JWT (RS256, 9 minutes), exchanges it for a 1-hour
installation token per clone, and verifies every webhook delivery's X-Hub-Signature-256 HMAC.
Without them, the UI says "GitHub App not configured" and /hooks/github answers 503; public git
URLs and the per-app deploy hook work either way.
*/

// ErrGitHubNotConfigured: the App's credentials are not set.
var ErrGitHubNotConfigured = errors.New("GitHub App not configured")

// GitHubApp is what Hosbah needs from GitHub.
type GitHubApp interface {
	Configured() bool
	Slug() string
	// InstallationToken is a short-lived token that can clone the installation's repositories.
	InstallationToken(ctx context.Context, installationID int64) (token string, expires time.Time, err error)
	// VerifyWebhook checks a delivery's X-Hub-Signature-256 header against the webhook secret.
	VerifyWebhook(body []byte, signatureHeader string) bool
}

// GitHubFromEnv builds the integration from the environment: unconfigured when anything is missing.
func GitHubFromEnv() GitHubApp {
	id, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv("CLOUDY_GITHUB_APP_ID")), 10, 64)
	keyFile := strings.TrimSpace(os.Getenv("CLOUDY_GITHUB_APP_PRIVATE_KEY_FILE"))
	secret := os.Getenv("CLOUDY_GITHUB_WEBHOOK_SECRET")
	if id == 0 || keyFile == "" || secret == "" {
		return unconfiguredGitHub{}
	}
	pemBytes, err := os.ReadFile(keyFile) //#nosec G304 -- operator-configured key file
	if err != nil {
		return unconfiguredGitHub{}
	}
	key, err := ParseRSAPrivateKey(pemBytes)
	if err != nil {
		return unconfiguredGitHub{}
	}
	return &githubApp{appID: id, slug: os.Getenv("CLOUDY_GITHUB_APP_SLUG"), key: key, secret: []byte(secret),
		api: "https://api.github.com", http: &http.Client{Timeout: 15 * time.Second}, now: time.Now}
}

type unconfiguredGitHub struct{}

func (unconfiguredGitHub) Configured() bool { return false }
func (unconfiguredGitHub) Slug() string     { return "" }
func (unconfiguredGitHub) InstallationToken(context.Context, int64) (string, time.Time, error) {
	return "", time.Time{}, ErrGitHubNotConfigured
}
func (unconfiguredGitHub) VerifyWebhook([]byte, string) bool { return false }

type githubApp struct {
	appID  int64
	slug   string
	key    *rsa.PrivateKey
	secret []byte
	api    string
	http   *http.Client
	now    func() time.Time
}

func (g *githubApp) Configured() bool { return true }
func (g *githubApp) Slug() string     { return g.slug }

func (g *githubApp) VerifyWebhook(body []byte, header string) bool {
	return VerifySignature(g.secret, body, header)
}

// VerifySignature checks GitHub's "sha256=<hex HMAC of the raw body>" in constant time.
func VerifySignature(secret, body []byte, header string) bool {
	hexSig, ok := strings.CutPrefix(strings.TrimSpace(header), "sha256=")
	if !ok || len(secret) == 0 {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// appJWT is the App's own credential: RS256, issued a minute in the past (clock skew), 9 minutes long.
func (g *githubApp) appJWT() (string, error) {
	now := g.now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": strconv.FormatInt(g.appID, 10)})
	signing := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(nil, g.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (g *githubApp) InstallationToken(ctx context.Context, installationID int64) (string, time.Time, error) {
	jwt, err := g.appJWT()
	if err != nil {
		return "", time.Time{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/app/installations/%d/access_tokens", g.api, installationID), nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := g.http.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return "", time.Time{}, fmt.Errorf("GitHub answered %d minting an installation token", resp.StatusCode)
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Token == "" {
		return "", time.Time{}, errors.New("GitHub's installation-token answer is unreadable")
	}
	return out.Token, out.ExpiresAt, nil
}

// ParseRSAPrivateKey reads a PEM RSA key (PKCS#1, as GitHub issues them, or PKCS#8).
func ParseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("not a PEM file")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA key")
	}
	return rk, nil
}
