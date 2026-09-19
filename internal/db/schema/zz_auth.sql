-- Hosbah: the togo auth plugin's tables that Hosbah reads, declared here so the migration
-- ledger owns them and sqlc can type queries against them.
--
-- The auth plugin (github.com/togo-framework/auth v0.8.0) creates these itself at boot with
-- CREATE TABLE IF NOT EXISTS and exactly these columns; because migrations run first, its
-- statements become no-ops. Keep the column lists identical to the plugin's — it inserts by
-- name. users is platform data (not a tenant row) and has no row-level security: the plugin
-- reads it through the pool on every login.
CREATE TABLE users (
    id text PRIMARY KEY,
    email text UNIQUE NOT NULL,
    password_hash text NOT NULL,
    roles text NOT NULL DEFAULT '',
    permissions text NOT NULL DEFAULT '',
    created_at text NOT NULL
);

-- TOTP secrets from POST /api/auth/2fa/enroll. Hosbah's login wrapper reads them to require
-- the second factor (the plugin's own login never checks it).
CREATE TABLE auth_totp (
    subject text PRIMARY KEY,
    secret text NOT NULL,
    enabled text NOT NULL DEFAULT ''
);
