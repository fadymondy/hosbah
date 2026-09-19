-- Hosbah queries: a server's uploaded logo (team scope).

-- name: UpsertServerLogo :exec
INSERT INTO server_logos (server_id, team_id, content_type, data, etag)
VALUES (sqlc.arg('server_id'), sqlc.arg('team_id'), sqlc.arg('content_type'), sqlc.arg('data'), sqlc.arg('etag'))
ON CONFLICT (server_id) DO UPDATE SET content_type = EXCLUDED.content_type, data = EXCLUDED.data,
    etag = EXCLUDED.etag, updated_at = now()
WHERE server_logos.team_id = EXCLUDED.team_id;

-- name: GetServerLogo :one
SELECT * FROM server_logos WHERE server_id = $1 AND team_id = $2;

-- name: ServerLogoVersion :one
SELECT etag FROM server_logos WHERE server_id = $1 AND team_id = $2;

-- name: ListServerLogoVersionsOfTeam :many
-- No bytes: the server list only needs each logo's version.
SELECT server_id, etag FROM server_logos WHERE team_id = $1;

-- name: DeleteServerLogo :execrows
DELETE FROM server_logos WHERE server_id = $1 AND team_id = $2;
