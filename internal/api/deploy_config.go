package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
A per-app deploy script configuration (app_deploy_configs), with a version history
(app_deploy_config_versions) appended on every save. mode picks how much of the pipeline runs
(auto | fast | full); preset names the stack the steps came from; steps is the ordered pipeline
(install, build, migrate, release, after_release, on_failure), each with its shell command and
whether it runs.

Read: any member. Save: deployer, admin or owner (AbilityDeploy) — the same tier that may deploy
the app itself. A team the caller is not in, or an app the caller may not see: 404.
*/

const maxDeployConfigSteps = 32

// DeployStepIn/Out mirrors deploy.DeployStep over the wire.
type DeployStepIn struct {
	Key     string `json:"key" enum:"install,build,migrate,release,after_release,on_failure"`
	Name    string `json:"name" maxLength:"120"`
	Run     string `json:"run" maxLength:"4000"`
	Enabled bool   `json:"enabled"`
}

// DeployConfigOut is GET …/deploy-config.
type DeployConfigOut struct {
	Mode   string         `json:"mode"`
	Preset string         `json:"preset"`
	Steps  []DeployStepIn `json:"steps"`
	// BuilderImage and Outputs (MH-573 A7b, Advanced) apply to the fast track's builder mode: an
	// app with no Dockerfile but a configured builder image runs its steps there, then copies
	// Outputs onto the current runtime image. Both empty (the default for dockerfile/static) means
	// the fast track either builds the Dockerfile directly or falls back to the original in-place
	// (runtime) mode.
	BuilderImage   string     `json:"builder_image"`
	Outputs        []string   `json:"outputs"`
	DetectedPreset string     `json:"detected_preset,omitempty" doc:"What DetectPreset last saw from the repository's own files, if a build ever ran."`
	Version        int        `json:"version"`
	UpdatedBy      *string    `json:"updated_by,omitempty"`
	UpdatedAt      *time.Time `json:"updated_at" doc:"null: never saved — this is the preset's default."`
	CanSave        bool       `json:"can_save" doc:"Whether the caller may save a configuration (deploy)."`
}

// DeployConfigVersionOut is one entry of GET …/deploy-config/versions.
type DeployConfigVersionOut struct {
	Version   int            `json:"version"`
	Mode      string         `json:"mode"`
	Preset    string         `json:"preset"`
	Steps     []DeployStepIn `json:"steps"`
	UpdatedBy *string        `json:"updated_by,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// DeployPresetOut is one entry of GET /api/teams/{team}/deploy-presets.
type DeployPresetOut struct {
	Preset       string         `json:"preset"`
	Steps        []DeployStepIn `json:"steps"`
	BuilderImage string         `json:"builder_image"`
	Outputs      []string       `json:"outputs"`
}

func outputsOrEmpty(raw string) []string {
	var out []string
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

func toDeployStepsOut(steps []deploy.DeployStep) []DeployStepIn {
	out := make([]DeployStepIn, 0, len(steps))
	for _, s := range steps {
		out = append(out, DeployStepIn{Key: s.Key, Name: s.Name, Run: s.Run, Enabled: s.Enabled})
	}
	return out
}

func fromDeployStepsIn(steps []DeployStepIn) []deploy.DeployStep {
	out := make([]deploy.DeployStep, 0, len(steps))
	for _, s := range steps {
		out = append(out, deploy.DeployStep{Key: s.Key, Name: s.Name, Run: s.Run, Enabled: s.Enabled})
	}
	return out
}

// checkDeploySteps validates what the schema tags cannot say: known keys, no duplicates, and a
// step count the 64 KiB column check backs up.
func checkDeploySteps(steps []DeployStepIn) error {
	if len(steps) == 0 || len(steps) > maxDeployConfigSteps {
		return huma.Error422UnprocessableEntity("a deploy configuration has between 1 and 32 steps")
	}
	seen := map[string]bool{}
	for _, s := range steps {
		if !deploy.ValidStepKey(s.Key) {
			return huma.Error422UnprocessableEntity("unknown step key " + s.Key)
		}
		if seen[s.Key] {
			return huma.Error422UnprocessableEntity("duplicate step key " + s.Key)
		}
		seen[s.Key] = true
	}
	return nil
}

// checkDeployOutputs validates the builder mode's output paths: relative, no traversal.
func checkDeployOutputs(outputs []string) error {
	for _, o := range outputs {
		if o == "" || strings.Contains(o, "..") || strings.HasPrefix(o, "/") {
			return huma.Error422UnprocessableEntity("bad output path " + o)
		}
	}
	return nil
}

func checkDeployMode(mode string) error {
	switch mode {
	case "auto", "fast", "full":
		return nil
	default:
		return huma.Error422UnprocessableEntity("mode must be auto, fast or full")
	}
}

func (d *deps) registerDeployConfig(api huma.API) {
	tags := []string{"Apps"}
	basePath := "/api/teams/{team}/apps/{app}/deploy-config"

	type getOut struct{ Body DeployConfigOut }
	huma.Register(api, huma.Operation{OperationID: "get-app-deploy-config", Method: http.MethodGet, Path: basePath, Tags: tags,
		Summary: "An app's deploy script configuration: mode, preset, and its pipeline steps (install, build, migrate, release, " +
			"after_release, on_failure). When none was ever saved, a default built from the app's detected preset. Any member.",
	}, func(ctx context.Context, in *AppPath) (*getOut, error) {
		out := &getOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			out.Body.CanSave = tenancy.Can(ctx, m, tenancy.AbilityDeploy)
			row, err := q.GetAppDeployConfig(ctx, db.GetAppDeployConfigParams{AppID: a.ID, TeamID: m.TeamID})
			switch {
			case err == nil:
				var steps []deploy.DeployStep
				if json.Unmarshal([]byte(row.Steps), &steps) != nil {
					steps = deploy.PresetSteps(row.Preset)
				}
				out.Body.Mode = row.Mode
				out.Body.Preset = row.Preset
				out.Body.Steps = toDeployStepsOut(steps)
				out.Body.BuilderImage = row.BuilderImage
				out.Body.Outputs = outputsOrEmpty(row.Outputs)
				out.Body.DetectedPreset = row.DetectedPreset
				out.Body.Version = int(row.Version)
				out.Body.UpdatedBy = row.UpdatedBy
				out.Body.UpdatedAt = &row.UpdatedAt
			case errors.Is(err, sql.ErrNoRows):
				preset := deploy.DetectPreset(nil)
				out.Body.Mode = "auto"
				out.Body.Preset = preset
				out.Body.Steps = toDeployStepsOut(deploy.PresetSteps(preset))
				out.Body.BuilderImage = deploy.PresetBuilderImage(preset)
				out.Body.Outputs = deploy.PresetOutputs(preset)
				if out.Body.Outputs == nil {
					out.Body.Outputs = []string{}
				}
				out.Body.Version = 0
			default:
				return huma.Error500InternalServerError("could not read the deploy configuration")
			}
			return nil
		})
		return out, err
	})

	type putOut struct{ Body DeployConfigOut }
	huma.Register(api, huma.Operation{OperationID: "put-app-deploy-config", Method: http.MethodPut, Path: basePath, Tags: tags,
		Summary: "Save an app's deploy script configuration (replaces the whole pipeline) and append it to the version history. " +
			"Deployer, admin or owner.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Body struct {
			Mode         string         `json:"mode" enum:"auto,fast,full"`
			Preset       string         `json:"preset" maxLength:"32"`
			Steps        []DeployStepIn `json:"steps"`
			BuilderImage string         `json:"builder_image,omitempty" maxLength:"200"`
			Outputs      []string       `json:"outputs,omitempty" maxItems:"32"`
		}
	}) (*putOut, error) {
		out := &putOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if err := need(ctx, m, tenancy.AbilityDeploy); err != nil {
				return err
			}
			if err := checkDeployMode(in.Body.Mode); err != nil {
				return err
			}
			if err := checkDeploySteps(in.Body.Steps); err != nil {
				return err
			}
			if err := checkDeployOutputs(in.Body.Outputs); err != nil {
				return err
			}
			steps := fromDeployStepsIn(in.Body.Steps)
			raw, err := json.Marshal(steps)
			if err != nil {
				return huma.Error422UnprocessableEntity("the steps could not be encoded")
			}
			outputs := in.Body.Outputs
			if outputs == nil {
				outputs = []string{}
			}
			outputsRaw, err := json.Marshal(outputs)
			if err != nil {
				return huma.Error422UnprocessableEntity("the outputs could not be encoded")
			}

			existing, err := q.GetAppDeployConfig(ctx, db.GetAppDeployConfigParams{AppID: a.ID, TeamID: m.TeamID})
			nextVersion := int32(1)
			switch {
			case err == nil:
				nextVersion = existing.Version + 1
			case errors.Is(err, sql.ErrNoRows):
				// first save: version 1
			default:
				return huma.Error500InternalServerError("could not read the deploy configuration")
			}

			userID := id.ID
			row, err := q.UpsertAppDeployConfig(ctx, db.UpsertAppDeployConfigParams{
				AppID: a.ID, TeamID: m.TeamID, Mode: in.Body.Mode, Preset: in.Body.Preset, Steps: string(raw),
				BuilderImage: in.Body.BuilderImage, Outputs: string(outputsRaw), DetectedPreset: existing.DetectedPreset,
				Version: nextVersion, UpdatedBy: &userID,
			})
			if err != nil {
				return huma.Error500InternalServerError("could not save the deploy configuration")
			}
			if _, err := q.InsertAppDeployConfigVersion(ctx, db.InsertAppDeployConfigVersionParams{
				AppID: a.ID, TeamID: m.TeamID, Version: nextVersion, Mode: in.Body.Mode, Preset: in.Body.Preset,
				Steps: string(raw), UpdatedBy: &userID,
			}); err != nil {
				return huma.Error500InternalServerError("could not record the deploy configuration version")
			}

			out.Body.Mode = row.Mode
			out.Body.Preset = row.Preset
			out.Body.Steps = toDeployStepsOut(steps)
			out.Body.BuilderImage = row.BuilderImage
			out.Body.Outputs = outputsOrEmpty(row.Outputs)
			out.Body.DetectedPreset = row.DetectedPreset
			out.Body.Version = int(row.Version)
			out.Body.UpdatedBy = row.UpdatedBy
			out.Body.UpdatedAt = &row.UpdatedAt
			out.Body.CanSave = true
			return nil
		})
		return out, err
	})

	type versionsOut struct {
		Body struct {
			Versions []DeployConfigVersionOut `json:"versions"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-app-deploy-config-versions", Method: http.MethodGet, Path: basePath + "/versions", Tags: tags,
		Summary: "An app's deploy configuration version history, newest first. Any member.",
	}, func(ctx context.Context, in *AppPath) (*versionsOut, error) {
		out := &versionsOut{}
		out.Body.Versions = []DeployConfigVersionOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			rows, err := q.ListAppDeployConfigVersions(ctx, db.ListAppDeployConfigVersionsParams{AppID: a.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not read the version history")
			}
			for _, row := range rows {
				var steps []deploy.DeployStep
				if json.Unmarshal([]byte(row.Steps), &steps) != nil {
					steps = nil
				}
				out.Body.Versions = append(out.Body.Versions, DeployConfigVersionOut{
					Version: int(row.Version), Mode: row.Mode, Preset: row.Preset, Steps: toDeployStepsOut(steps),
					UpdatedBy: row.UpdatedBy, CreatedAt: row.CreatedAt,
				})
			}
			return nil
		})
		return out, err
	})

	// VersionPath addresses one version of an app's deploy configuration.
	type VersionPath struct {
		AppPath
		Version string `path:"version" maxLength:"10"`
	}

	huma.Register(api, huma.Operation{OperationID: "restore-app-deploy-config-version", Method: http.MethodPost, Path: basePath + "/versions/{version}/restore", Tags: tags,
		Summary: "Restore a past version of an app's deploy configuration as the current one (a new version is appended, its steps " +
			"copied from the one restored). Deployer, admin or owner.",
	}, func(ctx context.Context, in *VersionPath) (*putOut, error) {
		out := &putOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			if err := need(ctx, m, tenancy.AbilityDeploy); err != nil {
				return err
			}
			version, convErr := strconv.ParseInt(in.Version, 10, 32)
			if convErr != nil || version < 1 {
				return errNotFound
			}
			target, err := q.GetAppDeployConfigVersion(ctx, db.GetAppDeployConfigVersionParams{AppID: a.ID, TeamID: m.TeamID, Version: int32(version)})
			if err != nil {
				return errNotFound
			}

			existing, err := q.GetAppDeployConfig(ctx, db.GetAppDeployConfigParams{AppID: a.ID, TeamID: m.TeamID})
			nextVersion := int32(1)
			if err == nil {
				nextVersion = existing.Version + 1
			} else if errors.Is(err, sql.ErrNoRows) {
				existing.Outputs = "[]"
			} else {
				return huma.Error500InternalServerError("could not read the deploy configuration")
			}

			userID := id.ID
			row, err := q.UpsertAppDeployConfig(ctx, db.UpsertAppDeployConfigParams{
				AppID: a.ID, TeamID: m.TeamID, Mode: target.Mode, Preset: target.Preset, Steps: target.Steps,
				BuilderImage: existing.BuilderImage, Outputs: existing.Outputs, DetectedPreset: existing.DetectedPreset,
				Version: nextVersion, UpdatedBy: &userID,
			})
			if err != nil {
				return huma.Error500InternalServerError("could not save the deploy configuration")
			}
			if _, err := q.InsertAppDeployConfigVersion(ctx, db.InsertAppDeployConfigVersionParams{
				AppID: a.ID, TeamID: m.TeamID, Version: nextVersion, Mode: target.Mode, Preset: target.Preset,
				Steps: target.Steps, UpdatedBy: &userID,
			}); err != nil {
				return huma.Error500InternalServerError("could not record the deploy configuration version")
			}

			var steps []deploy.DeployStep
			_ = json.Unmarshal([]byte(target.Steps), &steps)
			out.Body.Mode = row.Mode
			out.Body.Preset = row.Preset
			out.Body.Steps = toDeployStepsOut(steps)
			out.Body.BuilderImage = row.BuilderImage
			out.Body.Outputs = outputsOrEmpty(row.Outputs)
			out.Body.DetectedPreset = row.DetectedPreset
			out.Body.Version = int(row.Version)
			out.Body.UpdatedBy = row.UpdatedBy
			out.Body.UpdatedAt = &row.UpdatedAt
			out.Body.CanSave = true
			return nil
		})
		return out, err
	})

	type presetsOut struct {
		Body struct {
			Presets []DeployPresetOut `json:"presets"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-deploy-presets", Method: http.MethodGet, Path: "/api/teams/{team}/deploy-presets", Tags: tags,
		Summary: "Every deploy script preset (go, node, next, laravel, php, python, static) and its default pipeline steps. Any member.",
	}, func(ctx context.Context, in *TeamPath) (*presetsOut, error) {
		out := &presetsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(_ *db.Queries, _ *sql.Tx, _ *auth.Identity, _ *tenancy.Membership) error {
			for _, p := range deploy.Presets {
				outputs := deploy.PresetOutputs(p)
				if outputs == nil {
					outputs = []string{}
				}
				out.Body.Presets = append(out.Body.Presets, DeployPresetOut{
					Preset: p, Steps: toDeployStepsOut(deploy.PresetSteps(p)),
					BuilderImage: deploy.PresetBuilderImage(p), Outputs: outputs,
				})
			}
			return nil
		})
		return out, err
	})

	type detectOut struct{ Body DeployPresetOut }
	huma.Register(api, huma.Operation{OperationID: "detect-app-deploy-preset", Method: http.MethodGet, Path: basePath + "/detect", Tags: tags,
		Summary: "The preset last detected from the app's own repository files (MH-573 A7b: a Dockerfile at the root, or the deploy " +
			"pipeline's marker files, seen the last time a build ran there) with its default steps, builder image and outputs — a " +
			"suggestion to apply, not a saved configuration. Any member.",
	}, func(ctx context.Context, in *AppPath) (*detectOut, error) {
		out := &detectOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			preset := deploy.DetectPreset(nil)
			if row, err := q.GetAppDeployConfig(ctx, db.GetAppDeployConfigParams{AppID: a.ID, TeamID: m.TeamID}); err == nil && row.DetectedPreset != "" {
				preset = row.DetectedPreset
			}
			outputs := deploy.PresetOutputs(preset)
			if outputs == nil {
				outputs = []string{}
			}
			out.Body = DeployPresetOut{Preset: preset, Steps: toDeployStepsOut(deploy.PresetSteps(preset)), BuilderImage: deploy.PresetBuilderImage(preset), Outputs: outputs}
			return nil
		})
		return out, err
	})
}
