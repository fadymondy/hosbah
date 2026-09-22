-- Sign-in with Google and GitHub (internal/account/oauth.go) and the one-time states of every
-- provider round trip (internal/oauthstate). oauth_states and users have no RLS and run on the pool;
-- user_identities has FORCE RLS: run those inside tenancy.Tx (system scope before sign-in, the
-- user's own scope in the account settings).

-- name: CreateOAuthState :exec
INSERT INTO oauth_states (id, browser_hash, purpose, provider, code_verifier, nonce, next, locale, invitation_hash, user_id, team_id, expires_at)
VALUES (sqlc.arg('id'), sqlc.arg('browser_hash'), sqlc.arg('purpose'), sqlc.arg('provider'), sqlc.arg('code_verifier'), sqlc.arg('nonce'),
    sqlc.arg('next'), sqlc.arg('locale'), sqlc.arg('invitation_hash'), sqlc.narg('user_id'), sqlc.narg('team_id'), sqlc.arg('expires_at'));

-- name: ConsumeOAuthState :one
-- Single use: whoever deletes the row owns it; a second callback with the same state finds nothing.
DELETE FROM oauth_states WHERE id = $1 RETURNING *;

-- name: DeleteExpiredOAuthStates :exec
DELETE FROM oauth_states WHERE expires_at < now();

-- name: OAuthIdentityBySubject :one
SELECT * FROM user_identities WHERE provider = $1 AND subject = $2;

-- name: InsertUserIdentity :one
INSERT INTO user_identities (user_id, provider, subject, email_at_link, last_used_at)
VALUES (sqlc.arg('user_id'), sqlc.arg('provider'), sqlc.arg('subject'), sqlc.arg('email_at_link'), now())
RETURNING *;

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
