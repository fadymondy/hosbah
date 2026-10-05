"use client"

import Link from "next/link"
import type { ReactNode } from "react"
import { ServerMark } from "@/components/server-mark"
import { BoxesIcon, ChevronRightIcon, CpuIcon, HardDriveIcon, MemoryStickIcon, NetworkIcon, RocketIcon, ZapIcon } from "lucide-react"

import { DeploymentStatusBadge } from "@/components/app-status"
import { Sparkline, useLiveSeries, useTimeAgo } from "@/components/home/sparkline"
import { humanBytes } from "@/lib/databases"
import type { App, Deployment } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"

/** Hardware facts newer agents report (facts.hardware); every field optional. */
export type Hardware = {
  cpu_model?: string
  cpu_cores?: number
  cpu_threads?: number
  disks?: { mount: string; device?: string; fs?: string; size_bytes: number; used_bytes?: number }[]
  interfaces?: { name: string; addrs?: string[]; mac?: string }[]
  gpus?: { index?: number; name: string; vendor?: string; memory_total_bytes?: number; driver?: string }[]
  public_ip?: string
}

export type TunnelSummary = { kind: "frp" | "tailscale" | string; status: string; address?: string | null }

export type Spark = { cpu?: number[]; mem?: number[]; gpu?: number[] | null }

function uptime(s: number, t: (k: string, v?: Record<string, string | number>) => string) {
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  return d ? t("home.uptimeDays", { d, h }) : t("home.uptimeHours", { h, m: Math.floor((s % 3600) / 60) })
}

function Meter({ label, value, detail, icon }: { label: string; value: number; detail: string; icon: ReactNode }) {
  const pct = Math.max(0, Math.min(100, Math.round(value)))
  const tone = pct >= 90 ? "bg-nq-danger" : pct >= 75 ? "bg-nq-accent" : "bg-nq-brand"
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="inline-flex items-center gap-1.5 text-muted-foreground [&_svg]:size-3.5">
          {icon}
          {label}
        </span>
        <span dir="ltr" className="font-mono tabular-nums text-foreground">
          {pct}%
        </span>
      </div>
      <div className="h-1.5 w-full bg-border" aria-hidden>
        <div className={`h-full ${tone}`} style={{ width: `${pct}%` }} />
      </div>
      <span dir="ltr" className="truncate text-start font-mono text-xs text-muted-foreground">
        {detail}
      </span>
    </div>
  )
}

/**
 * One server on the home page: who it is (status, address, tunnel), what it is (the hardware the
 * agent reports), how it is doing (live usage and the last hour as charts) and what shipped to it
 * last. The whole card links to the server.
 */
export function ServerCard({
  server,
  base,
  tunnel,
  spark,
  lastDeploy,
  streamUrl,
  guests,
}: {
  server: Server & { facts?: Server["facts"] & { hardware?: Hardware; primary_ip?: string } }
  base: string
  tunnel?: TunnelSummary | null
  spark?: Spark | null
  lastDeploy?: { app: App; deployment: Deployment } | null
  /** The server's live stream (SSE); its readings move the charts in real time. */
  streamUrl?: string
  /** Set for a Proxmox node: how many guests it has and how many run. They link to its Guests tab
   * instead of showing as servers of their own. */
  guests?: { total: number; running: number } | null
}) {
  const { t, locale } = useI18n()
  const ago = useTimeAgo(locale)
  const m = server.metrics
  const f = server.facts
  const hw = f?.hardware
  const cpus = hw?.cpu_threads ?? f?.cpus
  const ip = hw?.public_ip ?? server.ip_address ?? f?.primary_ip ?? null
  const gpus = hw?.gpus ?? []
  const disk = hw?.disks?.find((d) => d.mount === "/") ?? null
  const online = server.online
  const live = useLiveSeries(online && streamUrl ? streamUrl : null, spark)
  const lastOf = (a: number[], fallback: number) => (a.length ? a[a.length - 1] : fallback)
  const href = `${base}/servers/${server.id}`
  tunnel = tunnel ?? server.tunnel ?? null

  const facts: [string, ReactNode][] = [
    [t("home.os"), f?.os_pretty || [server.os, server.os_version].filter(Boolean).join(" ") || "—"],
    [
      t("home.cpu"),
      hw?.cpu_model ? `${hw.cpu_model}${cpus ? ` · ${cpus}` : ""}` : cpus ? t("home.cpuCount", { n: cpus }) : "—",
    ],
    [t("home.memory"), f?.mem_total_bytes ? humanBytes(f.mem_total_bytes) : m?.mem_total_bytes ? humanBytes(m.mem_total_bytes) : "—"],
    [t("home.disk"), disk ? humanBytes(disk.size_bytes) : m?.disk_total_bytes ? humanBytes(m.disk_total_bytes) : "—"],
  ]
  if (gpus.length) facts.push([t("home.gpu"), gpus.map((g) => g.name).join(", ")])
  facts.push([t("home.virt"), [server.provider === "proxmox" ? "Proxmox" : null, server.kind !== "byo" ? server.kind.toUpperCase() : null, f?.virtualization || null].filter(Boolean).join(" · ") || "—"])

  return (
    <article
      className="flex min-w-0 flex-col border border-border bg-card"
      style={server.color ? { borderTopColor: server.color, borderTopWidth: 3 } : undefined}
      data-server={server.name}
    >
      <Link href={href} className="flex flex-col gap-3 px-5 pt-4 pb-3 hover:bg-secondary">
        <div className="flex items-center gap-3">
          <ServerMark server={server} />
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-2">
              <span dir="ltr" className="truncate font-medium text-foreground">
                {server.name}
              </span>
              <span
                aria-hidden
                className={online ? "size-2 shrink-0 bg-nq-success" : "size-2 shrink-0 bg-nq-fg-muted opacity-60"}
              />
              <span className="text-xs text-muted-foreground">{online ? t("home.online") : t(`servers.statuses.${server.status}`, undefined, t("home.offline"))}</span>
            </span>
            <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
              {ip ? (
                <span dir="ltr" className="font-mono">
                  {ip}
                </span>
              ) : null}
              {m?.uptime_seconds && online ? <span>{uptime(m.uptime_seconds, t)}</span> : null}
              {server.agent_version ? (
                <span dir="ltr" className="font-mono">
                  agent {server.agent_version}
                </span>
              ) : null}
            </span>
          </span>
          {tunnel ? (
            <span
              className={
                tunnel.status === "active" || tunnel.status === "up" || tunnel.status === "running"
                  ? "inline-flex shrink-0 items-center gap-1.5 border border-border bg-background px-2 py-1 text-xs text-foreground"
                  : "inline-flex shrink-0 items-center gap-1.5 border border-border px-2 py-1 text-xs text-muted-foreground"
              }
              title={tunnel.address ?? undefined}
            >
              <NetworkIcon className="size-3.5 text-nq-brand" />
              <span className="uppercase">{tunnel.kind}</span>
              <span>{t(`home.tunnel.${tunnel.status}`, undefined, tunnel.status)}</span>
            </span>
          ) : null}
        </div>

        <dl className="grid grid-cols-2 gap-x-4 gap-y-1.5 text-xs sm:grid-cols-3">
          {facts.map(([k, v]) => (
            <div key={k} className="min-w-0">
              <dt className="eyebrow">{k}</dt>
              <dd className="truncate text-foreground" title={typeof v === "string" ? v : undefined}>
                {v}
              </dd>
            </div>
          ))}
        </dl>
      </Link>

      {guests && guests.total > 0 ? (
        <Link
          href={`${href}/settings#import`}
          className="flex items-center gap-2 border-t border-border px-5 py-2.5 text-xs hover:bg-secondary"
          data-testid="node-guests"
        >
          <BoxesIcon className="size-3.5 shrink-0 text-nq-brand" />
          <span className="font-medium text-foreground">{t("home.guests", { n: guests.total })}</span>
          <span className="text-muted-foreground">{t("home.guestsRunning", { n: guests.running })}</span>
          <ChevronRightIcon className="ms-auto size-3.5 shrink-0 text-muted-foreground rtl:rotate-180" />
        </Link>
      ) : null}

      {online && m ? (
        <Link
          href={`${href}/metrics`}
          className="grid grid-cols-1 gap-4 border-t border-border px-5 py-4 hover:bg-secondary sm:grid-cols-3"
          title={t("home.metricsLink")}
        >
          <div className="flex min-w-0 flex-col gap-2">
            <Meter
              label={t("home.cpu")}
              value={lastOf(live.cpu, m.cpu_percent)}
              detail={t("home.load", { load: m.load1.toFixed(2) })}
              icon={<CpuIcon />}
            />
            {live.cpu.length > 1 ? <Sparkline values={live.cpu} label={t("home.cpuChart")} /> : null}
          </div>
          <div className="flex min-w-0 flex-col gap-2">
            <Meter
              label={t("home.memory")}
              value={lastOf(live.mem, m.mem_total_bytes ? (m.mem_used_bytes / m.mem_total_bytes) * 100 : 0)}
              detail={`${humanBytes(m.mem_used_bytes)} / ${humanBytes(m.mem_total_bytes)}`}
              icon={<MemoryStickIcon />}
            />
            {live.mem.length > 1 ? <Sparkline values={live.mem} label={t("home.memChart")} tone="gold" /> : null}
          </div>
          <div className="flex min-w-0 flex-col gap-2">
            {live.gpu.length ? (
              <>
                <Meter label={t("home.gpu")} value={lastOf(live.gpu, 0)} detail={gpus[0]?.name ?? ""} icon={<ZapIcon />} />
                {live.gpu.length > 1 ? <Sparkline values={live.gpu} label={t("home.gpuChart")} tone="ok" /> : null}
              </>
            ) : (
              <Meter
                label={t("home.disk")}
                value={m.disk_total_bytes ? (m.disk_used_bytes / m.disk_total_bytes) * 100 : 0}
                detail={`${humanBytes(m.disk_used_bytes)} / ${humanBytes(m.disk_total_bytes)}`}
                icon={<HardDriveIcon />}
              />
            )}
          </div>
        </Link>
      ) : (
        <p className="border-t border-border px-5 py-4 text-xs text-muted-foreground">
          {server.last_seen_at ? t("home.lastSeen", { when: ago(server.last_seen_at) }) : t("home.noReport")}
        </p>
      )}

      <div className="mt-auto flex items-center gap-2 border-t border-border px-5 py-3 text-xs">
        <RocketIcon className="size-3.5 shrink-0 text-muted-foreground" />
        {lastDeploy ? (
          <Link href={`${base}/apps/${lastDeploy.app.name}`} className="flex min-w-0 flex-1 items-center gap-2 hover:underline">
            <span dir="ltr" className="truncate font-medium text-foreground">
              {lastDeploy.app.name}
            </span>
            <span dir="ltr" className="font-mono text-muted-foreground">
              #{lastDeploy.deployment.number}
            </span>
            <DeploymentStatusBadge status={lastDeploy.deployment.status} />
            <span className="ms-auto shrink-0 text-muted-foreground">{ago(lastDeploy.deployment.created_at)}</span>
          </Link>
        ) : (
          <span className="text-muted-foreground">{t("home.noDeploys")}</span>
        )}
      </div>
    </article>
  )
}
