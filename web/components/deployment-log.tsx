"use client"

import { ScrollArea } from "@fadymondy/nasaq/web"
import { useEffect, useRef, useState } from "react"
import { LoaderIcon } from "lucide-react"

import { DeploymentStatusBadge } from "@/components/app-status"
import { clock, lineTone, render, scrollToEnd } from "@/components/op-log"

import { isActive, type Deployment, type DeployLogLine } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import { useEventStream } from "@/lib/servers"

/**
 * One deployment's build/deploy log, live over SSE. The stream replays what is stored, then follows;
 * lines are de-duplicated by seq and each carries the step it came from (app.build, route.set…).
 */
export function DeploymentLog({
  team,
  app,
  deployment,
  onStatus,
  actions,
}: {
  team: string
  app: string
  deployment: Deployment
  onStatus?: (d: Deployment) => void
  actions?: React.ReactNode
}) {
  const { t } = useI18n()
  const [lines, setLines] = useState<DeployLogLine[]>([])
  const [live, setLive] = useState<Deployment>(deployment)
  const bottom = useRef<HTMLDivElement>(null)
  const follow = useRef(true)
  const onStatusRef = useRef(onStatus)
  onStatusRef.current = onStatus

  useEffect(() => {
    setLines([])
    setLive(deployment)
    follow.current = true
    // Only when a different deployment is selected.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deployment.id])

  // The list (app stream) may know a newer status than this stream's last frame.
  useEffect(() => {
    setLive((cur) => (cur.id === deployment.id ? { ...cur, ...deployment } : cur))
  }, [deployment])

  const url = `/api/teams/${encodeURIComponent(team)}/apps/${encodeURIComponent(app)}/deployments/${deployment.id}/stream`
  const connected = useEventStream(url, {
    line: (d) => {
      const l = d as DeployLogLine
      setLines((prev) => (prev.some((p) => p.seq === l.seq) ? prev : [...prev, l].sort((a, b) => a.seq - b.seq)))
    },
    status: (d) => {
      setLive(d as Deployment)
      onStatusRef.current?.(d as Deployment)
    },
    done: () => {},
    gone: () => {},
  })

  useEffect(() => {
    if (follow.current) scrollToEnd(bottom.current)
  }, [lines.length])

  const active = isActive(live)
  return (
    <div
      className="flex min-w-0 flex-col"
      data-testid="deployment-log"
      data-deployment={deployment.id}
      data-status={live.status}
      data-lines={lines.length}
    >
      <div className="border-b border-border flex flex-wrap items-center gap-2 px-4 py-2 text-sm">
        <span className="font-medium">
          {t(`apps.deploy.kinds.${live.kind}`, undefined, live.kind)}{" "}
          <span dir="ltr" className="font-mono">
            #{live.number}
          </span>
        </span>
        <DeploymentStatusBadge status={live.status} />
        {live.step && active ? (
          <span className="text-xs text-muted-foreground">
            {t(`apps.deploy.steps.${live.step}`, undefined, live.step)}
          </span>
        ) : null}
        {live.cancel_requested && active ? (
          <span className="text-xs text-muted-foreground">{t("apps.deploy.cancelRequested")}</span>
        ) : null}
        <span className="ms-auto flex items-center gap-2">
          <span className="text-xs text-muted-foreground" aria-live="polite">
            {connected ? t("servers.detail.live") : null}
          </span>
          {actions}
        </span>
      </div>
      {live.error ? (
        <p dir="auto" role="alert" className="border-b border-border px-4 py-2 text-sm text-destructive">
          {live.error}
        </p>
      ) : null}
      <ScrollArea className="h-[28rem] bg-muted/30">
        <pre dir="ltr" className="p-4 text-start font-mono text-xs leading-relaxed whitespace-pre-wrap">
          {lines.length === 0 ? (
            <span className="flex items-center gap-2 text-muted-foreground">
              {active ? <LoaderIcon className="size-3 animate-spin" aria-hidden /> : null}
              {active ? t("apps.deploy.logWaiting") : t("apps.deploy.logEmpty")}
            </span>
          ) : null}
          {lines.map((l) => (
            <div key={l.seq} className={`flex gap-3 ${lineTone(l)}`} data-seq={l.seq}>
              <span className="shrink-0 text-muted-foreground tabular-nums select-none">{clock(l.at)}</span>
              {l.op ? (
                <span className="w-24 shrink-0 truncate text-muted-foreground select-none" title={l.op}>
                  {l.op}
                </span>
              ) : null}
              <span className="min-w-0 break-all">{render(l)}</span>
            </div>
          ))}
          <div ref={bottom} />
        </pre>
      </ScrollArea>
    </div>
  )
}
