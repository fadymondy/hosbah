-- Hosbah queries: app-to-service links (auto-detected from env, or set by hand), team scope.

-- name: ListAppServiceLinks :many
SELECT * FROM app_service_links WHERE app_id = $1 AND team_id = $2 ORDER BY variable;

-- name: ReplaceAppServiceLinksAuto :exec
DELETE FROM app_service_links WHERE app_id = $1 AND team_id = $2 AND source = 'auto';

-- name: UpsertAppServiceLink :one
INSERT INTO app_service_links (id, team_id, app_id, service_kind, service_id, variable, source)
VALUES (sqlc.arg('id'), sqlc.arg('team_id'), sqlc.arg('app_id'), sqlc.arg('service_kind'), sqlc.arg('service_id'), sqlc.arg('variable'), sqlc.arg('source'))
ON CONFLICT (app_id, variable) DO UPDATE SET
    service_kind = EXCLUDED.service_kind, service_id = EXCLUDED.service_id, source = EXCLUDED.source, updated_at = now()
WHERE app_service_links.team_id = EXCLUDED.team_id
RETURNING *;

-- name: DeleteAppServiceLinkInTeam :execrows
DELETE FROM app_service_links WHERE id = $1 AND team_id = $2;
