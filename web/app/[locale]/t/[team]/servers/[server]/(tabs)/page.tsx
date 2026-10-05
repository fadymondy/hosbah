"use client"

import { Button, Card, Progress, StatCard, StatGrid } from "@fadymondy/nasaq/web"
import { useState, type ReactNode } from "react"
import { ActivityIcon, AlertCircleIcon, CpuIcon, GaugeIcon, HardDriveIcon, LoaderIcon, MemoryStickIcon, TimerIcon } from "lucide-react"

import { CopyField } from "@/components/copy-field"
import { useErrorText } from "@/components/form-error"
import { useLoadedServer } from "@/components/server-tabs/context"
import { CheckBadge } from "@/components/server-status"
import { RunningHere } from "@/components/server-tabs/running-here"
import { Notice, SectionTitle } from "@/components/states"

import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { formatBytes, formatUptime, percent, platformOf, type Install } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/** The server's overview tab: its state, live usage, facts and enrolment checks. */
export default function ServerOverview() {
  const { t } = useI18n()
  const { manage } = useTeam()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const { server, setServer, setError, path } = useLoadedServer()
  const [install, setInstall] = useState<Install | null>(null)

  async function newInstall() {
    setError(null)
    try {
      const inst = await apiFetch<Install>(`${path}/enrollment`, { method: "POST" })
      setInstall(inst)
      setServer((s) => (s ? { ...s, enrollment: { expires_at: inst.expires_at } } : s))
    } catch (err) {
      setError(errorText(err))
    }
  }

  const enrolled = server.status === "enrolled"
  const m = server.metrics
  const docker = server.facts?.docker

  return (
    <>
      {server.status === "pending" ? (
        <Notice data-testid="pending" icon={<LoaderIcon className="animate-spin" />} title={t("servers.detail.pendingTitle")}>
          <span>{t("servers.detail.pendingHint")}</span>
          <span>
            {server.enrollment
              ? t("servers.detail.pendingExpires", { when: fmt(server.enrollment.expires_at) })
              : t("servers.detail.pendingExpired")}
          </span>
          {install ? (
            <div className="flex max-w-3xl flex-col gap-3">
              <CopyField value={install.command} />
              <CopyField value={install.command_root} />
            </div>
          ) : null}
          {manage ? (
            <div>
              <Button size="sm" variant="secondary" onClick={newInstall}>
                {t("servers.detail.newInstall")}
              </Button>
            </div>
          ) : null}
        </Notice>
      ) : null}
      {server.status === "provisioning" ? (
        <Notice icon={<LoaderIcon className="animate-spin" />} title={t("servers.detail.provisioningTitle")}>
          {t("servers.detail.provisioningHint")}
        </Notice>
      ) : null}
      {server.status === "failed" ? (
        <Notice tone="danger" icon={<AlertCircleIcon />} title={t("servers.detail.failedTitle")}>
          <span dir="auto">{server.error}</span>
        </Notice>
      ) : null}

      {enrolled ? <RunningHere serverId={server.id} /> : null}

      {enrolled ? (
        <section aria-labelledby="metrics-h">
          <SectionTitle id="metrics-h" title={t("servers.detail.metrics")} />
          {m ? (
            <StatGrid className="mt-3" data-testid="metrics">
              <Meter icon={<CpuIcon />} label={t("servers.cpu")} value={Math.round(m.cpu_percent)} text={`${m.cpu_percent.toFixed(1)}%`} />
              <Meter
                icon={<MemoryStickIcon />}
                label={t("servers.memory")}
                value={percent(m.mem_used_bytes, m.mem_total_bytes)}
                text={`${formatBytes(m.mem_used_bytes)} / ${formatBytes(m.mem_total_bytes)}`}
              />
              <Meter
                icon={<HardDriveIcon />}
                label={t("servers.disk")}
                value={percent(m.disk_used_bytes, m.disk_total_bytes)}
                text={`${formatBytes(m.disk_used_bytes)} / ${formatBytes(m.disk_total_bytes)}`}
              />
              <StatCard icon={<GaugeIcon />} label={t("servers.load")} value={<Ltr>{m.load1.toFixed(2)}</Ltr>} />
              <StatCard icon={<TimerIcon />} label={t("servers.uptime")} value={<Ltr>{formatUptime(m.uptime_seconds)}</Ltr>} />
              <StatCard
                icon={<ActivityIcon />}
                label={t("servers.lastSeen")}
                value={<span className="text-base">{server.last_seen_at ? fmt(server.last_seen_at) : t("servers.never")}</span>}
              />
            </StatGrid>
          ) : (
            <Card className="mt-3 px-5 py-6 text-center text-sm text-muted-foreground">{t("servers.detail.noMetrics")}</Card>
          )}
        </section>
      ) : null}

      <div className="grid gap-4 lg:grid-cols-2">
        <Card aria-labelledby="facts-h" className="min-w-0 gap-0 py-0">
          <h2 id="facts-h" className="border-b border-border px-5 py-4 text-h4 font-semibold text-foreground">
            {t("servers.detail.facts")}
          </h2>
          <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-3 px-5 py-4 text-sm">
            <Fact
              label={t("servers.discovered.platform")}
              value={
                platformOf(server.facts)
                  ? t(`servers.discovered.platforms.${platformOf(server.facts)}`) +
                    (server.facts?.proxmox?.version ? ` ${server.facts.proxmox.version}` : "")
                  : null
              }
            />
            <Fact label={t("servers.detail.hostname")} value={server.hostname} />
            <Fact label={t("servers.address")} value={server.ip_address} />
            <Fact label={t("servers.os")} value={server.facts?.os_pretty || (server.os ? `${server.os} ${server.os_version ?? ""}` : null)} />
            <Fact label={t("servers.detail.kernel")} value={server.kernel} />
            <Fact label={t("servers.detail.arch")} value={server.arch} />
            <Fact label={t("servers.agent")} value={server.agent_version} />
            <Fact
              label={t("servers.detail.docker")}
              value={
                !docker
                  ? null
                  : !docker.installed
                    ? t("servers.detail.dockerMissing")
                    : !docker.running
                      ? t("servers.detail.dockerStopped")
                      : `${docker.version} (${docker.storage_driver})`
              }
            />
          </dl>
        </Card>
        <Card aria-labelledby="checks-h" className="min-w-0 gap-0 py-0">
          <h2 id="checks-h" className="border-b border-border px-5 py-4 text-h4 font-semibold text-foreground">
            {t("servers.detail.checks")}
          </h2>
          {server.checks.length === 0 ? (
            <p className="px-5 py-6 text-center text-sm text-muted-foreground">{t("servers.detail.checksEmpty")}</p>
          ) : (
            <ul className="flex flex-col divide-y divide-border text-sm" data-testid="checks">
              {server.checks.map((c) => (
                <li key={c.name} className="flex flex-wrap items-center gap-2 px-5 py-3">
                  <CheckBadge status={c.status} />
                  <span className="font-medium">{t(`servers.check.names.${c.name}`, undefined, c.name)}</span>
                  <span dir="auto" className="text-muted-foreground">
                    {c.detail}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>

    </>
  )
}

function Meter({ icon, label, value, text }: { icon: ReactNode; label: string; value: number; text: string }) {
  return (
    <StatCard
      icon={icon}
      label={label}
      value={
        <span className="flex flex-col gap-2">
          <span className="tabular-nums">{value}%</span>
          <Progress value={value} aria-label={label} size="sm" />
          <Ltr className="text-caption font-normal text-muted-foreground">{text}</Ltr>
        </span>
      }
    />
  )
}

function Ltr({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span dir="ltr" className={cn("font-mono tabular-nums", className)}>
      {children}
    </span>
  )
}

function Fact({ label, value }: { label: string; value: string | null | undefined }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd dir="ltr" className="min-w-0 text-start font-mono text-xs leading-5 break-all">
        {value || "—"}
      </dd>
    </>
  )
}

