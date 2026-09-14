"use client"

import { Badge } from "@/components/ui/badge"
import { useI18n } from "@/lib/i18n"
import { presenceOf, type CheckStatus, type OperationStatus, type Presence, type Server } from "@/lib/servers"

type Variant = "default" | "secondary" | "outline" | "destructive"

const presenceVariant: Record<Presence, Variant> = {
  online: "default",
  offline: "secondary",
  pending: "outline",
  provisioning: "outline",
  enrolled: "secondary",
  failed: "destructive",
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
  sent: "secondary",
  running: "secondary",
  ok: "default",
  failed: "destructive",
  timeout: "destructive",
}

export function OpStatusBadge({ status }: { status: OperationStatus }) {
  const { t } = useI18n()
  return (
    <Badge variant={opVariant[status]} data-status={status}>
      {t(`servers.ops.statuses.${status}`)}
    </Badge>
  )
}

const checkVariant: Record<CheckStatus, Variant> = { pass: "secondary", warn: "outline", fail: "destructive" }

export function CheckBadge({ status }: { status: CheckStatus }) {
  const { t } = useI18n()
  return <Badge variant={checkVariant[status]}>{t(`servers.check.${status}`)}</Badge>
}
