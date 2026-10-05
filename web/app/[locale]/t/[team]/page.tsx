"use client"

import Link from "next/link"
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react"
import { Card, Chip, ChipGroup, Input, SectionHeader, Skeleton } from "@fadymondy/nasaq/web"
import { AppWindowIcon, ArrowRightIcon, SearchIcon, DatabaseIcon, GlobeIcon, RocketIcon, ServerIcon } from "lucide-react"

import { ActivityFeed } from "@/components/home/activity-feed"
import { DashboardAppCard, DashboardServerCard, type LoadPoint } from "@/components/home/dashboard-cards"
import { AppCardSkeleton, ServerCardSkeleton } from "@/components/home/skeletons"
import { DeployButton } from "@/components/home/deploy-dialog"
import { apiFetch } from "@/lib/api"
import { appActivity, type App } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import type { Product } from "@/lib/products"
import { type Server, useEventStream } from "@/lib/servers"
import { useSession } from "@/lib/session"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

type AppFilter = "all" | "running" | "failed" | "stopped"
type ProxyHost = { id: string; hostname: string; verified: boolean; enabled: boolean }

/** Calls an endpoint that newer APIs have; null (not an error) when this API does not have it yet. */
async function optional<T>(path: string): Promise<T | null> {
  try {
    return await apiFetch<T>(path)
  } catch {
    return null
  }
}

/** One count: an icon tile, the label, the figure and a health caption. Links to its list. */
function Stat({
  href,
  icon,
  label,
  value,
  detail,
  tone,
}: {
  href: string
  icon: ReactNode
  label: string
  value: number | null
  detail: ReactNode
  tone: "ok" | "warn" | "muted"
}) {
  return (
    <Link
      href={href}
      className="flex items-center gap-4 rounded-2xl border border-border bg-card px-4 py-4 transition-colors hover:bg-nq-hover focus-visible:outline-2 focus-visible:outline-nq-focus"
    >
      <span className="flex size-11 shrink-0 items-center justify-center rounded-xl bg-secondary text-foreground [&_svg]:size-5">
        {icon}
      </span>
      <span className="flex min-w-0 flex-col">
        <span className="truncate text-body-sm text-muted-foreground">{label}</span>
        {value === null ? (
          <Skeleton className="my-1 h-7 w-12" />
        ) : (
          <span className="text-2xl leading-tight font-semibold text-foreground tabular-nums">{value}</span>
        )}
        <span className={cn("truncate text-caption", tone === "warn" ? "text-nq-danger-text" : "text-muted-foreground")}>{detail}</span>
      </span>
    </Link>
  )
}

/**
 * The team's home: a greeting with the overall health, four counts, a "what do you want to deploy"
 * panel with real sources, the team's apps and latest deployments, quick actions, and a side rail with
 * live server load and recent activity. Every number comes from the API; refreshes every 30 s.
 */
export default function HomePage() {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const { me } = useSession()
  useDocumentTitle(`${t("nav.home")} — ${team.name}`)
  const base = `/${locale}/t/${team.slug}`
  const api = `/api/teams/${team.slug}`

  const [apps, setApps] = useState<App[] | null>(null)
  const [servers, setServers] = useState<Server[] | null>(null)
  const [services, setServices] = useState<DataService[] | null>(null)
  const [proxies, setProxies] = useState<ProxyHost[]>([])
  const [loads, setLoads] = useState<Record<string, LoadPoint[]>>({})
  const [serverQuery, setServerQuery] = useState("")
  const [sparks, setSparks] = useState<Record<string, { cpu: number[] }>>({})
  const [appFilter, setAppFilter] = useState<AppFilter>("all")
  const [appQuery, setAppQuery] = useState("")
  const [products, setProducts] = useState<Product[]>([])

  const load = useCallback(async () => {
    const [a, s, d, p] = await Promise.all([
      optional<{ apps: App[] }>(`${api}/apps`),
      optional<{ servers: Server[] }>(`${api}/servers`),
      optional<{ services: DataService[] }>(`${api}/data-services`),
      optional<{ proxy_hosts: ProxyHost[] }>(`${api}/proxy-hosts`),
    ])
    setApps(a?.apps ?? [])
    setServers(s?.servers ?? [])
    setServices(d?.services ?? [])
    setProxies(p?.proxy_hosts ?? [])
  }, [api])

  useEffect(() => {
    void load()
    const id = setInterval(load, 30_000)
    return () => clearInterval(id)
  }, [load])

  // Live parts: server status and load, CPU sparklines, and each app's container load for its chart,
  // refreshed every 5 s while the tab is visible and at once on any team event.
  const loadLive = useCallback(async () => {
    const [s, sp, f] = await Promise.all([
      optional<{ servers: Server[] }>(`${api}/servers`),
      optional<{ servers: Record<string, { cpu: number[] }> }>(`${api}/servers/metrics?range=1h`),
      optional<{ apps: Record<string, LoadPoint[]> }>(`${api}/apps/metrics`),
    ])
    if (s) setServers(s.servers ?? [])
    if (sp) setSparks(sp.servers ?? {})
    if (f) setLoads(f.apps ?? {})
  }, [api])

  useEffect(() => {
    void loadLive()
    const id = setInterval(() => {
      if (document.visibilityState === "visible") void loadLive()
    }, 5_000)
    return () => clearInterval(id)
  }, [loadLive])

  useEventStream(`${api}/notifications/stream`, { notifications: () => void loadLive() })

  // Ready-to-deploy sources only change when someone adds one; load them once.
  useEffect(() => {
    optional<{ products: Product[] }>(`${api}/products`).then((r) => setProducts(r?.products ?? []))
  }, [api])

  // Proxmox guests are listed under their node; the home page counts and shows only the hosts.
  const hosts = useMemo(() => {
    if (!servers) return null
    const ids = new Set(servers.map((s) => s.id))
    return servers.filter((s) => !(s.parent_server_id && ids.has(s.parent_server_id)))
  }, [servers])

  const stats = useMemo(() => {
    const as = apps ?? []
    const ss = hosts ?? []
    const ds = services ?? []
    const appDomains = as.flatMap((a) => a.domains ?? [])
    return {
      apps: as.length,
      appsRunning: as.filter((a) => a.status === "running" || a.imported_state === "running").length,
      appsFailed: as.filter((a) => a.status === "failed").length,
      servers: ss.length,
      serversOnline: ss.filter((s) => s.online).length,
      serversOffline: ss.filter((s) => s.status === "enrolled" && !s.online).length,
      dbs: ds.length,
      dbsRunning: ds.filter((s) => s.status === "running").length,
      dbsFailed: ds.filter((s) => s.status === "failed").length,
      domains: appDomains.length + proxies.length,
      domainsPending: appDomains.filter((d) => !d.verified).length + proxies.filter((p) => !p.verified).length,
    }
  }, [apps, hosts, services, proxies])

  // Hosbah has no server power control yet: the card's power menu says so instead of failing silently.
  const noPower = async () => ({ error: t("home3.noPower") })

  const loading = apps === null || servers === null
  const who = me.user.email.split("@")[0]
  const hour = new Date().getHours()
  const greeting = t(hour < 12 ? "home2.morning" : hour < 18 ? "home2.afternoon" : "home2.evening", { name: who })

  // CircleXO as a ready-to-deploy product or catalogue stack, when the team has one.
  const circlexo = products.find((p) => /circle\s*xo/i.test(`${p.name} ${p.title ?? ""}`)) ?? null
  const appCounts = {
    all: apps?.length ?? 0,
    running: stats.appsRunning,
    failed: stats.appsFailed,
    stopped: (apps ?? []).filter((a) => a.status === "stopped").length,
  }
  const q = appQuery.trim().toLowerCase()
  const shownApps = (apps ?? [])
    .filter((a) =>
      appFilter === "all"
        ? true
        : appFilter === "running"
          ? a.status === "running" || a.imported_state === "running"
          : a.status === appFilter,
    )
    .filter(
      (a) =>
        !q ||
        [a.name, a.title, a.description, a.server_name, ...(a.domains ?? []).map((d) => d.hostname)].some((v) =>
          v?.toLowerCase().includes(q),
        ),
    )
    // Most recently deployed first; never-deployed apps last, newest first.
    .sort((a, b) => appActivity(b) - appActivity(a))
    .slice(0, 8)

  // A server's last activity: the newest deploy of an app on it, else its last heartbeat.
  const serverActivity = new Map<string, number>()
  for (const a of apps ?? []) serverActivity.set(a.server_id, Math.max(serverActivity.get(a.server_id) ?? 0, appActivity(a)))
  const activeAt = (h: Server) => serverActivity.get(h.id) || Date.parse(h.enrolled_at ?? h.created_at) || 0
  const sq = serverQuery.trim().toLowerCase()
  const shownServers = (hosts ?? [])
    .filter((h) => !sq || [h.name, h.hostname, h.ip_address, h.facts?.os_pretty].some((v) => v?.toLowerCase().includes(sq)))
    .sort((a, b) => activeAt(b) - activeAt(a) || a.name.localeCompare(b.name))
    .slice(0, 4)

  return (
    <div className="flex flex-col gap-6 px-6 py-6">
      {/* The greeting, with no hairline under it: on the home page the cards carry the structure. */}
      <div className="flex flex-col gap-1.5">
        <SectionHeader
          as="h1"
          title={<span dir="auto">{greeting}</span>}
          description={t("home3.lede")}
          action={
            <div className="flex flex-wrap items-center gap-4">
              <DeployButton />
            </div>
          }
        />
      </div>

      {/* What is running, and is it healthy? */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Stat
          href={`${base}/apps`}
          icon={<AppWindowIcon />}
          label={t("nav.apps")}
          value={loading ? null : stats.apps}
          tone={stats.appsFailed ? "warn" : "ok"}
          detail={stats.appsFailed ? t("home2.failing", { n: stats.appsFailed }) : t("home2.running", { n: stats.appsRunning })}
        />
        <Stat
          href={`${base}/servers`}
          icon={<ServerIcon />}
          label={t("nav.servers")}
          value={loading ? null : stats.servers}
          tone={stats.serversOffline ? "warn" : "ok"}
          detail={stats.serversOffline ? t("home2.offline", { n: stats.serversOffline }) : t("home2.online", { n: stats.serversOnline })}
        />
        <Stat
          href={`${base}/databases`}
          icon={<DatabaseIcon />}
          label={t("nav.databases")}
          value={services === null ? null : stats.dbs}
          tone={stats.dbsFailed ? "warn" : "ok"}
          detail={stats.dbsFailed ? t("home2.failing", { n: stats.dbsFailed }) : t("home2.running", { n: stats.dbsRunning })}
        />
        <Stat
          href={`${base}/domains`}
          icon={<GlobeIcon />}
          label={t("nav.domains")}
          value={loading ? null : stats.domains}
          tone={stats.domainsPending ? "warn" : "ok"}
          detail={stats.domainsPending ? t("home2.awaitingDns", { n: stats.domainsPending }) : t("home2.allVerified")}
        />
      </div>

      {/* Servers and apps on the main column; live activity, the part people watch, is the widget at the start side. */}
      <div className="grid items-start gap-6 xl:grid-cols-[26rem_minmax(0,1fr)]">
        <aside className="min-w-0 xl:sticky xl:top-4">
          <ActivityFeed team={team.slug} />
        </aside>
        <div className="flex min-w-0 flex-col gap-6">
          {/* The team's servers, most recently active first, two to a row; search reaches every one. */}
          <section className="flex flex-col gap-4" aria-label={t("nav.servers")}>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <Link href={`${base}/servers`} className="flex items-center gap-1 text-body-sm text-nq-brand hover:underline">
                {t("home3.allServers", { n: hosts?.length ?? 0 })}
                <ArrowRightIcon className="size-3.5 rtl:-scale-x-100" />
              </Link>
              <div className="relative w-full sm:w-64">
                <SearchIcon className="pointer-events-none absolute start-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={serverQuery}
                  onChange={(e) => setServerQuery(e.target.value)}
                  placeholder={t("home3.searchServers")}
                  aria-label={t("home3.searchServers")}
                  className="rounded-xl ps-9"
                />
              </div>
            </div>
            {hosts === null ? (
              <div className="grid gap-4 lg:max-xl:grid-cols-2 2xl:grid-cols-2">
                {[0, 1].map((i) => (
                  <ServerCardSkeleton key={i} />
                ))}
              </div>
            ) : hosts.length === 0 ? (
              <p className="rounded-2xl border border-dashed border-border px-4 py-4 text-body-sm text-muted-foreground">
                {t("home3.noServers")}
              </p>
            ) : shownServers.length === 0 ? (
              <p className="rounded-2xl border border-dashed border-border px-4 py-6 text-center text-body-sm text-muted-foreground">
                {t("home3.noServersMatch")}
              </p>
            ) : (
              <div className="grid items-start gap-4 lg:max-xl:grid-cols-2 2xl:grid-cols-2">
                {shownServers.map((h) => (
                  <DashboardServerCard key={h.id} server={h} base={base} cpu={sparks[h.id]?.cpu} onPower={noPower} />
                ))}
              </div>
            )}
          </section>

          {/* The team's apps, like the reference's projects panel: filter chips, search, cards. */}
          <section className="flex flex-col gap-4" aria-label={t("home3.yourApps")}>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <ChipGroup value={appFilter} onValueChange={(v) => setAppFilter(v as AppFilter)} aria-label={t("home3.filterApps")}>
                {(["all", "running", "failed", "stopped"] as const).map((f) => (
                  <Chip key={f} value={f} className="rounded-full">
                    {t(`home3.appFilter.${f}`)}
                    <span className="ms-1.5 text-caption tabular-nums opacity-70">{appCounts[f]}</span>
                  </Chip>
                ))}
              </ChipGroup>
              <div className="relative w-full sm:w-64">
                <SearchIcon className="pointer-events-none absolute start-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  value={appQuery}
                  onChange={(e) => setAppQuery(e.target.value)}
                  placeholder={t("home3.searchApps")}
                  aria-label={t("home3.searchApps")}
                  className="rounded-xl ps-9"
                />
              </div>
            </div>
            {apps === null ? (
              <div className="grid gap-3 sm:grid-cols-2 2xl:grid-cols-3">
                {[0, 1, 2, 3].map((i) => (
                  <AppCardSkeleton key={i} />
                ))}
              </div>
            ) : apps.length === 0 ? (
              <div className="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-dashed border-border px-4 py-4 text-body-sm text-muted-foreground">
                {t("apps2.emptyBody")}
                <DeployButton />
              </div>
            ) : shownApps.length === 0 ? (
              <p className="rounded-2xl border border-dashed border-border px-4 py-6 text-center text-body-sm text-muted-foreground">
                {t("home3.noAppsMatch")}
              </p>
            ) : (
              <div className="grid gap-3 sm:grid-cols-2 2xl:grid-cols-3">
                {shownApps.map((a) => (
                  <DashboardAppCard key={a.id} app={a} base={base} load={loads[a.id]} />
                ))}
              </div>
            )}
          </section>
        </div>
      </div>

      {circlexo ? (
        <Card className="flex-row flex-wrap items-center gap-4 rounded-2xl bg-gradient-to-br from-nq-brand/15 to-card p-5">
          <RocketIcon className="size-6 shrink-0 text-nq-brand" />
          <div className="min-w-0 flex-1">
            <p className="font-medium text-foreground">{t("home3.circlexoTitle")}</p>
            <p className="text-body-sm text-muted-foreground">{t("home2.productBody")}</p>
          </div>
          <Link href={`${base}/products`} className="flex items-center gap-1 text-body-sm text-nq-brand hover:underline">
            {t("home3.browse")}
            <ArrowRightIcon className="size-3.5 rtl:-scale-x-100" />
          </Link>
        </Card>
      ) : null}
    </div>
  )
}
