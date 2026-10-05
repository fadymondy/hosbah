"use client"

import { Button, StatCard, StatGrid } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useEffect, useState } from "react"
import { CpuIcon, HeartPulseIcon, HistoryIcon, RocketIcon } from "lucide-react"

import { DeploymentStatusBadge } from "@/components/app-status"
import { isActive, Pipeline } from "@/components/deploy/pipeline"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { ImportedNotice, OriginFacts } from "@/components/imported"
import { WorkloadStats } from "@/components/imported-controls"
import { AdoptDialog } from "@/components/site/adopt-dialog"
import { useLoadedSite } from "@/components/site/context"
import { Notice, SectionTitle } from "@/components/states"
import { SITE_TABS } from "@/components/site/sections"
import { SiteViews } from "@/components/site/views"

import { apiFetch } from "@/lib/api"
import { isImported, runtimeLabel, shortSha } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam, useTeamBase } from "@/lib/team"

/**
 * The site's overview: an interactive canvas of how a request reaches it and everything it is
 * joined to (canvas/canvas.tsx), then live usage and the facts about what is running.
 */
const OVERVIEW_VIEWS = SITE_TABS[0].views ?? []

export function SiteOverview() {
  const { t } = useI18n()
  const { team } = useTeam()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const router = useRouter()
  const params = useSearchParams()
  const { detail, app, deployments, path, reload } = useLoadedSite()
  const imported = isImported(app)
  const operate = canOperate(team.role)
  const base = useTeamBase()
  const root = `${base}/apps/${encodeURIComponent(app.name)}`
  const live = detail.live_release
  const last = deployments[0]
  // Set by adopt-app notifications and the trigger below; cleared once the dialog opens so a
  // refresh does not reopen it.
  const [adoptOpen, setAdoptOpen] = useState(false)
  // An adopted guest or unit whose old workload has not been confirmed stopped yet: still git-sourced,
  // but origin_kind survives adoption for exactly this (a container clears it itself once retired).
  const awaitingStopConfirm = !imported && !!app.origin_kind
  // An imported Proxmox guest, not yet connected: offer to put a Hosbah agent inside it, instead of
  // the user mistakenly building a git app on the hypervisor node.
  const unconnectedGuest = imported && app.origin_kind === "guest" && !app.connected_server_id
  const [connecting, setConnecting] = useState(false)
  const [connectError, setConnectError] = useState<string | null>(null)

  useEffect(() => {
    if (params.get("adopt") === "1") {
      setAdoptOpen(true)
      router.replace(`${root}${window.location.hash}`, { scroll: false })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <>
      {imported ? <ImportedNotice kind="app" /> : null}
      {imported && operate && unconnectedGuest ? (
        <Notice title={t("connectGuest.noticeTitle")}>
          {t("connectGuest.noticeBody")}
          <div className="pt-2">
            <Button
              type="button"
              size="sm"
              variant="secondary"
              disabled={connecting}
              onClick={async () => {
                setConnecting(true)
                setConnectError(null)
                try {
                  await apiFetch(`${path}/connect-guest`, { method: "POST" })
                  await reload()
                } catch (err) {
                  setConnectError(errorText(err))
                } finally {
                  setConnecting(false)
                }
              }}
            >
              {connecting ? t("common.working") : t("connectGuest.trigger")}
            </Button>
          </div>
          {connectError ? (
            <p role="alert" className="pt-2 text-sm text-destructive" dir="auto">
              {connectError}
            </p>
          ) : null}
        </Notice>
      ) : null}
      {imported && app.connected_server_id ? (
        <Notice title={t("connectGuest.connectedTitle")}>
          {t("connectGuest.connectedBody", { server: app.connected_server_name || app.connected_server_id })}
          <div className="pt-2">
            <Button type="button" size="sm" variant="secondary" nativeButton={false} render={<Link href={`${base}/servers/${app.connected_server_id}`} />}>
              {t("connectGuest.viewServer")}
            </Button>
          </div>
        </Notice>
      ) : null}
      {imported && operate ? (
        <>
          <Notice title={t("adopt.noticeTitle")}>
            {t("adopt.noticeApp")}
            <div className="pt-2">
              <Button type="button" size="sm" variant="secondary" onClick={() => setAdoptOpen(true)}>
                {t("adopt.trigger")}
              </Button>
            </div>
          </Notice>
          <AdoptDialog path={path} open={adoptOpen} onOpenChange={setAdoptOpen} onAdopted={reload} />
        </>
      ) : null}
      {awaitingStopConfirm && operate ? (
        <Notice title={t("adopt.stopNoticeTitle")}>
          {t("adopt.stopNoticeBody")}
          <div className="pt-2">
            <ConfirmButton
              label={t("adopt.confirmStop")}
              title={t("adopt.confirmStopTitle")}
              description={t("adopt.confirmStopHint")}
              confirmLabel={t("adopt.confirmStop")}
              onConfirm={async () => {
                try {
                  await apiFetch(`${path}/adopt/confirm-stop`, { method: "POST" })
                } catch (err) {
                  throw new Error(errorText(err))
                }
                await reload()
              }}
            />
          </div>
        </Notice>
      ) : null}
      {imported ? (
        <>
          {operate && app.imported_state !== "missing" ? <WorkloadStats path={path} /> : null}
          <OriginFacts item={app} />
        </>
      ) : (
        <section aria-labelledby="details-h">
          <SectionTitle id="details-h" title={t("site.details")} />
          <StatGrid data-testid="app-summary">
            <StatCard
              icon={<RocketIcon />}
              label={t("apps.liveRelease")}
              value={
                live ? (
                  <span dir="ltr" className="font-mono text-base">
                    v{live.version} · {shortSha(live.commit_sha)}
                  </span>
                ) : (
                  <span className="text-base text-muted-foreground">{t("apps.noLive")}</span>
                )
              }
            />
            <StatCard
              icon={<HistoryIcon />}
              label={t("apps.latestDeploy")}
              value={
                last ? (
                  <Link href={`${root}/deployments`} className="flex flex-wrap items-center gap-2 text-base hover:underline">
                    <span dir="ltr" className="font-mono">
                      #{last.number}
                    </span>
                    <DeploymentStatusBadge status={last.status} />
                    <span dir="ltr" className="text-xs text-muted-foreground">
                      {fmt(last.created_at)}
                    </span>
                  </Link>
                ) : (
                  <span className="text-base text-muted-foreground">{t("apps.neverDeployed")}</span>
                )
              }
            />
            <StatCard
              icon={<CpuIcon />}
              label={t("apps.runtime")}
              value={
                <span dir="ltr" className="font-mono text-base">
                  {runtimeLabel(app.runtime) ?? t("apps.runtimeUnknown")} · :{app.port}
                </span>
              }
            />
            <StatCard
              icon={<HeartPulseIcon />}
              label={t("site.health")}
              value={
                <span dir="ltr" className="font-mono text-base">
                  {app.healthcheck_path || "/"} · {app.healthcheck_timeout}s
                </span>
              }
            />
          </StatGrid>
          {last && (isActive(last) || last.status === "failed") ? (
            <div className="flex flex-wrap items-center gap-3 rounded-card border border-border bg-card px-4 py-3 shadow-xs">
              <span className="text-xs text-muted-foreground">
                {t("site.deployNow", { n: last.number })}
              </span>
              <Pipeline d={last} />
              <Link href={`${root}/deployments?d=${last.id}`} className="ms-auto text-xs text-grid-action hover:underline">
                {last.status === "failed" ? t("deploys2.openFailedLog") : t("deploys2.openLog")}
              </Link>
            </div>
          ) : null}
        </section>
      )}
      <section aria-labelledby="traffic-h" className="flex flex-col gap-3">
        <SectionTitle id="traffic-h" title={t("site.traffic")} />
        <SiteViews views={OVERVIEW_VIEWS} />
      </section>
    </>
  )
}
