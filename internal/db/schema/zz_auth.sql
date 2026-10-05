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
    created_at text NOT NULL,
    -- Hosbah: set by a platform admin. A disabled account cannot sign in, and its existing
    -- sessions and MCP tokens stop working on their next request (the plugin never writes it).
    disabled_at timestamptz,
    -- Hosbah: sessions are stateless JWTs; one issued at or before this instant is refused.
    -- Set when the account is disabled, so re-enabling it does not revive old sessions.
    sessions_revoked_at timestamptz,
    -- Hosbah: set when a password sign-up has not proved its address yet (internal/account).
    -- NULL means verified, so every account that existed before verification did stays usable,
    -- and so do invitation sign-ups (the invitation proved the address).
    email_unverified_at timestamptz,
    -- Hosbah (MH-1141): set for a panel account — a customer who signs in to one server's
    -- white-label panel only. Its email column holds "<email>#panel:<server id>" so the same
    -- address can have a Hosbah account and panel accounts on many servers, and the console
    -- refuses its sessions (internal/server/accounts.go). NULL for every Hosbah account.
    panel_server_id text REFERENCES servers (id) ON DELETE CASCADE
);

-- TOTP secrets from POST /api/auth/2fa/enroll. Hosbah's login wrapper reads them to require
-- the second factor (the plugin's own login never checks it).
CREATE TABLE auth_totp (
    subject text PRIMARY KEY,
    secret text NOT NULL,
    enabled text NOT NULL DEFAULT ''
);
