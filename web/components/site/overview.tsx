"use client"

import Link from "next/link"
import { useEffect, useState, type ReactNode } from "react"
import { ChevronRightIcon, CloudIcon, CogIcon, DatabaseIcon, GlobeIcon, LayoutGridIcon, NetworkIcon, ServerIcon, UsersIcon } from "lucide-react"

import { DeploymentStatusBadge } from "@/components/app-status"
import { ImportedNotice, OriginFacts } from "@/components/imported"
import { WorkloadStats } from "@/components/imported-controls"
import { useLoadedSite } from "@/components/site/context"
import { SectionTitle } from "@/components/states"
import { Button } from "@/components/ui/button"
import { apiFetch } from "@/lib/api"
import { isImported, runtimeLabel, shortSha } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/** One stop on the request path; the whole node opens the section that manages it. */
function Node({
  href,
  icon,
  label,
  lines,
  tone = "plain",
}: {
  href: string
  icon: ReactNode
  label: string
  lines: ReactNode[]
  tone?: "plain" | "app"
}) {
  return (
    <Link
      href={href}
      className={cn(
        "group flex min-w-40 flex-1 flex-col gap-2 border bg-grid-card px-4 py-3 transition-colors hover:border-[var(--grid-brand)] hover:bg-grid-soft",
        tone === "app" ? "border-[var(--grid-brand)]" : "border-line",
      )}
    >
      <span className="flex items-center gap-2 text-xs text-grid-muted [&_svg]:size-4 [&_svg]:text-grid-brand">
        {icon}
        {label}
      </span>
      <span className="flex flex-col gap-0.5 text-sm text-grid-fg">
        {lines.map((l, i) => (
          <span key={i} dir="ltr" className="truncate text-start font-mono text-xs">
            {l}
          </span>
        ))}
      </span>
    </Link>
  )
}

const Arrow = () => <ChevronRightIcon aria-hidden className="hidden size-5 shrink-0 self-center text-grid-muted lg:block rtl:-scale-x-100" />

/**
 * The site's overview: how a request reaches it (visitors → DNS → Caddy → the app → its data, on
 * its server) as a clickable flow, then live usage and the facts about what is running.
 */
export function SiteOverview() {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const fmt = useFormatDate()
  const { detail, app, deployments, path } = useLoadedSite()
  const imported = isImported(app)
  const operate = canOperate(team.role)
  const base = `/${locale}/t/${team.slug}`
  const root = `${base}/apps/${encodeURIComponent(app.name)}`
  const [services, setServices] = useState<DataService[]>([])

  useEffect(() => {
    apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`)
      .then((r) => setServices((r.services ?? []).filter((s) => s.server_id === app.server_id)))
      .catch(() => setServices([]))
  }, [team.slug, app.server_id])

  const domains = detail.domains
  const live = detail.live_release
  const last = deployments[0]
  const none = t("site.flow.none")

  return (
    <>
      {imported ? <ImportedNotice kind="app" /> : null}
      <section aria-labelledby="flow-h">
        <SectionTitle
          id="flow-h"
          title={t("site.flow.title")}
          hint={t("site.flow.hint")}
          action={
            <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`${base}/servers/${app.server_id}/sites/${app.id}`} />}>
              <LayoutGridIcon />
              {t("sites.canvas")}
            </Button>
          }
        />
        <div className="flex flex-col gap-3 border-b border-line px-6 py-6 lg:flex-row" data-testid="site-flow">
          <Node href={`${root}/monitors`} icon={<UsersIcon />} label={t("site.flow.visitors")} lines={[domains[0]?.hostname ?? none]} />
          <Arrow />
          <Node
            href={`${root}/dns`}
            icon={<CloudIcon />}
            label={t("site.flow.dns")}
            lines={domains.length ? domains.slice(0, 3).map((d) => d.hostname) : [none]}
          />
          <Arrow />
          <Node
            href={`${root}/proxy`}
            icon={<NetworkIcon />}
            label={t("site.flow.proxy")}
            lines={[domains.length ? t("site.flow.hosts", { n: domains.length }) : none, `TLS ${domains[0]?.tls ?? "—"}`]}
          />
          <Arrow />
          <Node
            tone="app"
            href={imported ? `${root}/settings` : `${root}/deployments`}
            icon={<GlobeIcon />}
            label={t("site.flow.app")}
            lines={
              imported
                ? [app.name, app.imported_state ?? ""]
                : [live ? `v${live.version} · ${shortSha(live.commit_sha)}` : t("apps.noLive"), runtimeLabel(app.runtime) ?? "", `:${app.port}`]
            }
          />
          {!imported ? (
            <>
              <Arrow />
              <Node
                href={`${root}/workers`}
                icon={<CogIcon />}
                label={t("site.flow.processes")}
                lines={detail.processes.length ? detail.processes.map((p) => p.name) : [none]}
              />
              <Arrow />
              <Node
                href={`${root}/databases`}
                icon={<DatabaseIcon />}
                label={t("site.flow.data")}
                lines={services.length ? services.slice(0, 3).map((s) => `${s.name} · ${s.kind}`) : [none]}
              />
            </>
          ) : null}
        </div>
        <Link
          href={`${base}/servers/${app.server_id}`}
          className="flex items-center gap-2 border-b border-line px-6 py-3 text-sm text-grid-body hover:bg-grid-soft"
        >
          <ServerIcon className="size-4 text-grid-brand" />
          {t("site.flow.runsOn")}
          <span dir="ltr" className="font-medium text-grid-fg">
            {app.server_name ?? app.server_id}
          </span>
        </Link>
      </section>

      {imported ? (
        <>
          {operate && app.imported_state !== "missing" ? <WorkloadStats path={path} /> : null}
          <OriginFacts item={app} />
        </>
      ) : (
        <section aria-labelledby="details-h">
          <SectionTitle id="details-h" title={t("site.details")} />
          <dl className="grid grid-cols-1 gap-px border-b border-line bg-line sm:grid-cols-2 xl:grid-cols-4" data-testid="app-summary">
            <Stat label={t("apps.liveRelease")}>
              {live ? (
                <span dir="ltr" className="font-mono text-sm">
                  v{live.version} · {shortSha(live.commit_sha)}
                </span>
              ) : (
                <span className="text-sm text-grid-muted">{t("apps.noLive")}</span>
              )}
            </Stat>
            <Stat label={t("apps.latestDeploy")}>
              {last ? (
                <Link href={`${root}/deployments`} className="flex flex-wrap items-center gap-2 hover:underline">
                  <span dir="ltr" className="font-mono text-sm">
                    #{last.number}
                  </span>
                  <DeploymentStatusBadge status={last.status} />
                  <span dir="ltr" className="text-xs text-grid-muted">
                    {fmt(last.created_at)}
                  </span>
                </Link>
              ) : (
                <span className="text-sm text-grid-muted">{t("apps.neverDeployed")}</span>
              )}
            </Stat>
            <Stat label={t("apps.runtime")}>
              <span dir="ltr" className="font-mono text-sm">
                {runtimeLabel(app.runtime) ?? t("apps.runtimeUnknown")} · :{app.port}
              </span>
            </Stat>
            <Stat label={t("site.health")}>
              <span dir="ltr" className="font-mono text-sm">
                {app.healthcheck_path || "/"} · {app.healthcheck_timeout}s
              </span>
            </Stat>
          </dl>
        </section>
      )}
    </>
  )
}

function Stat({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-2 bg-grid-bg px-6 py-4">
      <dt className="grid-micro">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  )
}
