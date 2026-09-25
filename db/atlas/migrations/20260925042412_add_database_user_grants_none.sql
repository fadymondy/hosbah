-- Modify "database_users" table
ALTER TABLE "public"."database_users" DROP CONSTRAINT "database_users_grants_check", ADD CONSTRAINT "database_users_grants_check" CHECK (grants = ANY (ARRAY['all'::text, 'readwrite'::text, 'readonly'::text, 'none'::text]));
