# How to test Cloudy (owner walkthrough)

Everything below runs against the **live** demo. Sign in and click through it.

- **URL:** https://cloudy-app.fadymondy.com
- **You:** `info@3x1.io` (platform admin + owner of the demo team). Use your own password/PIN — it was not changed.
- **Team:** **Demo** (renamed from "P1 Exit"). You own it; it is the only team you land in.

The stack was verified live on 2026-09-17: API healthy, web serving, the demo
server's agent **online** (last heartbeat ~20s, live metrics), inner Docker 29.1.3
running, and all 4 sample apps' containers actually Up (status in the UI is truthful,
not a stale "running").

## 1. See the online server with live metrics
1. Log in → you land in **Demo**.
2. Open **Servers**. `target-1` shows **online** (green), Ubuntu 24.04, 64 CPU.
3. Open `target-1` → live **CPU / memory / disk** tiles update as the agent heartbeats.

## 2. See the 4 apps, deployments, logs, env, domains
1. Open **Apps**. You see **sample-go, sample-node, sample-python, sample-php**, each **running**.
2. Open any app:
   - **Logs** tab → live container logs stream.
   - **Deployments** tab → release history (Go has 8, Node 4, PHP 3, Python 3) with build logs.
   - **Env** tab → environment variables (secrets sealed).
   - **Domains** tab → the app's routed domain through Caddy on the target.

## 3. Trigger a deploy and a rollback
1. In an app → **Deployments** → **Deploy** → watch the build log stream to completion.
2. After it is live → **Rollback** → the previous release keeps serving with no failed requests.

## 4. Drive it from the user MCP
1. **MCP tokens → Create token** (tick `read` + `deploy`). The token (`cld_…`) is shown once.
2. Point an MCP client at the endpoint on that page —
   `http://localhost:8710/api/mcp`, `Authorization: Bearer cld_…` — and call `deploy_app`
   then `rollback_app` (both accept `{"app":"sample-node","wait":true}`).
3. A call needing a scope the token lacks (e.g. `delete_app`, which needs `destructive`) is
   refused in-band with a clear message — it does not act.

All of section 1–4 was run end-to-end on 2026-09-17: a UI deploy of sample-go (release v3,
17s, zero-downtime) then rollback to v2, with a 100 ms probe through Caddy showing **600/600
requests answered 200 — zero failed requests**; and an MCP deploy+rollback of sample-node.

## What is still owner-gated (cannot be demoed without your input)
- **Your login credential** — only you can sign in as `info@3x1.io`; the automated run
  cannot (password/PIN not held, and it must not be changed).
- **GitHub App** — intentionally "not configured".
- **Custom apex domain** — needs real DNS.
- **S3 backup credentials** — remote backup target is owner-gated (local demo target stubbed).
- **Proxmox node / storage / IP pool / token** — not wired; no real cloud provider is touched.
