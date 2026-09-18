# Cloudy

Server management and app hosting on Proxmox, Docker and the major clouds — one-click Go, Python,
Node and PHP deploys. **Phase P1: in development, local only.** Plan:
`fadymondy.com-v2/.setup/plans/cloudy.md`. Clean-room: no code or templates from Eddy (AGPL).

P0 is the multi-tenant foundation: teams, members, invitations, default-deny authorization,
row-level security, envelope encryption, audit, sign-in with TOTP 2FA, the user and admin MCP
servers, and Cloudy's own issue tracker fed by "Report a problem".

P2 adds one-click app deploys (below). P1 added servers: the `cloudy-agent`, bring-your-own install with a one-liner, signed typed
operations with live logs, and the Proxmox driver (tested against a fake PVE API only). No deploys
yet (P2).

## Servers and the agent (P1)

- **Agent** (`cmd/agent`, `internal/agent`): static Go binary under systemd. It enrols once with a
  one-time token that lasts 15 minutes and is stored hashed, generating its own ed25519 key. After
  that it **dials out** to `/agent/ws` and answers a signed challenge. It sends heartbeats with
  CPU, memory, disk, load and uptime every 15 s.
- **Operations** (`internal/agentproto`, `internal/ops`): typed only. The kinds are `system.info`,
  `docker.check`, `provision.step` (`packages.base`, `docker.install`, `swap.ensure`, all
  idempotent) and `agent.update`.
  - The control plane signs `op_id, server_id, kind, params hash, exp, timeout` with ed25519. The
    key is derived from the KEK with HKDF.
  - The agent verifies against the key it pinned at enrolment. It refuses tampered, unsigned,
    expired, misaddressed or unknown-kind operations.
  - The queue is the `operations` table: pending → sent → running → ok/failed/timeout. Dispatch
    uses `FOR UPDATE SKIP LOCKED`, and operations are idempotent by `op_id`.
  - Every event carries a per-operation `seq`. After a reconnect the server sends the last `seq`
    it stored, and the agent replays the rest.
- **Logs**: `operation_logs` are sealed with the team key and append-only. The browser gets them
  through team-scoped SSE: `/api/teams/{team}/servers/{server}/operations/{op}/stream` and
  `/servers/{server}/stream`. Never through the global `/events`.
- **Self-update**: `agent.update` puts the version, URL and SHA-256 inside the signed params. The
  agent downloads the binary, verifies it, swaps it in and re-execs. The only release channel so
  far is this control plane's `bin/agent`.
- **RLS**: there are two new contexts. `app.server_id` is set once an agent has authenticated, and
  it only sees its own server. `app.system` is used only by the dispatcher, the timeout sweeper and
  the provisioning worker.

### Try the bring-your-own path locally

```sh
# build the agent the control plane serves (make agent, or without make:)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X main.version=dev" -o bin/agent/cloudy-agent-linux-amd64 ./cmd/agent
echo dev > bin/agent/VERSION
# in .env: CLOUDY_AGENT_URL=http://host.docker.internal:8710   (then start the API)

docker build -t cloudy-dev-target deploy/dev-target      # Ubuntu 24.04 with systemd
docker run -d --name cloudy-target --privileged --cgroupns=host \
  -v /sys/fs/cgroup:/sys/fs/cgroup:rw --add-host=host.docker.internal:host-gateway cloudy-dev-target
# Servers → Add server → "A server I already have" → copy the root command, then:
docker exec cloudy-target sh -c 'curl -fsSL http://host.docker.internal:8710/install/cle_… | sh'
```

### Connect a server over SSH

**Servers → Add server → Connect with SSH**: an address, root (or a passwordless-sudo user) and a
password or private key. The control-plane operation `server.connect` (`internal/provisioning/connect.go`)
logs in once, probes the OS and whether it is plain Linux, Docker or Proxmox VE, installs Cloudy's
own per-server key for root, installs the agent and waits for it to enrol. The login is sealed with
the team key only while the operation runs and is forgotten afterwards, whatever the outcome. On a
Proxmox node it can also create a `cloudy@pve` API token and save it as a team credential
(`register_proxmox`, on by default). The agent's facts carry the platform and what already runs
(containers, well-known systemd units, Proxmox guests), and the server page lists them. Ubuntu, Debian
and the RHEL family (CentOS Stream, RHEL, Rocky and Alma 9–10, via dnf) are supported.

### Proxmox

`internal/providers/proxmox` implements the plan's Provider interface. It can clone a VM from a
cloud-init template (full clone) or create an unprivileged LXC with `nesting=1,keyctl=1`. The
agent is delivered by cloud-init user-data when a snippets directory is mounted
(`snippets_storage` + `snippets_dir`), because the PVE API cannot upload snippets. Otherwise
Cloudy bootstraps it over SSH with a per-server ed25519 key, sealed with the team key.

Tests use an in-process fake of the PVE endpoints listed in `internal/providers/proxmox/doc.go`.
The live test only runs when you ask for it:

```sh
CLOUDY_PROXMOX_LIVE=1 PVE_URL=https://pve:8006 PVE_TOKEN_ID='cloudy@pve!ctl' PVE_TOKEN_SECRET=… \
  PVE_NODE=pve-3x1 PVE_STORAGE=local PVE_BRIDGE=vmbr1 PVE_POOL=cloudy PVE_TLS_FINGERPRINT=… \
  go test -count=1 -run TestLive -v ./internal/providers/proxmox/   # read-only
# add CLOUDY_PROXMOX_LIVE_CREATE=1 (+ PVE_LXC_TEMPLATE / PVE_TEMPLATE_VMID) to create and delete a guest
```

## Apps and deploys (P2)

- **An app** is a public git URL, a branch and one of the team's servers. It builds with the
  repository's `Dockerfile` when there is one, otherwise with **Railpack** (MIT; version pinned in
  `internal/deploy/toolchain.go`) through its BuildKit frontend image. Images are
  `cloudy/<app>:<deployment_id>`; the newest 5 are kept, so a rollback needs no rebuild.
- **`cloudy.toml`** (optional, repository root) sets `port`, `release`, `[healthcheck]` and
  `[processes]`, and wins over the app's settings in Cloudy. Examples: `deploy/samples/*`.
- **Zero-downtime pipeline**: provision → build → release (a one-off container) → start `web` on an
  ephemeral `127.0.0.1` port → **health gate** (2xx within N seconds) → start workers → switch the
  Caddy upstream → drain the old containers. Any failure before the switch leaves the previous
  release serving, a failed switch puts the route back, and either files an in-app notification.
  Workers start *before* the switch — a deliberate change from the plan, so a failing worker cannot
  leave an app half-switched.
- **Rollback and restart** skip the build entirely: one `app.rollback` operation starts a kept image,
  gates it, switches the route and drains the rest (about a second on the dev target).
- **Routing** is Caddy on the server through its admin API on `localhost:2019`. The agent owns the
  whole configuration (`/var/lib/cloudy/routes.json`, rendered and POSTed to `/load`, which is
  graceful and atomic): `tls = off` hosts on :80, `internal` (Caddy's own CA) and `auto` on :443.
  `auto` uses on-demand TLS whose `ask` endpoint is the control plane's `/internal/tls/allow`, and
  that answers yes only for a **verified** domain (TXT `_cloudy-verify.<host>`).
- **Env values** are sealed with the team DEK, and re-sealed for a deploy to the target agent's own
  key (ed25519 → X25519, bound to app, deployment and process), so `operations.params` never holds a
  plaintext secret. They reach `docker run` as `-e NAME`, with the value in the CLI's environment.
- **Logs**: an agent's events for a deployment are sealed into `deployment_logs` and streamed to the
  browser over team-scoped SSE. Runtime logs are relayed on demand (`docker logs --follow`, bounded
  in lines and time).
- **Deploy hook**: `POST /hooks/deploy/<token>` — only the token's SHA-256 is stored, and it is shown
  once (rotate it in Settings). A push payload for another branch is acknowledged and skipped; an app
  with auto-deploy paused answers 409.
- **GitHub App**: written behind an interface but **not configured**. Set `CLOUDY_GITHUB_APP_ID`,
  `CLOUDY_GITHUB_APP_SLUG`, `CLOUDY_GITHUB_APP_PRIVATE_KEY_FILE` and `CLOUDY_GITHUB_WEBHOOK_SECRET`
  to enable it; until then `/hooks/github` answers 503 and the UI says "GitHub App not configured".
- **Apps domain**: set `CLOUDY_APPS_DOMAIN` (e.g. `fadymondy.com`) and every new app gets `<CLOUDY_APPS_PREFIX><app>.<domain>`
  (default prefix `cloudy-app-`), created already verified. With `CLOUDY_CLOUDFLARE_TOKEN` Cloudy also creates and
  deletes its A/AAAA record (only records it made, marked "managed by Cloudy"). Unset = off. Moving to a dedicated
  domain later is only an `.env` change.
- **Caddy proxy manager** (`internal/deploy/proxyhosts.go`, agent `proxy.set`): a *proxy host* puts any upstream a server
  can reach (`host:port`, `http://…`, `https://…` with optional skip-verify) behind a hostname on that server's Caddy.
  Like Nginx Proxy Manager, but on the Caddy Cloudy already runs. Hostnames share one namespace with app domains, use
  the same TLS modes, and get a public certificate only once verified (TXT, or at once through the team's
  Cloudflare). Web: **Proxy hosts**; API: `/api/teams/{team}/proxy-hosts`; MCP: `list_proxy_hosts`, `add_proxy_host`,
  `update_proxy_host`, `verify_proxy_host`, `remove_proxy_host`.
- **Team Cloudflare**: a team can save a `cloudflare` credential (API token with Zone:Read + DNS:Edit). A custom
  domain in one of its zones then gets its DNS record created and is verified at once, with no TXT step.
  Removing the domain removes only the records Cloudy created.

The team MCP adds `list_apps`, `get_app`, `list_deployments`, `get_deployment`, `deploy_logs`,
`list_env`, `list_domains` (read), `deploy_app` (with `wait`), `cancel_deployment`, `rollback_app`,
`restart_app`, `pause_autodeploy` (deploy), `create_app`, `set_env`, `unset_env`, `add_domain`,
`verify_domain` (write), `reveal_env` (secrets:read, audited) and `delete_app` (destructive).

### Try a deploy locally

`deploy/samples/{go,node,python,php}` are four tiny apps (Go with a Dockerfile; the rest detected by
Railpack; Node also has a release command and a worker). Serve them over http from bare clones, or
point an app at any public repository, then: **Apps → New app** → repository URL, branch, server →
*Detect runtime* → *Create and deploy*.

## Buckets and importing

- **Buckets**: a data service of kind `minio` is S3-compatible object storage (`quay.io/minio/minio`, published on the
  server's private address only). Its "databases" are buckets (`my_files` becomes the bucket `my-files`) and its users are access
  keys with MinIO's `readwrite` or `readonly` policy. Attaching it to an app sets `S3_URL` plus the `AWS_*` variables
  S3 clients read, with path-style addressing. Backups stream the data directory through restic (`docker cp`, since the
  image has no tar). This copies a live directory, so each object is consistent but the whole backup is not a point-in-time snapshot. A restore-test
  copies it into a throwaway MinIO, restarts it and lists the buckets.
- **Importing what runs**: a connected server lists its containers, well-known services and Proxmox guests. A container
  with a published port can be **exposed** behind a hostname from the server page, which creates a proxy host for it.

## SDKs, CLI and mobile

See `sdk/README.md`. The Go (`pkg/cloudy`), Node, Python and PHP/Laravel SDKs and the `cloudy` CLI
(`cmd/cloudy`, `make cli` builds it for Windows, macOS and Linux) call the team MCP endpoint with a `cld_…` token.
A mobile app uses the REST API with the Bearer token `POST /api/auth/login` returns.

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
| `POST /api/mcp` | team-bound `cld_…` (mint in *MCP tokens*) | `whoami`, `get_team`, `list_members`, `list_invitations`, `list_audit`, `list_mcp_tokens`, `list_servers`, `get_server`, `server_metrics`, `list_operations`, `list_credentials` (read) · `invite_member`, `add_server` (install / SSH connect / Proxmox), `add_credential` (Proxmox, Cloudflare) (write) · databases: `create_db`, `attach_database`, `set_backup_schedule`, `drop_database`, `delete_data_service` · servers: `run_server_step` · apps: `update_app`, `list_releases`, `rotate_deploy_hook` · team: `update_member_role`, `delete_credential` · `revoke_invitation`, `remove_member` (destructive, `confirm` = email) |
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
