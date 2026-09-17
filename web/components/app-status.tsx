"use client"

import { Badge } from "@/components/ui/badge"
import type { AppStatus, DeploymentStatus } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"

type Variant = "default" | "secondary" | "outline" | "destructive"

const appVariant: Record<AppStatus, Variant> = {
  new: "outline",
  deploying: "secondary",
  running: "default",
  failed: "destructive",
  stopped: "secondary",
}

export function AppStatusBadge({ status }: { status: AppStatus }) {
  const { t } = useI18n()
  return (
    <Badge variant={appVariant[status] ?? "outline"} data-status={status}>
      <span
        aria-hidden
        className={`size-1.5 rounded-full bg-current ${status === "deploying" ? "animate-pulse" : "opacity-60"}`}
      />
      {t(`apps.statuses.${status}`, undefined, status)}
    </Badge>
  )
}

const depVariant: Record<DeploymentStatus, Variant> = {
  queued: "outline",
  running: "secondary",
  succeeded: "default",
  failed: "destructive",
  cancelled: "secondary",
}

export function DeploymentStatusBadge({ status }: { status: DeploymentStatus }) {
  const { t } = useI18n()
  return (
    <Badge variant={depVariant[status] ?? "outline"} data-status={status}>
      {t(`apps.deploy.statuses.${status}`, undefined, status)}
    </Badge>
  )
}
