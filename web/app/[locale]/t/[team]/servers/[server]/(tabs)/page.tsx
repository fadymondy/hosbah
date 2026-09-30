"use client"

import { Button } from "@fadymondy/nasaq/web"
import { useState } from "react"
import { AlertCircleIcon, LoaderIcon } from "lucide-react"

import { CopyField } from "@/components/copy-field"
import { useErrorText } from "@/components/form-error"
import { useLoadedServer } from "@/components/server-tabs/context"
import { CheckBadge } from "@/components/server-status"
import { Notice, SectionTitle } from "@/components/states"

import { Progress } from "@/components/ui/progress"
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

      {enrolled ? (
        <section aria-labelledby="metrics-h">
          <SectionTitle id="metrics-h" title={t("servers.detail.metrics")} />
          {m ? (
            <dl className="grid grid-cols-1 gap-px border-b border-border bg-border sm:grid-cols-3 lg:grid-cols-6" data-testid="metrics">
              <Meter label={t("servers.cpu")} value={Math.round(m.cpu_percent)} text={`${m.cpu_percent.toFixed(1)}%`} />
              <Meter
                label={t("servers.memory")}
                value={percent(m.mem_used_bytes, m.mem_total_bytes)}
                text={`${formatBytes(m.mem_used_bytes)} / ${formatBytes(m.mem_total_bytes)}`}
              />
              <Meter
                label={t("servers.disk")}
                value={percent(m.disk_used_bytes, m.disk_total_bytes)}
                text={`${formatBytes(m.disk_used_bytes)} / ${formatBytes(m.disk_total_bytes)}`}
              />
              <Stat label={t("servers.load")} value={m.load1.toFixed(2)} />
              <Stat label={t("servers.uptime")} value={formatUptime(m.uptime_seconds)} />
              <Stat label={t("servers.lastSeen")} value={server.last_seen_at ? fmt(server.last_seen_at) : t("servers.never")} small />
            </dl>
          ) : (
            <p className="border-b border-border px-6 py-4 text-sm text-muted-foreground">{t("servers.detail.noMetrics")}</p>
          )}
        </section>
      ) : null}

      <div className="grid gap-px border-b border-border bg-border lg:grid-cols-2">
        <section aria-labelledby="facts-h" className="min-w-0 bg-background">
          <h2 id="facts-h" className="px-6 pt-5 pb-3 text-base font-medium text-foreground">
            {t("servers.detail.facts")}
          </h2>
          <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 border-t border-border px-6 py-4 text-sm">
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
        </section>
        <section aria-labelledby="checks-h" className="min-w-0 bg-background">
          <h2 id="checks-h" className="px-6 pt-5 pb-3 text-base font-medium text-foreground">
            {t("servers.detail.checks")}
          </h2>
          {server.checks.length === 0 ? (
            <p className="border-t border-border px-6 py-4 text-sm text-muted-foreground">{t("servers.detail.checksEmpty")}</p>
          ) : (
            <ul className="flex flex-col divide-y divide-border border-t border-border text-sm" data-testid="checks">
              {server.checks.map((c) => (
                <li key={c.name} className="flex flex-wrap items-center gap-2 px-6 py-2.5">
                  <CheckBadge status={c.status} />
                  <span className="font-medium">{t(`servers.check.names.${c.name}`, undefined, c.name)}</span>
                  <span dir="auto" className="text-muted-foreground">
                    {c.detail}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>

    </>
  )
}

function Meter({ label, value, text }: { label: string; value: number; text: string }) {
  return (
    <div className="flex flex-col gap-2 bg-background px-6 py-4">
      <dt className="eyebrow">{label}</dt>
      <dd className="flex flex-col gap-2">
        <span dir="ltr" className="text-start font-mono text-sm tabular-nums">
          {text}
        </span>
        <Progress value={value} aria-label={label} />
      </dd>
    </div>
  )
}

function Stat({ label, value, small }: { label: string; value: string; small?: boolean }) {
  return (
    <div className="flex flex-col gap-2 bg-background px-6 py-4">
      <dt className="eyebrow">{label}</dt>
      <dd dir="ltr" className={cn("text-start font-mono tabular-nums", small ? "text-xs" : "text-sm")}>
        {value}
      </dd>
    </div>
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

