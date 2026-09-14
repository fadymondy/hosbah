-- First-boot setup for the local Postgres container (docker-entrypoint-initdb.d; runs as postgres).
-- cloudy_app is what the app, migrations and tests connect as: NOT superuser, NOT BYPASSRLS —
-- the row-level-security policies only bind such a role.
CREATE ROLE cloudy_app LOGIN PASSWORD 'cloudy_app_dev' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;

ALTER DATABASE cloudy OWNER TO cloudy_app;
ALTER SCHEMA public OWNER TO cloudy_app;

CREATE DATABASE cloudy_test OWNER cloudy_app;       -- integration tests (isolation sweep, RLS)
CREATE DATABASE cloudy_atlas_dev OWNER cloudy_app;  -- Atlas's scratch database for migrate diff

\connect cloudy_test
ALTER SCHEMA public OWNER TO cloudy_app;
\connect cloudy_atlas_dev
ALTER SCHEMA public OWNER TO cloudy_app;
