-- Panel accounts and sessions (MH-1141). users and panel_sessions have no RLS; every query is
-- filtered by the panel's server, and the team endpoints check the server is in the team first.

-- name: CreatePanelAccount :one
INSERT INTO users (id, email, password_hash, roles, permissions, created_at, panel_server_id)
VALUES (gen_random_uuid()::text, $1, $2, '', '', $3, $4)
RETURNING id, email, created_at, disabled_at;

-- name: ListPanelAccounts :many
-- Only the server's own panel accounts: an app's integration customers (MH-1153, "<email>#app:<app id>")
-- share panel_server_id but are not accounts of the panel.
SELECT id, email, created_at, disabled_at FROM users WHERE panel_server_id = $1 AND position('#panel:' in email) > 0 ORDER BY email;

-- name: DeletePanelAccount :execrows
DELETE FROM users WHERE id = $1 AND panel_server_id = $2;

-- name: SetPanelAccountPassword :execrows
UPDATE users SET password_hash = $3 WHERE id = $1 AND panel_server_id = $2;

-- name: SetPanelAppAccountScope :execrows
-- An app's integration customer (MH-1153): the team and app its panel-link sessions are limited to.
-- Since auth v0.10.0 the plugin replaces a session's permissions with the stored ones on every
-- request, so the scope lives on the account (one per app) as well as in the token.
UPDATE users SET permissions = $3 WHERE id = $1 AND panel_server_id = $2 AND position('#app:' in email) > 0;

-- name: PanelAccountByEmail :one
SELECT id, email, password_hash, disabled_at FROM users WHERE email = $1 AND panel_server_id = $2;

-- name: CreatePanelSession :exec
INSERT INTO panel_sessions (token_hash, user_id, server_id, expires_at) VALUES ($1, $2, $3, $4);

-- name: PanelSession :one
SELECT u.id, u.email, s.server_id FROM panel_sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.server_id = $2 AND s.expires_at > now() AND u.disabled_at IS NULL;

-- name: DeletePanelSession :exec
DELETE FROM panel_sessions WHERE token_hash = $1;

-- name: DeletePanelSessionsOfUser :exec
DELETE FROM panel_sessions WHERE user_id = $1;

-- name: PanelServer :one
-- System scope (servers_system_select): the panel's brand, before anyone has signed in.
SELECT id, team_id, name, color, icon, logo_url FROM servers WHERE id = $1;
