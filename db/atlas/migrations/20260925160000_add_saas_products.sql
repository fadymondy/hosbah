-- Modify "approval_requests" table
ALTER TABLE "public"."approval_requests" DROP CONSTRAINT "approval_requests_resource_type_check", ADD CONSTRAINT "approval_requests_resource_type_check" CHECK (resource_type = ANY (ARRAY['app'::text, 'server'::text, 'database'::text, 'domain'::text, 'data_service'::text, 'backup'::text, 'invitation'::text, 'member'::text, 'team'::text, 'log_drain'::text, 'script'::text, 'script_run'::text, 'service'::text, 'product'::text, 'product_instance'::text]));
-- Create "product_instances" table
CREATE TABLE "public"."product_instances" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "product_id" text NOT NULL,
  "app_id" text NULL,
  "customer_ref" text NOT NULL,
  "name" text NOT NULL,
  "hostname" text NULL,
  "database_id" text NULL,
  "status" text NOT NULL DEFAULT 'provisioning',
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "suspended_at" timestamptz NULL,
  "destroyed_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "product_instances_product_idx" to table: "product_instances"
CREATE INDEX "product_instances_product_idx" ON "public"."product_instances" ("product_id");
-- Create index "product_instances_product_name_key" to table: "product_instances"
CREATE UNIQUE INDEX "product_instances_product_name_key" ON "public"."product_instances" ("product_id", "name");
-- Create index "product_instances_team_idx" to table: "product_instances"
CREATE INDEX "product_instances_team_idx" ON "public"."product_instances" ("team_id", "created_at");
-- Create "product_keys" table
CREATE TABLE "public"."product_keys" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "product_id" text NOT NULL,
  "name" text NOT NULL,
  "key_hash" text NOT NULL,
  "webhook_url" text NULL,
  "webhook_secret_sealed" text NULL,
  "created_by" text NOT NULL,
  "last_used_at" timestamptz NULL,
  "revoked_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "product_keys_key_hash_key" to table: "product_keys"
CREATE UNIQUE INDEX "product_keys_key_hash_key" ON "public"."product_keys" ("key_hash");
-- Create index "product_keys_product_idx" to table: "product_keys"
CREATE INDEX "product_keys_product_idx" ON "public"."product_keys" ("product_id");
-- Create index "product_keys_team_idx" to table: "product_keys"
CREATE INDEX "product_keys_team_idx" ON "public"."product_keys" ("team_id", "created_at");
-- Create "product_upgrades" table
CREATE TABLE "public"."product_upgrades" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "product_id" text NOT NULL,
  "ref" text NOT NULL,
  "status" text NOT NULL DEFAULT 'running',
  "total" bigint NOT NULL DEFAULT 0,
  "completed" bigint NOT NULL DEFAULT 0,
  "failed" bigint NOT NULL DEFAULT 0,
  "current_instance_id" text NULL,
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "finished_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "product_upgrades_product_idx" to table: "product_upgrades"
CREATE INDEX "product_upgrades_product_idx" ON "public"."product_upgrades" ("product_id", "created_at");
-- Create index "product_upgrades_team_idx" to table: "product_upgrades"
CREATE INDEX "product_upgrades_team_idx" ON "public"."product_upgrades" ("team_id", "created_at");
-- Create "products" table
CREATE TABLE "public"."products" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "name" text NOT NULL,
  "title" text NULL,
  "description" text NULL,
  "source_kind" text NOT NULL DEFAULT 'app',
  "source_ref" text NULL,
  "repo_url" text NOT NULL,
  "branch" text NOT NULL DEFAULT 'main',
  "runtime" text NULL,
  "port" bigint NOT NULL DEFAULT 8080,
  "healthcheck_path" text NOT NULL DEFAULT '/',
  "healthcheck_timeout" bigint NOT NULL DEFAULT 60,
  "resources_memory_mb" bigint NULL,
  "resources_cpus" double precision NULL,
  "domain_suffix" text NULL,
  "env_defaults" jsonb NOT NULL DEFAULT '{}',
  "status" text NOT NULL DEFAULT 'active',
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "products_team_idx" to table: "products"
CREATE INDEX "products_team_idx" ON "public"."products" ("team_id", "created_at");
-- Create index "products_team_name_key" to table: "products"
CREATE UNIQUE INDEX "products_team_name_key" ON "public"."products" ("team_id", "name");
