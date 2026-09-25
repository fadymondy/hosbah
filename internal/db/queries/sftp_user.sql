-- Hand-written (not togo-generated). PostgreSQL query fragment - safe to edit.

-- name: CreateSFTPUser :one
INSERT INTO sftp_users (team_id, server_id, username, public_keys, password_sealed, root_dir, read_only, status, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListSFTPUsersOfServer :many
SELECT * FROM sftp_users WHERE server_id = $1 AND team_id = $2 ORDER BY username;

-- name: ListSFTPUsersOfTeam :many
SELECT u.*, s.name AS server_name FROM sftp_users u JOIN servers s ON s.id = u.server_id WHERE u.team_id = $1 ORDER BY s.name, u.username;

-- name: SFTPUserInTeam :one
SELECT * FROM sftp_users WHERE id = $1 AND team_id = $2;

-- name: SFTPUserInTeamByRef :one
SELECT * FROM sftp_users WHERE team_id = sqlc.arg('team_id') AND (id = sqlc.arg('ref') OR username = sqlc.arg('ref')) AND server_id = sqlc.arg('server_id');

-- name: UpdateSFTPUser :one
UPDATE sftp_users SET public_keys = $3, password_sealed = $4, root_dir = $5, read_only = $6, status = $7, error = NULL, updated_at = now()
WHERE id = $1 AND team_id = $2
RETURNING *;

-- name: SetSFTPUserState :exec
UPDATE sftp_users SET status = $3, error = $4, updated_at = now() WHERE id = $1 AND team_id = $2;

-- name: DeleteSFTPUserInTeam :execrows
DELETE FROM sftp_users WHERE id = $1 AND team_id = $2;
