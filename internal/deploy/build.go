package deploy

import (
	"encoding/json"
	"fmt"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
Per-app build settings (MH-579): a custom Dockerfile path and build context (gap B), and the list
of env var names exposed at build time (gap A). Stored on app_deploy_configs (dockerfile_path,
build_context, build_env — not versioned, like php_version/php_ini: advanced settings a team tunes
in place, not part of the pipeline's step history).

dockerfile_path/build_context are validated here with agentproto.ValidBuildPath (relative, no "..",
no absolute, a restricted charset, bounded length) before they are ever saved, and again by the
agent once it can resolve the checkout's real path (internal/agent's safeJoin) — symlink escapes
only the agent, which has the checkout on disk, can catch.

build_env is a list of variable names (not values): at build time the control plane looks up the
app's current env values for exactly those names, seals the ones that are actually set to the
target agent's key (buildEnvBox, mirroring envBox) and the agent opens the box and passes each as
`--build-arg NAME`, the value only ever reaching docker through its own process environment. A name
with no matching env var is silently skipped, both here and by the agent.
*/

// MaxBuildEnvNames caps how many variable names one app may expose at build time.
const MaxBuildEnvNames = 64

// ValidBuildEnvNames checks every name is a well-formed env var name (validEnvName) and the list
// is not absurdly large.
func ValidBuildEnvNames(names []string) error {
	if len(names) > MaxBuildEnvNames {
		return fmt.Errorf("too many build-time variables (max %d)", MaxBuildEnvNames)
	}
	for _, n := range names {
		if !validEnvName(n) {
			return fmt.Errorf("bad build-time variable name %q", n)
		}
	}
	return nil
}

// DedupBuildEnvNames removes duplicates, preserving first-seen order — so a saved list is stable
// and the jsonb column never grows unbounded from repeated toggles of the same name.
func DedupBuildEnvNames(names []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// MarshalBuildEnv renders a build_env name list to the jsonb column's text form.
func MarshalBuildEnv(names []string) string {
	if names == nil {
		names = []string{}
	}
	b, _ := json.Marshal(names)
	return string(b)
}

// UnmarshalBuildEnv reads a build_env column back into a name list, "" or invalid JSON reading as
// none — never a saved deploy config failing to load because of a build setting.
func UnmarshalBuildEnv(raw string) []string {
	var names []string
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil
	}
	return names
}

// ValidDockerfilePath / ValidBuildContext accept "" (the default) or a path agentproto.ValidBuildPath
// allows. Kept as named wrappers so a future divergence between the two settings' rules (e.g. a
// context allowed to be "." explicitly) has an obvious home.
func ValidDockerfilePath(s string) bool { return s == "" || agentproto.ValidBuildPath(s) }
func ValidBuildContext(s string) bool   { return s == "" || agentproto.ValidBuildPath(s) }
