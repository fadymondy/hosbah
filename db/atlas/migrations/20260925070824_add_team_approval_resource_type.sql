-- Modify "approval_requests" table
ALTER TABLE "public"."approval_requests" DROP CONSTRAINT "approval_requests_resource_type_check", ADD CONSTRAINT "approval_requests_resource_type_check" CHECK (resource_type = ANY (ARRAY['app'::text, 'server'::text, 'database'::text, 'domain'::text, 'data_service'::text, 'backup'::text, 'invitation'::text, 'member'::text, 'team'::text]));
