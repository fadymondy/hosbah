package apm

import (
	"fmt"

	"github.com/fadymondy/hosbah/internal/tokens"
)

// NewKey mints a fresh APM ingest key: a secret bearer credential (unlike RUM's key, this one is
// never shown to a visitor's browser). Returns the plaintext (shown once, then only the hash and
// the team-DEK-sealed copy are kept) and its sha256 hash (app_apm_settings.key_hash, matched against
// tenancy.Scope{TokenHash: ...} by the public ingest routes).
func NewKey() (plain, hash string) { return tokens.New(tokens.PrefixApm) }

// EnvSealContext binds the sealed copy of an app's APM key to that app, mirroring
// internal/deploy.EnvSealContext's per-value binding (deploy env vars are bound to app+name; there
// is only one APM key per app, so app alone is enough context to prevent it being opened for a
// different app's sealed blob).
func EnvSealContext(appID string) string { return "apm-key:" + appID }

// Snippet is the env block to paste into the app's own environment (or, once auto-injection runs
// on next deploy, purely informational — see internal/deploy's injectApmEnv, which sets the same
// four vars automatically whenever they are not already set).
func Snippet(publicURL, key, serviceName string) string {
	return fmt.Sprintf(
		"OTEL_EXPORTER_OTLP_ENDPOINT=%s/api/otlp\nOTEL_EXPORTER_OTLP_PROTOCOL=http/json\nOTEL_EXPORTER_OTLP_HEADERS=x-hosbah-key=%s\nOTEL_SERVICE_NAME=%s\n",
		publicURL, key, serviceName)
}
