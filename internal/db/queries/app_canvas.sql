-- Hosbah queries: where the boxes of a site's overview canvas sit (team scope).

-- name: GetAppCanvas :one
SELECT * FROM app_canvas WHERE app_id = sqlc.arg('app_id') AND team_id = sqlc.arg('team_id');

-- name: UpsertAppCanvas :one
INSERT INTO app_canvas (app_id, team_id, layout)
VALUES (sqlc.arg('app_id'), sqlc.arg('team_id'), sqlc.arg('layout'))
ON CONFLICT (app_id) DO UPDATE SET layout = EXCLUDED.layout, updated_at = now()
WHERE app_canvas.team_id = EXCLUDED.team_id
RETURNING *;
