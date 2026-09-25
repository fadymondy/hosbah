-- Modify "apps" table
ALTER TABLE "public"."apps" ADD CONSTRAINT "apps_resources_cpus_check" CHECK ((resources_cpus IS NULL) OR ((resources_cpus >= (0.05)::double precision) AND (resources_cpus <= (256)::double precision))), ADD CONSTRAINT "apps_resources_memory_check" CHECK ((resources_memory_mb IS NULL) OR ((resources_memory_mb >= 32) AND (resources_memory_mb <= 1048576))), ADD COLUMN "resources_memory_mb" bigint NULL, ADD COLUMN "resources_cpus" double precision NULL, ADD COLUMN "resources_processes" jsonb NOT NULL DEFAULT '{}';
-- Modify "data_services" table
ALTER TABLE "public"."data_services" ADD CONSTRAINT "data_services_resources_cpus_check" CHECK ((resources_cpus IS NULL) OR ((resources_cpus >= (0.05)::double precision) AND (resources_cpus <= (256)::double precision))), ADD CONSTRAINT "data_services_resources_memory_check" CHECK ((resources_memory_mb IS NULL) OR ((resources_memory_mb >= 32) AND (resources_memory_mb <= 1048576))), ADD COLUMN "resources_memory_mb" bigint NULL, ADD COLUMN "resources_cpus" double precision NULL;
-- Create "team_quotas" table
CREATE TABLE "public"."team_quotas" (
  "team_id" text NOT NULL,
  "max_servers" bigint NULL,
  "max_apps" bigint NULL,
  "max_data_services" bigint NULL,
  "updated_by" text NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("team_id"),
  CONSTRAINT "team_quotas_max_apps_check" CHECK ((max_apps IS NULL) OR ((max_apps >= 0) AND (max_apps <= 100000))),
  CONSTRAINT "team_quotas_max_data_services_check" CHECK ((max_data_services IS NULL) OR ((max_data_services >= 0) AND (max_data_services <= 100000))),
  CONSTRAINT "team_quotas_max_servers_check" CHECK ((max_servers IS NULL) OR ((max_servers >= 0) AND (max_servers <= 100000)))
);
