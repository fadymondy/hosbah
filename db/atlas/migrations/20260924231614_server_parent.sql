-- Modify "servers" table
ALTER TABLE "public"."servers" ADD CONSTRAINT "servers_guest_type_check" CHECK ((guest_type IS NULL) OR (guest_type = ANY (ARRAY['lxc'::text, 'qemu'::text]))), ADD COLUMN "parent_server_id" text NULL, ADD COLUMN "guest_vmid" integer NULL, ADD COLUMN "guest_type" text NULL, ADD COLUMN "guest_link_manual" boolean NOT NULL DEFAULT false, ADD CONSTRAINT "servers_parent_fk" FOREIGN KEY ("parent_server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE SET NULL;
-- Create index "servers_parent_idx" to table: "servers"
CREATE INDEX "servers_parent_idx" ON "public"."servers" ("parent_server_id");
