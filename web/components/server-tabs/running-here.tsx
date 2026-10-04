"use client"

import Link from "next/link"
import { useEffect, useState } from "react"
import { AppWindowIcon, DatabaseIcon, GlobeIcon } from "lucide-react"

import { appTone, StatusDot } from "@/components/lists/list-bar"
import { SectionTitle } from "@/components/states"
import { apiFetch } from "@/lib/api"
import { isImported, type App } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import type { ProxyHost } from "@/lib/servers"
import { useTeam } from "@/lib/team"

function appState(a: App) {
  if (isImported(a)) return a.imported_state ?? "stopped"
  if (a.latest_deployment && (a.latest_deployment.status === "queued" || a.latest_deployment.status === "running")) return "deploying"
  return a.status
}

function Column({ icon, title, href, count, empty, children }: { icon: React.ReactNode; title: string; href: string; count: number; empty: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col bg-grid-bg">
      <Link href={href} className="flex items-center gap-2 border-b border-line px-4 py-2.5 text-sm font-medium text-grid-fg hover:bg-grid-soft [&_svg]:size-4 [&_svg]:text-grid-muted">
        {icon}
        {title}
        <span className="ms-auto text-xs font-normal text-grid-muted">{count}</span>
      </Link>
      {count === 0 ? <p className="px-4 py-3 text-xs text-grid-muted">{empty}</p> : <ul className="divide-y divide-line">{children}</ul>}
    </div>
  )
}

const MAX = 6

/** What runs on a server — apps, databases and proxy hosts — so the overview leads with workloads, not hardware. */
export function RunningHere({ serverId }: { serverId: string }) {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const base = `/${locale}/t/${team.slug}`
  const srv = `${base}/servers/${serverId}`
  const [apps, setApps] = useState<App[] | null>(null)
  const [dbs, setDbs] = useState<DataService[]>([])
  const [hosts, setHosts] = useState<ProxyHost[]>([])

  useEffect(() => {
    let live = true
    void Promise.all([
      apiFetch<{ apps: App[] }>(`/api/teams/${team.slug}/apps`).catch(() => ({ apps: [] as App[] })),
      apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`).catch(() => ({ services: [] as DataService[] })),
      apiFetch<{ proxy_hosts: ProxyHost[] }>(`/api/teams/${team.slug}/proxy-hosts?server=${serverId}`).catch(() => ({ proxy_hosts: [] as ProxyHost[] })),
    ]).then(([a, d, p]) => {
      if (!live) return
      setApps((a.apps ?? []).filter((x) => x.server_id === serverId))
      setDbs((d.services ?? []).filter((x) => x.server_id === serverId))
      setHosts(p.proxy_hosts ?? [])
    })
    return () => {
      live = false
    }
  }, [team.slug, serverId])

  if (apps === null) return null
  const more = (n: number, href: string) =>
    n > MAX ? (
      <li>
        <Link href={href} className="block px-4 py-2 text-xs text-grid-action hover:underline">
          {t("serverHere.more", { n: n - MAX })}
        </Link>
      </li>
    ) : null

  return (
    <section aria-labelledby="here-h">
      <SectionTitle id="here-h" title={t("serverHere.title")} />
      <div className="grid grid-cols-1 gap-px border-b border-line bg-line md:grid-cols-3">
        <Column icon={<AppWindowIcon />} title={t("nav.apps")} href={`${srv}/apps`} count={apps.length} empty={t("serverHere.noApps")}>
          {apps.slice(0, MAX).map((a) => {
            const st = appState(a)
            return (
              <li key={a.id}>
                <Link href={`${base}/apps/${encodeURIComponent(a.name)}`} className="flex items-center gap-2 px-4 py-2 text-sm hover:bg-grid-soft">
                  <span dir="ltr" className="min-w-0 flex-1 truncate text-grid-fg">
                    {a.title || a.name}
                  </span>
                  <StatusDot tone={appTone(st)} label={t(`apps.statuses.${st}`, undefined, st)} />
                </Link>
              </li>
            )
          })}
          {more(apps.length, `${srv}/apps`)}
        </Column>
        <Column icon={<DatabaseIcon />} title={t("nav.databases")} href={`${srv}/databases`} count={dbs.length} empty={t("serverHere.noDatabases")}>
          {dbs.slice(0, MAX).map((s) => {
            const st = isImported(s) ? (s.imported_state ?? "stopped") : s.status
            return (
              <li key={s.id}>
                <Link href={`${base}/databases/${s.id}`} className="flex items-center gap-2 px-4 py-2 text-sm hover:bg-grid-soft">
                  <span dir="ltr" className="min-w-0 flex-1 truncate text-grid-fg">
                    {s.name} <span className="text-xs text-grid-muted">{s.kind}</span>
                  </span>
                  <StatusDot tone={appTone(st)} label={t(`databases.statuses.${st}`, undefined, st)} />
                </Link>
              </li>
            )
          })}
          {more(dbs.length, `${srv}/databases`)}
        </Column>
        <Column icon={<GlobeIcon />} title={t("proxy.title")} href={`${srv}/proxy`} count={hosts.length} empty={t("serverHere.noProxy")}>
          {hosts.slice(0, MAX).map((h) => (
            <li key={h.id}>
              <Link href={`${srv}/proxy`} className="flex items-center gap-2 px-4 py-2 text-sm hover:bg-grid-soft">
                <span dir="ltr" className="min-w-0 flex-1 truncate text-grid-fg">
                  {h.hostname}
                </span>
                <StatusDot tone={h.enabled ? (h.verified || h.tls !== "auto" ? "ok" : "idle") : "idle"} label={h.enabled ? (h.verified || h.tls !== "auto" ? t("proxy.verified") : t("proxy.unverified")) : t("proxy.disabled")} />
              </Link>
            </li>
          ))}
          {more(hosts.length, `${srv}/proxy`)}
        </Column>
      </div>
    </section>
  )
}
