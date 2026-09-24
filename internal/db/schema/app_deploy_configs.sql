-- Hosbah: a per-app deploy script configuration (hand-written: one current row per app, plus an
-- append-only version history in app_deploy_config_versions). mode picks how much of the pipeline
-- runs (auto detects from the repository; fast skips migrate/after_release; full runs every step);
-- preset names the detected or chosen stack (go, node, next, laravel, php, python, static); steps is
-- a JSON array of {key, name, run, enabled}, key one of install|build|migrate|release|after_release|
-- on_failure (internal/deploy.presets.go gives the defaults per preset). version starts at 1 and
-- increments on every save; the same number is appended to app_deploy_config_versions so a save can
-- be restored. updated_by is the user who saved it (null for a system default that was never saved).
CREATE TABLE app_deploy_configs (
    app_id text PRIMARY KEY,
    team_id text NOT NULL,
    mode text NOT NULL DEFAULT 'auto',
    preset text NOT NULL DEFAULT '',
    steps jsonb NOT NULL DEFAULT '[]'::jsonb,
    version integer NOT NULL DEFAULT 1,
    updated_by text,
    updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE app_deploy_configs ADD CONSTRAINT app_deploy_configs_mode_check CHECK (mode IN ('auto', 'fast', 'full'));
ALTER TABLE app_deploy_configs ADD CONSTRAINT app_deploy_configs_steps_check CHECK (jsonb_typeof(steps) = 'array' AND octet_length(steps::text) <= 65536);
CREATE INDEX app_deploy_configs_team_idx ON app_deploy_configs (team_id);

-- One row per save, so a version can be listed and restored.
CREATE TABLE app_deploy_config_versions (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    app_id text NOT NULL,
    team_id text NOT NULL,
    version integer NOT NULL,
    mode text NOT NULL,
    preset text NOT NULL DEFAULT '',
    steps jsonb NOT NULL DEFAULT '[]'::jsonb,
    updated_by text,
    created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE app_deploy_config_versions ADD CONSTRAINT app_deploy_config_versions_mode_check CHECK (mode IN ('auto', 'fast', 'full'));
ALTER TABLE app_deploy_config_versions ADD CONSTRAINT app_deploy_config_versions_steps_check CHECK (jsonb_typeof(steps) = 'array' AND octet_length(steps::text) <= 65536);
CREATE UNIQUE INDEX app_deploy_config_versions_app_version_idx ON app_deploy_config_versions (app_id, version);
CREATE INDEX app_deploy_config_versions_team_idx ON app_deploy_config_versions (team_id);
