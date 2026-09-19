-- Hosbah queries: an app's uploaded logo (team scope).

-- name: UpsertAppLogo :exec
INSERT INTO app_logos (app_id, team_id, content_type, data, etag)
VALUES (sqlc.arg('app_id'), sqlc.arg('team_id'), sqlc.arg('content_type'), sqlc.arg('data'), sqlc.arg('etag'))
ON CONFLICT (app_id) DO UPDATE SET content_type = EXCLUDED.content_type, data = EXCLUDED.data,
    etag = EXCLUDED.etag, updated_at = now()
WHERE app_logos.team_id = EXCLUDED.team_id;

-- name: GetAppLogo :one
SELECT * FROM app_logos WHERE app_id = $1 AND team_id = $2;

-- name: AppLogoVersion :one
SELECT etag FROM app_logos WHERE app_id = $1 AND team_id = $2;

-- name: ListAppLogoVersionsOfTeam :many
-- No bytes: the app list and the deployments feed only need each logo's version.
SELECT app_id, etag FROM app_logos WHERE team_id = $1;

-- name: DeleteAppLogo :execrows
DELETE FROM app_logos WHERE app_id = $1 AND team_id = $2;
