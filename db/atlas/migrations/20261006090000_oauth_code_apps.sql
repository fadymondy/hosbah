-- MH-1194: an OAuth code can name several apps of the same team (web / api / mcp of one project).
ALTER TABLE "oauth_codes" ADD COLUMN "app_ids" text[] NOT NULL DEFAULT '{}';
