-- Hosbah: SSH logins on a server (internal/api/ssh_users.go). Always team scope.

-- name: ListServerSSHUsers :many
SELECT * FROM server_ssh_users WHERE server_id = sqlc.arg('server_id') AND team_id = sqlc.arg('team_id') ORDER BY username;

-- name: GetServerSSHUser :one
SELECT * FROM server_ssh_users WHERE server_id = sqlc.arg('server_id') AND team_id = sqlc.arg('team_id') AND username = sqlc.arg('username');

-- name: UpsertServerSSHUser :one
INSERT INTO server_ssh_users (team_id, server_id, username, sudo, status, last_error, created_by)
VALUES (sqlc.arg('team_id'), sqlc.arg('server_id'), sqlc.arg('username'), sqlc.arg('sudo'), sqlc.arg('status'), sqlc.arg('last_error'), sqlc.arg('created_by'))
ON CONFLICT (server_id, username) DO UPDATE SET
    sudo = excluded.sudo, status = excluded.status, last_error = excluded.last_error, updated_at = now()
RETURNING *;

-- name: DeleteServerSSHUser :execrows
DELETE FROM server_ssh_users WHERE server_id = sqlc.arg('server_id') AND team_id = sqlc.arg('team_id') AND username = sqlc.arg('username');

-- name: ListServerSSHKeyInstalls :many
-- Every library key installed (or once installed) on one server, with the key's name and fingerprint.
SELECT i.id, i.ssh_key_id, i.unix_user, i.status, i.last_error, i.installed_at, i.removed_at,
    k.name AS key_name, k.key_type, k.fingerprint
FROM ssh_key_installs i JOIN ssh_keys k ON k.id = i.ssh_key_id AND k.team_id = i.team_id
WHERE i.server_id = sqlc.arg('server_id') AND i.team_id = sqlc.arg('team_id')
ORDER BY i.unix_user, k.name;

-- name: GetTeamSSHKeyByFingerprint :one
SELECT * FROM ssh_keys WHERE team_id = sqlc.arg('team_id') AND fingerprint = sqlc.arg('fingerprint');
