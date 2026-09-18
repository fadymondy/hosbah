# How to test Cloudy (owner walkthrough)

Everything below runs against the **live** demo. Sign in and click through it.

- **URL:** https://cloudy-app.fadymondy.com
- **You:** `info@3x1.io` (platform admin + owner of the demo team).
- **Team:** **Demo**. You own it; it is the only team you land in.

**Nothing in this demo depends on the workstation any more.** The control plane is
LXC 117 on pve-3x1, the managed server is LXC 120, and the sample apps' git remotes
are a Gitea on LXC 117. All three are `onboot=1` and restart automatically, so the
walkthrough works with the workstation switched off. This was verified on 2026-09-18
by stopping every local piece (`cloudy-target`, `cloudy-git`, the git HTTP server on
:3792) and then running a full deploy, a rollback and an MCP deploy/rollback.

| Piece | Where | What it is |
|---|---|---|
| Control plane (`cloudy-api`, `cloudy-web`) | LXC 117, 10.10.10.117 | `/opt/cloudy/docker-compose.yml`, `restart: unless-stopped` |
| Database | pg-shared, LXC 102 | database `cloudy` |
| Managed server **demo-1** | LXC 120, 10.10.10.120 | Debian 13, Docker CE, the `cloudy-agent` under systemd |
| Sample git repos | LXC 117 `:3791` | Gitea, `cloudy/sample-{go,node,python,php}`, public |
| Edge | NPM host 61 → 10.10.10.117:3710 | Cloudflare in front |

## 1. See the online server with live metrics
1. Log in → you land in **Demo**.
2. Open **Servers**. **demo-1** shows **online** (green), Debian 13, 2 CPU, and its
   enrolment checks all pass — including **Docker 29.8.1**.
3. Open `demo-1` → live **CPU / memory / disk** tiles update as the agent heartbeats
   (every 15 s).

> demo-1 was enrolled through the product's own bring-your-own path — `curl -fsSL
> https://cloudy-app.fadymondy.com/install/<token> | sh` on a bare Debian box — not by
> hand. It worked first try, over Cloudflare, and a later `agent.update` rolled it onto
> a new agent build without dropping the connection.

## 2. See the 4 apps, deployments, logs, env, domains
1. Open **Apps**. You see **sample-go, sample-node, sample-python, sample-php**, each **running** on demo-1.
2. Open any app:
   - **Logs** tab → live container logs stream.
   - **Deployments** tab → release history with build logs.
   - **Env** tab → environment variables (secrets sealed).
   - **Domains** tab → `go|node|python|php.cloudy.localhost`, routed by Caddy on demo-1.

To see an app actually answer, from the Proxmox host:

```sh
pct exec 120 -- curl -s -H "Host: go.cloudy.localhost" http://127.0.0.1/
# hello from Go v1 ()
```

## 3. Trigger a deploy and a rollback
1. In an app → **Deployments** → **Deploy** → watch the build log stream to completion.
2. After it is live → **Rollback** → the previous release keeps serving.

> Verified on demo-1 with a 100 ms probe through Caddy running across the rollback:
> **600/600 requests answered 200 — zero failed requests.**

## 4. Drive it from the user MCP
1. **MCP tokens → Create token** (tick `read` + `deploy`). The token (`cld_…`) is shown once.
2. Point an MCP client at `https://cloudy-app.fadymondy.com/api/mcp` with
   `Authorization: Bearer cld_…` and call `deploy_app` then `rollback_app`
   (both accept `{"app":"sample-node","wait":true}`).
3. A call needing a scope the token lacks (e.g. `delete_app`, which needs `destructive`)
   is refused in-band with a clear message — it does not act.

## 5. Databases and backups (P3)

1. **Databases → New data service** → name it, pick **postgres**, pick **demo-1** →
   **Provision**. It goes **Provisioning**, then **Running** with a private endpoint
   (`172.17.0.1:5432`). A **redis** service works the same way.
2. **Add database** (`app`/`app`, grants `all`) → both go **ready**.
3. **Reveal connection string** (shown once, audited), then **Attach** it to
   **sample-node**: it injects a sealed `DATABASE_URL`. Restart the app to pick it up.
4. **Back up now** → **succeeded** with a size and a restic snapshot. Then
   **Restore-test** → **Passed** (it restores into a throwaway container; the live
   service is untouched).
5. **Schedule** a cron with retention and "restore-test each scheduled backup".

> Verified live on demo-1 (2026-09-18): `demo-pg` (postgres 16) and `demo-redis`
> provisioned, `app`/`app` created, a manual backup **succeeded** (3882 bytes, snapshot
> `6b3bed6b`) and its restore-test **Passed** ("209 tables reachable").

## 6. Require approval for destructive MCP calls (P4)

A token with the `destructive` scope can delete things. It always had to name the
resource in `confirm`; now a team can require a person as well.

1. Open **Approvals**. Tick **Require approval for destructive calls**.
2. With an MCP token carrying `destructive`, call something destructive — e.g.
   `remove_domain {"app":"sample-php","domain":"php.cloudy.localhost","confirm":"php.cloudy.localhost"}`.
   It is **refused**, nothing changes, and the answer names the request id.
3. Back in **Approvals**, the call is listed with the **MCP token that asked** and the
   **names** of its arguments (never their values). Click **Approve**.
4. Call the same thing again → it goes through **once**. The request flips to **Used**;
   a third call is held again.
5. **Audit** shows the whole trail — `mcp.destructive.held`, `.allowed`, `.refused` —
   each carrying the token id.

> Approving does not run the call. It is a one-shot permission the original caller
> spends by retrying, which is what lets you approve without the dashboard needing the
> caller's arguments.

Leave the setting **off** afterwards if you want the demo back to its default.

> Verified live on 2026-09-18 end to end: wrong `confirm` refused; with the setting on
> the call was held and `php.cloudy.localhost` still answered 200; after approval the
> retry removed the route (Caddy then answered 404) and the request went to **Used**;
> a repeat was held again; and an out-of-scope `delete_app` was refused in-band.

## What is still owner-gated (cannot be demoed without your input)
- **GitHub App** — intentionally "not configured" (`/hooks/github` answers 503).
- **Tenant apps domain** — no apex bought, so apps are reached through their own
  domains only. The demo uses `*.cloudy.localhost` with `tls: off`.
- **S3 backup credentials** — the remote backup target is disabled and labelled
  *"S3 — not configured"*; the demo uses a local restic repository on demo-1.
- **Proxmox pool-scoped API token** — Cloudy can create guests through its Proxmox
  driver, but no credential is wired, so demo-1 was added as bring-your-own. The
  provider-driven "create a server" path is untested against the real PVE.
- **`drop_database` over MCP** — not implemented; it needs an agent operation that
  does not exist yet.
