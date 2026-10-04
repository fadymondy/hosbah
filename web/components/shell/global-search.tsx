"use client"

import { useRouter } from "next/navigation"
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import { AppWindowIcon, DatabaseIcon, GlobeIcon, RocketIcon, SearchIcon, ServerIcon } from "lucide-react"

import type { FeedItem } from "@/components/home/deployments-feed"
import { ServerMark } from "@/components/server-mark"
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog"
import { apiFetch } from "@/lib/api"
import type { App } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { useOptionalTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

type ProxyHost = { id: string; hostname: string; upstream: string; server_name?: string }

type Index = {
  apps: App[]
  servers: Server[]
  services: DataService[]
  proxies: ProxyHost[]
  deployments: FeedItem[]
}

type Hit = { key: string; group: string; href: string; icon: ReactNode; title: string; meta?: string; ltr?: boolean }

/** Every word of the query appears in one of the fields. */
function hit(q: string, ...fields: (string | null | undefined)[]) {
  const hay = fields.filter(Boolean).join(" ").toLowerCase()
  return q
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((w) => hay.includes(w))
}

/**
 * The team-wide search (⌘K / Ctrl K): apps, servers, domains, databases and recent deployments,
 * loaded once when it first opens and filtered as you type. Arrow keys move, Enter opens.
 */
export function GlobalSearch() {
  const { t, locale } = useI18n()
  const teamCtx = useOptionalTeam()
  const router = useRouter()
  const slug = teamCtx?.team.slug
  const base = slug ? `/${locale}/t/${slug}` : ""
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState("")
  const [index, setIndex] = useState<Index | null>(null)
  const [cursor, setCursor] = useState(0)
  const listRef = useRef<HTMLUListElement>(null)

  useEffect(() => setIndex(null), [slug])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault()
        setOpen((o) => !o)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  useEffect(() => {
    if (!open || !slug || index) return
    const api = `/api/teams/${slug}`
    const get = <T,>(path: string, fallback: T) => apiFetch<T>(path).catch(() => fallback)
    Promise.all([
      get<{ apps: App[] }>(`${api}/apps`, { apps: [] }),
      get<{ servers: Server[] }>(`${api}/servers`, { servers: [] }),
      get<{ services: DataService[] }>(`${api}/data-services`, { services: [] }),
      get<{ proxy_hosts?: ProxyHost[]; hosts?: ProxyHost[] }>(`${api}/proxy-hosts`, {}),
      get<{ deployments: FeedItem[] }>(`${api}/deployments?limit=30`, { deployments: [] }),
    ]).then(([a, s, d, p, f]) =>
      setIndex({
        apps: a.apps ?? [],
        servers: s.servers ?? [],
        services: d.services ?? [],
        proxies: p.proxy_hosts ?? p.hosts ?? [],
        deployments: f.deployments ?? [],
      }),
    )
  }, [open, slug, index])

  const hits = useMemo<Hit[]>(() => {
    if (!index) return []
    const query = q.trim()
    const cap = query ? 8 : 4
    const appHref = (name: string) => `${base}/apps/${encodeURIComponent(name)}`
    const apps: Hit[] = index.apps
      .filter((a) => !query || hit(query, a.name, a.title, a.server_name, a.repo_url, ...(a.domains ?? []).map((d) => d.hostname)))
      .slice(0, cap)
      .map((a) => ({
        key: `app:${a.id}`,
        group: t("nav.apps"),
        href: appHref(a.name),
        icon: <ServerMark server={{ color: a.color, icon: a.icon ?? "globe", logo_url: a.logo_url }} size={6} />,
        title: a.title || a.name,
        meta: [a.title ? a.name : null, a.server_name].filter(Boolean).join(" · "),
      }))
    const servers: Hit[] = index.servers
      .filter((s) => !query || hit(query, s.name, s.hostname, s.ip_address, s.os))
      .slice(0, cap)
      .map((s) => ({
        key: `server:${s.id}`,
        group: t("nav.servers"),
        href: `${base}/servers/${s.id}`,
        icon: <ServerMark server={s} size={6} />,
        title: s.name,
        meta: s.ip_address ?? s.hostname ?? undefined,
        ltr: true,
      }))
    const domains: Hit[] = [
      ...index.apps.flatMap((a) =>
        (a.domains ?? []).map((d) => ({ key: `domain:${d.id}`, host: d.hostname, href: appHref(a.name), meta: a.title || a.name })),
      ),
      ...index.proxies.map((p) => ({ key: `proxy:${p.id}`, host: p.hostname, href: `${base}/domains?q=${encodeURIComponent(p.hostname)}`, meta: p.upstream })),
    ]
      .filter((d) => (query ? hit(query, d.host, d.meta) : false))
      .slice(0, cap)
      .map((d) => ({ key: d.key, group: t("nav.domains"), href: d.href, icon: <GlobeIcon />, title: d.host, meta: d.meta, ltr: true }))
    const dbs: Hit[] = index.services
      .filter((s) => (query ? hit(query, s.name, s.kind, s.version, s.host) : false))
      .slice(0, cap)
      .map((s) => ({
        key: `db:${s.id}`,
        group: t("nav.databases"),
        href: `${base}/databases/${s.id}`,
        icon: <DatabaseIcon />,
        title: s.name,
        meta: `${s.kind} ${s.version}`,
        ltr: true,
      }))
    const deps: Hit[] = index.deployments
      .filter((d) => (query ? hit(query, d.app.name, d.commit_sha, d.commit_message, d.branch, String(d.number)) : true))
      .slice(0, query ? cap : 3)
      .map((d) => ({
        key: `dep:${d.id}`,
        group: t("nav.deployments"),
        href: `${base}/apps/${encodeURIComponent(d.app.name)}/deployments?d=${d.id}`,
        icon: <RocketIcon />,
        title: `${d.app.name} #${d.number}`,
        meta: [t(`apps.deploy.statuses.${d.status}`, undefined, d.status), d.commit_message].filter(Boolean).join(" · "),
      }))
    return [...apps, ...servers, ...domains, ...dbs, ...deps]
  }, [index, q, base, t])

  useEffect(() => setCursor(0), [q])

  const go = useCallback(
    (h: Hit | undefined) => {
      if (!h) return
      setOpen(false)
      setQ("")
      router.push(h.href)
    },
    [router],
  )

  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>(`[data-index="${cursor}"]`)?.scrollIntoView({ block: "nearest" })
  }, [cursor])

  if (!slug) return null

  const groups: string[] = []
  for (const h of hits) if (!groups.includes(h.group)) groups.push(h.group)

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-label={t("search.open")}
        className="flex h-9 items-center gap-2 border border-line px-2.5 text-sm text-grid-muted transition-colors hover:bg-grid-soft hover:text-grid-fg sm:min-w-56"
      >
        <SearchIcon className="size-4 shrink-0" />
        <span className="hidden flex-1 text-start sm:inline">{t("search.placeholder")}</span>
        <kbd className="hidden border border-line px-1.5 font-mono text-[10px] lg:inline" dir="ltr">
          Ctrl K
        </kbd>
      </button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent showCloseButton={false} className="top-[15%] translate-y-0 gap-0 p-0 sm:max-w-xl">
          <DialogTitle className="sr-only">{t("search.title")}</DialogTitle>
          <div className="flex items-center gap-2 border-b border-line px-3">
            <SearchIcon className="size-4 shrink-0 text-grid-muted" />
            <input
              autoFocus
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "ArrowDown") {
                  e.preventDefault()
                  setCursor((c) => Math.min(c + 1, hits.length - 1))
                } else if (e.key === "ArrowUp") {
                  e.preventDefault()
                  setCursor((c) => Math.max(c - 1, 0))
                } else if (e.key === "Enter") {
                  e.preventDefault()
                  go(hits[cursor])
                }
              }}
              placeholder={t("search.placeholder")}
              aria-label={t("search.title")}
              className="h-12 flex-1 bg-transparent text-sm outline-none placeholder:text-grid-muted"
            />
          </div>
          <ul ref={listRef} role="listbox" aria-label={t("search.title")} className="max-h-[60vh] overflow-y-auto py-1">
            {!index ? (
              <li className="px-4 py-6 text-center text-sm text-grid-muted">{t("common.loading")}</li>
            ) : hits.length === 0 ? (
              <li className="px-4 py-6 text-center text-sm text-grid-muted">{t("search.none")}</li>
            ) : (
              groups.map((g) => (
                <li key={g} role="presentation">
                  <p className="grid-micro px-4 pt-3 pb-1">{g}</p>
                  <ul role="presentation">
                    {hits.map((h, i) =>
                      h.group !== g ? null : (
                        <li
                          key={h.key}
                          role="option"
                          aria-selected={i === cursor}
                          data-index={i}
                          onMouseEnter={() => setCursor(i)}
                          onClick={() => go(h)}
                          className={cn(
                            "flex cursor-pointer items-center gap-3 px-4 py-2 text-sm [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-grid-muted",
                            i === cursor && "bg-grid-soft",
                          )}
                        >
                          {h.icon}
                          <span dir={h.ltr ? "ltr" : "auto"} className="min-w-0 truncate font-medium text-grid-fg">
                            {h.title}
                          </span>
                          {h.meta ? (
                            <span dir="auto" className="ms-auto min-w-0 truncate text-xs text-grid-muted">
                              {h.meta}
                            </span>
                          ) : null}
                        </li>
                      ),
                    )}
                  </ul>
                </li>
              ))
            )}
          </ul>
          <div className="flex flex-wrap gap-x-4 gap-y-1 border-t border-line px-4 py-2 text-xs text-grid-muted">
            <span className="flex items-center gap-1">
              <AppWindowIcon className="size-3.5" /> {t("nav.apps")}
            </span>
            <span className="flex items-center gap-1">
              <ServerIcon className="size-3.5" /> {t("nav.servers")}
            </span>
            <span className="flex items-center gap-1">
              <GlobeIcon className="size-3.5" /> {t("nav.domains")}
            </span>
            <span className="flex items-center gap-1">
              <DatabaseIcon className="size-3.5" /> {t("nav.databases")}
            </span>
            <span className="ms-auto">{t("search.hint")}</span>
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}
