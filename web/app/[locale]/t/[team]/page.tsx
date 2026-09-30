"use client"

import { Button, EmptyState, Input } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { PlusIcon, SearchIcon, ServerIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { DeploymentsFeed, type FeedItem } from "@/components/home/deployments-feed"
import { ServerCard, type Spark, type TunnelSummary } from "@/components/home/server-card"
import { LoadingRows, SectionTitle } from "@/components/states"



import { apiFetch } from "@/lib/api"
import type { App, Deployment } from "@/lib/apps"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

const PAGE = 8

/** A small segmented control for one filter. */
function FilterPills<T extends string>({ value, onChange, options }: { value: T; onChange: (v: T) => void; options: [T, string][] }) {
  return (
    <div role="radiogroup" className="flex border border-border">
      {options.map(([v, label]) => (
        <button
          key={v || "all"}
          type="button"
          role="radio"
          aria-checked={value === v}
          onClick={() => onChange(v)}
          className={
            value === v
              ? "bg-secondary px-2.5 py-1 text-xs font-medium text-foreground"
              : "px-2.5 py-1 text-xs text-muted-foreground hover:bg-secondary hover:text-foreground"
          }
        >
          {label}
        </button>
      ))}
    </div>
  )
}

type TunnelRow = { server_id: string; kind: string; status: string; address: string | null }

/** Calls an endpoint that newer APIs have; null (not an error) when this API does not have it yet. */
async function optional<T>(path: string): Promise<T | null> {
  try {
    return await apiFetch<T>(path)
  } catch {
    return null
  }
}

/**
 * The team's home: every server as a card (status, address, tunnel, hardware, live usage and the
 * last hour as charts, what shipped to it last), then the latest deployments across all apps.
 * Refreshes every 30 s while open.
 */
export default function HomePage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.overview")} — ${team.name}`)
  const base = `/${locale}/t/${team.slug}`
  const api = `/api/teams/${team.slug}`

  const [servers, setServers] = useState<Server[] | null>(null)
  const [apps, setApps] = useState<App[]>([])
  const [tunnels, setTunnels] = useState<TunnelRow[]>([])
  const [spark, setSpark] = useState<Record<string, Spark>>({})
  const [feed, setFeed] = useState<FeedItem[] | null>(null)

  const load = useCallback(async () => {
    const [s, a, tn, sp, fd] = await Promise.all([
      optional<{ servers: Server[] }>(`${api}/servers`),
      optional<{ apps: App[] }>(`${api}/apps`),
      optional<{ tunnels: TunnelRow[] }>(`${api}/tunnels`),
      optional<{ servers: Record<string, Spark> }>(`${api}/servers/metrics?range=1h`),
      optional<{ deployments: FeedItem[] }>(`${api}/deployments?limit=20`),
    ])
    setServers(s?.servers ?? [])
    setApps(a?.apps ?? [])
    setTunnels(tn?.tunnels ?? [])
    setSpark(sp?.servers ?? {})
    setFeed(fd?.deployments ?? null)
  }, [api])

  useEffect(() => {
    void load()
    const id = setInterval(load, 30_000)
    return () => clearInterval(id)
  }, [load])

  // Per server: its tunnel (an active one first) and the most recent deploy of any app on it.
  const tunnelOf = useMemo(() => {
    const m: Record<string, TunnelSummary> = {}
    for (const tn of tunnels) {
      const cur = m[tn.server_id]
      if (!cur || (tn.status === "active" && cur.status !== "active")) m[tn.server_id] = tn
    }
    return m
  }, [tunnels])

  const lastDeployOf = useMemo(() => {
    const m: Record<string, { app: App; deployment: Deployment }> = {}
    for (const app of apps) {
      const d = app.latest_deployment
      if (!d) continue
      const cur = m[app.server_id]
      if (!cur || d.created_at > cur.deployment.created_at) m[app.server_id] = { app, deployment: d }
    }
    return m
  }, [apps])

  // The feed, or (on an API without it yet) each app's latest deployment, newest first.
  const items: FeedItem[] = useMemo(() => {
    if (feed) return feed
    return apps
      .filter((a) => a.latest_deployment)
      .map((a) => {
        const d = a.latest_deployment as Deployment
        return {
          id: d.id,
          number: d.number,
          app: { id: a.id, name: a.name },
          status: d.status,
          branch: d.ref || a.branch,
          commit_sha: d.commit_sha,
          commit_message: d.commit_message,
          trigger: d.trigger,
          created_at: d.created_at,
        }
      })
      .sort((x, y) => (x.created_at < y.created_at ? 1 : -1))
  }, [feed, apps])


  // Nodes and their enrolled guest servers (MH-574 A1): a guest with a parent that is also one of
  // the team's servers nests under its node instead of appearing as an unrelated row.
  const byId = useMemo(() => {
    const m: Record<string, Server> = {}
    for (const s of servers ?? []) m[s.id] = s
    return m
  }, [servers])
  const childrenOf = useMemo(() => {
    const m: Record<string, Server[]> = {}
    for (const s of servers ?? []) {
      if (s.parent_server_id && byId[s.parent_server_id]) {
        ;(m[s.parent_server_id] ??= []).push(s)
      }
    }
    return m
  }, [servers, byId])
  const isNestedGuest = (s: Server) => Boolean(s.parent_server_id && byId[s.parent_server_id])
  // A node's guests (the ones it reports, plus any Hosbah is still creating on it) are a count on
  // its card that opens its Guests tab: they are never servers of their own on the home page.
  const guestsOf = (s: Server) => {
    const reported = s.guests ?? []
    const ids = new Set(reported.map((g) => g.server_id).filter(Boolean))
    const pending = (childrenOf[s.id] ?? []).filter((c) => !ids.has(c.id))
    const total = reported.length + pending.length
    if (!total) return null
    const running = reported.filter((g) => /^(running|active|up)/i.test(g.status)).length + pending.filter((c) => c.online).length
    return { total, running }
  }

  // Search and filters over the servers; the list grows as you scroll (PAGE at a time).
  const [q, setQ] = useState("")
  const [state, setState] = useState<"" | "online" | "offline">("")
  const [provider, setProvider] = useState<"" | "proxmox" | "byo">("")
  const [tunnelled, setTunnelled] = useState(false)
  const [shown, setShown] = useState(PAGE)
  const matches = useCallback(
    (s: Server) => {
      const needle = q.trim().toLowerCase()
      if (state === "online" && !s.online) return false
      if (state === "offline" && s.online) return false
      if (provider && s.provider !== provider) return false
      if (tunnelled && !tunnelOf[s.id]) return false
      if (!needle) return true
      return [s.name, s.ip_address, s.hostname, s.os, s.facts?.os_pretty].some((v) => v?.toLowerCase().includes(needle))
    },
    [q, state, provider, tunnelled, tunnelOf],
  )
  const filtered = useMemo(() => {
    // Top-level rows only: a nested guest never shows up as its own row, only under its node — but
    // its node still shows if only the guest matches the filters.
    return (servers ?? []).filter((s) => !isNestedGuest(s) && (matches(s) || (childrenOf[s.id] ?? []).some(matches)))
  }, [servers, matches, childrenOf, byId])
  useEffect(() => setShown(PAGE), [q, state, provider, tunnelled])
  const sentinel = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = sentinel.current
    if (!el) return
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) setShown((n) => n + PAGE)
    }, { rootMargin: "400px" })
    io.observe(el)
    return () => io.disconnect()
  }, [filtered.length, shown])

  return (
    <PageBody
      eyebrow={t("nav.overview")}
      title={team.name}
      description={
        servers
          ? t("home.summary", {
              servers: servers.filter((s) => !isNestedGuest(s)).length,
              online: servers.filter((s) => !isNestedGuest(s) && s.online).length,
              apps: apps.length,
            })
          : undefined
      }
      actions={
        <>
          {manage ? (
            <Button variant="secondary" nativeButton={false} render={<Link href={`${base}/servers/new`} />}>
              <ServerIcon />
              {t("servers.add")}
            </Button>
          ) : null}
          <Button variant="primary" nativeButton={false} render={<Link href={`${base}/apps/new`} />}>
            <PlusIcon />
            {t("home.newApp")}
          </Button>
        </>
      }
      bleed
    >
      <SectionTitle title={t("home.servers")} hint={t("home.serversHint")} />
      {servers === null ? (
        <LoadingRows rows={3} />
      ) : servers.length === 0 ? (
        <EmptyState className="border-y" icon={ServerIcon} title={t("overview.serversTitle")} description={manage ? t("overview.serversHint") : t("common.readOnly")} actions={<>{manage ? (
            <Button variant="primary" nativeButton={false} render={<Link href={`${base}/servers/new`} />}>
              <PlusIcon />
              {t("servers.add")}
            </Button>
          ) : null}</>} />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2 border-b border-border px-6 py-3">
            <div className="relative w-full max-w-xs">
              <SearchIcon className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                type="search"
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder={t("home.search")}
                aria-label={t("home.search")}
                className="ps-8"
              />
            </div>
            <div className="ms-auto flex flex-wrap items-center gap-2">
              <FilterPills
                value={state}
                onChange={setState}
                options={[
                  ["", t("home.filterAll")],
                  ["online", t("home.online")],
                  ["offline", t("home.offline")],
                ]}
              />
              <FilterPills
                value={provider}
                onChange={setProvider}
                options={[
                  ["", t("home.anyProvider")],
                  ["proxmox", "Proxmox"],
                  ["byo", t("home.ownServers")],
                ]}
              />
              <Button variant={tunnelled ? "primary" : "secondary"} size="sm" onClick={() => setTunnelled((v) => !v)} aria-pressed={tunnelled}>
                {t("home.withTunnel")}
              </Button>
            </div>
          </div>
          {filtered.length === 0 ? (
            <p className="border-b border-border px-6 py-8 text-center text-sm text-muted-foreground">{t("home.noMatch")}</p>
          ) : (
            <div className="flex flex-col gap-4 px-6 pt-6 pb-6">
              {filtered.slice(0, shown).map((s) => (
                <ServerCard
                  key={s.id}
                  server={s}
                  base={base}
                  tunnel={s.tunnel ?? tunnelOf[s.id] ?? null}
                  spark={spark[s.id] ?? null}
                  lastDeploy={lastDeployOf[s.id] ?? null}
                  streamUrl={`${api}/servers/${s.id}/stream`}
                  guests={guestsOf(s)}
                />
              ))}
            </div>
          )}
          {shown < filtered.length ? <div ref={sentinel} className="h-8" aria-hidden /> : null}
        </>
      )}

      <SectionTitle
        title={t("home.latestDeployments")}
        className="border-t"
      />
      <div className="px-6 pt-4 pb-8">
        {items.length === 0 ? (
          <p className="border border-border bg-card px-4 py-6 text-center text-sm text-muted-foreground">{t("home.noDeploysYet")}</p>
        ) : (
          <DeploymentsFeed items={items} base={base} />
        )}
      </div>
    </PageBody>
  )
}
