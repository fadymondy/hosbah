-- Modify "data_services" table
ALTER TABLE "public"."data_services" DROP CONSTRAINT "data_services_kind_check", ADD CONSTRAINT "data_services_kind_check" CHECK (kind = ANY (ARRAY['postgres'::text, 'mysql'::text, 'redis'::text, 'minio'::text]));
