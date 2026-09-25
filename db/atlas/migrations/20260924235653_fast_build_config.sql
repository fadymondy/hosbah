-- Modify "app_deploy_configs" table
ALTER TABLE "public"."app_deploy_configs" ADD CONSTRAINT "app_deploy_configs_outputs_check" CHECK ((jsonb_typeof(outputs) = 'array'::text) AND (octet_length((outputs)::text) <= 4096)), ADD COLUMN "builder_image" text NOT NULL DEFAULT '', ADD COLUMN "outputs" jsonb NOT NULL DEFAULT '[]', ADD COLUMN "detected_preset" text NOT NULL DEFAULT '';
