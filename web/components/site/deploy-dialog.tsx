"use client"

import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useState } from "react"
import { RocketIcon } from "lucide-react"

import { duration, isActive, Pipeline } from "@/components/deploy/pipeline"
import { FormError, useErrorText } from "@/components/form-error"
import { StatusIcon } from "@/components/home/deployments-feed"
import { useSite } from "@/components/site/context"
import { apiFetch } from "@/lib/api"
import { shortSha, type Deployment } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import { useTeamBase } from "@/lib/team"

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-2 text-sm">
      <dt className="text-grid-muted">{label}</dt>
      <dd className="min-w-0 truncate text-end text-grid-fg">{children}</dd>
    </div>
  )
}

/**
 * Deploy with a look first: what gets built, where, and what is live now. Once started, the same
 * dialog follows the deployment through its steps (fed by the app's live stream), so nobody has to
 * go hunting for it — the full log is one link away.
 */
export function DeployButton() {
  const { t } = useI18n()
  const teamBase = useTeamBase()
  const errorText = useErrorText()
  const { detail, deployments, path, pushDeployment } = useSite()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [startedId, setStartedId] = useState<string | null>(null)
  if (!detail) return null
  const app = detail.app
  const live = detail.live_release
  const running = deployments.find((d) => isActive(d))
  const started = startedId ? deployments.find((d) => d.id === startedId) : undefined
  const logHref = (id: string) => `${teamBase}/apps/${encodeURIComponent(app.name)}/deployments?d=${id}`

  function change(next: boolean) {
    setOpen(next)
    if (!next) {
      setError(null)
      setStartedId(null)
    }
  }

  async function deploy() {
    setBusy(true)
    setError(null)
    try {
      const d = await apiFetch<Deployment>(`${path}/deployments`, { method: "POST", body: {} })
      pushDeployment(d)
      setStartedId(d.id)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Button variant="primary" onClick={() => change(true)}>
        <RocketIcon />
        {t("apps.actions.deploy")}
      </Button>
      <Dialog open={open} onOpenChange={change}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{started ? t("deployDialog.progressTitle", { n: started.number }) : t("deployDialog.title", { app: app.title || app.name })}</DialogTitle>
            <DialogDescription>{started ? t("deployDialog.progressHint") : t("deployDialog.hint")}</DialogDescription>
          </DialogHeader>
          {started ? (
            <div className="flex flex-col gap-3">
              <div className="flex items-center gap-2 text-sm">
                <StatusIcon status={started.status} />
                <span className="font-medium text-grid-fg">{t(`apps.deploy.statuses.${started.status}`, undefined, started.status)}</span>
                <span className="ms-auto text-xs text-grid-muted tabular-nums">{duration(started.started_at ?? started.created_at, started.finished_at)}</span>
              </div>
              <Pipeline d={started} />
              {started.status === "failed" ? (
                <p className="text-sm text-grid-danger">
                  {started.error || t(`deploys2.hint.${started.step ?? "none"}`, undefined, t("deploys2.hint.none"))}
                </p>
              ) : null}
              {started.status === "succeeded" ? <p className="text-sm text-grid-body">{t("deployDialog.done")}</p> : null}
            </div>
          ) : (
            <dl className="divide-y divide-line border-y border-line">
              <Row label={t("deployDialog.repo")}>
                <span dir="ltr" className="font-mono text-xs">{app.repo_url || "—"}</span>
              </Row>
              <Row label={t("deployDialog.branch")}>
                <span dir="ltr" className="font-mono text-xs">{app.branch || "—"}</span>
              </Row>
              <Row label={t("deployDialog.server")}>
                <span dir="ltr">{app.server_name || "—"}</span>
              </Row>
              <Row label={t("deployDialog.live")}>
                {live ? (
                  <span dir="ltr" className="font-mono text-xs">
                    v{live.version}
                    {live.commit_sha ? ` · ${shortSha(live.commit_sha)}` : ""}
                  </span>
                ) : (
                  t("deployDialog.nothingLive")
                )}
              </Row>
            </dl>
          )}
          {!started && running ? (
            <p className="text-sm text-grid-muted">
              {t("deployDialog.alreadyRunning", { n: running.number })}{" "}
              <Link href={logHref(running.id)} className="text-grid-action hover:underline" onClick={() => change(false)}>
                {t("deploys2.openLog")}
              </Link>
            </p>
          ) : null}
          <FormError message={error} />
          <DialogFooter>
            {started ? (
              <>
                <Button variant="ghost" onClick={() => change(false)}>
                  {t("common.close")}
                </Button>
                <Button variant="secondary" nativeButton={false} render={<Link href={logHref(started.id)} onClick={() => change(false)} />}>
                  {t("deploys2.openLog")}
                </Button>
              </>
            ) : (
              <>
                <Button variant="ghost" onClick={() => change(false)}>
                  {t("common.cancel")}
                </Button>
                <Button variant="primary" disabled={busy} onClick={() => void deploy()}>
                  <RocketIcon />
                  {busy ? t("deployDialog.starting") : t("deployDialog.confirm")}
                </Button>
              </>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
