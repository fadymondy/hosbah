# Current Hosbah information architecture (audit, 2026-10-05)

This is how the authenticated web app (`web/app/[locale]/t/[team]/…`) is organised before the
app-first redesign. It is the baseline for [TARGET-HOSBAH-IA.md](TARGET-HOSBAH-IA.md) and
[HOSBAH-MIGRATION.md](HOSBAH-MIGRATION.md).

## Mental model today: server-first

The team home (`/t/{team}`) is a grid of **server cards** (status, tunnel, hardware, live usage,
last deploy), followed by the team's latest deployments. Apps, databases and proxy hosts are reached
**through their server**: `/apps`, `/databases`, `/servers` and `/proxy-hosts` redirect to the home
page ("Apps, databases and proxy hosts live inside their server now").

To reach an app you need to know which server it runs on: Home → server card → Apps tab → app.
The header then shows `Team › [Server ▾] › [App ▾]`. Both crumbs are dropdown switchers, and the app
switcher lists only the apps on the same server. Users found this confusing.

## Shell

| Piece | File | Notes |
|---|---|---|
| Sidebar | `components/app-shell.tsx` (`ShellNav`) | It swaps to `ServerNav` inside a server and to `SiteNav` inside an app. |
| Scope | `components/shell/scope.tsx` | `scopeOf(pathname)` → team / server / site. |
| Breadcrumb | `components/shell/scope-nav.tsx` (`ScopeCrumbs`) | The server dropdown plus the app dropdown. |
| Top bar | `app-shell.tsx` | Team switcher, crumbs, report problem, notifications, language, theme, user. |

There is no global search.

### Team sidebar (flat)

Overview · Shared services · Scripts · Mail · Templates · Products · Members · Invitations ·
Approvals · Costs · Audit log, then **Settings**: MCP tokens · SSH keys · Certificates · DNS ·
Credentials (manage only) · Team settings, then **Platform**: Admin (platform admins).

Apps, deployments, servers, databases and domains have **no entry**.

### Server sidebar (`ServerNav`, 17 items)

- (top) Overview, Metrics
- Workloads: Apps, Databases, Guests (Proxmox only), Proxy, Tunnels
- Sources: GitHub, Discovered, Import, Files, SFTP, Terminal
- Manage: Operations, Services, Security, Settings

### App sidebar (`SiteNav`, 25 items for a git app)

- (top) Overview
- Code: Repository, Deployments, Deploy script, Environment
- Services: Databases, Storage, Workers, Queues, Schedules, Realtime, Browsers, Apps & stacks
- Observe: Logs, Analytics, Web Vitals, APM, Alerts & monitors, Security
- Network: Proxy manager, Edge cache, Rules, DNS & SSL
- Manage: Backups, Settings & actions

An imported workload hides the git-only sections. The back link goes to the server's Apps tab.

## Route inventory

### Team level

| Route | What it is today |
|---|---|
| `/t/{team}` | Server cards with search and filters, then the latest deployments feed. |
| `/apps` | Redirects to home. |
| `/apps/new` | The new-app flow (repo, catalog, template). |
| `/apps/{app}` + 24 sections | App detail (see the sidebar above). The overview holds a canvas. |
| `/servers` | Redirects to home. |
| `/servers/new` | Add / connect a server. |
| `/servers/{id}` + 16 tabs | Server detail. |
| `/servers/{id}/sites/{app}` | Legacy app URL (redirect). |
| `/databases` | Redirects to home. |
| `/databases/{service}` | Data service detail (databases, users, backups, console, insights). |
| `/proxy-hosts` | Redirects to home. |
| `/dns` | Cloudflare DNS zones and records. |
| `/certificates` | TLS certificate watch (certcheck). |
| `/shared-services` | Proxmox guests classified as shared infrastructure. |
| `/scripts` | Script library, host-software catalogue, run history. |
| `/mail` | Mail servers (Stalwart), domains, mailboxes. |
| `/templates` | YAML site templates. |
| `/products` | SaaS products (instances, keys, upgrades). |
| `/members` | Team members and roles. |
| `/invitations` | Pending invitations. |
| `/approvals` | Approval requests (deletes and other gated actions). |
| `/costs` | Server costs and line items. |
| `/audit` | Team audit log. |
| `/tokens` | MCP tokens. |
| `/ssh-keys` | SSH keys. |
| `/credentials` | Provider credentials (owner/admin). |
| `/settings` | Team name, quotas, notification channels, status pages, log drains, GitHub App. |

### Outside the team

`/account`, `/admin/*` (users, teams, servers, operations, audit, settings, tokens), `/login`,
`/register`, `/forgot-password`, `/verify-email`, `/onboarding`, `/invite/{token}`,
`/status/{slug}` (public status page).

## Data the API already offers (team scope)

| Need | Endpoint | Notes |
|---|---|---|
| All apps | `GET /apps` | Includes server name, status, latest deployment and domains per app. |
| All deployments | `GET /deployments?limit&before` | Cursor pages. Returns app, server, status, kind, branch, commit, trigger, triggered_by, created/finished. |
| One deployment | `GET /apps/{app}/deployments/{id}` (+ `/stream`) | Step, track, error, logs. |
| App resources | `GET /apps/{app}/resources` | Per app (CPU/memory). There is no batch call. |
| All servers | `GET /servers`, `GET /servers/metrics?range=` | Sparklines for every server. |
| All data services | `GET /data-services` | Kind, version, status, server, host/port. |
| Service backups | `GET /data-services/{id}` | Backups and schedule per service. |
| Proxy hosts | `GET /proxy-hosts` | Hostname, upstream, TLS, verified, server. |
| Deploy sources | `GET /catalog`, `/deploy-presets`, `/github/app/repos`, `/templates`, `/products` | Real sources for the deploy wizard. |
| Tunnels | `GET /tunnels` | |

**Gaps (not invented in the UI):**

- Apps have no *environment* field.
- There is no team-wide domains endpoint. The UI composes app domains with proxy hosts.
- There is no per-database size or connection count on the list. Insights exist per service.
- There is no batched app CPU/memory.

## Pain points

1. **Server-first navigation.** "What is running?" needs a click per server.
2. No global lists for apps, deployments, databases or domains.
3. The app/server dropdown breadcrumbs mix two hierarchies.
4. The app sidebar has 25 flat items. Overview is a canvas, not answers.
5. Settings items (tokens, SSH keys, credentials, DNS, certificates) share the sidebar with
   day-to-day items. Invitations sit apart from Members.
6. Deploying from GitHub sits under a server's tabs (`github`, `import`, `discovered`), not in a
   deploy flow.
7. There is no global search.
