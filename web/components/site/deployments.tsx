"use client"

import { Button } from "@fadymondy/nasaq/web"
import { useEffect, useState } from "react"
import { RotateCcwIcon, RotateCwIcon, SquareIcon } from "lucide-react"

import { DeploymentStatusBadge, DeploymentTrackBadge } from "@/components/app-status"
import { ReleasesPanel } from "@/components/apps/releases"
import { ConfirmButton } from "@/components/confirm-button"
import { DeploymentLog } from "@/components/deployment-log"
import { useErrorText } from "@/components/form-error"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { useLoadedSite } from "@/components/site/context"
import { SectionTitle } from "@/components/states"

import { apiFetch } from "@/lib/api"
import { isActive, shortSha, type Deployment } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/** Deploy management: every deployment with its live log, rollback and restart, then the releases. */
export function SiteDeployments() {
  const { t } = useI18n()
  const { team } = useTeam()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const operate = canOperate(team.role)
  const { detail, app, deployments: deps, pushDeployment, setError, path } = useLoadedSite()
  const [selected, setSelected] = useState<string | null>(null)
  const [pinned, setPinned] = useState(false)
  const [busy, setBusy] = useState(false)
  const [q, setQ] = useState("")

  // Follow the newest deployment unless the person picked an older one.
  useEffect(() => {
    if (deps[0]) setSelected((cur) => (pinned && cur ? cur : deps[0].id))
  }, [deps, pinned])

  // ?d=<id> (from the team's deployment feed or search) opens that deployment and keeps it open.
  useEffect(() => {
    const want = new URLSearchParams(window.location.search).get("d")
    if (want && deps.some((d) => d.id === want)) {
      setSelected(want)
      setPinned(true)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deps.length > 0])

  const live = detail.live_release
  const prev = detail.previous_release
  const shown = deps.filter((d) => matches(q, d.number, d.status, d.kind, d.trigger, d.commit_sha, d.commit_message, d.ref))
  const selectedDep = deps.find((d) => d.id === selected) ?? null

  function started(d: Deployment) {
    pushDeployment(d)
    setSelected(d.id)
    setPinned(false)
  }

  return (
    <>
      <section aria-labelledby="deps-h">
        <SectionTitle
          id="deps-h"
          title={t("site.nav.deployments")}
          hint={t("site.deploymentsHint")}
          action={
            operate ? (
              <span className="flex flex-wrap items-center gap-2">
                {prev ? (
                  <ConfirmButton
                    icon={<RotateCcwIcon />}
                    label={t("apps.actions.rollbackTo", { version: prev.version })}
                    title={t("apps.actions.rollbackTitle", { version: prev.version })}
                    description={t("apps.actions.rollbackHint", { version: prev.version, name: app.name })}
                    confirmLabel={t("apps.actions.rollback")}
                    onConfirm={async () => {
                      try {
                        const r = await apiFetch<{ deployment: Deployment }>(`${path}/rollback`, { method: "POST", body: { release: prev.id } })
                        started(r.deployment)
                      } catch (err) {
                        throw new Error(errorText(err))
                      }
                    }}
                  />
                ) : null}
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={busy || !live}
                  title={live ? undefined : t("apps.actions.noLive")}
                  onClick={async () => {
                    setBusy(true)
                    setError(null)
                    try {
                      started(await apiFetch<Deployment>(`${path}/restart`, { method: "POST" }))
                    } catch (err) {
                      setError(errorText(err))
                    } finally {
                      setBusy(false)
                    }
                  }}
                >
                  <RotateCwIcon />
                  {t("apps.actions.restart")}
                </Button>
              </span>
            ) : null
          }
        />
        {deps.length ? <ListSearch value={q} onChange={setQ} placeholder={t("site.searchDeployments")} shown={shown.length} total={deps.length} /> : null}
        <div className="grid border-b border-border xl:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]" data-testid="deployments">
          <div className="min-w-0 border-b border-border xl:border-e xl:border-b-0">
            {deps.length === 0 ? (
              <p className="px-6 py-4 text-sm text-muted-foreground">{t("apps.deploy.empty")}</p>
            ) : (
              <ol className="flex max-h-[32rem] flex-col divide-y divide-border overflow-y-auto">
                {shown.map((d) => (
                  <li key={d.id}>
                    <button
                      type="button"
                      data-deployment={d.id}
                      aria-current={d.id === selected}
                      onClick={() => {
                        setSelected(d.id)
                        setPinned(d.id !== deps[0]?.id)
                      }}
                      className={cn("flex w-full flex-col gap-1 px-6 py-3 text-start hover:bg-secondary", d.id === selected && "bg-secondary")}
                    >
                      <span className="flex flex-wrap items-center gap-2 text-sm">
                        <span dir="ltr" className="font-mono">
                          #{d.number}
                        </span>
                        <span className="font-medium">{t(`apps.deploy.kinds.${d.kind}`, undefined, d.kind)}</span>
                        <DeploymentStatusBadge status={d.status} />
                        <DeploymentTrackBadge track={d.track} reason={d.track_reason} />
                        <span className="ms-auto text-xs text-muted-foreground">{t(`apps.deploy.triggers.${d.trigger}`, undefined, d.trigger)}</span>
                      </span>
                      <span dir="ltr" className="truncate text-start font-mono text-xs text-muted-foreground">
                        {shortSha(d.commit_sha)} {d.commit_message ?? d.ref}
                      </span>
                      <span dir="ltr" className="text-start text-xs text-muted-foreground">
                        {fmt(d.created_at)}
                      </span>
                    </button>
                  </li>
                ))}
              </ol>
            )}
          </div>
          {selectedDep ? (
            <DeploymentLog
              team={team.slug}
              app={app.id}
              deployment={selectedDep}
              actions={
                operate && isActive(selectedDep) && !selectedDep.cancel_requested ? (
                  <CancelButton path={`${path}/deployments/${selectedDep.id}/cancel`} onError={setError} />
                ) : null
              }
            />
          ) : (
            <p className="px-6 py-4 text-sm text-muted-foreground">{t("apps.deploy.select")}</p>
          )}
        </div>
      </section>
      <section aria-labelledby="rel-h">
        <SectionTitle id="rel-h" title={t("apps.tabs.releases")} />
        <ReleasesPanel path={path} operate={operate} liveId={app.current_release_id} onRollback={started} />
      </section>
    </>
  )
}

function CancelButton({ path, onError }: { path: string; onError: (m: string | null) => void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [busy, setBusy] = useState(false)
  return (
    <Button
      size="sm"
      variant="secondary"
      disabled={busy}
      onClick={async () => {
        setBusy(true)
        try {
          await apiFetch(path, { method: "POST" })
        } catch (err) {
          onError(errorText(err))
        } finally {
          setBusy(false)
        }
      }}
    >
      <SquareIcon />
      {t("apps.deploy.cancel")}
    </Button>
  )
}
