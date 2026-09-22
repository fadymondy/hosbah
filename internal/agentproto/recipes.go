package agentproto

import (
	"errors"
	"regexp"
	"strings"
)

// Installed recipes: an "app" recipe of the built-in catalog (package catalog), run as one container
// next to a site. The params name a recipe by id + version and carry NOTHING the agent runs: the
// image, the limits, the capabilities, the mounts and the literal env come from the agent's OWN
// embedded copy of the catalog. Image travels only so the agent can say "update the agent" when the
// two copies differ. What is secret (generated values, a data service's connection string) travels
// in a box sealed to the agent — never in the params.
const (
	KindRecipeInstall = "recipe.install" // pull, docker run with the recipe's limits, wait for its health path
	KindRecipeRemove  = "recipe.remove"  // stop and remove the container; its volumes only when asked; idempotent
	KindRecipeStatus  = "recipe.status"  // read-only: running?, restart count, memory, the current loopback port

	// Names (cloudy-install-, cloudy-ivol- and cloudy.* before the rename; the agent still finds those).
	InstallContainerPrefix = NamePrefix + "install-"
	InstallVolumePrefix    = NamePrefix + "ivol-"
	// Labels. NOT hosbah.app: a deploy's drain and a rollback stop every container with that label.
	InstallLabel       = LabelPrefix + "install"
	InstallAppLabel    = LabelPrefix + "install-app"
	InstallRecipeLabel = LabelPrefix + "install-recipe"

	// InstallPidsLimit bounds an install's processes and threads.
	InstallPidsLimit = 512
	MaxAppInstalls   = 10
	// MaxInstallSecrets bounds the env names in the sealed box; MaxInstallSecretValue one value.
	MaxInstallSecrets     = 40
	MaxInstallSecretValue = 4096
)

var (
	recipeIDRE      = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	recipeVersionRE = regexp.MustCompile(`^[0-9]{1,4}(\.[0-9]{1,4}){0,2}$`)
	installNameRE   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	installEnvRE    = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
)

// ValidRecipeID: a recipe's id in the catalog.
func ValidRecipeID(s string) bool { return recipeIDRE.MatchString(s) }

// ValidInstallName: the user's name of an install.
func ValidInstallName(s string) bool { return installNameRE.MatchString(s) }

// InstallContainerName is the container of an install. Derived, never user-supplied.
func InstallContainerName(installID string) string { return InstallContainerPrefix + installID }

// InstallVolumeName is the Docker volume of one of an install's volumes. Derived from the install id
// and the slug in the agent's catalog.
func InstallVolumeName(installID, slug string) string {
	return InstallVolumePrefix + installID + "-" + slug
}

// InstallSecretsContext is the box context of an install's secret env: {"env": {NAME: value}}.
func InstallSecretsContext(installID string) string { return "install:" + installID + ":env" }

// InstallSecrets is what the box holds.
type InstallSecrets struct {
	Env map[string]string `json:"env"`
}

// Validate: env names, and values that fit one line of an env file.
func (s InstallSecrets) Validate() error {
	if len(s.Env) > MaxInstallSecrets {
		return errors.New("too many secret variables")
	}
	for k, v := range s.Env {
		if !installEnvRE.MatchString(k) || len(v) > MaxInstallSecretValue || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("a secret variable is malformed")
		}
	}
	return nil
}

// RecipeInstallParams: recipe.install. Secrets is a box for InstallSecretsContext(InstallID); it may
// be empty when the recipe has nothing secret.
type RecipeInstallParams struct {
	InstallID     string `json:"install_id"`
	AppID         string `json:"app_id"`
	RecipeID      string `json:"recipe_id"`
	RecipeVersion string `json:"recipe_version"`
	Image         string `json:"image"` // compared with the agent's catalog, never run as given
	Secrets       string `json:"secrets,omitempty"`
}

func (p RecipeInstallParams) Validate() error {
	switch {
	case !ValidID(p.InstallID) || !ValidID(p.AppID):
		return errors.New("bad install or app id")
	case !ValidRecipeID(p.RecipeID) || !recipeVersionRE.MatchString(p.RecipeVersion):
		return errors.New("bad recipe id or version")
	case p.Image == "" || len(p.Image) > 255:
		return errors.New("recipe.install names the image it expects")
	}
	return nil
}

type RecipeInstallResult struct {
	Container string `json:"container"`
	HostPort  int    `json:"host_port"` // on 127.0.0.1; 0 for a headless recipe
}

// RecipeRemoveParams: recipe.remove. The volumes are found by the install's derived prefix, so a
// removal works whatever catalog the agent has.
type RecipeRemoveParams struct {
	InstallID  string `json:"install_id"`
	AppID      string `json:"app_id"`
	DeleteData bool   `json:"delete_data"`
}

func (p RecipeRemoveParams) Validate() error {
	if !ValidID(p.InstallID) || !ValidID(p.AppID) {
		return errors.New("bad recipe.remove params")
	}
	return nil
}

type RecipeRemoveResult struct {
	Container      string   `json:"container"`
	Removed        bool     `json:"removed"` // false: it was already gone
	VolumesRemoved []string `json:"volumes_removed"`
}

// RecipeStatusParams: recipe.status. RecipeIDs[i] is the recipe of InstallIDs[i].
type RecipeStatusParams struct {
	AppID      string   `json:"app_id"`
	InstallIDs []string `json:"install_ids"`
	RecipeIDs  []string `json:"recipe_ids"`
}

func (p RecipeStatusParams) Validate() error {
	if !ValidID(p.AppID) || len(p.InstallIDs) == 0 || len(p.InstallIDs) > MaxAppInstalls || len(p.RecipeIDs) != len(p.InstallIDs) {
		return errors.New("bad recipe.status params")
	}
	for i, id := range p.InstallIDs {
		if !ValidID(id) || !ValidRecipeID(p.RecipeIDs[i]) {
			return errors.New("bad install or recipe id")
		}
	}
	return nil
}

// InstallState is one install as the server sees it. HostPort is the loopback port its HTTP port is
// published on NOW (Docker picks a new one when the container restarts); 0 when unknown or headless.
type InstallState struct {
	InstallID   string `json:"install_id"`
	Running     bool   `json:"running"`
	Restarts    int    `json:"restarts"`
	MemoryBytes *int64 `json:"memory_bytes"`
	HostPort    int    `json:"host_port"`
}

type RecipeStatusResult struct {
	Installs []InstallState `json:"installs"`
}
