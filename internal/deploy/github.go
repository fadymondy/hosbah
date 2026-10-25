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

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/brand"
)

/*
The GitHub App integration, behind an interface so Hosbah runs without it.

What the owner must supply (nothing is configured yet):
  HOSBAH_GITHUB_APP_ID                the App's numeric id
  HOSBAH_GITHUB_APP_SLUG              its URL name (for the "Install" link)
  HOSBAH_GITHUB_APP_PRIVATE_KEY_FILE  the App's private key (.pem), readable only by the service
  HOSBAH_GITHUB_WEBHOOK_SECRET        the webhook secret set on the App
  (each also read as CLOUDY_…, its name before the rename)

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
	// Installation reads an installation with the App's own JWT (GET /app/installations/{id}):
	// the only source of truth for which account it is on. ErrInstallationNotFound when GitHub
	// does not know it (or it is another App's).
	Installation(ctx context.Context, installationID int64) (GitHubInstallation, error)
	// InstallationRepos lists the repositories an installation can reach (GET
	// /installation/repositories with a fresh installation token, every page, at most max).
	InstallationRepos(ctx context.Context, installationID int64, max int) ([]GitHubAppRepo, error)
	// AddDeployKey adds publicKey as a deploy key of owner/repo (MH-578 C11), with a fresh
	// installation token. readOnly deploy keys can only fetch; Hosbah's server deploy keys are
	// always read-only. Returns GitHub's numeric key id.
	AddDeployKey(ctx context.Context, installationID int64, owner, repo, title, publicKey string, readOnly bool) (int64, error)
	// Compare is GET /repos/{owner}/{repo}/compare/{base}...{head} with a fresh installation token:
	// the files head changed since its merge base with base (the build rules' changed paths).
	Compare(ctx context.Context, installationID int64, owner, repo, base, head string) (GitHubComparison, error)
}

// GitHubComparison is GitHub's answer to base...head. Status is ahead (head descends from base),
// identical, behind or diverged; Files is complete unless Truncated (GitHub lists at most 300).
type GitHubComparison struct {
	Status    string
	Files     []string
	Truncated bool
}

// maxCompareFiles is GitHub's cap on a comparison's file list: an answer that long may have more.
const maxCompareFiles = 300

// ErrInstallationNotFound: GitHub does not know that installation (for this App).
var ErrInstallationNotFound = errors.New("GitHub does not know that installation of the App")

// GitHubInstallation is GitHub's own answer about an installation.
type GitHubInstallation struct {
	ID                  int64
	AccountLogin        string
	AccountType         string // User | Organization | Enterprise
	RepositorySelection string // all | selected
	SuspendedAt         *time.Time
}

// GitHubAppRepo is a repository an installation can reach.
type GitHubAppRepo struct {
	InstallationID int64     `json:"installation_id"`
	Name           string    `json:"name"`
	NameWithOwner  string    `json:"name_with_owner"`
	IsPrivate      bool      `json:"is_private"`
	DefaultBranch  string    `json:"default_branch"`
	URL            string    `json:"url"`
	Description    string    `json:"description,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// MaxAppRepos bounds a team's repository listing.
const MaxAppRepos = 2000

// GitHubFromEnv builds the integration from the environment: unconfigured when anything is missing.
func GitHubFromEnv() GitHubApp {
	id, _ := strconv.ParseInt(strings.TrimSpace(brand.Getenv("GITHUB_APP_ID")), 10, 64)
	keyFile := strings.TrimSpace(brand.Getenv("GITHUB_APP_PRIVATE_KEY_FILE"))
	secret := brand.Getenv("GITHUB_WEBHOOK_SECRET")
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
	return &githubApp{appID: id, slug: brand.Getenv("GITHUB_APP_SLUG"), key: key, secret: []byte(secret),
		api: "https://api.github.com", http: &http.Client{Timeout: 15 * time.Second}, now: time.Now}
}

type unconfiguredGitHub struct{}

func (unconfiguredGitHub) Configured() bool { return false }
func (unconfiguredGitHub) Slug() string     { return "" }
func (unconfiguredGitHub) InstallationToken(context.Context, int64) (string, time.Time, error) {
	return "", time.Time{}, ErrGitHubNotConfigured
}
func (unconfiguredGitHub) VerifyWebhook([]byte, string) bool { return false }
func (unconfiguredGitHub) Installation(context.Context, int64) (GitHubInstallation, error) {
	return GitHubInstallation{}, ErrGitHubNotConfigured
}
func (unconfiguredGitHub) AddDeployKey(context.Context, int64, string, string, string, string, bool) (int64, error) {
	return 0, ErrGitHubNotConfigured
}
func (unconfiguredGitHub) InstallationRepos(context.Context, int64, int) ([]GitHubAppRepo, error) {
	return nil, ErrGitHubNotConfigured
}
func (unconfiguredGitHub) Compare(context.Context, int64, string, string, string, string) (GitHubComparison, error) {
	return GitHubComparison{}, ErrGitHubNotConfigured
}

// NewGitHubApp builds the integration from its parts; apiBase is https://api.github.com in
// production (tests pass a fake's URL).
func NewGitHubApp(appID int64, slug string, key *rsa.PrivateKey, webhookSecret []byte, apiBase string, client *http.Client) GitHubApp {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &githubApp{appID: appID, slug: slug, key: key, secret: webhookSecret, api: strings.TrimRight(apiBase, "/"), http: client, now: time.Now}
}

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

// apiGet GETs path with a bearer credential into out. 404 is ErrInstallationNotFound.
func (g *githubApp) apiGet(ctx context.Context, bearer, path string, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.api+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := g.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return resp.Header, ErrInstallationNotFound
	case resp.StatusCode != http.StatusOK:
		return resp.Header, fmt.Errorf("GitHub answered %d for %s", resp.StatusCode, strings.SplitN(path, "?", 2)[0])
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.Header, errors.New("GitHub's answer is unreadable")
	}
	return resp.Header, nil
}

func (g *githubApp) Installation(ctx context.Context, installationID int64) (GitHubInstallation, error) {
	if installationID <= 0 {
		return GitHubInstallation{}, ErrInstallationNotFound
	}
	jwt, err := g.appJWT()
	if err != nil {
		return GitHubInstallation{}, err
	}
	var out struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
		RepositorySelection string     `json:"repository_selection"`
		SuspendedAt         *time.Time `json:"suspended_at"`
	}
	if _, err := g.apiGet(ctx, jwt, fmt.Sprintf("/app/installations/%d", installationID), &out); err != nil {
		return GitHubInstallation{}, err
	}
	if out.ID != installationID || out.Account.Login == "" {
		return GitHubInstallation{}, ErrInstallationNotFound
	}
	return GitHubInstallation{ID: out.ID, AccountLogin: out.Account.Login, AccountType: out.Account.Type,
		RepositorySelection: out.RepositorySelection, SuspendedAt: out.SuspendedAt}, nil
}

func (g *githubApp) InstallationRepos(ctx context.Context, installationID int64, max int) ([]GitHubAppRepo, error) {
	token, _, err := g.InstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	var repos []GitHubAppRepo
	for page := 1; page <= 50 && len(repos) < max; page++ {
		var out struct {
			TotalCount   int `json:"total_count"`
			Repositories []struct {
				Name          string    `json:"name"`
				FullName      string    `json:"full_name"`
				Private       bool      `json:"private"`
				DefaultBranch string    `json:"default_branch"`
				HTMLURL       string    `json:"html_url"`
				Description   *string   `json:"description"`
				UpdatedAt     time.Time `json:"updated_at"`
			} `json:"repositories"`
		}
		if _, err := g.apiGet(ctx, token, fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page), &out); err != nil {
			return nil, err
		}
		for _, r := range out.Repositories {
			d := ""
			if r.Description != nil {
				d = *r.Description
			}
			repos = append(repos, GitHubAppRepo{InstallationID: installationID, Name: r.Name, NameWithOwner: r.FullName, IsPrivate: r.Private,
				DefaultBranch: r.DefaultBranch, URL: r.HTMLURL, Description: d, UpdatedAt: r.UpdatedAt})
		}
		if len(out.Repositories) < 100 || len(repos) >= out.TotalCount {
			break
		}
	}
	if len(repos) > max {
		repos = repos[:max]
	}
	return repos, nil
}

// Compare: per_page=1 keeps the commit list short; the file list is the whole comparison's anyway
// (GitHub sends it on the first page, up to 300 files).
func (g *githubApp) Compare(ctx context.Context, installationID int64, owner, repo, base, head string) (GitHubComparison, error) {
	if !agentproto.ValidGitHubRev(owner+"/"+repo) || strings.Count(owner+"/"+repo, "/") != 1 || !agentproto.ValidGitHubRev(base) || !agentproto.ValidGitHubRev(head) {
		return GitHubComparison{}, errors.New("not a comparable repository or commit")
	}
	token, _, err := g.InstallationToken(ctx, installationID)
	if err != nil {
		return GitHubComparison{}, err
	}
	var out struct {
		Status string `json:"status"`
		Files  []struct {
			Filename         string `json:"filename"`
			PreviousFilename string `json:"previous_filename"`
		} `json:"files"`
	}
	if _, err := g.apiGet(ctx, token, fmt.Sprintf("/repos/%s/%s/compare/%s...%s?per_page=1", owner, repo, base, head), &out); err != nil {
		return GitHubComparison{}, err
	}
	c := GitHubComparison{Status: out.Status, Truncated: len(out.Files) >= maxCompareFiles}
	for _, f := range out.Files {
		c.Files = append(c.Files, f.Filename)
		if f.PreviousFilename != "" { // a rename changes its old path too
			c.Files = append(c.Files, f.PreviousFilename)
		}
	}
	return c, nil
}

// AddDeployKey: POST /repos/{owner}/{repo}/keys with a fresh installation token. 422 (GitHub's
// answer when the exact key is already attached to that repository, or the title is taken) is
// reported as a plain error the caller can show — Hosbah does not try to disambiguate GitHub's
// message further.
func (g *githubApp) AddDeployKey(ctx context.Context, installationID int64, owner, repo, title, publicKey string, readOnly bool) (int64, error) {
	token, _, err := g.InstallationToken(ctx, installationID)
	if err != nil {
		return 0, err
	}
	body, _ := json.Marshal(map[string]any{"title": title, "key": publicKey, "read_only": readOnly})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/repos/%s/%s/keys", g.api, owner, repo), strings.NewReader(string(body)))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		var ghErr struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &ghErr)
		if ghErr.Message == "" {
			ghErr.Message = fmt.Sprintf("GitHub answered %d", resp.StatusCode)
		}
		return 0, fmt.Errorf("GitHub refused the deploy key: %s", ghErr.Message)
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, errors.New("GitHub's deploy-key answer is unreadable")
	}
	return out.ID, nil
}
