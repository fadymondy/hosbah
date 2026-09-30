"use client"

import { Badge } from "@fadymondy/nasaq/web"
import type { ReactNode } from "react"
import { EyeIcon } from "lucide-react"

import { Facts } from "@/components/admin/admin-list"
import { Notice } from "@/components/states"

import type { Imported } from "@/lib/apps"
import { humanBytes } from "@/lib/databases"
import { useFormatDate, useI18n } from "@/lib/i18n"

/** "Imported" plus its state (running, stopped, or missing from the server's last report). */
export function ImportedBadge({ item }: { item: Imported }) {
  const { t } = useI18n()
  const state = item.imported_state ?? "running"
  return (
    <span className="inline-flex items-center gap-1.5">
      <Badge variant="outline">{t("imported.badge")}</Badge>
      <span
        className={
          state === "running"
            ? "text-xs text-foreground"
            : state === "missing"
              ? "text-xs text-nq-danger-text"
              : "text-xs text-muted-foreground"
        }
      >
        {t(`imported.states.${state}`, undefined, state)}
      </span>
    </span>
  )
}

/** What it is, in a few words: "Container · nginx:1.27" or "LXC guest · 10.10.10.117". */
export function originSummary(item: Imported, t: (k: string, v?: Record<string, string | number>, f?: string) => string) {
  const o = item.origin ?? {}
  const kind =
    item.origin_kind === "guest"
      ? t(`imported.guest.${o.guest_type ?? "guest"}`, undefined, t("imported.kinds.guest"))
      : t(`imported.kinds.${item.origin_kind ?? "container"}`, undefined, item.origin_kind ?? "")
  const detail = o.image || o.address || ""
  return detail ? `${kind} · ${detail}` : kind
}

/** The notice every imported record carries: Hosbah shows it, never changes it. */
export function ImportedNotice({ kind }: { kind: "app" | "database" }) {
  const { t } = useI18n()
  return (
    <Notice icon={<EyeIcon />} title={t("imported.noticeTitle")} data-testid="imported-notice">
      {t(kind === "app" ? "imported.noticeApp" : "imported.noticeDatabase")}
    </Notice>
  )
}

/** The workload's details as discovered: kind, image or guest, address, ports, resources, last seen. */
export function OriginFacts({ item, extra = [] }: { item: Imported; extra?: [ReactNode, ReactNode][] }) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const o = item.origin ?? {}
  const facts: [ReactNode, ReactNode][] = [
    [t("imported.facts.kind"), originSummary({ ...item, origin: { ...o, image: undefined, address: undefined } }, t)],
    [t("imported.facts.state"), <ImportedBadge key="s" item={item} />],
    [t("imported.facts.lastSeen"), <span key="l" dir="ltr">{fmt(item.last_seen_at)}</span>],
    ...extra,
  ]
  if (o.image) facts.push([t("imported.facts.image"), <code key="i" dir="ltr" className="font-mono text-xs break-all">{o.image}</code>])
  if (o.address) facts.push([t("imported.facts.address"), <span key="a" dir="ltr" className="font-mono">{o.address}</span>])
  if (o.ports?.length)
    facts.push([
      t("imported.facts.ports"),
      <span key="p" dir="ltr" className="font-mono text-xs">
        {o.ports.join(", ")}
      </span>,
    ])
  if (o.cpus) facts.push([t("imported.facts.cpus"), <span key="c" className="font-mono">{o.cpus}</span>])
  if (o.mem_bytes) facts.push([t("imported.facts.memory"), <span key="m" dir="ltr" className="font-mono">{humanBytes(o.mem_bytes)}</span>])
  if (o.disk_bytes) facts.push([t("imported.facts.disk"), <span key="d" dir="ltr" className="font-mono">{humanBytes(o.disk_bytes)}</span>])
  return <Facts items={facts} />
}
