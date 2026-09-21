-- Create "app_buckets" table
CREATE TABLE "public"."app_buckets" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "service_id" text NOT NULL,
  "bucket" text NOT NULL,
  "access_key" text NOT NULL,
  "secret_sealed" text NOT NULL,
  "env_prefix" text NOT NULL DEFAULT 'S3',
  "status" text NOT NULL DEFAULT 'creating',
  "error" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_buckets_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_buckets_service_fk" FOREIGN KEY ("service_id", "team_id") REFERENCES "public"."data_services" ("id", "team_id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "app_buckets_access_key_check" CHECK (access_key ~ '^[A-Za-z0-9]{8,20}$'::text),
  CONSTRAINT "app_buckets_bucket_check" CHECK (bucket ~ '^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$'::text),
  CONSTRAINT "app_buckets_env_prefix_check" CHECK (env_prefix ~ '^[A-Z][A-Z0-9_]{0,20}$'::text),
  CONSTRAINT "app_buckets_sealed_check" CHECK (secret_sealed ~~ 'csv1:%'::text),
  CONSTRAINT "app_buckets_status_check" CHECK (status = ANY (ARRAY['creating'::text, 'ready'::text, 'failed'::text, 'removing'::text]))
);
-- Create index "app_buckets_app_idx" to table: "app_buckets"
CREATE INDEX "app_buckets_app_idx" ON "public"."app_buckets" ("app_id");
-- Create index "app_buckets_app_prefix_key" to table: "app_buckets"
CREATE UNIQUE INDEX "app_buckets_app_prefix_key" ON "public"."app_buckets" ("app_id", "env_prefix");
-- Create index "app_buckets_id_team_key" to table: "app_buckets"
CREATE UNIQUE INDEX "app_buckets_id_team_key" ON "public"."app_buckets" ("id", "team_id");
-- Create index "app_buckets_service_bucket_key" to table: "app_buckets"
CREATE UNIQUE INDEX "app_buckets_service_bucket_key" ON "public"."app_buckets" ("service_id", "bucket");
-- Create index "app_buckets_team_idx" to table: "app_buckets"
CREATE INDEX "app_buckets_team_idx" ON "public"."app_buckets" ("team_id");
-- Create "app_volumes" table
CREATE TABLE "public"."app_volumes" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "name" text NOT NULL,
  "mount_path" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_volumes_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_volumes_mount_path_check" CHECK (((length(mount_path) >= 2) AND (length(mount_path) <= 200)) AND (mount_path ~ '^/[A-Za-z0-9._/-]*$'::text)),
  CONSTRAINT "app_volumes_name_check" CHECK (name ~ '^[a-z][a-z0-9-]{0,30}$'::text)
);
-- Create index "app_volumes_app_mount_path_key" to table: "app_volumes"
CREATE UNIQUE INDEX "app_volumes_app_mount_path_key" ON "public"."app_volumes" ("app_id", "mount_path");
-- Create index "app_volumes_app_name_key" to table: "app_volumes"
CREATE UNIQUE INDEX "app_volumes_app_name_key" ON "public"."app_volumes" ("app_id", "name");
-- Create index "app_volumes_id_team_key" to table: "app_volumes"
CREATE UNIQUE INDEX "app_volumes_id_team_key" ON "public"."app_volumes" ("id", "team_id");
-- Create index "app_volumes_team_idx" to table: "app_volumes"
CREATE INDEX "app_volumes_team_idx" ON "public"."app_volumes" ("team_id");
