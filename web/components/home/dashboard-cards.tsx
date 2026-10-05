"use client"

import Link from "next/link"
import { ServerCard, Sparkline, type ServerInfo } from "@fadymondy/nasaq/web"
import { GlobeIcon, ServerIcon } from "lucide-react"

import { AppStatusBadge } from "@/components/app-status"
import { ServerMark } from "@/components/server-mark"
import type { App } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"

const pct = (used: number, total: number) => (total > 0 ? Math.round((used / total) * 1000) / 10 : 0)
const mib = (b: number) => (b >= 1073741824 ? `${(b / 1073741824).toFixed(1)} GiB` : `${Math.round(b / 1048576)} MiB`)

/** Hosbah's server as Nasaq's ServerInfo: an online agent is "running", live metrics as percentages. */
export function serverInfo(s: Server): ServerInfo {
  const m = s.metrics
  return {
    id: s.id,
    name: s.name,
    status: s.online ? "running" : s.status === "failed" ? "error" : s.status === "enrolled" ? "stopped" : "provisioning",
    address: s.ip_address ?? s.hostname ?? "—",
    os: s.facts?.os_pretty || s.os || undefined,
    limits: {
      cpuCores: s.facts?.cpus || 1,
      memoryMb: Math.round((m?.mem_total_bytes || s.facts?.mem_total_bytes || 0) / 1048576),
      diskGb: Math.round((m?.disk_total_bytes ?? 0) / 1073741824),
    },
    metrics: m
      ? {
          cpu: m.cpu_percent,
          memory: pct(m.mem_used_bytes, m.mem_total_bytes),
          disk: pct(m.disk_used_bytes, m.disk_total_bytes),
        }
      : undefined,
    snapshots: [],
  }
}

/**
 * A server as on the dashboard: Nasaq's full ServerCard with the last hour's CPU as a band at its
 * foot. The whole card opens the server; the card's own buttons and links stay clickable above it.
 */
export function DashboardServerCard({
  server,
  base,
  cpu = [],
  onPower,
}: {
  server: Server
  base: string
  cpu?: number[]
  onPower: () => Promise<{ error?: string }>
}) {
  const { t } = useI18n()
  return (
    <div className="relative overflow-hidden rounded-2xl border bg-card transition-colors hover:bg-nq-hover">
      <Link
        href={`${base}/servers/${server.id}`}
        aria-label={server.name}
        className="absolute inset-0 z-0 rounded-2xl focus-visible:outline-2 focus-visible:outline-nq-focus"
      />
      <ServerCard
        className="pointer-events-none relative z-10 rounded-none border-0 bg-transparent shadow-none [&_a]:pointer-events-auto [&_button]:pointer-events-auto [&_[role=menu]]:pointer-events-auto"
        server={serverInfo(server)}
        onPower={onPower}
      />
      {cpu.length > 1 ? (
        <div className="pointer-events-none relative z-10 border-t px-4 pt-3 pb-2">
          <div className="flex items-center justify-between text-caption text-muted-foreground">
            <span>{t("home3.cpuTrend")}</span>
            <span className="tabular-nums" dir="ltr">
              {Math.round(cpu[cpu.length - 1])}%
            </span>
          </div>
          <Sparkline data={cpu} label={t("home3.cpuTrend")} className="h-12 w-full" />
        </div>
      ) : null}
    </div>
  )
}

export type LoadPoint = { at: string; cpu_percent: number; mem_used_bytes: number; mem_limit_bytes: number; containers: number }

/** An app's container load over the last hour (CPU line, memory in use), live from its server's heartbeats. */
function AppLoad({ points, running }: { points: LoadPoint[]; running: boolean }) {
  const { t } = useI18n()
  const last = points[points.length - 1]
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between gap-2 text-caption text-muted-foreground">
        <span>{t("home3.appLoad")}</span>
        {last ? (
          <span className="tabular-nums" dir="ltr">
            {t("home3.appLoadNow", { cpu: last.cpu_percent.toFixed(1), mem: mib(last.mem_used_bytes) })}
          </span>
        ) : null}
      </div>
      {points.length > 1 ? (
        <Sparkline data={points.map((p) => p.cpu_percent)} label={t("home3.appLoad")} className="h-10 w-full" />
      ) : (
        <div className="flex h-10 items-center rounded-xl bg-secondary/60 px-3 text-caption text-muted-foreground">
          {running ? t("home3.appLoadWaiting") : t("home3.appLoadIdle")}
        </div>
      )}
    </div>
  )
}

/** An app as on the dashboard: mark, title and description, domain, live load, server and status. */
export function DashboardAppCard({ app: a, base, load = [] }: { app: App; base: string; load?: LoadPoint[] }) {
  const { t } = useI18n()
  const domain = a.domains?.[0]?.hostname
  return (
    <div className="relative flex flex-col gap-4 rounded-2xl border border-border bg-card p-4 transition-colors hover:bg-nq-hover">
      <div className="flex items-center gap-3">
        <ServerMark server={{ color: a.color, icon: a.icon ?? "globe", logo_url: a.logo_url }} size={10} />
        <div className="min-w-0 flex-1">
          <Link
            href={`${base}/apps/${encodeURIComponent(a.name)}`}
            className="block truncate font-medium text-foreground after:absolute after:inset-0 after:rounded-2xl"
            dir="auto"
          >
            {a.title || a.name}
          </Link>
          <span className="block truncate text-caption text-muted-foreground" dir="auto">
            {a.description || t("home3.noDescription")}
          </span>
        </div>
      </div>
      <div className="flex min-w-0 items-center gap-1.5 text-caption">
        <GlobeIcon className="size-3.5 shrink-0 text-muted-foreground" />
        {domain ? (
          <a
            href={`https://${domain}`}
            target="_blank"
            rel="noreferrer"
            dir="ltr"
            className="relative z-10 truncate text-nq-brand hover:underline"
          >
            {domain}
          </a>
        ) : (
          <span className="text-muted-foreground">{t("home3.noDomain")}</span>
        )}
      </div>
      <AppLoad points={load} running={a.status === "running" || a.imported_state === "running"} />
      <div className="mt-auto flex items-center justify-between gap-2 text-caption text-muted-foreground">
        <span className="flex min-w-0 items-center gap-1.5">
          <ServerIcon className="size-3.5 shrink-0" />
          <span className="truncate" dir="ltr">
            {a.server_name ?? "—"}
          </span>
        </span>
        <AppStatusBadge status={a.status} />
      </div>
    </div>
  )
}
