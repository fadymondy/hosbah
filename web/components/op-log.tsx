"use client"

import { useEffect, useRef, useState } from "react"

import { OpStatusBadge } from "@/components/server-status"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useI18n } from "@/lib/i18n"
import { opKey, useEventStream, type LogLine, type Operation } from "@/lib/servers"
import { cn } from "@/lib/utils"

/**
 * One operation's log, live over SSE (the stream replays what is stored, then follows). Lines are
 * de-duplicated by seq, so a reconnect — the browser resumes with Last-Event-ID — never repeats one.
 */
export function OperationLog({ team, serverId, op }: { team: string; serverId: string; op: Operation | null }) {
  const { t } = useI18n()
  const [lines, setLines] = useState<LogLine[]>([])
  const [live, setLive] = useState<Operation | null>(op)
  const bottom = useRef<HTMLDivElement>(null)

  useEffect(() => {
    setLines([])
    setLive(op)
    // Only when a different operation is selected.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [op?.id])

  const url = op ? `/api/teams/${encodeURIComponent(team)}/servers/${serverId}/operations/${op.id}/stream` : null
  const connected = useEventStream(url, {
    line: (d) => {
      const l = d as LogLine
      setLines((prev) => (prev.some((p) => p.seq === l.seq) ? prev : [...prev, l].sort((a, b) => a.seq - b.seq)))
    },
    status: (d) => setLive(d as Operation),
    done: () => {},
    gone: () => {},
  })

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "nearest" })
  }, [lines.length])

  if (!op) {
    return <p className="p-4 text-sm text-muted-foreground">{t("servers.ops.logEmpty")}</p>
  }
  const status = live?.status ?? op.status
  return (
    <div className="flex min-w-0 flex-col" data-testid="op-log" data-op={op.id} data-status={status}>
      <div className="grid-rule-bottom flex flex-wrap items-center gap-2 px-4 py-2 text-sm">
        <span className="font-medium">{t(`servers.ops.kinds.${opKey(op.kind)}`, undefined, op.kind)}</span>
        <OpStatusBadge status={status} />
        <span className="ms-auto text-xs text-muted-foreground" aria-live="polite">
          {connected ? t("servers.detail.live") : null}
        </span>
      </div>
      <ScrollArea className="h-80 bg-muted/30">
        <pre dir="ltr" className="p-4 text-start font-mono text-xs leading-relaxed whitespace-pre-wrap">
          {lines.length === 0 ? <span className="text-muted-foreground">{t("servers.ops.logWaiting")}</span> : null}
          {lines.map((l) => (
            <div key={l.seq} className={cn("flex gap-3", lineTone(l))} data-seq={l.seq}>
              <span className="shrink-0 text-muted-foreground tabular-nums select-none">{clock(l.at)}</span>
              <span className="min-w-0">{render(l)}</span>
            </div>
          ))}
          <div ref={bottom} />
        </pre>
      </ScrollArea>
    </div>
  )
}

export function clock(at: string): string {
  const d = new Date(at)
  return Number.isNaN(d.getTime()) ? "" : d.toISOString().slice(11, 19)
}

export function render(l: LogLine): string {
  if (l.event === "progress") return `[${l.step}/${l.total}]`
  if (l.event === "status") return l.status === "running" ? "▶ started" : (l.line ?? `finished: ${l.status}`)
  return l.line ?? ""
}

export function lineTone(l: LogLine): string {
  if (l.event === "status") return l.status === "ok" || l.status === "running" ? "font-medium" : "font-medium text-destructive"
  if (l.stream === "stderr" || l.stream === "error") return "text-destructive"
  if (l.stream === "info" || l.event === "progress") return "text-muted-foreground"
  return ""
}
