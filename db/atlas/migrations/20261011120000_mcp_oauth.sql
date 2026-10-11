-- MCP OAuth (internal/api/mcp_oauth.go): connectors such as claude.ai, Claude Code and ChatGPT register
-- themselves (RFC 7591), the person picks a team on the consent page, and the connector trades the
-- code for a short-lived team MCP token plus a rotating refresh token. Desired state:
-- internal/db/schema/zz_mcp_oauth.sql.

-- Clients: 'integration' (admin-registered, app tokens, always a secret) or 'mcp' (self-registered,
-- MCP tokens, usually public: no secret).
ALTER TABLE "oauth_clients" ALTER COLUMN "secret_hash" DROP NOT NULL;
ALTER TABLE "oauth_clients" ADD COLUMN "kind" text NOT NULL DEFAULT 'integration';
ALTER TABLE "oauth_clients" ADD CONSTRAINT "oauth_clients_kind_check" CHECK (kind IN ('integration', 'mcp'));
ALTER TABLE "oauth_clients" ADD CONSTRAINT "oauth_clients_secret_check" CHECK (kind = 'mcp' OR secret_hash IS NOT NULL);

-- Dynamic client registration is anonymous, so the API writes the row in system scope; it can only
-- ever create an 'mcp' client (which can only ever obtain tokens a signed-in person consented to).
CREATE POLICY oauth_clients_system_insert ON oauth_clients FOR INSERT
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on' AND kind = 'mcp');

CREATE TABLE "mcp_oauth_codes" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "code_hash" text NOT NULL,
  "client_id" text NOT NULL,
  "team_id" text NOT NULL,
  "user_id" text NOT NULL,
  "redirect_uri" text NOT NULL,
  "code_challenge" text NOT NULL,
  "scopes" text NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "used_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "mcp_oauth_codes_client_fk" FOREIGN KEY ("client_id") REFERENCES "oauth_clients" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "mcp_oauth_codes_team_fk" FOREIGN KEY ("team_id") REFERENCES "teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE UNIQUE INDEX "mcp_oauth_codes_code_hash_key" ON "mcp_oauth_codes" ("code_hash");
CREATE INDEX "mcp_oauth_codes_team_idx" ON "mcp_oauth_codes" ("team_id");

-- Codes are team data: written in the consenting person's team, read back by hash
-- (Scope{TokenHash}) at the token endpoint, which then switches to the code's team.
ALTER TABLE mcp_oauth_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE mcp_oauth_codes FORCE ROW LEVEL SECURITY;
CREATE POLICY mcp_oauth_codes_team_select ON mcp_oauth_codes FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY mcp_oauth_codes_token_select ON mcp_oauth_codes FOR SELECT
  USING (code_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY mcp_oauth_codes_team_insert ON mcp_oauth_codes FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY mcp_oauth_codes_team_update ON mcp_oauth_codes FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY mcp_oauth_codes_team_delete ON mcp_oauth_codes FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));

-- A connector's token: its client and its refresh token (hash only).
ALTER TABLE "mcp_tokens" ADD COLUMN "oauth_client_id" text NULL;
ALTER TABLE "mcp_tokens" ADD COLUMN "refresh_hash" text NULL;
ALTER TABLE "mcp_tokens" ADD COLUMN "refresh_expires_at" timestamptz NULL;
ALTER TABLE "mcp_tokens" ADD CONSTRAINT "mcp_tokens_oauth_client_fk" FOREIGN KEY ("oauth_client_id") REFERENCES "oauth_clients" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
CREATE UNIQUE INDEX "mcp_tokens_refresh_hash_key" ON "mcp_tokens" ("refresh_hash");

-- The token endpoint finds a token by its presented refresh token's hash (Scope{TokenHash}), then
-- switches to the token's team to rotate or revoke it (mcp_tokens_update's team arm).
CREATE POLICY mcp_tokens_refresh_select ON mcp_tokens FOR SELECT
  USING (refresh_hash = nullif(current_setting('app.token_hash', true), ''));
