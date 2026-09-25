-- Hosbah tenant queries (MH-574 E13): per-team quotas.
-- name: GetTeamQuota :one
SELECT * FROM team_quotas WHERE team_id = $1;

-- name: UpsertTeamQuota :one
INSERT INTO team_quotas (team_id, max_servers, max_apps, max_data_services, updated_by)
VALUES (sqlc.arg('team_id'), sqlc.narg('max_servers'), sqlc.narg('max_apps'), sqlc.narg('max_data_services'), sqlc.arg('updated_by'))
ON CONFLICT (team_id) DO UPDATE SET max_servers = excluded.max_servers, max_apps = excluded.max_apps,
    max_data_services = excluded.max_data_services, updated_by = excluded.updated_by, updated_at = now()
RETURNING *;
