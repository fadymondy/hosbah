-- Modify "deployments" table
ALTER TABLE "public"."deployments" DROP CONSTRAINT "deployments_kind_check", ADD CONSTRAINT "deployments_kind_check" CHECK (kind = ANY (ARRAY['deploy'::text, 'rollback'::text, 'restart'::text, 'adopt'::text]));
