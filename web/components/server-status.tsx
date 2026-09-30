"use client"


import { Badge } from "@fadymondy/nasaq/web"
import { useI18n } from "@/lib/i18n"
import { presenceOf, type CheckStatus, type OperationStatus, type Presence, type Server } from "@/lib/servers"

type Variant = "success" | "neutral" | "outline" | "danger"

const presenceVariant: Record<Presence, Variant> = {
  online: "success",
  offline: "neutral",
  pending: "outline",
  provisioning: "outline",
  enrolled: "neutral",
  failed: "danger",
}

/** Online / offline / waiting / provisioning / failed, with a dot that pulses while online. */
export function PresenceBadge({ server }: { server: Pick<Server, "status" | "online"> }) {
  const { t } = useI18n()
  const p = presenceOf(server)
  return (
    <Badge variant={presenceVariant[p]} data-presence={p}>
      <span aria-hidden className={`size-1.5 rounded-full bg-current ${p === "online" ? "animate-pulse" : "opacity-60"}`} />
      {t(`servers.presence.${p}`)}
    </Badge>
  )
}

const opVariant: Record<OperationStatus, Variant> = {
  pending: "outline",
  sent: "neutral",
  running: "neutral",
  ok: "success",
  failed: "danger",
  timeout: "danger",
}

export function OpStatusBadge({ status }: { status: OperationStatus }) {
  const { t } = useI18n()
  return (
    <Badge variant={opVariant[status]} data-status={status}>
      {t(`servers.ops.statuses.${status}`)}
    </Badge>
  )
}

const checkVariant: Record<CheckStatus, Variant> = { pass: "neutral", warn: "outline", fail: "danger" }

export function CheckBadge({ status }: { status: CheckStatus }) {
  const { t } = useI18n()
  return <Badge variant={checkVariant[status]}>{t(`servers.check.${status}`)}</Badge>
}
