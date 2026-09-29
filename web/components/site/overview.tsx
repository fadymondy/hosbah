"use client"

import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useEffect, useState, type ReactNode } from "react"

import { DeploymentStatusBadge } from "@/components/app-status"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { ImportedNotice, OriginFacts } from "@/components/imported"
import { WorkloadStats } from "@/components/imported-controls"
import { AdoptDialog } from "@/components/site/adopt-dialog"
import { SiteCanvas } from "@/components/site/canvas/canvas"
import { useLoadedSite } from "@/components/site/context"
import { Notice, SectionTitle } from "@/components/states"
import { Button } from "@/components/ui/button"
import { apiFetch } from "@/lib/api"
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
  const errorText = useErrorText()
  const router = useRouter()
  const params = useSearchParams()
  const { detail, app, deployments, path, reload } = useLoadedSite()
  const imported = isImported(app)
  const operate = canOperate(team.role)
  const base = `/${locale}/t/${team.slug}`
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
              variant="outline"
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
            <Button type="button" size="sm" variant="outline" nativeButton={false} render={<Link href={`${base}/servers/${app.connected_server_id}`} />}>
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
              <Button type="button" size="sm" variant="outline" onClick={() => setAdoptOpen(true)}>
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
