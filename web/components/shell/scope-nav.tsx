"use client"

import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import { useEffect, useState, type ReactNode } from "react"
import {
  AppWindowIcon,
  ArrowLeftIcon,
  CableIcon,
  CheckIcon,
  ChevronRightIcon,
  ChevronsUpDownIcon,
  DatabaseIcon,
  DownloadIcon,
  FolderIcon,
  GithubIcon,
  LayersIcon,
  LayoutDashboardIcon,
  LayoutGridIcon,
  NetworkIcon,
  RadarIcon,
  SettingsIcon,
  SquareTerminalIcon,
  WorkflowIcon,
  type LucideIcon,
} from "lucide-react"

import { ServerMark } from "@/components/server-mark"
import { SERVER_TABS, type ServerTab } from "@/components/server-tabs/header"
import { GroupLabel, NavLink } from "@/components/shell/nav-link"
import { scopeOf, useShellScope } from "@/components/shell/scope"
import { sectionsFor, SITE_GROUPS } from "@/components/site/sections"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"
import { apiFetch } from "@/lib/api"
import { isImported, type App } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { useOptionalTeam } from "@/lib/team"

/** Each server section's icon, and the group it sits in on the server's sidebar. */
const SERVER_ITEMS: Record<ServerTab, { icon: LucideIcon; group: "" | "workloads" | "sources" | "manage" }> = {
  "": { icon: LayoutDashboardIcon, group: "" },
  apps: { icon: AppWindowIcon, group: "workloads" },
  databases: { icon: DatabaseIcon, group: "workloads" },
  guests: { icon: LayersIcon, group: "workloads" },
  proxy: { icon: NetworkIcon, group: "workloads" },
  tunnels: { icon: CableIcon, group: "workloads" },
  github: { icon: GithubIcon, group: "sources" },
  discovered: { icon: RadarIcon, group: "sources" },
  import: { icon: DownloadIcon, group: "sources" },
  files: { icon: FolderIcon, group: "sources" },
  terminal: { icon: SquareTerminalIcon, group: "sources" },
  operations: { icon: WorkflowIcon, group: "manage" },
  settings: { icon: SettingsIcon, group: "manage" },
}
const SERVER_GROUPS = ["", "workloads", "sources", "manage"] as const

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
  return (
    <div className="border-b border-line pb-3">
      <Link
        href={back}
        onClick={onNavigate}
        title={backLabel}
        className="flex items-center gap-2 px-4 py-2 text-xs text-grid-muted hover:text-grid-fg group-data-[collapsed=true]/nav:justify-center group-data-[collapsed=true]/nav:px-0"
      >
        <ArrowLeftIcon className="size-3.5 shrink-0 rtl:-scale-x-100" />
        <span className="truncate group-data-[collapsed=true]/nav:hidden">{backLabel}</span>
      </Link>
      <div className="flex items-center gap-2.5 px-4 pt-1 group-data-[collapsed=true]/nav:justify-center group-data-[collapsed=true]/nav:px-0">
        {mark}
        <span className="min-w-0 group-data-[collapsed=true]/nav:hidden">
          <span dir="auto" className="block truncate text-sm font-medium text-grid-fg">
            {title}
          </span>
          {subtitle ? <span className="block truncate text-xs text-grid-muted">{subtitle}</span> : null}
        </span>
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
  const isNode = Boolean(loaded?.facts?.proxmox?.installed)
  const tabs = SERVER_TABS.filter((tab) => tab !== "guests" || isNode).filter(
    (tab) => enrolled || tab === "" || tab === "operations" || tab === "settings",
  )

  return (
    <nav className="flex flex-col pb-3" aria-label={t("servers.tabs.label")} data-testid="server-nav">
      <ScopeHead
        back={base}
        backLabel={team.name}
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
          <div key={g || "top"} className="flex flex-col">
            {g ? <GroupLabel>{t(`shell.scope.${g}`)}</GroupLabel> : <div className="h-2" />}
            {items.map((tab) => {
              const Icon = SERVER_ITEMS[tab].icon
              return (
                <NavLink key={tab || "overview"} href={tab ? `${root}/${tab}` : root} exact={!tab} icon={<Icon />} onNavigate={onNavigate}>
                  {t(`servers.tabs.${tab || "overview"}`)}
                </NavLink>
              )
            })}
          </div>
        )
      })}
    </nav>
  )
}

/** A site's sidebar: its sections, grouped (code, services, observe, network, manage). */
export function SiteNav({ site: name, onNavigate }: { site: string; onNavigate?: () => void }) {
  const { t } = useI18n()
  const { base } = useScope()
  const { site, server } = useShellScope()
  if (!base) return null
  const loaded = site?.name === name ? site : null
  const root = `${base}/apps/${encodeURIComponent(name)}`
  const back = loaded ? `${base}/servers/${loaded.serverId}/apps` : base
  const serverName = loaded && server?.id === loaded.serverId ? server.name : ""

  return (
    <nav className="flex flex-col pb-3" aria-label={t("site.nav.label")} data-testid="site-nav">
      <ScopeHead
        back={back}
        backLabel={serverName || t("nav.servers")}
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
        SITE_GROUPS.map((g) => {
          const items = sectionsFor(loaded.imported).filter((s) => s.group === g)
          if (!items.length) return null
          return (
            <div key={g} className="flex flex-col">
              {g !== "site" ? <GroupLabel>{t(`site.groups.${g}`)}</GroupLabel> : <div className="h-2" />}
              {items.map((s) => (
                <NavLink
                  key={s.key || "overview"}
                  href={s.key ? `${root}/${s.key}` : root}
                  exact={!s.key}
                  icon={<s.icon />}
                  onNavigate={onNavigate}
                >
                  {t(`site.nav.${s.key || "overview"}`)}
                </NavLink>
              ))}
            </div>
          )
        })
      ) : (
        <div className="flex flex-col gap-2 px-4 pt-4">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-6 w-full" />
          ))}
        </div>
      )}
    </nav>
  )
}

function Sep() {
  return <ChevronRightIcon aria-hidden className="size-3.5 shrink-0 text-grid-muted rtl:-scale-x-100" />
}

/**
 * The breadcrumb after the team switcher: › Server › Site, each a switcher of its own (the other
 * servers of the team; the other sites on this server). Switching keeps the section when the
 * target has it.
 */
export function ScopeCrumbs() {
  const { t } = useI18n()
  const router = useRouter()
  const scope = useScope()
  const { server: liveServer, site: liveSite } = useShellScope()
  const [servers, setServers] = useState<Server[] | null>(null)
  const [apps, setApps] = useState<App[] | null>(null)
  const slug = scope.team?.slug

  // The lists are small; load them once per team the first time a server or site is open.
  const inScope = scope.kind !== "team"
  useEffect(() => {
    setServers(null)
    setApps(null)
  }, [slug])
  useEffect(() => {
    if (!inScope || !slug || servers) return
    apiFetch<{ servers: Server[] }>(`/api/teams/${slug}/servers`)
      .then((r) => setServers(r.servers ?? []))
      .catch(() => setServers([]))
  }, [inScope, slug, servers])
  useEffect(() => {
    if (!inScope || !slug || apps) return
    apiFetch<{ apps: App[] }>(`/api/teams/${slug}/apps`)
      .then((r) => setApps(r.apps ?? []))
      .catch(() => setApps([]))
  }, [inScope, slug, apps])

  if (!inScope || !scope.base) return null
  const base = scope.base

  const siteName = scope.kind === "site" ? scope.site : null
  const listedSite = siteName ? apps?.find((a) => a.name === siteName) : undefined
  const serverId =
    scope.kind === "server" ? scope.serverId : (liveSite?.name === siteName ? liveSite?.serverId : undefined) ?? listedSite?.server_id
  const listedServer = servers?.find((s) => s.id === serverId)
  // The page's own copy is live (a rename shows at once); the list fills in what a site page does not know.
  const live = liveServer?.id === serverId ? liveServer : null
  const server = live || listedServer ? { ...listedServer, ...live, name: live?.name || listedServer?.name || "" } : undefined
  const site = liveSite?.name === siteName && liveSite ? { ...liveSite } : listedSite

  const serverHref = (id: string) => {
    const section = scope.kind === "server" && (SERVER_TABS as readonly string[]).includes(scope.section) ? scope.section : ""
    return `${base}/servers/${id}${section ? `/${section}` : ""}`
  }
  const siteHref = (a: App) => {
    const section = scope.kind === "site" && sectionsFor(isImported(a)).some((s) => s.key === scope.section) ? scope.section : ""
    return `${base}/apps/${encodeURIComponent(a.name)}${section ? `/${section}` : ""}`
  }
  const siblings = (apps ?? []).filter((a) => a.server_id === serverId)

  const trigger = "h-9 max-w-[8rem] justify-between gap-2 px-2 sm:max-w-[14rem]"
  return (
    <nav aria-label={t("shell.scope.breadcrumb")} className="flex min-w-0 items-center gap-0.5">
      <Sep />
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              className={`${trigger} ${siteName ? "hidden sm:inline-flex" : ""}`}
              aria-label={t("shell.scope.switchServer")}
            />
          }
        >
          {server ? <ServerMark server={server} size={6} /> : <Skeleton className="size-6" />}
          <span dir="ltr" className="truncate text-sm font-medium">
            {server?.name ?? "…"}
          </span>
          <ChevronsUpDownIcon className="text-grid-muted" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="max-h-96 min-w-64">
          <DropdownMenuGroup>
            <DropdownMenuLabel>{t("nav.servers")}</DropdownMenuLabel>
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
          <DropdownMenuItem onClick={() => router.push(base)}>
            <LayoutGridIcon />
            {t("shell.scope.allServers")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {siteName ? (
        <>
          <Sep />
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button variant="ghost" className={trigger} aria-label={t("shell.scope.switchSite")} />}>
              <ServerMark server={{ color: site?.color, icon: site?.icon ?? "globe", logo_url: site?.logo_url }} size={6} />
              <span dir="auto" className="truncate text-sm font-medium">
                {site?.title || siteName}
              </span>
              <ChevronsUpDownIcon className="text-grid-muted" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="max-h-96 min-w-64">
              <DropdownMenuGroup>
                <DropdownMenuLabel>{t("nav.apps")}</DropdownMenuLabel>
                {siblings.map((a) => (
                  <DropdownMenuItem key={a.id} onClick={() => router.push(siteHref(a))}>
                    <ServerMark server={{ color: a.color, icon: a.icon ?? "globe", logo_url: a.logo_url }} size={6} />
                    <span dir="auto" className="min-w-0 flex-1 truncate">
                      {a.title || a.name}
                    </span>
                    {a.name === siteName ? <CheckIcon aria-label={t("shell.current")} /> : null}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuGroup>
              {serverId ? (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onClick={() => router.push(`${base}/servers/${serverId}/apps`)}>
                    <AppWindowIcon />
                    {t("shell.scope.allSites")}
                  </DropdownMenuItem>
                </>
              ) : null}
            </DropdownMenuContent>
          </DropdownMenu>
        </>
      ) : null}
    </nav>
  )
}
