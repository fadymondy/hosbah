"use client"

import { Badge, Card } from "@fadymondy/nasaq/web"
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
import { useInPanel, useServerHref, useTeam, useTeamBase } from "@/lib/team"

function appState(a: App) {
  if (isImported(a)) return a.imported_state ?? "stopped"
  if (a.latest_deployment && (a.latest_deployment.status === "queued" || a.latest_deployment.status === "running")) return "deploying"
  return a.status
}

function Column({ icon, title, href, count, empty, children }: { icon: React.ReactNode; title: string; href: string; count: number; empty: string; children: React.ReactNode }) {
  return (
    <Card className="min-w-0 gap-0 py-0">
      <Link
        href={href}
        className="flex items-center gap-2.5 border-b border-border px-4 py-3 text-sm font-semibold text-foreground hover:bg-nq-hover [&_svg]:size-4"
      >
        <span className="flex size-8 items-center justify-center rounded-md bg-secondary text-muted-foreground">{icon}</span>
        {title}
        <Badge variant="neutral" className="ms-auto tabular-nums">
          {count}
        </Badge>
      </Link>
      {count === 0 ? <p className="px-4 py-6 text-center text-caption text-muted-foreground">{empty}</p> : <ul className="divide-y divide-border">{children}</ul>}
    </Card>
  )
}

const MAX = 6

/** What runs on a server — apps, databases and proxy hosts — so the overview leads with workloads, not hardware. */
export function RunningHere({ serverId }: { serverId: string }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const base = useTeamBase()
  const srv = useServerHref()(serverId)
  // A customer's panel shows what they run, not the server's proxy hosts.
  const panel = useInPanel()
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
        <Link href={href} className="block px-4 py-2 text-xs text-nq-action hover:underline">
          {t("serverHere.more", { n: n - MAX })}
        </Link>
      </li>
    ) : null

  return (
    <section aria-labelledby="here-h">
      <SectionTitle id="here-h" title={t("serverHere.title")} />
      <div className={panel ? "mt-3 grid grid-cols-1 gap-4 md:grid-cols-2" : "mt-3 grid grid-cols-1 gap-4 md:grid-cols-3"}>
        <Column icon={<AppWindowIcon />} title={t("nav.apps")} href={`${srv}/apps`} count={apps.length} empty={t("serverHere.noApps")}>
          {apps.slice(0, MAX).map((a) => {
            const st = appState(a)
            return (
              <li key={a.id}>
                <Link href={`${base}/apps/${encodeURIComponent(a.name)}`} className="flex items-center gap-2 px-4 py-2.5 text-sm hover:bg-nq-hover">
                  <span dir="ltr" className="min-w-0 flex-1 truncate text-foreground">
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
                <Link href={`${base}/databases/${s.id}`} className="flex items-center gap-2 px-4 py-2.5 text-sm hover:bg-nq-hover">
                  <span dir="ltr" className="min-w-0 flex-1 truncate text-foreground">
                    {s.name} <span className="text-xs text-muted-foreground">{s.kind}</span>
                  </span>
                  <StatusDot tone={appTone(st)} label={t(`databases.statuses.${st}`, undefined, st)} />
                </Link>
              </li>
            )
          })}
          {more(dbs.length, `${srv}/databases`)}
        </Column>
        {panel ? null : (
        <Column icon={<GlobeIcon />} title={t("proxy.title")} href={`${srv}/proxy`} count={hosts.length} empty={t("serverHere.noProxy")}>
          {hosts.slice(0, MAX).map((h) => (
            <li key={h.id}>
              <Link href={`${srv}/proxy`} className="flex items-center gap-2 px-4 py-2.5 text-sm hover:bg-nq-hover">
                <span dir="ltr" className="min-w-0 flex-1 truncate text-foreground">
                  {h.hostname}
                </span>
                <StatusDot tone={h.enabled ? (h.verified || h.tls !== "auto" ? "ok" : "idle") : "idle"} label={h.enabled ? (h.verified || h.tls !== "auto" ? t("proxy.verified") : t("proxy.unverified")) : t("proxy.disabled")} />
              </Link>
            </li>
          ))}
          {more(hosts.length, `${srv}/proxy`)}
        </Column>
        )}
      </div>
    </section>
  )
}
