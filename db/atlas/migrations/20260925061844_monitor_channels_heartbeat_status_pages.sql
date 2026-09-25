-- Modify "monitors" table
ALTER TABLE "public"."monitors" DROP CONSTRAINT "monitors_kind_check", ADD CONSTRAINT "monitors_kind_check" CHECK (kind = ANY (ARRAY['http'::text, 'cpu'::text, 'memory'::text, 'disk'::text, 'heartbeat'::text])), ADD CONSTRAINT "monitors_heartbeat_grace_check" CHECK ((heartbeat_grace_seconds >= 0) AND (heartbeat_grace_seconds <= 86400)), ADD CONSTRAINT "monitors_heartbeat_interval_check" CHECK ((heartbeat_interval_seconds >= 30) AND (heartbeat_interval_seconds <= 86400)), ADD CONSTRAINT "monitors_ping_token_check" CHECK ((kind = 'heartbeat'::text) = (ping_token_hash IS NOT NULL)), ADD COLUMN "notify_channel_ids" text[] NOT NULL DEFAULT '{}', ADD COLUMN "heartbeat_interval_seconds" bigint NOT NULL DEFAULT 300, ADD COLUMN "heartbeat_grace_seconds" bigint NOT NULL DEFAULT 120, ADD COLUMN "ping_token_hash" text NULL, ADD COLUMN "last_ping_at" timestamptz NULL;
-- Create index "monitors_heartbeat_due_idx" to table: "monitors"
CREATE INDEX "monitors_heartbeat_due_idx" ON "public"."monitors" ("last_ping_at") WHERE (enabled AND (kind = 'heartbeat'::text));
-- Create index "monitors_ping_token_key" to table: "monitors"
CREATE UNIQUE INDEX "monitors_ping_token_key" ON "public"."monitors" ("ping_token_hash") WHERE (ping_token_hash IS NOT NULL);
-- Create "notification_channels" table
CREATE TABLE "public"."notification_channels" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "kind" text NOT NULL,
  "name" text NOT NULL,
  "emails" text[] NOT NULL DEFAULT '{}',
  "url_sealed" text NULL,
  "secret_sealed" text NULL,
  "events" text[] NOT NULL DEFAULT '{}',
  "enabled" boolean NOT NULL DEFAULT true,
  "created_by" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "notification_channels_emails_check" CHECK (((kind = 'email'::text) AND ((COALESCE(array_length(emails, 1), 0) >= 1) AND (COALESCE(array_length(emails, 1), 0) <= 10))) OR ((kind <> 'email'::text) AND (emails = '{}'::text[]))),
  CONSTRAINT "notification_channels_events_check" CHECK ((events <@ ARRAY['deploy.failed'::text, 'server.offline'::text, 'backup.failed'::text])),
  CONSTRAINT "notification_channels_kind_check" CHECK (kind = ANY (ARRAY['email'::text, 'slack'::text, 'discord'::text, 'webhook'::text])),
  CONSTRAINT "notification_channels_name_check" CHECK ((length(name) >= 1) AND (length(name) <= 80)),
  CONSTRAINT "notification_channels_url_check" CHECK (((kind = 'email'::text) AND (url_sealed IS NULL)) OR ((kind <> 'email'::text) AND (url_sealed IS NOT NULL)))
);
-- Create index "notification_channels_team_idx" to table: "notification_channels"
CREATE INDEX "notification_channels_team_idx" ON "public"."notification_channels" ("team_id");
-- Create index "notification_channels_team_name_key" to table: "notification_channels"
CREATE UNIQUE INDEX "notification_channels_team_name_key" ON "public"."notification_channels" ("team_id", "name");
-- Create "status_pages" table
CREATE TABLE "public"."status_pages" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "slug" text NOT NULL,
  "title" text NOT NULL,
  "monitor_ids" text[] NOT NULL DEFAULT '{}',
  "published" boolean NOT NULL DEFAULT false,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "status_pages_slug_check" CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'::text),
  CONSTRAINT "status_pages_title_check" CHECK ((length(title) >= 1) AND (length(title) <= 120))
);
-- Create index "status_pages_slug_key" to table: "status_pages"
CREATE UNIQUE INDEX "status_pages_slug_key" ON "public"."status_pages" ("slug");
-- Create index "status_pages_team_idx" to table: "status_pages"
CREATE INDEX "status_pages_team_idx" ON "public"."status_pages" ("team_id");
-- Create index "status_pages_team_title_key" to table: "status_pages"
CREATE UNIQUE INDEX "status_pages_team_title_key" ON "public"."status_pages" ("team_id", "title");
