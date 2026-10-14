-- Sign-in with Google and GitHub (internal/account/oauth.go) and the one-time states of every
-- provider round trip (internal/oauthstate). oauth_states and users have no RLS and run on the pool;
-- user_identities has FORCE RLS: run those inside tenancy.Tx (system scope before sign-in, the
-- user's own scope in the account settings).

-- name: CreateOAuthState :exec
INSERT INTO oauth_states (id, browser_hash, purpose, provider, code_verifier, nonce, next, locale, invitation_hash, user_id, team_id, app_challenge, expires_at)
VALUES (sqlc.arg('id'), sqlc.arg('browser_hash'), sqlc.arg('purpose'), sqlc.arg('provider'), sqlc.arg('code_verifier'), sqlc.arg('nonce'),
    sqlc.arg('next'), sqlc.arg('locale'), sqlc.arg('invitation_hash'), sqlc.narg('user_id'), sqlc.narg('team_id'), sqlc.arg('app_challenge'), sqlc.arg('expires_at'));

-- name: StashOAuthProviderAnswer :execrows
-- Apple's form_post: keep what it POSTed for the same-site GET that finishes the trip. Once only, and
-- only on a live state for that provider; the row is still consumed (and checked) by the GET.
UPDATE oauth_states SET provider_code = sqlc.arg('provider_code'), provider_user = sqlc.arg('provider_user')
WHERE id = sqlc.arg('id') AND provider = sqlc.arg('provider') AND provider_code = '' AND expires_at > now();

-- name: ConsumeOAuthState :one
-- Single use: whoever deletes the row owns it; a second callback with the same state finds nothing.
DELETE FROM oauth_states WHERE id = $1 RETURNING *;

-- name: DeleteExpiredOAuthStates :exec
DELETE FROM oauth_states WHERE expires_at < now();

-- name: OAuthIdentityBySubject :one
SELECT * FROM user_identities WHERE provider = $1 AND subject = $2;

-- name: InsertUserIdentity :one
INSERT INTO user_identities (user_id, provider, subject, email_at_link, display_name, last_used_at)
VALUES (sqlc.arg('user_id'), sqlc.arg('provider'), sqlc.arg('subject'), sqlc.arg('email_at_link'), sqlc.arg('display_name'), now())
RETURNING *;

-- name: UpdateIdentityExtras :exec
-- A name the provider shared is kept when there was none (Apple sends it on the first consent only);
-- a new provider token (Apple's refresh token, sealed) replaces the old one.
UPDATE user_identities SET
    display_name = CASE WHEN display_name = '' THEN sqlc.arg('display_name') ELSE display_name END,
    provider_token = CASE WHEN sqlc.arg('provider_token')::text <> '' THEN sqlc.arg('provider_token')::text ELSE provider_token END
WHERE provider = sqlc.arg('provider') AND subject = sqlc.arg('subject');

-- name: IdentityTokensOfUser :many
-- The sealed provider tokens to revoke when the account is deleted.
SELECT id, provider, provider_token FROM user_identities WHERE user_id = $1 AND provider_token <> '';

-- name: TouchUserIdentity :exec
UPDATE user_identities SET last_used_at = now() WHERE id = $1;

-- name: ListUserIdentities :many
SELECT * FROM user_identities WHERE user_id = $1 ORDER BY created_at, id;

-- name: UserIdentityOfUser :one
SELECT * FROM user_identities WHERE id = $1 AND user_id = $2;

-- name: DeleteUserIdentity :execrows
DELETE FROM user_identities WHERE id = $1 AND user_id = $2;

-- name: UserHasPassword :one
-- A bcrypt hash; accounts made through a provider hold a marker no password can match.
SELECT (password_hash LIKE '$2%')::boolean AS has_password FROM users WHERE id = $1;

-- name: CreateOAuthUser :exec
-- A new account from a provider-verified address: verified already, no password (the marker can never
-- match a bcrypt comparison; "forgot password" sets one).
INSERT INTO users (id, email, password_hash, roles, permissions, created_at)
VALUES (sqlc.arg('id'), sqlc.arg('email'), '!oauth', '', '', sqlc.arg('created_at'));

-- name: ClaimUnverifiedAccount :exec
-- A provider proved the inbox of an account whose address was never verified. Whoever registered
-- it chose its password without proving the address, so that password is dropped (a pre-hijacked
-- account stays hijacked otherwise) and every session issued until now stops working.
UPDATE users SET email_unverified_at = NULL, password_hash = '!oauth',
    sessions_revoked_at = GREATEST(coalesce(sessions_revoked_at, sqlc.arg(revoked_at)::timestamptz), sqlc.arg(revoked_at)::timestamptz)
WHERE id = sqlc.arg('id') AND email_unverified_at IS NOT NULL;

-- name: CreateOAuthAppCode :exec
INSERT INTO oauth_app_codes (id, user_id, provider, code_challenge, expires_at)
VALUES (sqlc.arg('id'), sqlc.arg('user_id'), sqlc.arg('provider'), sqlc.arg('code_challenge'), sqlc.arg('expires_at'));

-- name: ConsumeOAuthAppCode :one
-- Single use, like ConsumeOAuthState.
DELETE FROM oauth_app_codes WHERE id = $1 RETURNING *;

-- name: DeleteExpiredOAuthAppCodes :exec
DELETE FROM oauth_app_codes WHERE expires_at < now();

-- name: UserPasswordHash :one
-- Re-authentication before deleting an account (internal/account/delete.go).
SELECT password_hash FROM users WHERE id = $1;
