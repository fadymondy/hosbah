-- The account cycle (internal/account). Platform tables with no RLS, like users: every query
-- runs on the pool, outside any tenant scope.

-- name: AccountUserByEmail :one
SELECT id, email, roles, permissions, disabled_at, email_unverified_at FROM users WHERE lower(email) = lower($1);

-- name: AccountUserByID :one
SELECT id, email, roles, permissions, disabled_at, email_unverified_at FROM users WHERE id = $1;

-- name: MarkEmailUnverified :exec
UPDATE users SET email_unverified_at = now() WHERE id = $1;

-- name: MarkEmailVerified :exec
UPDATE users SET email_unverified_at = NULL WHERE id = $1;

-- name: ResetPassword :exec
-- A new password proves the inbox, and every session issued until now stops working
-- (users.sessions_revoked_at, checked on every signed-in request).
UPDATE users SET password_hash = $2, email_unverified_at = NULL,
    sessions_revoked_at = GREATEST(coalesce(sessions_revoked_at, sqlc.arg(revoked_at)::timestamptz), sqlc.arg(revoked_at)::timestamptz)
WHERE id = $1;

-- name: AccountCodeSentAt :one
SELECT sent_at FROM account_codes WHERE email = $1 AND purpose = $2;

-- name: UpsertAccountCode :exec
INSERT INTO account_codes (email, purpose, code_hash, attempts, expires_at, sent_at)
VALUES ($1, $2, $3, 0, $4, $5)
ON CONFLICT (email, purpose) DO UPDATE SET code_hash = EXCLUDED.code_hash, attempts = 0,
    expires_at = EXCLUDED.expires_at, sent_at = EXCLUDED.sent_at;

-- name: AccountCode :one
SELECT code_hash, attempts, expires_at FROM account_codes WHERE email = $1 AND purpose = $2;

-- name: CountAccountCodeAttempt :exec
UPDATE account_codes SET attempts = attempts + 1 WHERE email = $1 AND purpose = $2;

-- name: DeleteAccountCode :execrows
DELETE FROM account_codes WHERE email = $1 AND purpose = $2;

-- name: AccountTotp :one
SELECT secret_sealed, enabled, last_step FROM account_totp WHERE user_id = $1;

-- name: UpsertPendingTotp :exec
INSERT INTO account_totp (user_id, secret_sealed, enabled, last_step) VALUES ($1, $2, false, 0)
ON CONFLICT (user_id) DO UPDATE SET secret_sealed = EXCLUDED.secret_sealed, enabled = false,
    last_step = 0, created_at = now(), enabled_at = NULL;

-- name: ImportTotp :execrows
-- A secret carried over from the plugin's auth_totp, already enabled. Never overwrites one set up
-- through Hosbah.
INSERT INTO account_totp (user_id, secret_sealed, enabled, last_step, enabled_at) VALUES ($1, $2, true, 0, now())
ON CONFLICT (user_id) DO NOTHING;

-- name: EnableTotp :execrows
UPDATE account_totp SET enabled = true, enabled_at = now(), last_step = $2 WHERE user_id = $1 AND NOT enabled;

-- name: AdvanceTotpStep :execrows
-- Only one request can move last_step past a step: a code is accepted once.
UPDATE account_totp SET last_step = $2 WHERE user_id = $1 AND enabled AND last_step < $2;

-- name: DeleteTotp :exec
DELETE FROM account_totp WHERE user_id = $1;

-- name: DeleteRecoveryCodes :exec
DELETE FROM account_recovery_codes WHERE user_id = $1;

-- name: InsertRecoveryCode :exec
INSERT INTO account_recovery_codes (user_id, code_hash) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: UseRecoveryCode :execrows
UPDATE account_recovery_codes SET used_at = now() WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL;

-- name: RecoveryCodesLeft :one
SELECT count(*) FROM account_recovery_codes WHERE user_id = $1 AND used_at IS NULL;

-- name: CreateChallenge :exec
INSERT INTO account_challenges (id, user_id, expires_at) VALUES ($1, $2, $3);

-- name: Challenge :one
SELECT user_id, attempts, expires_at FROM account_challenges WHERE id = $1;

-- name: CountChallengeAttempt :exec
UPDATE account_challenges SET attempts = attempts + 1 WHERE id = $1;

-- name: DeleteChallenge :execrows
DELETE FROM account_challenges WHERE id = $1;

-- name: DeleteExpiredChallenges :exec
DELETE FROM account_challenges WHERE expires_at < now();
