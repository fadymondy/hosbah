-- Create "credentials" table
CREATE TABLE "public"."credentials" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "provider" text NOT NULL,
  "name" text NOT NULL,
  "config" jsonb NOT NULL,
  "secret_sealed" text NOT NULL,
  "created_by" text NOT NULL,
  "last_validated_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "credentials_team_fk" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "credentials_provider_check" CHECK (provider = 'proxmox'::text)
);
-- Create index "credentials_team_name_key" to table: "credentials"
CREATE UNIQUE INDEX "credentials_team_name_key" ON "public"."credentials" ("team_id", "name");
-- Create "servers" table
CREATE TABLE "public"."servers" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "name" text NOT NULL,
  "provider" text NOT NULL,
  "kind" text NOT NULL,
  "status" text NOT NULL,
  "credential_id" text NULL,
  "provider_ref" text NULL,
  "hostname" text NULL,
  "ip_address" text NULL,
  "os" text NULL,
  "os_version" text NULL,
  "arch" text NULL,
  "kernel" text NULL,
  "agent_version" text NULL,
  "agent_public_key" text NULL,
  "ssh_public_key" text NULL,
  "ssh_private_key_sealed" text NULL,
  "facts" jsonb NULL,
  "checks" jsonb NULL,
  "metrics" jsonb NULL,
  "spec" jsonb NULL,
  "error" text NULL,
  "created_by" text NOT NULL,
  "last_seen_at" timestamptz NULL,
  "enrolled_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "servers_credential_fk" FOREIGN KEY ("credential_id") REFERENCES "public"."credentials" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "servers_team_fk" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "servers_kind_check" CHECK (kind = ANY (ARRAY['byo'::text, 'vm'::text, 'lxc'::text])),
  CONSTRAINT "servers_provider_check" CHECK (provider = ANY (ARRAY['byo'::text, 'proxmox'::text])),
  CONSTRAINT "servers_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'provisioning'::text, 'enrolled'::text, 'failed'::text]))
);
-- Create index "servers_id_team_key" to table: "servers"
CREATE UNIQUE INDEX "servers_id_team_key" ON "public"."servers" ("id", "team_id");
-- Create index "servers_team_idx" to table: "servers"
CREATE INDEX "servers_team_idx" ON "public"."servers" ("team_id", "created_at");
-- Create index "servers_team_name_key" to table: "servers"
CREATE UNIQUE INDEX "servers_team_name_key" ON "public"."servers" ("team_id", "name");
-- Create "agent_enrollments" table
CREATE TABLE "public"."agent_enrollments" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "token_hash" text NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "used_at" timestamptz NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "agent_enrollments_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "agent_enrollments_server_idx" to table: "agent_enrollments"
CREATE INDEX "agent_enrollments_server_idx" ON "public"."agent_enrollments" ("server_id", "created_at");
-- Create index "agent_enrollments_token_hash_key" to table: "agent_enrollments"
CREATE UNIQUE INDEX "agent_enrollments_token_hash_key" ON "public"."agent_enrollments" ("token_hash");
-- Create "operations" table
CREATE TABLE "public"."operations" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "kind" text NOT NULL,
  "executor" text NOT NULL,
  "params" text NOT NULL,
  "params_hash" text NOT NULL,
  "status" text NOT NULL,
  "created_by" text NOT NULL,
  "error" text NULL,
  "result" jsonb NULL,
  "exit_code" bigint NULL,
  "attempts" bigint NOT NULL DEFAULT 0,
  "last_seq" bigint NOT NULL DEFAULT 0,
  "timeout_seconds" bigint NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "sent_at" timestamptz NULL,
  "started_at" timestamptz NULL,
  "finished_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "operations_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "operations_executor_check" CHECK (executor = ANY (ARRAY['agent'::text, 'control_plane'::text])),
  CONSTRAINT "operations_params_object" CHECK (jsonb_typeof((params)::jsonb) = 'object'::text),
  CONSTRAINT "operations_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'sent'::text, 'running'::text, 'ok'::text, 'failed'::text, 'timeout'::text]))
);
-- Create index "operations_queue_idx" to table: "operations"
CREATE INDEX "operations_queue_idx" ON "public"."operations" ("status", "created_at") WHERE (status = ANY (ARRAY['pending'::text, 'sent'::text, 'running'::text]));
-- Create index "operations_server_idx" to table: "operations"
CREATE INDEX "operations_server_idx" ON "public"."operations" ("server_id", "created_at");
-- Create index "operations_team_idx" to table: "operations"
CREATE INDEX "operations_team_idx" ON "public"."operations" ("team_id", "created_at");
-- Create "operation_logs" table
CREATE TABLE "public"."operation_logs" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "operation_id" text NOT NULL,
  "seq" bigint NOT NULL,
  "stream" text NOT NULL,
  "line_sealed" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "operation_logs_operation_fk" FOREIGN KEY ("operation_id") REFERENCES "public"."operations" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "operation_logs_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "operation_logs_op_seq_key" to table: "operation_logs"
CREATE UNIQUE INDEX "operation_logs_op_seq_key" ON "public"."operation_logs" ("operation_id", "seq");
