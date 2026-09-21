-- Create "schedules" table
CREATE TABLE "public"."schedules" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "name" text NOT NULL,
  "cron" text NOT NULL,
  "timezone" text NOT NULL DEFAULT 'UTC',
  "command" text NOT NULL,
  "timeout_seconds" bigint NOT NULL DEFAULT 300,
  "enabled" boolean NOT NULL DEFAULT true,
  "overlap" text NOT NULL DEFAULT 'skip',
  "next_run_at" timestamptz NULL,
  "last_run_at" timestamptz NULL,
  "last_status" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "schedules_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "schedules_command_check" CHECK (((length(command) >= 1) AND (length(command) <= 1000)) AND (command !~ '[\n\r]'::text)),
  CONSTRAINT "schedules_cron_check" CHECK ((length(cron) >= 9) AND (length(cron) <= 200)),
  CONSTRAINT "schedules_last_status_check" CHECK ((last_status IS NULL) OR (last_status = ANY (ARRAY['queued'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'timeout'::text, 'skipped'::text]))),
  CONSTRAINT "schedules_name_check" CHECK ((length(name) >= 1) AND (length(name) <= 80)),
  CONSTRAINT "schedules_overlap_check" CHECK (overlap = 'skip'::text),
  CONSTRAINT "schedules_timeout_check" CHECK ((timeout_seconds >= 10) AND (timeout_seconds <= 3600)),
  CONSTRAINT "schedules_timezone_check" CHECK ((length(timezone) >= 1) AND (length(timezone) <= 64))
);
-- Create index "schedules_app_name_key" to table: "schedules"
CREATE UNIQUE INDEX "schedules_app_name_key" ON "public"."schedules" ("app_id", "name");
-- Create index "schedules_due_idx" to table: "schedules"
CREATE INDEX "schedules_due_idx" ON "public"."schedules" ("next_run_at") WHERE enabled;
-- Create index "schedules_id_team_key" to table: "schedules"
CREATE UNIQUE INDEX "schedules_id_team_key" ON "public"."schedules" ("id", "team_id");
-- Create index "schedules_team_idx" to table: "schedules"
CREATE INDEX "schedules_team_idx" ON "public"."schedules" ("team_id");
-- Create "schedule_runs" table
CREATE TABLE "public"."schedule_runs" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "schedule_id" text NOT NULL,
  "app_id" text NOT NULL,
  "operation_id" text NULL,
  "trigger" text NOT NULL DEFAULT 'schedule',
  "status" text NOT NULL DEFAULT 'queued',
  "exit_code" bigint NULL,
  "started_at" timestamptz NOT NULL DEFAULT now(),
  "finished_at" timestamptz NULL,
  "duration_ms" bigint NULL,
  "output" text NOT NULL DEFAULT '',
  "error" text NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "schedule_runs_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "schedule_runs_operation_fk" FOREIGN KEY ("operation_id") REFERENCES "public"."operations" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "schedule_runs_schedule_fk" FOREIGN KEY ("schedule_id", "team_id") REFERENCES "public"."schedules" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "schedule_runs_status_check" CHECK (status = ANY (ARRAY['queued'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'timeout'::text, 'skipped'::text])),
  CONSTRAINT "schedule_runs_trigger_check" CHECK (trigger = ANY (ARRAY['schedule'::text, 'manual'::text]))
);
-- Create index "schedule_runs_active_idx" to table: "schedule_runs"
CREATE INDEX "schedule_runs_active_idx" ON "public"."schedule_runs" ("schedule_id") WHERE (status = ANY (ARRAY['queued'::text, 'running'::text]));
-- Create index "schedule_runs_operation_key" to table: "schedule_runs"
CREATE UNIQUE INDEX "schedule_runs_operation_key" ON "public"."schedule_runs" ("operation_id") WHERE (operation_id IS NOT NULL);
-- Create index "schedule_runs_schedule_idx" to table: "schedule_runs"
CREATE INDEX "schedule_runs_schedule_idx" ON "public"."schedule_runs" ("schedule_id", "started_at" DESC, "id" DESC);
-- Create index "schedule_runs_team_idx" to table: "schedule_runs"
CREATE INDEX "schedule_runs_team_idx" ON "public"."schedule_runs" ("team_id");
