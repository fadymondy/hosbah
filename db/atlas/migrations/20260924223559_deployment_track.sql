-- Modify "deployments" table
ALTER TABLE "public"."deployments" ADD CONSTRAINT "deployments_track_check" CHECK ((track IS NULL) OR (track = ANY (ARRAY['fast'::text, 'full'::text]))), ADD COLUMN "track" text NULL, ADD COLUMN "track_reason" text NULL;
