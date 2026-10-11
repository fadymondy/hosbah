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
-- Team scope: the person's consent, for apps of that team.
INSERT INTO oauth_codes (code_hash, client_id, team_id, app_id, app_ids, user_id, redirect_uri, code_challenge, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: OAuthCodeByHash :one
-- Scope{TokenHash: hash} (oauth_codes_token_select), before the team is known.
SELECT * FROM oauth_codes WHERE code_hash = $1;

-- name: UseOAuthCode :execrows
-- Spends a code exactly once: a replay (or a race) finds used_at already set.
UPDATE oauth_codes SET used_at = now()
WHERE id = $1 AND team_id = $2 AND used_at IS NULL AND expires_at > now();

-- MCP OAuth (internal/api/mcp_oauth.go).

-- name: InsertMcpOAuthClient :one
-- System scope (oauth_clients_system_insert): dynamic client registration, kind 'mcp' only.
INSERT INTO oauth_clients (client_id, name, secret_hash, redirect_uris, kind)
VALUES ($1, $2, $3, $4, 'mcp')
RETURNING *;

-- name: InsertMcpOAuthCode :one
-- Team scope: the person's consent, for the team they picked.
INSERT INTO mcp_oauth_codes (code_hash, client_id, team_id, user_id, redirect_uri, code_challenge, scopes, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: McpOAuthCodeByHash :one
-- Scope{TokenHash: hash} (mcp_oauth_codes_token_select), before the team is known.
SELECT * FROM mcp_oauth_codes WHERE code_hash = $1;

-- name: UseMcpOAuthCode :execrows
UPDATE mcp_oauth_codes SET used_at = now()
WHERE id = $1 AND team_id = $2 AND used_at IS NULL AND expires_at > now();

-- name: InsertOAuthMcpToken :one
-- Team scope: a connector's token, minted at the token endpoint.
INSERT INTO mcp_tokens (team_id, user_id, name, token_hash, scopes, expires_at, oauth_client_id, refresh_hash, refresh_expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: McpTokenByRefreshHash :one
-- Scope{TokenHash: hash of the refresh token} (mcp_tokens_refresh_select).
SELECT * FROM mcp_tokens WHERE refresh_hash = $1;

-- name: RotateMcpToken :execrows
-- Team scope. Matches only the refresh token presented, so of two racing refreshes one wins.
UPDATE mcp_tokens SET token_hash = $3, expires_at = $4, refresh_hash = $5, refresh_expires_at = $6, updated_at = now()
WHERE id = $1 AND refresh_hash = $2 AND revoked_at IS NULL;

-- name: ListMcpTokensOfUserInTeam :many
-- Team scope: the person's own tokens in one team (the account's "AI connections").
SELECT * FROM mcp_tokens WHERE team_id = $1 AND user_id = $2 AND revoked_at IS NULL ORDER BY created_at DESC;

-- name: CountTeamFootprint :one
-- Team scope: how much lives in a team, to pick the consent page's default.
SELECT (SELECT count(*) FROM servers WHERE servers.team_id = $1)::bigint AS servers,
       (SELECT count(*) FROM apps WHERE apps.team_id = $1)::bigint AS apps;
