-- Hand-written (MH-1153): the OAuth authorization server's clients and codes.

-- name: InsertOAuthClient :one
-- Platform scope (cmd/admin, oauth_clients_admin_*).
INSERT INTO oauth_clients (client_id, name, secret_hash, redirect_uris)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: OAuthClientByClientID :one
-- System scope (oauth_clients_system_select): the consent page and the token endpoint.
SELECT * FROM oauth_clients WHERE client_id = $1 AND revoked_at IS NULL;

-- name: ListOAuthClients :many
SELECT * FROM oauth_clients ORDER BY created_at;

-- name: RevokeOAuthClient :execrows
UPDATE oauth_clients SET revoked_at = now() WHERE client_id = $1 AND revoked_at IS NULL;

-- name: InsertOAuthCode :one
-- Team scope: the person's consent, for an app of that team.
INSERT INTO oauth_codes (code_hash, client_id, team_id, app_id, user_id, redirect_uri, code_challenge, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: OAuthCodeByHash :one
-- Scope{TokenHash: hash} (oauth_codes_token_select), before the team is known.
SELECT * FROM oauth_codes WHERE code_hash = $1;

-- name: UseOAuthCode :execrows
-- Spends a code exactly once: a replay (or a race) finds used_at already set.
UPDATE oauth_codes SET used_at = now()
WHERE id = $1 AND team_id = $2 AND used_at IS NULL AND expires_at > now();
