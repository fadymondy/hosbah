package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// DeployConfigTools: an app's deploy script configuration (mode, preset, pipeline steps) and its
// version history. Same actions as the REST API (internal/api/deploy_config.go).
func DeployConfigTools() []Tool {
	appArg := str("App name or id")

	stepSchema := map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":     "object",
			"required": []string{"key", "run", "enabled"},
			"properties": map[string]any{
				"key":     map[string]any{"type": "string", "enum": deploy.StepKeys},
				"name":    str("A short label for the step"),
				"run":     str("The shell command; empty = nothing to run"),
				"enabled": map[string]any{"type": "boolean"},
			},
		},
		"description": "The pipeline, one entry per key (install, build, migrate, release, after_release, on_failure).",
	}

	describeSteps := func(steps []deploy.DeployStep) string {
		var b strings.Builder
		for _, s := range steps {
			state := "off"
			if s.Enabled {
				state = "on"
			}
			run := s.Run
			if run == "" {
				run = "(nothing)"
			}
			fmt.Fprintf(&b, "  %s (%s): %s\n", s.Key, state, run)
		}
		return b.String()
	}

	parseSteps := func(raw any) ([]deploy.DeployStep, error) {
		list, ok := raw.([]any)
		if !ok {
			return nil, errors.New("steps must be a list of {key, name, run, enabled}")
		}
		out := make([]deploy.DeployStep, 0, len(list))
		seen := map[string]bool{}
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("steps must be a list of {key, name, run, enabled}")
			}
			key, _ := m["key"].(string)
			if !deploy.ValidStepKey(key) {
				return nil, fmt.Errorf("unknown step key %q", key)
			}
			if seen[key] {
				return nil, fmt.Errorf("duplicate step key %q", key)
			}
			seen[key] = true
			name, _ := m["name"].(string)
			run, _ := m["run"].(string)
			enabled, _ := m["enabled"].(bool)
			out = append(out, deploy.DeployStep{Key: key, Name: name, Run: run, Enabled: enabled})
		}
		// No steps is valid: the dockerfile preset builds from the Dockerfile alone.
		return out, nil
	}

	parseOutputs := func(raw any) ([]string, error) {
		if raw == nil {
			return nil, nil
		}
		list, ok := raw.([]any)
		if !ok {
			return nil, errors.New("outputs must be a list of paths")
		}
		out := make([]string, 0, len(list))
		for _, item := range list {
			s, ok := item.(string)
			if !ok || s == "" || strings.Contains(s, "..") || strings.HasPrefix(s, "/") {
				return nil, fmt.Errorf("bad output path %v", item)
			}
			out = append(out, s)
		}
		return out, nil
	}

	saveConfig := func(ctx context.Context, env *Env, a db.App, mode, preset string, steps []deploy.DeployStep, builderImage string, outputs []string) (db.AppDeployConfig, error) {
		raw, err := json.Marshal(steps)
		if err != nil {
			return db.AppDeployConfig{}, err
		}
		if outputs == nil {
			outputs = []string{}
		}
		outputsRaw, err := json.Marshal(outputs)
		if err != nil {
			return db.AppDeployConfig{}, err
		}
		existing, err := env.Q.GetAppDeployConfig(ctx, db.GetAppDeployConfigParams{AppID: a.ID, TeamID: env.Caller.TeamID})
		nextVersion := int32(1)
		switch {
		case err == nil:
			nextVersion = existing.Version + 1
		case errors.Is(err, sql.ErrNoRows):
		default:
			return db.AppDeployConfig{}, err
		}
		userID := env.Caller.UserID
		row, err := env.Q.UpsertAppDeployConfig(ctx, db.UpsertAppDeployConfigParams{
			AppID: a.ID, TeamID: env.Caller.TeamID, Mode: mode, Preset: preset, Steps: string(raw),
			BuilderImage: builderImage, Outputs: string(outputsRaw), DetectedPreset: existing.DetectedPreset,
			Version: nextVersion, UpdatedBy: &userID,
		})
		if err != nil {
			return db.AppDeployConfig{}, err
		}
		if _, err := env.Q.InsertAppDeployConfigVersion(ctx, db.InsertAppDeployConfigVersionParams{
			AppID: a.ID, TeamID: env.Caller.TeamID, Version: nextVersion, Mode: mode, Preset: preset, Steps: string(raw), UpdatedBy: &userID,
		}); err != nil {
			return db.AppDeployConfig{}, err
		}
		return row, nil
	}

	return []Tool{
		{
			Name: "app_deploy_config_get", Scope: scopes.Read,
			Description: "An app's deploy script configuration: mode (auto | fast | full), preset, and its pipeline steps. When none was " +
				"ever saved, a default built from the static preset.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				row, err := env.Q.GetAppDeployConfig(ctx, db.GetAppDeployConfigParams{AppID: a.ID, TeamID: env.Caller.TeamID})
				switch {
				case err == nil:
					var steps []deploy.DeployStep
					if json.Unmarshal([]byte(row.Steps), &steps) != nil {
						steps = deploy.PresetSteps(row.Preset)
					}
					return fmt.Sprintf("%s: mode %s, preset %s, version %d (saved).\n%s", a.Name, row.Mode, row.Preset, row.Version, describeSteps(steps)), nil
				case errors.Is(err, sql.ErrNoRows):
					preset := deploy.DetectPreset(nil)
					steps := deploy.PresetSteps(preset)
					return fmt.Sprintf("%s: no configuration saved. Default from preset %s:\n%s", a.Name, preset, describeSteps(steps)), nil
				default:
					return "", err
				}
			},
		},
		{
			Name: "app_deploy_config_set", Scope: scopes.Deploy,
			Description: "Save an app's deploy script configuration (replaces the whole pipeline) and append it to the version history. " +
				"Deployer, admin or owner.",
			InputSchema: schema([]string{"app", "mode", "steps"}, map[string]any{"app": appArg,
				"mode":          map[string]any{"type": "string", "enum": []string{"auto", "fast", "full"}},
				"preset":        str("A stack name (go, node, next, laravel, php, python, static, dockerfile); freeform"),
				"steps":         stepSchema,
				"builder_image": str("Advanced, fast track: the image install/build/migrate steps run in when the app has no Dockerfile (e.g. golang:1.26, node:24). Empty = the fast track falls back to running in place."),
				"outputs": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Advanced, fast track: paths (relative to the checkout) copied onto the runtime image once the builder image's steps ran. Required with builder_image.",
				}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				mode := args.Str("mode")
				if mode != "auto" && mode != "fast" && mode != "full" {
					return "", errors.New("mode must be auto, fast or full")
				}
				steps, err := parseSteps(args["steps"])
				if err != nil {
					return "", err
				}
				outputs, err := parseOutputs(args["outputs"])
				if err != nil {
					return "", err
				}
				row, err := saveConfig(ctx, env, a, mode, args.Str("preset"), steps, args.Str("builder_image"), outputs)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Saved %s's deploy configuration as version %d.\n%s", a.Name, row.Version, describeSteps(steps)), nil
			},
		},
		{
			Name: "app_deploy_config_versions", Scope: scopes.Read,
			Description: "An app's deploy configuration version history, newest first.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				rows, err := env.Q.ListAppDeployConfigVersions(ctx, db.ListAppDeployConfigVersionsParams{AppID: a.ID, TeamID: env.Caller.TeamID})
				if err != nil {
					return "", err
				}
				if len(rows) == 0 {
					return a.Name + " has no saved deploy configuration versions.", nil
				}
				var b strings.Builder
				for _, row := range rows {
					fmt.Fprintf(&b, "v%d: mode %s, preset %s, saved %s\n", row.Version, row.Mode, row.Preset, row.CreatedAt.Format("2006-01-02 15:04:05"))
				}
				return b.String(), nil
			},
		},
		{
			Name: "app_deploy_config_restore", Scope: scopes.Deploy,
			Description: "Restore a past version of an app's deploy configuration as the current one (appends a new version copied from the " +
				"one restored). Deployer, admin or owner.",
			InputSchema: schema([]string{"app", "version"}, map[string]any{"app": appArg, "version": str("The version number to restore")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				versionStr := args.Str("version")
				version, convErr := strconv.ParseInt(versionStr, 10, 32)
				if convErr != nil || version < 1 {
					return "", fmt.Errorf("version %q not found", versionStr)
				}
				target, err := env.Q.GetAppDeployConfigVersion(ctx, db.GetAppDeployConfigVersionParams{AppID: a.ID, TeamID: env.Caller.TeamID, Version: int32(version)})
				if err != nil {
					return "", fmt.Errorf("version %d not found", version)
				}
				var steps []deploy.DeployStep
				if err := json.Unmarshal([]byte(target.Steps), &steps); err != nil {
					return "", err
				}
				var builderImage string
				var outputs []string
				if existing, err := env.Q.GetAppDeployConfig(ctx, db.GetAppDeployConfigParams{AppID: a.ID, TeamID: env.Caller.TeamID}); err == nil {
					builderImage = existing.BuilderImage
					_ = json.Unmarshal([]byte(existing.Outputs), &outputs)
				}
				row, err := saveConfig(ctx, env, a, target.Mode, target.Preset, steps, builderImage, outputs)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Restored %s's deploy configuration from version %d as version %d.\n%s", a.Name, version, row.Version, describeSteps(steps)), nil
			},
		},
		{
			Name: "deploy_presets_list", Scope: scopes.Read,
			Description: "Every deploy script preset (go, node, next, laravel, php, python, static) and its default pipeline steps.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				var b strings.Builder
				for _, p := range deploy.Presets {
					fmt.Fprintf(&b, "%s:\n%s", p, describeSteps(deploy.PresetSteps(p)))
				}
				return b.String(), nil
			},
		},
	}
}
