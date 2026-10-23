"use client"


import { Badge } from "@fadymondy/nasaq/web"
import type { AppStatus, DeploymentStatus, DeploymentTrack } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"

type Variant = "success" | "neutral" | "outline" | "danger"

const appVariant: Record<AppStatus, Variant> = {
  new: "outline",
  deploying: "neutral",
  running: "success",
  failed: "danger",
  stopped: "neutral",
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
  running: "neutral",
  succeeded: "success",
  failed: "danger",
  cancelled: "neutral",
  skipped: "outline",
}

export function DeploymentStatusBadge({ status }: { status: DeploymentStatus }) {
  const { t } = useI18n()
  return (
    <Badge variant={depVariant[status] ?? "outline"} data-status={status}>
      {t(`apps.deploy.statuses.${status}`, undefined, status)}
    </Badge>
  )
}

/** Fast/Full track badge for a deployment row; the reason (why this track was chosen) shows as a tooltip. */
export function DeploymentTrackBadge({ track, reason }: { track: DeploymentTrack | null; reason?: string | null }) {
  const { t } = useI18n()
  if (!track) return null
  return (
    <Badge variant="outline" data-track={track} title={reason ?? undefined}>
      {t(`apps.deploy.tracks.${track}`, undefined, track)}
    </Badge>
  )
}
