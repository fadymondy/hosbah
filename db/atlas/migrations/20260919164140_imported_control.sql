-- Modify "domains" table
ALTER TABLE "public"."domains" ADD CONSTRAINT "domains_upstream_check" CHECK ((upstream IS NULL) OR (length(upstream) <= 300)), ADD COLUMN "upstream" text NULL;
