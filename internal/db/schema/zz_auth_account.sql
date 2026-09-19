-- Hosbah: the account cycle around the togo auth plugin (internal/account): emailed codes for
-- verification, password reset and sign-in, and authenticator-app two-factor. Hand-written, not
-- make:resource entities: they are platform data read before anyone is signed in, like users,
-- and are never exposed through generated CRUD. Like users they have no row-level security.

-- One live 6-digit code per address and purpose, bcrypt-hashed. A new request replaces it; five
-- wrong tries or 15 minutes and it is dead.
CREATE TABLE account_codes (
    email text NOT NULL,
    purpose text NOT NULL,
    code_hash text NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL,
    sent_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (email, purpose)
);
ALTER TABLE account_codes ADD CONSTRAINT account_codes_purpose_check CHECK (purpose IN ('verify', 'reset', 'login'));

-- The authenticator secret, sealed with the deployment KEK (crypto.Keyring.SealPlatform). enabled
-- stays false until the first code is confirmed; last_step only moves forward, so a code is never
-- accepted twice.
CREATE TABLE account_totp (
    user_id text PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    secret_sealed text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    last_step bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    enabled_at timestamptz
);

-- Single-use recovery codes, SHA-256 hashed (they are random, so a slow hash adds nothing).
CREATE TABLE account_recovery_codes (
    user_id text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash text NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, code_hash)
);

-- A sign-in that passed its first factor and waits for the second: 5 minutes, 5 tries.
CREATE TABLE account_challenges (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    attempts integer NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX account_challenges_expires_idx ON account_challenges (expires_at);
