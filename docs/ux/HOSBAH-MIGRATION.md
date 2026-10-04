# Hosbah UX migration: route by route

Legend:

- **KEEP**: unchanged.
- **MOVE**: same page, new place in the IA (the URL is kept unless noted).
- **MERGE**: folded into another page.
- **REDESIGN**: same URL, new content.
- **DEPRECATE**: redirected, and the route is kept so old links keep working.

URLs are kept wherever possible so bookmarks, notifications and MCP links keep working.

## Team routes

| Route | Action | Target |
|---|---|---|
| `/t/{team}` | REDESIGN | Home: greeting, summary, needs attention, deploy sources, recent deploys, quick actions. The server cards move to `/servers`. |
| `/apps` | REDESIGN | Global apps list (it was a redirect). |
| `/apps/new` | REDESIGN | The one deployment wizard (Source → … → Deploy). |
| `/apps/{app}` | REDESIGN | Operational overview plus an Architecture preview. The full canvas stays reachable. |
| `/apps/{app}/*` (24 sections) | MOVE | Regrouped into Deploy / Services / Observe / Network / Operate. All sections are kept. |
| `/deployments` | REDESIGN | New global deployment history (`GET /deployments`). |
| `/servers` | REDESIGN | Server cards list (moved from home). |
| `/servers/new` | KEEP | Linked from Home quick actions and the wizard's Infrastructure step. |
| `/servers/{id}` | REDESIGN | "What is running here" first. |
| `/servers/{id}/apps,databases,guests,proxy,tunnels` | KEEP | Under "Running here". |
| `/servers/{id}/files,terminal,sftp,operations,services` | MOVE | Under "Tools". |
| `/servers/{id}/github,discovered,import` | MOVE | Under "Import". The deploy wizard links here. |
| `/servers/{id}/security,settings,metrics` | KEEP | |
| `/servers/{id}/sites/{app}` | KEEP | Legacy redirect. |
| `/databases` | REDESIGN | Global databases list (it was a redirect). |
| `/databases/{service}` | KEEP | |
| `/domains` | NEW | Global domains (app domains + proxy hosts) with DNS → SSL → App. |
| `/proxy-hosts` | MERGE | → `/domains` (redirect). |
| `/dns` | MOVE | Sidebar "Networking" (Infrastructure). |
| `/certificates` | MOVE | Settings → Security. Also linked from Domains. |
| `/shared-services` | MOVE | Infrastructure. |
| `/mail` | MOVE | Infrastructure. |
| `/storage` | NEW | Infrastructure: object storage services (MinIO) from `/data-services`. |
| `/backups` | NEW | Infrastructure: each data service's last backup and schedule. |
| `/templates` | MOVE | Automation. |
| `/scripts` | MOVE | Automation. |
| `/products` | MOVE | Automation. |
| `/members` | MOVE | Team. Members and Invitations share a sub-nav. |
| `/invitations` | MERGE | A tab of Members (URL kept). |
| `/approvals` | MOVE | Team. |
| `/settings` | MOVE | Settings → General (with Notifications). |
| `/tokens` | MOVE | Settings → Security → MCP tokens. |
| `/ssh-keys` | MOVE | Settings → Security → SSH keys. |
| `/credentials` | MOVE | Settings → Security → Credentials (owner/admin). |
| `/audit` | MOVE | Settings → Audit log. |
| `/costs` | MOVE | Settings → Billing. |

## Shell

| Piece | Action | Notes |
|---|---|---|
| `ShellNav` | REDESIGN | The grouped sidebar from the target IA. |
| `ScopeCrumbs` server dropdown | KEEP | Server pages only. It switches servers. |
| `ScopeCrumbs` app dropdown | DEPRECATE | Replaced by a plain `Apps › App` breadcrumb. |
| `SiteNav` back link | MOVE | Server apps tab → All apps. |
| `ServerNav` back link | MOVE | Team home → All servers. |
| Global search | NEW | ⌘K / Ctrl K palette. |

## Outside the team

`/account`, `/admin/*`, auth pages, `/onboarding`, `/invite/{token}` and `/status/{slug}` are KEEP.

## Phases

Each phase is a separate commit and is checked with `npm run typecheck && npm run build`.

1. Shell and navigation: grouped sidebar, settings sub-nav, breadcrumb and server-only switcher,
   global search.
2. Home.
3. Apps list.
4. App detail: header, grouped nav, operational overview, Architecture preview, deploy
   confirmation.
5. Deployments list and failure explanations.
6. Deployment wizard.
7. Servers list.
8. Server detail: "running here" first, tools secondary.
9. Databases.
10. Domains.
11. Infrastructure: Storage, Backups.
12. Settings cleanup.

## Not done (needs backend)

- An app environment field (production/staging).
- A team-wide domains endpoint with DNS state.
- Database size and connections on the list.
- Batched app CPU/memory for the list.

The UI shows these only where an existing endpoint provides them.
