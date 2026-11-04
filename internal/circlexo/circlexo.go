// Package circlexo connects Hosbah to the CircleXO hub (docs/circlexo.md): sign in with the hub
// (with account linking), hub orgs mapped to teams, plan entitlements from the hub, and hub-issued
// MCP tokens. Everything is off unless CIRCLEXO_ISSUER is set; with it unset the process behaves
// exactly as before, and Current() is nil.
//
// The hub's Go SDK (github.com/circlexo/circlexo-go) does the protocol work: OIDC, JWT
// verification, Standard Webhooks, the entitlements cache. This package is Hosbah's side of it:
// which user, which team, which role.
package circlexo

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/circlexo/circlexo-go"
	"github.com/circlexo/circlexo-go/entitlements"
	"github.com/circlexo/circlexo-go/oidc"
	"github.com/circlexo/circlexo-go/service"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/brand"
	"github.com/fadymondy/hosbah/internal/crypto"
	"github.com/fadymondy/hosbah/internal/quota"
)

// Paths, matching manifests/hosbah/circlexo.app.yaml in the hub repository.
const (
	LoginPath       = "/auth/circlexo/login"
	CallbackPath    = "/auth/circlexo/callback"
	LogoutPath      = "/auth/circlexo/logout"
	LaunchPath      = "/sso/circlexo"
	EventsPath      = "/api/circlexo/events"
	ProvisionPath   = "/api/circlexo/provision"
	DeprovisionPath = "/api/circlexo/deprovision"
	ConfigPath      = "/api/circlexo/config"
)

// Service is the integration, built once at boot from the environment.
type Service struct {
	DB   *sql.DB // the app pool: every circlexo_* table is platform data (no row-level security)
	Keys *crypto.Keyring
	Log  *slog.Logger
	Auth *auth.Service

	Cfg      circlexo.Config
	Client   *circlexo.Client
	Verifier *circlexo.Verifier
	Ents     *entitlements.Cache
	RP       *oidc.RP

	// WebhookSecrets verify the hub's Standard Webhooks signatures (the provision endpoint too).
	WebhookSecrets []string
	// Now is the clock the webhook checks use; nil is time.Now. Tests set it.
	Now func() time.Time
}

var current atomic.Pointer[Service]

// Current returns the running integration, or nil when it is off.
func Current() *Service { return current.Load() }

// Enabled reports whether the integration is on.
func Enabled() bool { return Current() != nil }

// Secrets are the two values that are not part of the hub client configuration.
type Secrets struct {
	Webhook string // whsec_<base64>, the app's webhook signing secret
	Session string // seals the sign-in state cookie; any string of 16+ characters
}

// FromEnv builds the service from CIRCLEXO_* (names in .env.example). It returns nil, nil when
// CIRCLEXO_ISSUER is unset: the integration is off. A partial or invalid configuration is an
// error, so the caller can say so loudly rather than run half-connected.
func FromEnv(db *sql.DB, keys *crypto.Keyring, log *slog.Logger, authSvc *auth.Service) (*Service, error) {
	if strings.TrimSpace(os.Getenv("CIRCLEXO_ISSUER")) == "" {
		return nil, nil
	}
	cfg, err := circlexo.FromEnv()
	if err != nil {
		return nil, err
	}
	return New(db, keys, log, authSvc, cfg, redirectURL(), Secrets{
		Webhook: os.Getenv("CIRCLEXO_WEBHOOK_SECRET"),
		Session: os.Getenv("CIRCLEXO_SESSION_KEY"),
	})
}

// New builds the service from an explicit configuration (tests point it at circlexotest).
func New(db *sql.DB, keys *crypto.Keyring, log *slog.Logger, authSvc *auth.Service, cfg circlexo.Config, redirect string, sec Secrets) (*Service, error) {
	if authSvc == nil {
		return nil, errors.New("circlexo: the auth service is not available")
	}
	if keys == nil {
		return nil, errors.New("circlexo: the keyring is not available")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.ClientSecret == "" && cfg.PrivateKeyPEM == "" {
		return nil, errors.New("circlexo: CIRCLEXO_CLIENT_SECRET or CIRCLEXO_PRIVATE_KEY is required (the sign-in code exchange authenticates with it)")
	}
	if strings.TrimSpace(sec.Webhook) == "" {
		return nil, errors.New("circlexo: CIRCLEXO_WEBHOOK_SECRET is required")
	}
	key, err := sessionKey(sec.Session)
	if err != nil {
		return nil, err
	}
	rp, err := oidc.New(cfg, redirect, key)
	if err != nil {
		return nil, err
	}
	tokens, err := tokenSource(cfg)
	if err != nil {
		return nil, err
	}
	client := circlexo.NewClient(cfg, tokens)
	return &Service{
		DB: db, Keys: keys, Log: log, Auth: authSvc, Cfg: cfg, Client: client, Verifier: circlexo.NewVerifier(cfg),
		Ents: entitlements.New(client), RP: rp, WebhookSecrets: []string{sec.Webhook},
	}, nil
}

// tokenSource is how Hosbah calls the hub's app APIs: the app key when there is one, else a
// service token from the client secret or private key (the scopes the calls below need).
func tokenSource(cfg circlexo.Config) (circlexo.TokenSource, error) {
	if cfg.AppKey != "" {
		return nil, nil // NewClient's default: the app key
	}
	c, err := service.New(cfg)
	if err != nil {
		return nil, err
	}
	return service.NewSource(c, "billing.entitlements", "orgs.read", "tenants.read", "tenants.write"), nil
}

// sessionKey turns CIRCLEXO_SESSION_KEY into the 32 bytes the OIDC state cookie is sealed with.
// Without it, one is derived (one way, domain-separated) from HOSBAH_KEK, which is already required
// to boot, so a deployment does not need another secret.
func sessionKey(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		if kek := strings.TrimSpace(brand.Getenv("KEK")); kek != "" {
			v = "circlexo-oidc-state:" + kek
		}
	}
	if len(v) < 16 {
		return nil, errors.New("circlexo: set CIRCLEXO_SESSION_KEY (16+ characters)")
	}
	sum := sha256.Sum256([]byte(v))
	return sum[:], nil
}

// baseURL is the public origin of the web app, which is also what the hub redirects back to: the
// callback is on the web host (proxied to the API), like every page the browser opens.
func baseURL() string { return strings.TrimRight(brand.Getenv("WEB_ORIGIN"), "/") }

func redirectURL() string {
	if v := strings.TrimSpace(os.Getenv("CIRCLEXO_REDIRECT_URL")); v != "" {
		return v
	}
	return baseURL() + CallbackPath
}

// Install makes s the running integration. Passing nil (the integration is off) clears it, which
// matters to a test process that boots more than once.
//
// Hosbah is free and open source (2026-10-15): the hub's plan no longer decides a team's limits, so
// no quota overlay is installed. A hub-linked team keeps the quotas a platform admin set (unlimited
// by default), which are abuse protection, not a plan.
func Install(s *Service) {
	current.Store(s)
	quota.SetOverlay(nil)
}

// ConfigHandler answers GET /api/circlexo/config (PUBLIC, no secrets): whether the sign-in button
// should show. The web reads it; with the integration off it answers {"enabled": false}.
func ConfigHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if Enabled() {
		_, _ = w.Write([]byte(`{"enabled":true,"login_url":"` + LoginPath + `"}`))
		return
	}
	_, _ = w.Write([]byte(`{"enabled":false}`))
}
