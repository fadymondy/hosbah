-- MH-1153: the OAuth authorization server's registered clients and one-time codes.
CREATE TABLE "oauth_clients" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "client_id" text NOT NULL,
  "name" text NOT NULL,
  "secret_hash" text NOT NULL,
  "redirect_uris" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "revoked_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "oauth_clients_name_check" CHECK (char_length(name) >= 1 AND char_length(name) <= 120)
);
CREATE UNIQUE INDEX "oauth_clients_client_id_key" ON "oauth_clients" ("client_id");

CREATE TABLE "oauth_codes" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "code_hash" text NOT NULL,
  "client_id" text NOT NULL,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "user_id" text NOT NULL,
  "redirect_uri" text NOT NULL,
  "code_challenge" text NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "used_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "oauth_codes_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "oauth_codes_client_fk" FOREIGN KEY ("client_id") REFERENCES "oauth_clients" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE UNIQUE INDEX "oauth_codes_code_hash_key" ON "oauth_codes" ("code_hash");
CREATE INDEX "oauth_codes_team_idx" ON "oauth_codes" ("team_id");
CREATE INDEX "oauth_codes_app_idx" ON "oauth_codes" ("app_id");

-- Row-level security. Clients are platform data: platform admins manage them (cmd/admin), and the
-- consent page and token endpoint read them in system scope. Codes are team data, written in the
-- consenting person's team and read back by hash (Scope{TokenHash}) at the token endpoint, which
-- then switches to the code's team, as app_panel_links does.
ALTER TABLE oauth_clients ENABLE ROW LEVEL SECURITY;
ALTER TABLE oauth_clients FORCE ROW LEVEL SECURITY;
CREATE POLICY oauth_clients_admin_all ON oauth_clients FOR ALL
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY oauth_clients_system_select ON oauth_clients FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');

ALTER TABLE oauth_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE oauth_codes FORCE ROW LEVEL SECURITY;
CREATE POLICY oauth_codes_team_select ON oauth_codes FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY oauth_codes_token_select ON oauth_codes FOR SELECT
  USING (code_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY oauth_codes_team_insert ON oauth_codes FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY oauth_codes_team_update ON oauth_codes FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY oauth_codes_team_delete ON oauth_codes FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
