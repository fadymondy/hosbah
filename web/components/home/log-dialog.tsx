"use client"

import Link from "next/link"
import { useEffect, useState } from "react"
import { Dialog, DialogContent, DialogDescription, DialogTitle, Skeleton } from "@fadymondy/nasaq/web"
import { ArrowRightIcon } from "lucide-react"

import { DeploymentStatusBadge } from "@/components/app-status"
import { DeploymentLog } from "@/components/deployment-log"
import { Button } from "@/components/ui/button"
import { apiFetch } from "@/lib/api"
import type { Deployment } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"

export type LogTarget = { app: string; appTitle: string; id: string; number: number }

/** A deployment's build and release log in a wide dialog, live while it runs. */
export function LogDialog({ team, target, onClose }: { team: string; target: LogTarget | null; onClose: () => void }) {
  const { t, locale } = useI18n()
  const [deployment, setDeployment] = useState<Deployment | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    setDeployment(null)
    setFailed(false)
    if (!target) return
    apiFetch<{ deployment: Deployment }>(`/api/teams/${team}/apps/${encodeURIComponent(target.app)}/deployments/${target.id}`)
      .then((r) => setDeployment(r.deployment))
      .catch(() => setFailed(true))
  }, [team, target])

  return (
    <Dialog open={target !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex max-h-[90vh] w-[min(64rem,calc(100vw-2rem))] max-w-none flex-col gap-4 overflow-hidden rounded-2xl">
        <div className="flex flex-wrap items-start justify-between gap-3 pe-8">
          <div className="min-w-0">
            <DialogTitle dir="auto">{target ? t("home3.deployed", { app: target.appTitle, n: target.number }) : ""}</DialogTitle>
            <DialogDescription>{t("home3.logHint")}</DialogDescription>
          </div>
          {deployment ? <DeploymentStatusBadge status={deployment.status} /> : null}
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          {failed ? (
            <p className="text-body-sm text-nq-danger-text">{t("home3.logFailed")}</p>
          ) : deployment && target ? (
            <DeploymentLog team={team} app={target.app} deployment={deployment} onStatus={setDeployment} />
          ) : (
            <Skeleton className="h-80 rounded-xl" />
          )}
        </div>
        {target ? (
          <div className="flex justify-end">
            <Button
              size="sm"
              variant="secondary"
              nativeButton={false}
              render={<Link href={`/${locale}/t/${team}/apps/${encodeURIComponent(target.app)}/deployments?d=${target.id}`} />}
              onClick={onClose}
            >
              {t("home3.openDeployments")}
              <ArrowRightIcon className="rtl:-scale-x-100" />
            </Button>
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
