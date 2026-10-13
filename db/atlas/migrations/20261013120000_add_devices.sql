-- Phone and desktop apps (docs/apps-plan.md): signed-in installs with their rotating refresh token
-- and FCM push token, and each user's muted push events. Desired state:
-- internal/db/schema/zz_device.sql, zz_notification_pref.sql.

CREATE TABLE "devices" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "user_id" text NOT NULL,
  "name" text NOT NULL,
  "platform" text NOT NULL,
  "app" text NOT NULL,
  "locale" text NOT NULL DEFAULT 'ar',
  "app_version" text NOT NULL DEFAULT '',
  "refresh_hash" text NOT NULL,
  "push_token" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "refreshed_at" timestamptz NOT NULL DEFAULT now(),
  "last_seen_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "devices_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "devices_platform_check" CHECK (platform IN ('ios', 'android', 'windows', 'macos', 'linux')),
  CONSTRAINT "devices_app_check" CHECK (app IN ('mobile', 'desktop')),
  CONSTRAINT "devices_locale_check" CHECK (locale IN ('ar', 'en')),
  CONSTRAINT "devices_name_check" CHECK (char_length(name) BETWEEN 1 AND 80)
);
CREATE UNIQUE INDEX "devices_refresh_hash_idx" ON "devices" ("refresh_hash");
CREATE UNIQUE INDEX "devices_push_token_idx" ON "devices" ("push_token") WHERE (push_token IS NOT NULL);
CREATE INDEX "devices_user_idx" ON "devices" ("user_id");

CREATE TABLE "notification_prefs" (
  "user_id" text NOT NULL,
  "muted_events" text[] NOT NULL DEFAULT '{}',
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("user_id"),
  CONSTRAINT "notification_prefs_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);

-- A device belongs to its user. The system scope covers the two callers that are not that user:
-- the refresh endpoint (not signed in yet: finds the row by its token hash and rotates it) and
-- the push sender (reads the tokens of a team's members, clears tokens FCM reports dead).
ALTER TABLE devices ENABLE ROW LEVEL SECURITY;
ALTER TABLE devices FORCE ROW LEVEL SECURITY;
CREATE POLICY devices_own_select ON devices FOR SELECT
  USING (user_id = nullif(current_setting('app.user_id', true), ''));
CREATE POLICY devices_own_insert ON devices FOR INSERT
  WITH CHECK (user_id = nullif(current_setting('app.user_id', true), ''));
CREATE POLICY devices_own_update ON devices FOR UPDATE
  USING (user_id = nullif(current_setting('app.user_id', true), ''))
  WITH CHECK (user_id = nullif(current_setting('app.user_id', true), ''));
CREATE POLICY devices_own_delete ON devices FOR DELETE
  USING (user_id = nullif(current_setting('app.user_id', true), ''));
CREATE POLICY devices_system_all ON devices FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

ALTER TABLE notification_prefs ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_prefs FORCE ROW LEVEL SECURITY;
CREATE POLICY notification_prefs_own_all ON notification_prefs FOR ALL
  USING (user_id = nullif(current_setting('app.user_id', true), ''))
  WITH CHECK (user_id = nullif(current_setting('app.user_id', true), ''));
CREATE POLICY notification_prefs_system_select ON notification_prefs FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
