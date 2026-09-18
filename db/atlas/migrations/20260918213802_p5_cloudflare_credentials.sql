-- Modify "credentials" table
ALTER TABLE "public"."credentials" DROP CONSTRAINT "credentials_provider_check", ADD CONSTRAINT "credentials_provider_check" CHECK (provider = ANY (ARRAY['proxmox'::text, 'cloudflare'::text]));
