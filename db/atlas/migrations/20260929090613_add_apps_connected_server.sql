-- Modify "apps" table
ALTER TABLE "public"."apps" ADD COLUMN "connected_server_id" text NULL, ADD CONSTRAINT "apps_connected_server_fk" FOREIGN KEY ("connected_server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE SET NULL;
-- Create index "apps_connected_server_idx" to table: "apps"
CREATE INDEX "apps_connected_server_idx" ON "public"."apps" ("connected_server_id") WHERE (connected_server_id IS NOT NULL);
