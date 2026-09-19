-- Hosbah queries over the togo auth plugin's tables.
-- name: UserByID :one
SELECT id, email, roles, created_at FROM users WHERE id = $1;

-- name: UserByEmail :one
SELECT id, email, roles, created_at FROM users WHERE lower(email) = lower($1);

-- name: SetUserRoles :exec
UPDATE users SET roles = $2 WHERE id = $1;

-- name: TotpForSubject :one
SELECT secret, enabled FROM auth_totp WHERE subject = $1;

-- name: DeleteTotp :exec
DELETE FROM auth_totp WHERE subject = $1;
