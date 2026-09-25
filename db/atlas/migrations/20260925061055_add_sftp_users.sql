-- Create "sftp_users" table
CREATE TABLE "public"."sftp_users" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "username" text NOT NULL,
  "public_keys" text NOT NULL DEFAULT '',
  "password_sealed" text NULL,
  "root_dir" text NOT NULL,
  "read_only" boolean NOT NULL DEFAULT false,
  "status" text NOT NULL DEFAULT 'pending',
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "sftp_users_password_check" CHECK ((password_sealed IS NULL) OR (password_sealed ~~ 'csv1:%'::text)),
  CONSTRAINT "sftp_users_root_dir_check" CHECK (root_dir ~ '^/'::text),
  CONSTRAINT "sftp_users_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'active'::text, 'failed'::text, 'removing'::text])),
  CONSTRAINT "sftp_users_username_check" CHECK (username ~ '^[a-z_][a-z0-9_-]{0,31}$'::text)
);
-- Create index "sftp_users_server_username_key" to table: "sftp_users"
CREATE UNIQUE INDEX "sftp_users_server_username_key" ON "public"."sftp_users" ("server_id", "username");
-- Create index "sftp_users_team_idx" to table: "sftp_users"
CREATE INDEX "sftp_users_team_idx" ON "public"."sftp_users" ("team_id");
