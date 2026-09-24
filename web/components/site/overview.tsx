"use client"

import Link from "next/link"
import type { ReactNode } from "react"

import { DeploymentStatusBadge } from "@/components/app-status"
import { ImportedNotice, OriginFacts } from "@/components/imported"
import { WorkloadStats } from "@/components/imported-controls"
import { SiteCanvas } from "@/components/site/canvas/canvas"
import { useLoadedSite } from "@/components/site/context"
import { SectionTitle } from "@/components/states"
import { isImported, runtimeLabel, shortSha } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam } from "@/lib/team"

/**
 * The site's overview: an interactive canvas of how a request reaches it and everything it is
 * joined to (canvas/canvas.tsx), then live usage and the facts about what is running.
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
  const live = detail.live_release
  const last = deployments[0]

  return (
    <>
      {imported ? <ImportedNotice kind="app" /> : null}
      <SiteCanvas />

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
