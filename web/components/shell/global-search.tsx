"use client"

import { type Command, CommandPalette, SearchTrigger, useCommandPaletteOpen, useRegisterCommands } from "@fadymondy/nasaq/web"
import { useRouter } from "next/navigation"
import { useEffect, useMemo, useState } from "react"
import { DatabaseIcon, GlobeIcon, LoaderCircleIcon, RocketIcon } from "lucide-react"

import type { FeedItem } from "@/components/home/deployments-feed"
import { ServerMark } from "@/components/server-mark"
import { apiFetch } from "@/lib/api"
import type { App } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { useOptionalTeam } from "@/lib/team"

type ProxyHost = { id: string; hostname: string; upstream: string; server_name?: string }

type Index = {
  apps: App[]
  servers: Server[]
  services: DataService[]
  proxies: ProxyHost[]
  deployments: FeedItem[]
}

/**
 * The team-wide search (⌘K / Ctrl K) on Nasaq's CommandPalette: apps, servers, domains, databases and
 * recent deployments, loaded once when it first opens and registered as commands, so the palette's own
 * filtering, keyboard handling and RTL apply. The trigger and the palette share the shell's open state.
 */
export function GlobalSearch() {
  const { t, locale } = useI18n()
  const teamCtx = useOptionalTeam()
  const router = useRouter()
  const slug = teamCtx?.team.slug
  const base = slug ? `/${locale}/t/${slug}` : ""
  const [open] = useCommandPaletteOpen()
  const [index, setIndex] = useState<Index | null>(null)

  useEffect(() => setIndex(null), [slug])

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

  const commands = useMemo<Command[] | null>(() => {
    // Until the API answers, one disabled row with a spinner heads the palette, so it never looks
    // empty or shows only the shell's own commands.
    if (!index)
      return open
        ? [
            {
              id: "hosbah.loading",
              label: t("search.loading"),
              section: "hosbah.loading",
              sectionLabel: t("search.title"),
              sectionOrder: 0,
              icon: <LoaderCircleIcon className="animate-spin motion-reduce:animate-none" aria-hidden />,
              disabled: true,
              perform: () => {},
            },
          ]
        : null
    const go = (href: string) => () => router.push(href)
    const appHref = (name: string) => `${base}/apps/${encodeURIComponent(name)}`
    const section = (id: string, label: string, order: number) => ({ section: `hosbah.${id}`, sectionLabel: label, sectionOrder: order })
    const apps: Command[] = index.apps.map((a) => ({
      id: `hosbah.app.${a.id}`,
      label: a.title || a.name,
      ...section("apps", t("nav.apps"), 0.1),
      icon: <ServerMark server={{ color: a.color, icon: a.icon ?? "globe", logo_url: a.logo_url }} size={6} />,
      keywords: [a.name, a.server_name, a.repo_url, ...(a.domains ?? []).map((d) => d.hostname)].filter(Boolean) as string[],
      hint: [a.title ? a.name : null, a.server_name].filter(Boolean).join(" · ") || undefined,
      perform: go(appHref(a.name)),
    }))
    const servers: Command[] = index.servers.map((s) => ({
      id: `hosbah.server.${s.id}`,
      label: s.name,
      ...section("servers", t("nav.servers"), 0.2),
      icon: <ServerMark server={s} size={6} />,
      keywords: [s.hostname, s.ip_address, s.os].filter(Boolean) as string[],
      hint: <span dir="ltr">{s.ip_address ?? s.hostname ?? ""}</span>,
      perform: go(`${base}/servers/${s.id}`),
    }))
    const domains: Command[] = [
      ...index.apps.flatMap((a) =>
        (a.domains ?? []).map((d) => ({ id: `domain.${d.id}`, host: d.hostname, href: appHref(a.name), meta: a.title || a.name })),
      ),
      ...index.proxies.map((p) => ({ id: `proxy.${p.id}`, host: p.hostname, href: `${base}/domains?q=${encodeURIComponent(p.hostname)}`, meta: p.upstream })),
    ].map((d) => ({
      id: `hosbah.${d.id}`,
      label: d.host,
      ...section("domains", t("nav.domains"), 0.3),
      icon: GlobeIcon,
      keywords: [d.meta],
      hint: d.meta,
      priority: -1,
      perform: go(d.href),
    }))
    const dbs: Command[] = index.services.map((s) => ({
      id: `hosbah.db.${s.id}`,
      label: s.name,
      ...section("databases", t("nav.databases"), 0.4),
      icon: DatabaseIcon,
      keywords: [s.kind, s.version, s.host].filter(Boolean) as string[],
      hint: `${s.kind} ${s.version}`,
      perform: go(`${base}/databases/${s.id}`),
    }))
    const deps: Command[] = index.deployments.map((d) => ({
      id: `hosbah.deployment.${d.id}`,
      label: `${d.app.name} #${d.number}`,
      ...section("deployments", t("nav.deployments"), 0.5),
      icon: RocketIcon,
      keywords: [d.commit_sha, d.commit_message, d.branch].filter(Boolean) as string[],
      hint: [t(`apps.deploy.statuses.${d.status}`, undefined, d.status), d.commit_message].filter(Boolean).join(" · "),
      perform: go(`${base}/apps/${encodeURIComponent(d.app.name)}/deployments?d=${d.id}`),
    }))
    return [...apps, ...servers, ...domains, ...dbs, ...deps]
  }, [index, open, base, router, t])

  useRegisterCommands(commands)

  if (!slug) return null

  return (
    <>
      <SearchTrigger label={t("search.placeholder")} />
      <CommandPalette
        placeholder={t("search.placeholder")}
        emptyLabel={index ? t("search.none") : t("common.loading")}
        labels={{ title: t("search.title") }}
      />
    </>
  )
}
