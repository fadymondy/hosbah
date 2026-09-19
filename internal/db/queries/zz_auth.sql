-- Hosbah queries over the togo auth plugin's tables.
-- name: UserByID :one
SELECT id, email, roles, created_at, disabled_at FROM users WHERE id = $1;

-- name: UserByEmail :one
SELECT id, email, roles, created_at, disabled_at FROM users WHERE lower(email) = lower($1);

-- name: SetUserRoles :exec
UPDATE users SET roles = $2 WHERE id = $1;

-- name: PluginTotpSecrets :many
-- The plugin's plain-text TOTP secrets (auth_totp), read once at boot to move enabled ones into
-- account_totp sealed (internal/account.ImportPluginTOTP).
SELECT t.subject, t.secret, t.enabled FROM auth_totp t JOIN users u ON u.id = t.subject;

-- name: DeletePluginTotp :exec
DELETE FROM auth_totp WHERE subject = $1;
