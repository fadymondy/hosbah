-- Hand-written (MH-1153): Hosbah's OAuth authorization server for integrations (Mahaam).
--
-- A registered client (cmd/admin oauth-client) sends a person to /oauth/authorize; they pick one
-- app they manage, and the client trades the one-time code (authorization code + PKCE S256) at
-- /oauth/token for an app token (hba_…, app_tokens). Only hashes are stored: the client secret's
-- and the code's.
CREATE TABLE oauth_clients (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    client_id text NOT NULL,
    name text NOT NULL,
    secret_hash text NOT NULL,
    redirect_uris text NOT NULL, -- the exact redirect URIs it may use, one per line
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz NULL,
    CONSTRAINT oauth_clients_name_check CHECK (char_length(name) BETWEEN 1 AND 120)
);

CREATE UNIQUE INDEX oauth_clients_client_id_key ON oauth_clients (client_id);

-- A code lives a few minutes and is spent once (used_at). It names the app the person picked.
CREATE TABLE oauth_codes (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    code_hash text NOT NULL,
    client_id text NOT NULL,
    team_id text NOT NULL,
    app_id text NOT NULL,
    user_id text NOT NULL,
    redirect_uri text NOT NULL,
    code_challenge text NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX oauth_codes_code_hash_key ON oauth_codes (code_hash);
CREATE INDEX oauth_codes_team_idx ON oauth_codes (team_id);
CREATE INDEX oauth_codes_app_idx ON oauth_codes (app_id);
