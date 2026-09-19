-- Create "server_logos" table
CREATE TABLE "public"."server_logos" (
  "server_id" text NOT NULL,
  "team_id" text NOT NULL,
  "content_type" text NOT NULL,
  "data" bytea NOT NULL,
  "etag" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("server_id"),
  CONSTRAINT "server_logos_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "server_logos_content_type_check" CHECK (content_type = ANY (ARRAY['image/png'::text, 'image/jpeg'::text, 'image/webp'::text])),
  CONSTRAINT "server_logos_size_check" CHECK ((octet_length(data) >= 1) AND (octet_length(data) <= 524288))
);
-- Create index "server_logos_team_idx" to table: "server_logos"
CREATE INDEX "server_logos_team_idx" ON "public"."server_logos" ("team_id");
