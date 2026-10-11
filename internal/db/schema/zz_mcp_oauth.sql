-- Hand-written: MCP OAuth (internal/api/mcp_oauth.go). Last in load order: it alters oauth_clients and
-- mcp_tokens and references teams.

-- MCP connectors (claude.ai, Claude Code, ChatGPT, …): OAuth 2.1 for the team MCP server.
-- kind 'mcp' clients register themselves (RFC 7591, POST /oauth/register) and may only obtain team
-- MCP tokens; 'integration' clients are the admin-registered ones above and only obtain app tokens.
-- A public client (token_endpoint_auth_method 'none', the usual for connectors) has no secret.
ALTER TABLE oauth_clients ALTER COLUMN secret_hash DROP NOT NULL;
ALTER TABLE oauth_clients ADD COLUMN kind text NOT NULL DEFAULT 'integration';
ALTER TABLE oauth_clients ADD CONSTRAINT oauth_clients_kind_check CHECK (kind IN ('integration', 'mcp'));
ALTER TABLE oauth_clients ADD CONSTRAINT oauth_clients_secret_check CHECK (kind = 'mcp' OR secret_hash IS NOT NULL);

-- An MCP authorization code: the team the person picked on the consent page and the scopes they
-- granted. One-time, a few minutes, bound to its client, redirect URI and PKCE S256 challenge.
CREATE TABLE mcp_oauth_codes (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    code_hash text NOT NULL,
    client_id text NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    team_id text NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id text NOT NULL,
    redirect_uri text NOT NULL,
    code_challenge text NOT NULL,
    scopes text NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX mcp_oauth_codes_code_hash_key ON mcp_oauth_codes (code_hash);
CREATE INDEX mcp_oauth_codes_team_idx ON mcp_oauth_codes (team_id);

-- A token issued to an MCP connector over OAuth (internal/api/mcp_oauth.go): its client, and the
-- refresh token (hash only) that rotates it. The access token itself is token_hash, short-lived.
ALTER TABLE mcp_tokens ADD COLUMN oauth_client_id text NULL REFERENCES oauth_clients (id) ON DELETE SET NULL;
ALTER TABLE mcp_tokens ADD COLUMN refresh_hash text NULL;
ALTER TABLE mcp_tokens ADD COLUMN refresh_expires_at timestamptz NULL;
CREATE UNIQUE INDEX mcp_tokens_refresh_hash_key ON mcp_tokens (refresh_hash);
