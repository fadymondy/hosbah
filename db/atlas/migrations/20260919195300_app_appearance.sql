-- Modify "apps" table
ALTER TABLE "public"."apps" ADD CONSTRAINT "apps_color_check" CHECK ((color IS NULL) OR (color ~ '^#[0-9A-Fa-f]{6}$'::text)), ADD CONSTRAINT "apps_description_check" CHECK ((description IS NULL) OR ((char_length(description) >= 1) AND (char_length(description) <= 280))), ADD CONSTRAINT "apps_icon_check" CHECK ((icon IS NULL) OR (icon = ANY (ARRAY['server'::text, 'database'::text, 'globe'::text, 'cloud'::text, 'cpu'::text, 'box'::text, 'rocket'::text, 'shield'::text, 'code'::text, 'terminal'::text, 'layers'::text, 'zap'::text, 'flame'::text, 'leaf'::text, 'star'::text, 'heart'::text, 'briefcase'::text, 'home'::text, 'store'::text, 'graduation-cap'::text, 'building'::text, 'gamepad'::text, 'music'::text, 'camera'::text]))), ADD CONSTRAINT "apps_title_check" CHECK ((title IS NULL) OR ((char_length(title) >= 1) AND (char_length(title) <= 80))), ADD COLUMN "title" text NULL, ADD COLUMN "description" text NULL, ADD COLUMN "color" text NULL, ADD COLUMN "icon" text NULL;
-- Create "app_logos" table
CREATE TABLE "public"."app_logos" (
  "app_id" text NOT NULL,
  "team_id" text NOT NULL,
  "content_type" text NOT NULL,
  "data" bytea NOT NULL,
  "etag" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("app_id"),
  CONSTRAINT "app_logos_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_logos_content_type_check" CHECK (content_type = ANY (ARRAY['image/png'::text, 'image/jpeg'::text, 'image/webp'::text])),
  CONSTRAINT "app_logos_size_check" CHECK ((octet_length(data) >= 1) AND (octet_length(data) <= 524288))
);
-- Create index "app_logos_team_idx" to table: "app_logos"
CREATE INDEX "app_logos_team_idx" ON "public"."app_logos" ("team_id");
