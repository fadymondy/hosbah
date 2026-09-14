# Cloudy

Server management and app hosting on Proxmox, Docker and the major clouds — one-click Go, Python,
Node and PHP deploys. **Phase P0: in development, local only.** Plan:
`fadymondy.com-v2/.setup/plans/cloudy.md`. Clean-room: no code or templates from Eddy (AGPL).

P0 is the multi-tenant foundation: teams, members, invitations, default-deny authorization,
row-level security, envelope encryption, audit, sign-in with TOTP 2FA, the user and admin MCP
servers, and Cloudy's own issue tracker fed by "Report a problem". No servers or deploys yet (P1+).

## Ports (local)

| What | Port |
|---|---|
| API (Go) | **8710** — REST `/api`, docs `/docs`, MCP `/api/mcp` + `/api/admin/mcp` |
| Web (Next.js) | **3710** — proxies `/api/*` to 8710 |
| Postgres (container `cloudy-postgres`) | **55491** — databases `cloudy`, `cloudy_test`, `cloudy_atlas_dev` |

## Run it

```sh
# 1. Database (first time: the volume is external so a `down` never deletes data)
docker volume create cloudy_pgdata
docker compose up -d

# 2. Config
cp .env.example .env     # then set AUTH_SECRET (openssl rand -hex 32) and CLOUDY_KEK (openssl rand -base64 32)

# 3. Schema, then the API
togo generate            # sqlc -> gqlgen -> OpenAPI export (the integration gate)
togo migrate             # = go run ./cmd/migrate — applies db/atlas/migrations through the ledger
go run ./cmd/api         # :8710

# 4. Web
cd web && npm install && npm run dev    # :3710

# 5. Platform admin (to use /admin and the admin MCP): sign up in the web app first, then
go run ./cmd/admin promote you@example.com        # then sign out and in again
go run ./cmd/admin mint-admin-token you@example.com triage   # or mint one in /admin
```

Tests (the integration ones use `TEST_DATABASE_URL` and migrate it themselves):

```sh
go test -count=1 ./...                 # includes the team-B isolation sweep and the RLS tests
cd web && npm run grid:check && npm run feedback:check && npm run build
```

## How tenancy works

- `team_id` on every tenant row; team routes are path-scoped: `/api/teams/{team}/...` (id or slug).
  A team you are not in answers **404**, same as one that does not exist.
- Generated CRUD (`internal/rest`, `/graphql`) is **not mounted** — it has no notion of a team.
  Tenant data is only reachable through `internal/api`'s hand-written handlers.
- Every tenant query runs in `tenancy.Tx`, which pins `app.user_id` / `app.team_id` with
  `set_config(..., true)` (= `SET LOCAL`), and Postgres **row-level security** (FORCE, non-superuser
  role) backs up every `WHERE team_id`. Policies: `db/atlas/migrations/*_row_level_security.sql`.
- Default-deny route tiers (`internal/server/authorization.go`): blocked, public, token (MCP),
  member, and platform admin as the default for anything unlisted.
- Team roles `owner`, `admin`, `deployer`, `viewer` via togo `authz` (`internal/tenancy/roles.go`).
- `internal/crypto`: `CLOUDY_KEK` wraps a per-team DEK (AES-256-GCM, bound to the team id); values
  are sealed with the team DEK, bound to team + field.
- Audit: togo audit's `activity_log` + `team_id`, written in the same transaction as the change,
  append-only under RLS.

## MCP

| Endpoint | Tokens | Tools |
|---|---|---|
| `POST /api/mcp` | team-bound `cld_…` (mint in *MCP tokens*) | `whoami`, `get_team`, `list_members`, `list_invitations`, `list_audit`, `list_mcp_tokens` (read) · `invite_member` (write) · `revoke_invitation`, `remove_member` (destructive, `confirm` = email) |
| `POST /api/admin/mcp` | platform-admin `cla_…` | `list_issues`, `get_issue`, `create_issue`, `update_issue`, `comment_issue`, `start_issue`, `finish_issue`, `list_teams`, `get_team`, `list_users`, `get_user`, `set_platform_admin` |

Every tool declares a scope (`internal/mcp/scopes_test.go` fails otherwise); a token without it gets
an in-band `isError`. Only SHA-256 hashes of tokens are stored.

```json
{ "mcpServers": { "cloudy": { "type": "http", "url": "http://localhost:8710/api/mcp",
  "headers": { "Authorization": "Bearer cld_..." } } } }
```

## Migrations (the ledger)

Desired state = `internal/db/schema/*.sql` (togo fragments, converted to Postgres). Versioned files
in `db/atlas/migrations` (+ committed `atlas.sum`) are the only thing applied; `schema_migrations`
records each with its checksum and an edited, already-applied file stops the run.

```sh
togo make:resource Thing name:string ...    # generator-first; then convert the fragment to Postgres
togo migrate:diff add_thing                  # atlas migrate diff --env local
atlas migrate new <name> --dir file://db/atlas/migrations && atlas migrate hash   # hand-written (RLS)
togo migrate
```

## Design

The fadymondy.com grid, synced — never edited here: `web/app/grid/` (`npm run grid:sync` /
`grid:check`). Brand `data-brand="cloudy"` (body #2E6F9E, accent #C9A227) lives in
`web/app/grid/brand-cloudy.css` until fadymondy.com-v2's `marks.ts` carries `cloudy`.
The Feedback SDK is the generated copy in `web/lib/feedback/` (`npm run feedback:sync` / `feedback:check`).
en + ar (RTL), light + dark.
