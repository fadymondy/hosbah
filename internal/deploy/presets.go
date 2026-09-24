package deploy

import "strings"

/*
Deploy script presets: the default steps for a per-app deploy configuration
(internal/api/deploy_config.go, app_deploy_configs). A step's Key is one of the pipeline's fixed
points (install, build, migrate, release, after_release, on_failure); Run is the shell command
Railpack or the app's own scripts would run there; Enabled says whether the saved configuration
runs it. DetectPreset picks one from a repository's top-level file names — the same list the
Railpack build step already gathers — so a new app's configuration starts from something sane
without a build ever having to run.
*/

// PresetGo, PresetNode, ... name the presets a deploy configuration may pick.
const (
	PresetGo      = "go"
	PresetNode    = "node"
	PresetNext    = "next"
	PresetLaravel = "laravel"
	PresetPHP     = "php"
	PresetPython  = "python"
	PresetStatic  = "static"
)

// Presets is every preset name, in the order DetectPreset tries them.
var Presets = []string{PresetNext, PresetLaravel, PresetPHP, PresetNode, PresetGo, PresetPython, PresetStatic}

// DeployStep is one step of a deploy configuration's pipeline.
type DeployStep struct {
	Key     string `json:"key"`  // install | build | migrate | release | after_release | on_failure
	Name    string `json:"name"` // a short label for the step
	Run     string `json:"run"`  // the shell command, empty = nothing to run
	Enabled bool   `json:"enabled"`
}

// StepKeys is every step key, in pipeline order.
var StepKeys = []string{"install", "build", "migrate", "release", "after_release", "on_failure"}

// ValidStepKey reports whether k is one of StepKeys.
func ValidStepKey(k string) bool {
	for _, s := range StepKeys {
		if s == k {
			return true
		}
	}
	return false
}

// PresetSteps returns the default steps for a preset name; an unknown preset falls back to Static
// (an empty pipeline: nothing runs but the container starts).
func PresetSteps(preset string) []DeployStep {
	switch preset {
	case PresetGo:
		return []DeployStep{
			{Key: "install", Name: "Download modules", Run: "go mod download", Enabled: true},
			{Key: "build", Name: "Build", Run: "go build -o app .", Enabled: true},
			{Key: "migrate", Name: "Migrate", Run: "", Enabled: false},
			{Key: "release", Name: "Release", Run: "./app", Enabled: true},
			{Key: "after_release", Name: "After release", Run: "", Enabled: false},
			{Key: "on_failure", Name: "On failure", Run: "", Enabled: false},
		}
	case PresetNode:
		return []DeployStep{
			{Key: "install", Name: "Install dependencies", Run: "npm ci", Enabled: true},
			{Key: "build", Name: "Build", Run: "npm run build", Enabled: true},
			{Key: "migrate", Name: "Migrate", Run: "", Enabled: false},
			{Key: "release", Name: "Start", Run: "npm start", Enabled: true},
			{Key: "after_release", Name: "After release", Run: "", Enabled: false},
			{Key: "on_failure", Name: "On failure", Run: "", Enabled: false},
		}
	case PresetNext:
		return []DeployStep{
			{Key: "install", Name: "Install dependencies", Run: "npm ci", Enabled: true},
			{Key: "build", Name: "Build", Run: "npm run build", Enabled: true},
			{Key: "migrate", Name: "Migrate", Run: "", Enabled: false},
			{Key: "release", Name: "Start", Run: "npm start", Enabled: true},
			{Key: "after_release", Name: "After release", Run: "", Enabled: false},
			{Key: "on_failure", Name: "On failure", Run: "", Enabled: false},
		}
	case PresetLaravel:
		return []DeployStep{
			{Key: "install", Name: "Install dependencies", Run: "composer install --no-dev --optimize-autoloader", Enabled: true},
			{Key: "build", Name: "Build assets", Run: "npm ci && npm run build", Enabled: false},
			{Key: "migrate", Name: "Migrate", Run: "php artisan migrate --force", Enabled: true},
			{Key: "release", Name: "Release", Run: "php artisan config:cache && php artisan route:cache", Enabled: true},
			{Key: "after_release", Name: "After release", Run: "php artisan queue:restart", Enabled: false},
			{Key: "on_failure", Name: "On failure", Run: "", Enabled: false},
		}
	case PresetPHP:
		return []DeployStep{
			{Key: "install", Name: "Install dependencies", Run: "composer install --no-dev --optimize-autoloader", Enabled: true},
			{Key: "build", Name: "Build", Run: "", Enabled: false},
			{Key: "migrate", Name: "Migrate", Run: "", Enabled: false},
			{Key: "release", Name: "Release", Run: "", Enabled: false},
			{Key: "after_release", Name: "After release", Run: "", Enabled: false},
			{Key: "on_failure", Name: "On failure", Run: "", Enabled: false},
		}
	case PresetPython:
		return []DeployStep{
			{Key: "install", Name: "Install dependencies", Run: "pip install -r requirements.txt", Enabled: true},
			{Key: "build", Name: "Build", Run: "", Enabled: false},
			{Key: "migrate", Name: "Migrate", Run: "", Enabled: false},
			{Key: "release", Name: "Release", Run: "python app.py", Enabled: true},
			{Key: "after_release", Name: "After release", Run: "", Enabled: false},
			{Key: "on_failure", Name: "On failure", Run: "", Enabled: false},
		}
	default: // PresetStatic and anything unknown
		return []DeployStep{
			{Key: "install", Name: "Install dependencies", Run: "", Enabled: false},
			{Key: "build", Name: "Build", Run: "", Enabled: false},
			{Key: "migrate", Name: "Migrate", Run: "", Enabled: false},
			{Key: "release", Name: "Release", Run: "", Enabled: false},
			{Key: "after_release", Name: "After release", Run: "", Enabled: false},
			{Key: "on_failure", Name: "On failure", Run: "", Enabled: false},
		}
	}
}

// DetectPreset picks a preset from a repository's file names (top-level, as Railpack's own
// detection looks at them). Order matters: a Next.js app has both package.json and next.config.*,
// and a Laravel app has both composer.json and package.json, so the more specific marker wins.
func DetectPreset(files []string) string {
	has := func(name string) bool {
		for _, f := range files {
			if strings.EqualFold(f, name) {
				return true
			}
		}
		return false
	}
	hasPrefix := func(prefix string) bool {
		for _, f := range files {
			if strings.HasPrefix(strings.ToLower(f), prefix) {
				return true
			}
		}
		return false
	}

	switch {
	case hasPrefix("next.config"):
		return PresetNext
	case has("artisan"):
		return PresetLaravel
	case has("composer.json"):
		return PresetPHP
	case has("package.json"):
		return PresetNode
	case has("go.mod"):
		return PresetGo
	case has("requirements.txt") || has("pyproject.toml") || has("Pipfile"):
		return PresetPython
	case has("index.html"):
		return PresetStatic
	default:
		return PresetStatic
	}
}
