-- Hosbah queries: shared services (Proxmox guests classified as shared infra), team scope.

-- name: ListSharedServicesOfTeam :many
SELECT * FROM shared_services WHERE team_id = $1 ORDER BY kind, name;

-- name: SharedServiceInTeam :one
SELECT * FROM shared_services WHERE id = $1 AND team_id = $2;

-- name: GetSharedServiceByGuest :one
SELECT * FROM shared_services WHERE server_id = $1 AND guest_vmid = $2;

-- name: UpsertSharedService :one
INSERT INTO shared_services (id, team_id, server_id, guest_vmid, guest_type, kind, name, host, port, last_seen_at)
VALUES (sqlc.arg('id'), sqlc.arg('team_id'), sqlc.arg('server_id'), sqlc.arg('guest_vmid'), sqlc.arg('guest_type'),
    sqlc.arg('kind'), sqlc.arg('name'), sqlc.narg('host'), sqlc.narg('port'), now())
ON CONFLICT (server_id, guest_vmid) DO UPDATE SET
    kind = CASE WHEN shared_services.kind_manual THEN shared_services.kind ELSE EXCLUDED.kind END,
    name = CASE WHEN shared_services.name_manual THEN shared_services.name ELSE EXCLUDED.name END,
    host = EXCLUDED.host, port = EXCLUDED.port, last_seen_at = now(), updated_at = now()
WHERE shared_services.team_id = EXCLUDED.team_id
RETURNING *;

-- name: UpdateSharedService :one
UPDATE shared_services SET
    name = COALESCE(sqlc.narg('name'), name),
    name_manual = CASE WHEN sqlc.narg('name') IS NOT NULL THEN true ELSE name_manual END,
    kind = COALESCE(sqlc.narg('kind'), kind),
    kind_manual = CASE WHEN sqlc.narg('kind') IS NOT NULL THEN true ELSE kind_manual END,
    hidden = COALESCE(sqlc.narg('hidden'), hidden),
    updated_at = now()
WHERE id = sqlc.arg('id') AND team_id = sqlc.arg('team_id')
RETURNING *;

-- name: DeleteSharedServiceInTeam :execrows
DELETE FROM shared_services WHERE id = $1 AND team_id = $2;
