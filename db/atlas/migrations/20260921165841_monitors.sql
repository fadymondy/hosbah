-- Create "monitors" table
CREATE TABLE "public"."monitors" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "name" text NOT NULL,
  "kind" text NOT NULL,
  "target" text NOT NULL DEFAULT '',
  "expected_status" bigint NOT NULL DEFAULT 200,
  "threshold" double precision NOT NULL DEFAULT 0,
  "window_minutes" bigint NOT NULL DEFAULT 5,
  "interval_seconds" bigint NOT NULL DEFAULT 60,
  "enabled" boolean NOT NULL DEFAULT true,
  "notify_emails" text[] NOT NULL DEFAULT '{}',
  "notify_webhook" text NOT NULL DEFAULT '',
  "state" text NOT NULL DEFAULT 'unknown',
  "last_checked_at" timestamptz NULL,
  "last_error" text NOT NULL DEFAULT '',
  "consecutive_failures" bigint NOT NULL DEFAULT 0,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "monitors_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "monitors_emails_check" CHECK (COALESCE(array_length(notify_emails, 1), 0) <= 10),
  CONSTRAINT "monitors_expected_status_check" CHECK ((expected_status = 0) OR ((expected_status >= 100) AND (expected_status <= 599))),
  CONSTRAINT "monitors_interval_check" CHECK ((interval_seconds >= 30) AND (interval_seconds <= 3600)),
  CONSTRAINT "monitors_kind_check" CHECK (kind = ANY (ARRAY['http'::text, 'cpu'::text, 'memory'::text, 'disk'::text])),
  CONSTRAINT "monitors_name_check" CHECK ((length(name) >= 1) AND (length(name) <= 80)),
  CONSTRAINT "monitors_state_check" CHECK (state = ANY (ARRAY['unknown'::text, 'ok'::text, 'failing'::text])),
  CONSTRAINT "monitors_target_check" CHECK (((kind = 'http'::text) AND (target ~ '^https?://'::text) AND (length(target) <= 500)) OR ((kind <> 'http'::text) AND (target = ''::text))),
  CONSTRAINT "monitors_threshold_check" CHECK (((kind = 'http'::text) AND (threshold >= (0)::double precision) AND (threshold <= (60000)::double precision)) OR ((kind <> 'http'::text) AND (threshold >= (1)::double precision) AND (threshold <= (100)::double precision))),
  CONSTRAINT "monitors_webhook_check" CHECK ((notify_webhook = ''::text) OR ((notify_webhook ~ '^https://'::text) AND (length(notify_webhook) <= 500))),
  CONSTRAINT "monitors_window_check" CHECK ((window_minutes >= 1) AND (window_minutes <= 60))
);
-- Create index "monitors_app_name_key" to table: "monitors"
CREATE UNIQUE INDEX "monitors_app_name_key" ON "public"."monitors" ("app_id", "name");
-- Create index "monitors_due_idx" to table: "monitors"
CREATE INDEX "monitors_due_idx" ON "public"."monitors" ("last_checked_at") WHERE enabled;
-- Create index "monitors_id_team_key" to table: "monitors"
CREATE UNIQUE INDEX "monitors_id_team_key" ON "public"."monitors" ("id", "team_id");
-- Create index "monitors_team_idx" to table: "monitors"
CREATE INDEX "monitors_team_idx" ON "public"."monitors" ("team_id");
-- Create "monitor_incidents" table
CREATE TABLE "public"."monitor_incidents" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "monitor_id" text NOT NULL,
  "app_id" text NOT NULL,
  "opened_at" timestamptz NOT NULL DEFAULT now(),
  "resolved_at" timestamptz NULL,
  "reason" text NOT NULL DEFAULT '',
  "last_value" text NOT NULL DEFAULT '',
  "notified" boolean NOT NULL DEFAULT false,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "monitor_incidents_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "monitor_incidents_monitor_fk" FOREIGN KEY ("monitor_id", "team_id") REFERENCES "public"."monitors" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "monitor_incidents_app_idx" to table: "monitor_incidents"
CREATE INDEX "monitor_incidents_app_idx" ON "public"."monitor_incidents" ("app_id", "opened_at" DESC, "id" DESC);
-- Create index "monitor_incidents_open_key" to table: "monitor_incidents"
CREATE UNIQUE INDEX "monitor_incidents_open_key" ON "public"."monitor_incidents" ("monitor_id") WHERE (resolved_at IS NULL);
-- Create index "monitor_incidents_team_idx" to table: "monitor_incidents"
CREATE INDEX "monitor_incidents_team_idx" ON "public"."monitor_incidents" ("team_id");
