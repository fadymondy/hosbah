-- MH-1153: the integration API's app tokens (hba_…) and one-time panel sign-in links.
CREATE TABLE "app_tokens" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "label" text NOT NULL,
  "token_hash" text NOT NULL,
  "created_by" text NOT NULL,
  "last_used_at" timestamptz NULL,
  "revoked_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_tokens_label_check" CHECK (char_length(label) >= 1 AND char_length(label) <= 120),
  CONSTRAINT "app_tokens_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE UNIQUE INDEX "app_tokens_token_hash_key" ON "app_tokens" ("token_hash");
CREATE INDEX "app_tokens_app_idx" ON "app_tokens" ("app_id", "created_at");
CREATE INDEX "app_tokens_team_idx" ON "app_tokens" ("team_id");

CREATE TABLE "app_panel_links" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "app_token_id" text NOT NULL,
  "link_hash" text NOT NULL,
  "email" text NOT NULL,
  "name" text NOT NULL DEFAULT '',
  "locale" text NOT NULL DEFAULT 'en',
  "expires_at" timestamptz NOT NULL,
  "used_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_panel_links_locale_check" CHECK (locale = ANY (ARRAY['en'::text, 'ar'::text])),
  CONSTRAINT "app_panel_links_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_panel_links_token_fk" FOREIGN KEY ("app_token_id") REFERENCES "app_tokens" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE UNIQUE INDEX "app_panel_links_link_hash_key" ON "app_panel_links" ("link_hash");
CREATE INDEX "app_panel_links_app_idx" ON "app_panel_links" ("app_id");
CREATE INDEX "app_panel_links_team_idx" ON "app_panel_links" ("team_id");

-- Row-level security. Both are team data; the integration endpoints don't know their team until
-- they have resolved the bearer token (or the link), so they open the transaction scoped to its
-- hash (Scope{TokenHash: hash}, like product_keys) and switch to the row's team once found.
ALTER TABLE app_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY app_tokens_team_select ON app_tokens FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_tokens_token_select ON app_tokens FOR SELECT
  USING (token_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY app_tokens_team_insert ON app_tokens FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_tokens_team_update ON app_tokens FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_tokens_team_delete ON app_tokens FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));

ALTER TABLE app_panel_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_panel_links FORCE ROW LEVEL SECURITY;
CREATE POLICY app_panel_links_team_select ON app_panel_links FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_panel_links_token_select ON app_panel_links FOR SELECT
  USING (link_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY app_panel_links_team_insert ON app_panel_links FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_panel_links_team_update ON app_panel_links FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_panel_links_team_delete ON app_panel_links FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
