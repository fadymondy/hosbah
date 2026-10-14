#!/bin/sh
# First boot of the self-host database (docker-entrypoint-initdb.d, runs as the postgres superuser).
# The app connects as hosbah_app: not superuser, not BYPASSRLS, so row-level security binds it.
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v pw="$HOSBAH_APP_DB_PASSWORD" -v db="$POSTGRES_DB" <<'SQL'
CREATE ROLE hosbah_app LOGIN PASSWORD :'pw' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
ALTER DATABASE :"db" OWNER TO hosbah_app;
ALTER SCHEMA public OWNER TO hosbah_app;
SQL
