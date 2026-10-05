-- MH-1141: panel accounts (users.panel_server_id) and their sessions.
ALTER TABLE "users" ADD COLUMN "panel_server_id" text NULL, ADD CONSTRAINT "users_panel_server_id_fkey" FOREIGN KEY ("panel_server_id") REFERENCES "servers" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
CREATE INDEX "users_panel_server_idx" ON "users" ("panel_server_id") WHERE (panel_server_id IS NOT NULL);
CREATE TABLE "panel_sessions" (
  "token_hash" text NOT NULL,
  "user_id" text NOT NULL,
  "server_id" text NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("token_hash"),
  CONSTRAINT "panel_sessions_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "panel_sessions_server_id_fkey" FOREIGN KEY ("server_id") REFERENCES "servers" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX "panel_sessions_user_idx" ON "panel_sessions" ("user_id");
