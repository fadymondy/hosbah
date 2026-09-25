-- Create "app_apm_settings" table
CREATE TABLE "public"."app_apm_settings" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "app_id" text NOT NULL,
  "team_id" text NOT NULL,
  "enabled" boolean NOT NULL DEFAULT false,
  "key_hash" text NULL,
  "key_sealed" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_apm_settings_app_id_key" UNIQUE ("app_id")
);
-- Create index "app_apm_settings_key_hash_key" to table: "app_apm_settings"
CREATE UNIQUE INDEX "app_apm_settings_key_hash_key" ON "public"."app_apm_settings" ("key_hash") WHERE (key_hash IS NOT NULL);
-- Create index "app_apm_settings_team_idx" to table: "app_apm_settings"
CREATE INDEX "app_apm_settings_team_idx" ON "public"."app_apm_settings" ("team_id");
-- Create "apm_spans" table
CREATE TABLE "public"."apm_spans" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "trace_id" text NOT NULL,
  "span_id" text NOT NULL,
  "parent_span_id" text NULL,
  "name" text NOT NULL,
  "kind" text NOT NULL,
  "service" text NOT NULL,
  "http_method" text NULL,
  "http_route" text NULL,
  "http_status" integer NULL,
  "duration_ms" real NOT NULL,
  "error" boolean NOT NULL DEFAULT false,
  "start_time" timestamptz NOT NULL,
  "attrs" jsonb NOT NULL DEFAULT '{}',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "apm_spans_kind_check" CHECK (kind = ANY (ARRAY['server'::text, 'client'::text, 'internal'::text, 'producer'::text, 'consumer'::text, 'root'::text]))
);
-- Create index "apm_spans_app_created_idx" to table: "apm_spans"
CREATE INDEX "apm_spans_app_created_idx" ON "public"."apm_spans" ("team_id", "app_id", "created_at");
-- Create index "apm_spans_app_route_idx" to table: "apm_spans"
CREATE INDEX "apm_spans_app_route_idx" ON "public"."apm_spans" ("team_id", "app_id", "http_method", "http_route", "start_time");
-- Create index "apm_spans_created_idx" to table: "apm_spans"
CREATE INDEX "apm_spans_created_idx" ON "public"."apm_spans" ("created_at");
-- Create index "apm_spans_trace_idx" to table: "apm_spans"
CREATE INDEX "apm_spans_trace_idx" ON "public"."apm_spans" ("team_id", "app_id", "trace_id");
-- Create "apm_issues" table
CREATE TABLE "public"."apm_issues" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "fingerprint" text NOT NULL,
  "type" text NOT NULL,
  "message" text NOT NULL,
  "first_seen" timestamptz NOT NULL,
  "last_seen" timestamptz NOT NULL,
  "count" integer NOT NULL DEFAULT 1,
  "status" text NOT NULL DEFAULT 'open',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "apm_issues_app_fingerprint_key" UNIQUE ("app_id", "fingerprint"),
  CONSTRAINT "apm_issues_status_check" CHECK (status = ANY (ARRAY['open'::text, 'resolved'::text, 'ignored'::text]))
);
-- Create index "apm_issues_app_last_seen_idx" to table: "apm_issues"
CREATE INDEX "apm_issues_app_last_seen_idx" ON "public"."apm_issues" ("team_id", "app_id", "last_seen" DESC);
-- Create "apm_exception_events" table
CREATE TABLE "public"."apm_exception_events" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "issue_id" text NOT NULL,
  "trace_id" text NULL,
  "span_id" text NULL,
  "type" text NOT NULL,
  "message" text NOT NULL,
  "stacktrace" text NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "apm_exception_events_stacktrace_check" CHECK (length(stacktrace) <= 16384)
);
-- Create index "apm_exception_events_created_idx" to table: "apm_exception_events"
CREATE INDEX "apm_exception_events_created_idx" ON "public"."apm_exception_events" ("created_at");
-- Create index "apm_exception_events_issue_idx" to table: "apm_exception_events"
CREATE INDEX "apm_exception_events_issue_idx" ON "public"."apm_exception_events" ("team_id", "app_id", "issue_id", "created_at" DESC);
