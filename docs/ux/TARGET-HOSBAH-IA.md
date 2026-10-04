# Target Hosbah information architecture: app-first, deployment-first, server-aware

The user should be able to answer these within seconds:

- What is running, and is it healthy?
- Where does it run?
- What was deployed recently, and what failed?
- How do I deploy, open, read logs or roll back?
- How healthy is each server?
- Which databases and domains belong to each app?

Hosbah keeps every capability it has today. The redesign moves and groups features; it does not
remove them, and it does not show data the API does not have.

## Sidebar (team scope)

| Group | Items |
|---|---|
| (primary) | **Home**, **Apps**, **Deployments**, **Servers**, **Databases**, **Domains** |
| Infrastructure | Networking (DNS zones), Storage, Backups, Shared services, Mail |
| Automation | Templates, Scripts, Products |
| Team | Members (with invitations), Approvals |
| Settings | General, Security, Billing |
| Platform | Admin (platform admins only) |

### The Settings area

Settings has its own sub-navigation:

- **General**: team name, quotas, status pages, log drains, GitHub App.
- **Security**: MCP tokens · SSH keys · Credentials · Certificates.
- **Notifications**: the notification channels section of General.
- **Audit log**
- **Billing**: costs.

### Scoped sidebars

- **Inside an app** (`SiteNav`): grouped sections. The back link goes to **All apps**.

  | Group | Sections |
  |---|---|
  | Overview | Overview |
  | Deploy | Deployments, Repository, Build & Deploy, Environment |
  | Services | Databases, Storage, Workers, Queues, Schedules, Realtime, Browsers, Apps & stacks |
  | Observe | Logs, Metrics (resources), Analytics, Web Vitals, APM, Alerts |
  | Network | Domains & proxy, Edge cache, Rules, DNS & SSL |
  | Operate | Backups, Security, Settings |

- **Inside a server** (`ServerNav`). The back link goes to **All servers**.

  | Group | Sections |
  |---|---|
  | Overview | Overview, Metrics |
  | Running here | Apps, Databases, Guests, Proxy, Tunnels |
  | Tools (advanced) | Files, Terminal, SFTP, Operations, Services |
  | Manage | Security, Settings |
  | Import | GitHub, Discovered, Import (secondary; deploy flows link here) |

## Top bar

`Team ▾ › Section › Entity`:

- The **server switcher** (a dropdown) appears only on server pages and only switches servers.
- App pages show a plain breadcrumb `Apps › <App title>`. "Runs on <server>" is metadata on the
  page, not a crumb.
- **Global search** (⌘K / Ctrl K) finds apps, servers, domains, databases and recent deployments.

## Pages

### Home `/t/{team}`

- A greeting and team name.
- Summary cards: Apps (running / failing), Servers (online / offline), Databases, Domains. Each
  links to its list.
- **Needs attention**: failed apps, offline servers, failed deployments (only when there are any).
- **What do you want to deploy?** Only real sources:
  - CircleXO (ready-to-deploy, from `/products` or `/catalog` when present)
  - GitHub repository
  - Docker image
  - Templates
  - Bring your own server
- **Recent deployments** (the feed).
- **Quick actions**: Deploy app, Add server, New database, Add domain.

### Apps `/apps`

A table of every app across servers:

- icon, title and slug
- status
- primary domain
- server
- commit
- last deploy
- health

Search, filters (status, server, source) and sorting (name, last deploy, status). The CTA is
**Deploy new app**. Mobile shows cards with status and actions.

### App detail `/apps/{app}`

The header:

- Product title, prominent; the slug secondary.
- Status, primary domain, repo · branch · commit, "Runs on <server>".
- Actions: [Open app] [Deploy] [⋯].

**Deploy** opens a confirmation dialog: ref, what changes, track. Then it opens live progress.

The overview answers:

- Health and current release.
- Resources (CPU, memory).
- Services: workers, databases.
- Domains.
- Recent deployments and events.

A compact read-only **Architecture** preview ("See how traffic, services and infrastructure
connect to this application.") links to **Open full canvas**.

### Deployments `/deployments`

A global history: app, commit and message, status, started, duration, triggered by, server.
Filters: status, app.

Deployment detail lives at `/apps/{app}/deployments?d={id}`. It shows:

- A pipeline timeline (provision → build → release → start → switch → drain).
- Per-stage logs.
- On failure: **what** failed, **where** (stage, server), **when**, and **next action** (retry,
  view logs, roll back, edit env).

### Deploy wizard `/apps/new`

One flow for every source: **Source → Application → Infrastructure → Services → Domain → Review
→ Deploy**. Sources: GitHub, Docker image, Template or catalog, CircleXO, Import from server.

### Servers `/servers`

The server cards that are on the home page today: live usage, filters, search.

### Server detail `/servers/{id}`

- First, **what is running here**: a resources header, then the apps on this server, then its
  databases, domains (proxy hosts), containers and backups.
- Technical details come last.
- Files, Terminal, SFTP and Operations are secondary tools.

### Databases `/databases`

Every data service: engine and version, server, status, host:port, apps that use it (when known),
backup status (last backup / schedule). Size and connections stay on the detail page (insights).

### Domains `/domains`

Every hostname, from app domains and proxy hosts: hostname, target (app or upstream), server, TLS,
verification, status.

A **DNS → SSL → App** chain shows each step's state.

## Status language (one vocabulary)

| Entity | States |
|---|---|
| App | Running · Deploying · Failed · Stopped · New |
| Server | Online · Offline · Enrolling · Pending |
| Deployment | Queued · Running · Succeeded · Failed · Cancelled |
| Database | Running · Provisioning · Failed · Stopped · New |
| Domain | Live · Awaiting DNS · TLS pending · Off |

The colours are the same everywhere:

- ok: green
- in progress: brand (animated)
- failed: danger
- idle or stopped: muted

## Principles

- Empty states always offer the next action. There are no giant empty bordered boxes.
- Progressive disclosure: tools that are technical or rarely used sit one level down.
- Every page works in en and ar (RTL), and in light and dark.
- Visual identity: the grid tokens and the shadcn/Base UI kit, consistent with Nasaq. Only
  meaningful visualisations: sparklines, pipeline, DNS→SSL→App.
