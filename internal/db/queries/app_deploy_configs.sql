-- Hosbah queries: a per-app deploy script configuration and its version history (team scope).

-- name: GetAppDeployConfig :one
SELECT * FROM app_deploy_configs WHERE app_id = sqlc.arg('app_id') AND team_id = sqlc.arg('team_id');

-- name: UpsertAppDeployConfig :one
INSERT INTO app_deploy_configs (app_id, team_id, mode, preset, steps, builder_image, outputs, detected_preset, php_version, php_ini, dockerfile_path, build_context, build_env, version, updated_by)
VALUES (sqlc.arg('app_id'), sqlc.arg('team_id'), sqlc.arg('mode'), sqlc.arg('preset'), sqlc.arg('steps'),
    sqlc.arg('builder_image'), sqlc.arg('outputs'), sqlc.arg('detected_preset'), sqlc.arg('php_version'), sqlc.arg('php_ini'),
    sqlc.arg('dockerfile_path'), sqlc.arg('build_context'), sqlc.arg('build_env'),
    sqlc.arg('version'), sqlc.arg('updated_by'))
ON CONFLICT (app_id) DO UPDATE SET mode = EXCLUDED.mode, preset = EXCLUDED.preset, steps = EXCLUDED.steps,
    builder_image = EXCLUDED.builder_image, outputs = EXCLUDED.outputs, detected_preset = EXCLUDED.detected_preset,
    php_version = EXCLUDED.php_version, php_ini = EXCLUDED.php_ini,
    dockerfile_path = EXCLUDED.dockerfile_path, build_context = EXCLUDED.build_context, build_env = EXCLUDED.build_env,
    version = EXCLUDED.version, updated_by = EXCLUDED.updated_by, updated_at = now()
WHERE app_deploy_configs.team_id = EXCLUDED.team_id
RETURNING *;

-- name: SetAppDeployConfigDetectedPreset :exec
UPDATE app_deploy_configs SET detected_preset = sqlc.arg('detected_preset') WHERE app_id = sqlc.arg('app_id') AND team_id = sqlc.arg('team_id');

-- name: InsertAppDeployConfigVersion :one
INSERT INTO app_deploy_config_versions (app_id, team_id, version, mode, preset, steps, updated_by)
VALUES (sqlc.arg('app_id'), sqlc.arg('team_id'), sqlc.arg('version'), sqlc.arg('mode'), sqlc.arg('preset'), sqlc.arg('steps'), sqlc.arg('updated_by'))
RETURNING *;

-- name: ListAppDeployConfigVersions :many
SELECT * FROM app_deploy_config_versions WHERE app_id = sqlc.arg('app_id') AND team_id = sqlc.arg('team_id') ORDER BY version DESC;

-- name: GetAppDeployConfigVersion :one
SELECT * FROM app_deploy_config_versions WHERE app_id = sqlc.arg('app_id') AND team_id = sqlc.arg('team_id') AND version = sqlc.arg('version');
