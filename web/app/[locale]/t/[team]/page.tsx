"use client"

import Link from "next/link"
import { useEffect, useState } from "react"
import { BoxesIcon, DatabaseIcon, KeyRoundIcon, NetworkIcon, PlusIcon, ServerIcon, UsersIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { apiFetch, type Member } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import type { App } from "@/lib/apps"
import type { Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

type Loaded<T> = T | null | "error"

export default function OverviewPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.overview")} — ${team.name}`)
  const [members, setMembers] = useState<Loaded<number>>(null)
  const [servers, setServers] = useState<Loaded<Server[]>>(null)
  const [apps, setApps] = useState<Loaded<App[]>>(null)

  useEffect(() => {
    apiFetch<{ members: Member[] }>(`/api/teams/${team.slug}/members`)
      .then((r) => setMembers(r.members?.length ?? 0))
      .catch(() => setMembers("error"))
    apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`)
      .then((r) => setServers(r.servers ?? []))
      .catch(() => setServers("error"))
    apiFetch<{ apps: App[] }>(`/api/teams/${team.slug}/apps`)
      .then((r) => setApps(r.apps ?? []))
      .catch(() => setApps("error"))
  }, [team.slug])

  const base = `/${locale}/t/${team.slug}`
  const figures: [string, Loaded<number>, string?][] = [
    [
      t("overview.servers"),
      Array.isArray(servers) ? servers.length : servers,
      Array.isArray(servers) ? `${servers.filter((s) => s.online).length} ${t("overview.online")}` : undefined,
    ],
    [
      t("overview.apps"),
      Array.isArray(apps) ? apps.length : apps,
      Array.isArray(apps) ? `${apps.filter((a) => a.status === "running").length} ${t("overview.running")}` : undefined,
    ],
    [t("overview.members"), members],
  ]

  return (
    <PageBody
      eyebrow={t("nav.overview")}
      title={team.name}
      actions={<Badge variant="outline">{t(`roles.${team.role}`)}</Badge>}
      bleed
    >
      {/* The "in development" notice: a hairline-framed hatched band with the message on a card. */}
      <div className="grid-hatch border-b border-line p-3" role="status">
        <div className="border border-line bg-grid-card px-5 py-4">
          <p className="grid-micro mb-2 text-grid-brand">{t("overview.devBadge")}</p>
          <p className="text-base font-medium text-grid-fg">{t("auth.devNotice")}</p>
          <p className="grid-body mt-1 text-sm">{t("shell.devBanner")}</p>
        </div>
      </div>

      <div className="flex flex-wrap gap-2 px-6 py-6">
        {manage ? (
          <Button nativeButton={false} render={<Link href={`${base}/servers/new`} />}>
            <PlusIcon />
            {t("servers.add")}
          </Button>
        ) : null}
        <Button variant="outline" nativeButton={false} render={<Link href={`${base}/apps`} />}>
          <BoxesIcon />
          {t("nav.apps")}
        </Button>
        <Button variant="outline" nativeButton={false} render={<Link href={`${base}/servers`} />}>
          <ServerIcon />
          {t("nav.servers")}
        </Button>
        <Button variant="outline" nativeButton={false} render={<Link href={`${base}/databases`} />}>
          <DatabaseIcon />
          {t("nav.databases")}
        </Button>
        <Button variant="outline" nativeButton={false} render={<Link href={`${base}/proxy-hosts`} />}>
          <NetworkIcon />
          {t("nav.proxyHosts")}
        </Button>
        <Button variant="outline" nativeButton={false} render={<Link href={`${base}/members`} />}>
          <UsersIcon />
          {t("nav.members")}
        </Button>
        <Button variant="outline" nativeButton={false} render={<Link href={`${base}/tokens`} />}>
          <KeyRoundIcon />
          {t("nav.tokens")}
        </Button>
      </div>

      <dl className="grid grid-cols-1 divide-y divide-line border-y border-line text-sm sm:grid-cols-4 sm:divide-x sm:divide-y-0 rtl:sm:divide-x-reverse">
        {figures.map(([label, value, note]) => (
          <div key={label} className="px-6 py-4">
            <dt className="grid-micro mb-1">{label}</dt>
            <dd className="flex items-baseline gap-2 text-grid-fg">
              {value === null ? (
                <Skeleton className="h-6 w-10" />
              ) : (
                <>
                  <span className="font-mono text-xl tabular-nums">{value === "error" ? "—" : value}</span>
                  {note ? <span className="text-xs text-grid-muted">{note}</span> : null}
                </>
              )}
            </dd>
          </div>
        ))}
        <div className="px-6 py-4">
          <dt className="grid-micro mb-1">{t("overview.slug")}</dt>
          <dd dir="ltr" className="text-start font-mono text-grid-fg">
            /{team.slug}
          </dd>
        </div>
      </dl>
    </PageBody>
  )
}
