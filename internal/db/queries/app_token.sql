-- Hand-written (MH-1153): app tokens and panel sign-in links for the integration API.

-- name: InsertAppToken :one
INSERT INTO app_tokens (team_id, app_id, label, token_hash, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: AppTokenByHash :one
SELECT * FROM app_tokens WHERE token_hash = $1;

-- name: ListAppTokens :many
SELECT t.*, coalesce(u.email, '')::text AS created_by_email
FROM app_tokens t
LEFT JOIN users u ON u.id = t.created_by
WHERE t.app_id = $1 AND t.team_id = $2 AND t.revoked_at IS NULL
ORDER BY t.created_at DESC;

-- name: TouchAppToken :exec
UPDATE app_tokens SET last_used_at = now()
WHERE id = $1 AND team_id = $2 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: RevokeAppToken :execrows
UPDATE app_tokens SET revoked_at = now()
WHERE id = $1 AND app_id = $2 AND team_id = $3 AND revoked_at IS NULL;

-- name: InsertAppPanelLink :one
INSERT INTO app_panel_links (team_id, app_id, app_token_id, link_hash, email, name, locale, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: AppPanelLinkByHash :one
SELECT * FROM app_panel_links WHERE link_hash = $1;

-- name: UseAppPanelLink :execrows
-- Spends a link exactly once: a second use (or a race) finds used_at already set.
UPDATE app_panel_links SET used_at = now()
WHERE id = $1 AND team_id = $2 AND used_at IS NULL AND expires_at > now();

-- name: AppTokenInTeam :one
SELECT * FROM app_tokens WHERE id = $1 AND team_id = $2;
