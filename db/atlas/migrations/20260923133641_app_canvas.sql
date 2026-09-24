-- Create "app_canvas" table
CREATE TABLE "public"."app_canvas" (
  "app_id" text NOT NULL,
  "team_id" text NOT NULL,
  "layout" jsonb NOT NULL DEFAULT '{}',
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("app_id"),
  CONSTRAINT "app_canvas_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_canvas_layout_check" CHECK ((jsonb_typeof(layout) = 'object'::text) AND (octet_length((layout)::text) <= 65536))
);
-- Create index "app_canvas_team_idx" to table: "app_canvas"
CREATE INDEX "app_canvas_team_idx" ON "public"."app_canvas" ("team_id");
