"use client"

import { Button, DropdownMenu, SidebarGroup, useSidebarCollapsed, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger, Skeleton } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import { useEffect, useState, type ReactNode } from "react"
import {
  AppWindowIcon,
  ArrowLeftIcon,
  CableIcon,
  ChartNoAxesColumnIcon,
  CheckIcon,
  ChevronRightIcon,
  ChevronsUpDownIcon,
  CogIcon,
  DatabaseIcon,
  FolderIcon,
  GithubIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  LayoutGridIcon,
  NetworkIcon,
  SettingsIcon,
  ShieldIcon,
  SquareTerminalIcon,
  TerminalIcon,
  UsersIcon,
  WorkflowIcon,
  type LucideIcon,
} from "lucide-react"

import { ServerMark } from "@/components/server-mark"
import { SERVER_TABS, type ServerTab } from "@/components/server-tabs/header"
import { NavLink } from "@/components/shell/nav-link"
import { scopeOf, useShellScope } from "@/components/shell/scope"
import { tabsFor } from "@/components/site/sections"



import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { useOptionalTeam } from "@/lib/team"

/** Each server section's icon, and the group it sits in on the server's sidebar: what runs here
 * first, then the hands-on tools, then its own settings, and importing (which the deploy flows
 * link to) last. */
export const SERVER_ITEMS: Record<ServerTab, { icon: LucideIcon; group: "" | "running" | "tools" | "manage" | "import" }> = {
  "": { icon: LayoutDashboardIcon, group: "" },
  metrics: { icon: ChartNoAxesColumnIcon, group: "" },
  users: { icon: UsersIcon, group: "running" },
  apps: { icon: AppWindowIcon, group: "running" },
  databases: { icon: DatabaseIcon, group: "running" },
  proxy: { icon: NetworkIcon, group: "running" },
  tunnels: { icon: CableIcon, group: "running" },
  files: { icon: FolderIcon, group: "tools" },
  terminal: { icon: SquareTerminalIcon, group: "tools" },
  sftp: { icon: KeyRoundIcon, group: "tools" },
  ssh: { icon: TerminalIcon, group: "tools" },
  operations: { icon: WorkflowIcon, group: "tools" },
  services: { icon: CogIcon, group: "tools" },
  security: { icon: ShieldIcon, group: "manage" },
  settings: { icon: SettingsIcon, group: "manage" },
  github: { icon: GithubIcon, group: "import" },
}
const SERVER_GROUPS = ["", "running", "tools", "manage", "import"] as const

/** Where the URL is (team, server or site), with the team's base path. */
export function useScope() {
  const { locale } = useI18n()
  const teamCtx = useOptionalTeam()
  const pathname = usePathname() ?? ""
  const slug = teamCtx?.team.slug
  return { ...scopeOf(pathname, locale, slug), base: slug ? `/${locale}/t/${slug}` : null, team: teamCtx?.team ?? null }
}

/** The top of a scoped sidebar: the way back up a level, then what this level is. */
function ScopeHead({
  back,
  backLabel,
  mark,
  title,
  subtitle,
  onNavigate,
}: {
  back: string
  backLabel: string
  mark: ReactNode
  title: ReactNode
  subtitle?: ReactNode
  onNavigate?: () => void
}) {
  const collapsed = useSidebarCollapsed()
  return (
    <div className="border-b border-border pb-3">
      <Link
        href={back}
        onClick={onNavigate}
        title={backLabel}
        aria-label={backLabel}
        className={`flex items-center gap-2 px-4 py-2 text-caption text-muted-foreground hover:text-foreground ${collapsed ? "justify-center px-0" : ""}`}
      >
        <ArrowLeftIcon className="size-3.5 shrink-0 rtl:-scale-x-100" />
        {collapsed ? null : <span className="truncate">{backLabel}</span>}
      </Link>
      <div className={`flex items-center gap-2.5 px-4 pt-1 ${collapsed ? "justify-center px-0" : ""}`}>
        {mark}
        {collapsed ? null : (
          <span className="min-w-0">
            <span dir="auto" className="block truncate text-label text-foreground">
              {title}
            </span>
            {subtitle ? <span className="block truncate text-caption text-muted-foreground">{subtitle}</span> : null}
          </span>
        )}
      </div>
    </div>
  )
}

/** A server's sidebar: its sections (they were tabs), grouped. */
export function ServerNav({ serverId, onNavigate }: { serverId: string; onNavigate?: () => void }) {
  const { t } = useI18n()
  const { base, team } = useScope()
  const { server } = useShellScope()
  if (!base || !team) return null
  const root = `${base}/servers/${serverId}`
  const loaded = server?.id === serverId ? server : null
  const enrolled = loaded?.status === "enrolled"
  const tabs = SERVER_TABS.filter(
    (tab) => enrolled || tab === "" || tab === "operations" || tab === "settings",
  )

  return (
    <div className="flex flex-col pb-3" data-testid="server-nav">
      <ScopeHead
        back={`${base}/servers`}
        backLabel={t("shell.scope.allServers")}
        onNavigate={onNavigate}
        mark={loaded ? <ServerMark server={loaded} size={8} /> : <Skeleton className="size-8" />}
        title={loaded ? <span dir="ltr">{loaded.name}</span> : <Skeleton className="h-4 w-24" />}
        subtitle={
          loaded ? (loaded.online ? t("home.online") : t(`servers.statuses.${loaded.status}`, undefined, t("home.offline"))) : undefined
        }
      />
      {SERVER_GROUPS.map((g) => {
        const items = tabs.filter((tab) => SERVER_ITEMS[tab].group === g)
        if (!items.length) return null
        return (
          <SidebarGroup key={g || "top"} label={g ? t(`shell.scope.${g}`) : undefined}>
            {items.map((tab) => {
              const Icon = SERVER_ITEMS[tab].icon
              return (
                <NavLink key={tab || "overview"} href={tab ? `${root}/${tab}` : root} exact={!tab} icon={<Icon />} onNavigate={onNavigate}>
                  {t(`servers.tabs.${tab || "overview"}`)}
                </NavLink>
              )
            })}
          </SidebarGroup>
        )
      })}
    </div>
  )
}

/** A site's sidebar: its tabs (related sections share one; see SITE_TABS). */
export function SiteNav({ site: name, base: baseOverride, onNavigate }: { site: string; base?: string; onNavigate?: () => void }) {
  const { t } = useI18n()
  const scope = useScope()
  const base = baseOverride ?? scope.base
  const { site } = useShellScope()
  if (!base) return null
  const loaded = site?.name === name ? site : null
  const root = `${base}/apps/${encodeURIComponent(name)}`

  return (
    <div className="flex flex-col pb-3" data-testid="site-nav">
      <ScopeHead
        back={`${base}/apps`}
        backLabel={t("shell.scope.allApps")}
        onNavigate={onNavigate}
        mark={
          loaded ? (
            <ServerMark server={{ color: loaded.color, icon: loaded.icon ?? "globe", logo_url: loaded.logo_url }} size={8} />
          ) : (
            <Skeleton className="size-8" />
          )
        }
        title={loaded ? loaded.title || loaded.name : <Skeleton className="h-4 w-24" />}
        subtitle={loaded?.title ? <span dir="ltr">{loaded.name}</span> : undefined}
      />
      {loaded ? (
        <SidebarGroup>
          {tabsFor(loaded.imported).map((s) => (
            <NavLink key={s.key || "overview"} href={s.key ? `${root}/${s.key}` : root} exact={!s.key} icon={<s.icon />} onNavigate={onNavigate}>
              {t(`site.nav.${s.key || "overview"}`)}
            </NavLink>
          ))}
        </SidebarGroup>
      ) : (
        <div className="flex flex-col gap-2 px-4 pt-4">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-6 w-full" />
          ))}
        </div>
      )}
    </div>
  )
}

function Sep() {
  return <ChevronRightIcon aria-hidden className="size-3.5 shrink-0 text-muted-foreground rtl:-scale-x-100" />
}

/**
 * The breadcrumb after the team switcher. On a server: › Servers › [server ▾], where the dropdown
 * only switches servers (keeping the section when the target has it). On an app: › Apps › App, a
 * plain trail; where the app runs is shown on its page, and other apps are one search away.
 */
export function ScopeCrumbs() {
  const { t } = useI18n()
  const router = useRouter()
  const scope = useScope()
  const { server: liveServer, site: liveSite } = useShellScope()
  const [servers, setServers] = useState<Server[] | null>(null)
  const slug = scope.team?.slug

  // The list is small; load it once per team the first time a server is open.
  const onServer = scope.kind === "server"
  useEffect(() => {
    setServers(null)
  }, [slug])
  useEffect(() => {
    if (!onServer || !slug || servers) return
    apiFetch<{ servers: Server[] }>(`/api/teams/${slug}/servers`)
      .then((r) => setServers(r.servers ?? []))
      .catch(() => setServers([]))
  }, [onServer, slug, servers])

  if (scope.kind === "team" || !scope.base) return null
  const base = scope.base
  const crumb = "hidden truncate text-sm text-grid-muted hover:text-grid-fg sm:inline"

  if (scope.kind === "site") {
    const site = liveSite?.name === scope.site ? liveSite : null
    return (
      <nav aria-label={t("shell.scope.breadcrumb")} className="flex min-w-0 items-center gap-1.5 px-1">
        <Sep />
        <Link href={`${base}/apps`} className={crumb}>
          {t("nav.apps")}
        </Link>
        <span className="hidden sm:inline">
          <Sep />
        </span>
        <span className="flex min-w-0 items-center gap-2" aria-current="page">
          {site ? (
            <ServerMark server={{ color: site.color, icon: site.icon ?? "globe", logo_url: site.logo_url }} size={6} />
          ) : (
            <Skeleton className="size-6" />
          )}
          <span dir="auto" className="truncate text-sm font-medium">
            {site?.title || scope.site}
          </span>
        </span>
      </nav>
    )
  }

  const serverId = scope.serverId
  const listed = servers?.find((s) => s.id === serverId)
  // The page's own copy is live (a rename shows at once); the list fills in the rest.
  const live = liveServer?.id === serverId ? liveServer : null
  const server = live || listed ? { ...listed, ...live, name: live?.name || listed?.name || "" } : undefined
  const serverHref = (id: string) => {
    const section = (SERVER_TABS as readonly string[]).includes(scope.section) ? scope.section : ""
    return `${base}/servers/${id}${section ? `/${section}` : ""}`
  }

  return (
    <nav aria-label={t("shell.scope.breadcrumb")} className="flex min-w-0 items-center gap-0.5">
      <Sep />
      <Link href={`${base}/servers`} className={`${crumb} px-1`}>
        {t("nav.servers")}
      </Link>
      <span className="hidden sm:inline">
        <Sep />
      </span>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={<Button variant="ghost" className="h-9 max-w-[10rem] justify-between gap-2 px-2 sm:max-w-[14rem]" aria-label={t("shell.scope.switchServer")} />}
        >
          {server ? <ServerMark server={server} size={6} /> : <Skeleton className="size-6" />}
          <span dir="ltr" className="truncate text-sm font-medium">
            {server?.name ?? "…"}
          </span>
          <ChevronsUpDownIcon className="text-muted-foreground" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="max-h-96 min-w-64">
          <DropdownMenuGroup>
            <DropdownMenuLabel>{t("shell.scope.switchServer")}</DropdownMenuLabel>
            {(servers ?? []).map((s) => (
              <DropdownMenuItem key={s.id} onClick={() => router.push(serverHref(s.id))}>
                <ServerMark server={s} size={6} />
                <span dir="ltr" className="min-w-0 flex-1 truncate">
                  {s.name}
                </span>
                {s.id === serverId ? <CheckIcon aria-label={t("shell.current")} /> : null}
              </DropdownMenuItem>
            ))}
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => router.push(`${base}/servers`)}>
            <LayoutGridIcon />
            {t("shell.scope.allServers")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </nav>
  )
}
