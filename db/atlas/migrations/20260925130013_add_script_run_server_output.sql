-- Modify "script_run_servers" table
ALTER TABLE "public"."script_run_servers" ADD COLUMN "output" text NOT NULL DEFAULT '';
