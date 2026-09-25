package deploy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

/*
Per-app PHP version and php.ini overrides (MH-576 D2). Stored on app_deploy_configs
(php_version, php_ini — not versioned, like builder_image/outputs: they are advanced settings a
team tunes in place, not part of the pipeline's step history).

Mechanism: Railpack's PHP provider (railwayapp/railpack core/providers/php) reads the version only
from composer.json's "require"."php" constraint (a leading "^" stripped), defaulting to 8.4 when
absent — it does not honor a RAILPACK_PHP_VERSION env var. So a deploy of a Railpack-built PHP app
that pins a version has the agent rewrite "require"."php" in the checkout's composer.json to that
exact version before `railpack prepare` runs (internal/agent's pinComposerPHPVersion); a repository
with no composer.json is left alone and the build log gets a clear note that the pin needs one.
Railpack only supports PHP 8.2+, so PHPVersions below stops at 8.2. php.ini overrides are written to
a php.ini file at the checkout root before `railpack prepare` runs (Railpack's PHP provider merges a
repository's own php.ini into the image it builds), unless the app already ships one — Hosbah never
overwrites an app's own file. Both are also exposed to the running container as HOSBAH_PHP_VERSION
and HOSBAH_PHP_INI env vars (deploy.injectPHPEnv), which a Dockerfile app's own image is free to
read since it owns its build and Hosbah cannot rewrite composer.json for a build it does not run.
*/

// PHPVersions is every version a team may pin, "auto" meaning "let Railpack detect it from
// composer.json". Railpack's PHP provider only supports 8.2 and up.
var PHPVersions = []string{"auto", "8.2", "8.3", "8.4"}

// ValidPHPVersion reports whether v is one of PHPVersions.
func ValidPHPVersion(v string) bool {
	for _, p := range PHPVersions {
		if p == v {
			return true
		}
	}
	return false
}

// phpIniValueRE rejects control characters, ini section/comment syntax and anything that could
// inject a second directive: no newlines, no ';' (ini comment/separator), no '[' ']' (sections).
var phpIniValueRE = regexp.MustCompile(`^[a-zA-Z0-9_./: +-]{0,200}$`)

// PHPIniKeys is every php.ini directive a team may override, with a short description for the UI.
// Deliberately small: enough to cover the common per-app tuning knobs without opening arbitrary
// ini directives (some, like disable_functions or open_basedir, are security-relevant and stay
// host-managed).
var PHPIniKeys = []string{
	"memory_limit",
	"upload_max_filesize",
	"post_max_size",
	"max_execution_time",
	"max_input_time",
	"max_input_vars",
	"display_errors",
	"date.timezone",
	"opcache.enable",
	"opcache.memory_consumption",
	"opcache.max_accelerated_files",
	"opcache.revalidate_freq",
	"opcache.validate_timestamps",
}

// ValidPHPIniKey reports whether k is one of PHPIniKeys.
func ValidPHPIniKey(k string) bool {
	for _, p := range PHPIniKeys {
		if p == k {
			return true
		}
	}
	return false
}

// MaxPHPIniSettings caps how many overrides one app may save (PHPIniKeys is smaller than this
// anyway; this just backs up the jsonb column's 4 KiB check with a clear error).
const MaxPHPIniSettings = 32

// ValidatePHPIni checks every key is known and every value is a plausible, injection-free ini
// scalar (no newlines, ';', '[' or ']', bounded length). It does not check a value is semantically
// correct for its key (e.g. memory_limit="banana") — Railpack/PHP itself rejects that at runtime,
// and this keeps the check simple and future-proof as PHP adds accepted formats (e.g. "512M", "-1").
func ValidatePHPIni(settings map[string]string) error {
	if len(settings) > MaxPHPIniSettings {
		return fmt.Errorf("too many php.ini overrides (max %d)", MaxPHPIniSettings)
	}
	for k, v := range settings {
		if !ValidPHPIniKey(k) {
			return fmt.Errorf("unknown php.ini setting %q", k)
		}
		if strings.ContainsAny(v, ";[]\r\n\x00") || !phpIniValueRE.MatchString(v) {
			return fmt.Errorf("bad value for %s", k)
		}
	}
	return nil
}

// RenderPHPIni turns a validated overrides map into a php.ini file body, keys sorted for a stable,
// diffable output.
func RenderPHPIni(settings map[string]string) string {
	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("; Written by Hosbah (MH-576 D2) — per-app php.ini overrides.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, settings[k])
	}
	return b.String()
}

// PHPIniEnvValue renders overrides as a single-line "key=value;key2=value2" string, safe because
// ValidatePHPIni already rejects ';' and newlines in values and PHPIniKeys never contains one
// either — for HOSBAH_PHP_INI, a Dockerfile app's own image may read at its own risk.
func PHPIniEnvValue(settings map[string]string) string {
	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+settings[k])
	}
	return strings.Join(parts, ";")
}
