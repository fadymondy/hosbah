# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Cloudy is server management and app hosting (Proxmox, Docker, clouds) built on the **togo** framework: Go API (chi + Huma REST/OpenAPI, gqlgen, sqlc, Atlas, pgx/Postgres) with a Next.js frontend in `web/`. README.md is the detailed spec for each phase (P0 tenancy, P1 servers and agent, P2 deploys, P3 databases and backups). HOW-TO-TEST.md has the manual click-paths. Clean-room project: never copy code or templates from Eddy (AGPL).

## Commands

```sh
docker compose up -d                 # Postgres on :55491 (dbs cloudy, cloudy_test, cloudy_atlas_dev); volume cloudy_pgdata is external
togo generate                        # sqlc -> gqlgen -> atlas diff -> OpenAPI export (compiles everything; the integration gate)
togo migrate                         # = go run ./cmd/migrate; applies db/atlas/migrations through the ledger
go run ./cmd/api                     # API on :8710 (REST /api, docs /docs, MCP /api/mcp, /api/admin/mcp)
cd web && npm run dev                # web on :3710, proxies /api/* to :8710
make agent                           # static cloudy-agent binaries into bin/agent (served to enrolling servers)

go test -count=1 ./...                               # integration tests use TEST_DATABASE_URL and migrate it themselves
go test -count=1 -run TestName ./internal/agent/     # single test
cd web && npm run typecheck && npm run grid:check && npm run feedback:check && npm run build

go run ./cmd/admin promote you@example.com           # platform admin (sign up in the web app first)
```

Proxmox live tests only run with `CLOUDY_PROXMOX_LIVE=1` plus `PVE_*` env vars (see README). By default they run against an in-process fake PVE API.

## togo conventions (from .claude/rules/conventions.md)

- **Generator-first**: add entities with `togo make:resource Thing field:type ...`. `togo.resources.yaml` is the source of truth. Fragments (models, queries, schema, resolvers, handlers, pages) can be edited by hand. `*.gen.go` registries and `internal/**/gen/` (sqlc/gqlgen output) must never be hand-edited.
- Config comes from `togo.yaml` + `.env`; never hard-code connections, URLs or endpoints.
- Every resource is exposed over both GraphQL and REST/OpenAPI.

## Architecture

- **Tenancy is the core invariant.** Tenant data is reachable only through the hand-written handlers in `internal/api` under `/api/teams/{team}/...`. Generated CRUD (`internal/rest`, `/graphql`) is **not mounted** because it has no notion of a team. A team you are not in returns 404.
- Every tenant query runs in `tenancy.Tx`, which sets `app.user_id`/`app.team_id` with `SET LOCAL`. Postgres FORCE row-level security backs up every `WHERE team_id`. Policies live in `db/atlas/migrations/*_row_level_security.sql`. Extra RLS contexts: `app.server_id` (authenticated agent) and `app.system` (dispatcher, sweeper and provisioning worker only).
- Route authorization is default-deny, in tiers (blocked, public, token/MCP, member, platform admin by default): `internal/server/authorization.go`. Team roles are owner/admin/deployer/viewer (`internal/tenancy/roles.go`).
- **Crypto** (`internal/crypto`): `CLOUDY_KEK` wraps a per-team DEK. Secrets and logs are sealed with the team DEK, bound to team and field. Deploy env values are re-sealed to the target agent's key, so `operations.params` never holds plaintext.
- **Agent** (`cmd/agent`, `internal/agent`): enrols with a one-time token, then dials out to `/agent/ws`. Operations are typed (`internal/agentproto`, `internal/ops`), ed25519-signed by the control plane and verified against the key pinned at enrolment. The queue is the `operations` table (pending → sent → running → ok/failed/timeout, `FOR UPDATE SKIP LOCKED`, idempotent by `op_id`, per-op `seq` replayed on reconnect). New operation kinds must be added to the typed protocol on both sides.
- **Deploys** (`internal/deploy`): Dockerfile or Railpack build → release → web on an ephemeral port → health gate → workers → Caddy upstream switch → drain. The agent owns the Caddy config through its admin API.
- **Data services and backups**: `internal/dbsvc` (databases, users, restic backups).
- **MCP** (`internal/mcp`): team server `/api/mcp` (`cld_…` tokens) and admin server `/api/admin/mcp` (`cla_…`). Every tool must declare a scope; `internal/mcp/scopes_test.go` enforces this.
- Logs reach the browser through team-scoped SSE endpoints, never through the global `/events`.

## Migrations

The desired state is `internal/db/schema/*.sql`. Only the versioned files in `db/atlas/migrations` (with the committed `atlas.sum`) are applied. `schema_migrations` records checksums, so an edited, already-applied migration stops the run. Always add a new file instead.

```sh
togo migrate:diff add_thing
atlas migrate new <name> --dir file://db/atlas/migrations && atlas migrate hash   # hand-written, e.g. RLS policies
```

## Web

- `web/app/grid/` and `web/lib/feedback/` are synced copies from fadymondy.com-v2. Never edit them here; use `npm run grid:sync` / `feedback:sync`.
- Brand CSS: `web/app/grid/brand-cloudy.css`.
- Every UI change must work in en + ar (RTL) and in light + dark.
