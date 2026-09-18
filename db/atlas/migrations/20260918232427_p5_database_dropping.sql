-- Modify "database_users" table
ALTER TABLE "public"."database_users" DROP CONSTRAINT "database_users_status_check", ADD CONSTRAINT "database_users_status_check" CHECK (status = ANY (ARRAY['new'::text, 'creating'::text, 'ready'::text, 'failed'::text, 'dropping'::text]));
-- Modify "databases" table
ALTER TABLE "public"."databases" DROP CONSTRAINT "databases_status_check", ADD CONSTRAINT "databases_status_check" CHECK (status = ANY (ARRAY['new'::text, 'creating'::text, 'ready'::text, 'failed'::text, 'dropping'::text]));
