-- Create "backup_schedules" table
CREATE TABLE "public"."backup_schedules" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "data_service_id" text NOT NULL,
  "cron" text NOT NULL,
  "retention" bigint NOT NULL DEFAULT 7,
  "target" text NOT NULL DEFAULT 'local',
  "restore_test" boolean NOT NULL DEFAULT true,
  "paused" boolean NOT NULL DEFAULT false,
  "last_run_at" timestamptz NULL,
  "next_run_at" timestamptz NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "backup_schedules_retention_check" CHECK ((retention >= 1) AND (retention <= 3650)),
  CONSTRAINT "backup_schedules_target_check" CHECK (target = ANY (ARRAY['local'::text, 'minio'::text, 's3'::text]))
);
-- Create index "backup_schedules_due_idx" to table: "backup_schedules"
CREATE INDEX "backup_schedules_due_idx" ON "public"."backup_schedules" ("next_run_at") WHERE (paused = false);
-- Create index "backup_schedules_id_team_key" to table: "backup_schedules"
CREATE UNIQUE INDEX "backup_schedules_id_team_key" ON "public"."backup_schedules" ("id", "team_id");
-- Create index "backup_schedules_service_key" to table: "backup_schedules"
CREATE UNIQUE INDEX "backup_schedules_service_key" ON "public"."backup_schedules" ("data_service_id");
-- Create "backups" table
CREATE TABLE "public"."backups" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "data_service_id" text NOT NULL,
  "schedule_id" text NULL,
  "kind" text NOT NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "target" text NOT NULL DEFAULT 'local',
  "snapshot_id" text NULL,
  "size_bytes" bigint NULL,
  "error" text NULL,
  "restore_status" text NULL,
  "restore_error" text NULL,
  "created_by" text NOT NULL,
  "started_at" timestamptz NULL,
  "taken_at" timestamptz NULL,
  "restore_tested_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "backups_kind_check" CHECK (kind = ANY (ARRAY['manual'::text, 'scheduled'::text])),
  CONSTRAINT "backups_restore_status_check" CHECK ((restore_status IS NULL) OR (restore_status = ANY (ARRAY['untested'::text, 'testing'::text, 'pass'::text, 'fail'::text]))),
  CONSTRAINT "backups_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'running'::text, 'succeeded'::text, 'failed'::text])),
  CONSTRAINT "backups_target_check" CHECK (target = ANY (ARRAY['local'::text, 'minio'::text, 's3'::text]))
);
-- Create index "backups_id_team_key" to table: "backups"
CREATE UNIQUE INDEX "backups_id_team_key" ON "public"."backups" ("id", "team_id");
-- Create index "backups_schedule_idx" to table: "backups"
CREATE INDEX "backups_schedule_idx" ON "public"."backups" ("schedule_id", "created_at");
-- Create index "backups_service_idx" to table: "backups"
CREATE INDEX "backups_service_idx" ON "public"."backups" ("data_service_id", "created_at");
-- Create "data_services" table
CREATE TABLE "public"."data_services" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "name" text NOT NULL,
  "kind" text NOT NULL,
  "version" text NOT NULL,
  "status" text NOT NULL DEFAULT 'new',
  "container" text NULL,
  "volume" text NULL,
  "host" text NULL,
  "port" bigint NULL,
  "root_sealed" text NOT NULL,
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "data_services_kind_check" CHECK (kind = ANY (ARRAY['postgres'::text, 'mysql'::text, 'redis'::text])),
  CONSTRAINT "data_services_name_check" CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,39}$'::text),
  CONSTRAINT "data_services_port_check" CHECK ((port IS NULL) OR ((port >= 1) AND (port <= 65535))),
  CONSTRAINT "data_services_sealed_check" CHECK (root_sealed ~~ 'csv1:%'::text),
  CONSTRAINT "data_services_status_check" CHECK (status = ANY (ARRAY['new'::text, 'provisioning'::text, 'running'::text, 'failed'::text, 'stopped'::text]))
);
-- Create index "data_services_id_team_key" to table: "data_services"
CREATE UNIQUE INDEX "data_services_id_team_key" ON "public"."data_services" ("id", "team_id");
-- Create index "data_services_server_idx" to table: "data_services"
CREATE INDEX "data_services_server_idx" ON "public"."data_services" ("server_id");
-- Create index "data_services_team_name_key" to table: "data_services"
CREATE UNIQUE INDEX "data_services_team_name_key" ON "public"."data_services" ("team_id", "name");
-- Create "database_users" table
CREATE TABLE "public"."database_users" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "data_service_id" text NOT NULL,
  "database_id" text NULL,
  "name" text NOT NULL,
  "grants" text NOT NULL DEFAULT 'all',
  "password_sealed" text NOT NULL,
  "status" text NOT NULL DEFAULT 'new',
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "database_users_grants_check" CHECK (grants = ANY (ARRAY['all'::text, 'readwrite'::text, 'readonly'::text])),
  CONSTRAINT "database_users_name_check" CHECK (name ~ '^[a-z_][a-z0-9_]{0,62}$'::text),
  CONSTRAINT "database_users_sealed_check" CHECK (password_sealed ~~ 'csv1:%'::text),
  CONSTRAINT "database_users_status_check" CHECK (status = ANY (ARRAY['new'::text, 'creating'::text, 'ready'::text, 'failed'::text]))
);
-- Create index "database_users_id_team_key" to table: "database_users"
CREATE UNIQUE INDEX "database_users_id_team_key" ON "public"."database_users" ("id", "team_id");
-- Create index "database_users_service_idx" to table: "database_users"
CREATE INDEX "database_users_service_idx" ON "public"."database_users" ("data_service_id");
-- Create index "database_users_service_name_key" to table: "database_users"
CREATE UNIQUE INDEX "database_users_service_name_key" ON "public"."database_users" ("data_service_id", "name");
-- Create "databases" table
CREATE TABLE "public"."databases" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "data_service_id" text NOT NULL,
  "name" text NOT NULL,
  "status" text NOT NULL DEFAULT 'new',
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "databases_name_check" CHECK (name ~ '^[a-z_][a-z0-9_]{0,62}$'::text),
  CONSTRAINT "databases_status_check" CHECK (status = ANY (ARRAY['new'::text, 'creating'::text, 'ready'::text, 'failed'::text]))
);
-- Create index "databases_id_team_key" to table: "databases"
CREATE UNIQUE INDEX "databases_id_team_key" ON "public"."databases" ("id", "team_id");
-- Create index "databases_service_idx" to table: "databases"
CREATE INDEX "databases_service_idx" ON "public"."databases" ("data_service_id");
-- Create index "databases_service_name_key" to table: "databases"
CREATE UNIQUE INDEX "databases_service_name_key" ON "public"."databases" ("data_service_id", "name");
