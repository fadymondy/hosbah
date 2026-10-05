"use client"

import { Button, EmptyState } from "@fadymondy/nasaq/web"
import { useEffect, useRef, useState } from "react"
import { RefreshCwIcon, SquareTerminalIcon } from "lucide-react"
import type { Terminal as XTerm } from "@xterm/xterm"

import { SectionTitle } from "@/components/states"

import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/*
Terminal (MH-574 E11): a live shell on a server, run through its agent (internal/agent/terminal.go,
internal/api/terminal.go, internal/ops/terminal.go). The browser dials a raw websocket at
/api/teams/{team}/servers/{server}/terminal/ws and exchanges agentproto.Frame JSON: term_data
(both ways, base64), term_resize, term_close. Nothing typed or shown here is ever sent anywhere
but the agent — no keystroke or output is logged; only that a session opened and closed (the
control plane's audit log). The terminal area is always dir="ltr", whatever the page's direction.
*/

type Status = "connecting" | "connected" | "closed"

type Frame = {
  type: string
  session_id?: string
  term_data?: string
  term_cols?: number
  term_rows?: number
  term_reason?: string
}

function base64FromBytes(bytes: Uint8Array): string {
  let bin = ""
  for (const b of bytes) bin += String.fromCharCode(b)
  return btoa(bin)
}

function bytesFromBase64(b64: string): Uint8Array {
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

/**
 * The Terminal tab: an xterm.js view wired to the terminal websocket. `target` picks a host shell
 * ("host", the default — owner/admin only) or an app's live web container ("app:<id>", deployer+).
 */
export function ServerTerminal({ serverId, manage, target = "host" }: { serverId: string; manage: boolean; target?: string }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const [status, setStatus] = useState<Status>("connecting")
  const [reason, setReason] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  const hostRef = useRef<HTMLDivElement | null>(null)
  const termRef = useRef<XTerm | null>(null)

  useEffect(() => {
    if (!manage || !hostRef.current) return
    let disposed = false
    let ws: WebSocket | null = null
    let term: XTerm | null = null
    let resizeObserver: ResizeObserver | null = null

    setStatus("connecting")
    setReason(null)

    void (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([import("@xterm/xterm"), import("@xterm/addon-fit")])
      if (disposed || !hostRef.current) return

      const styles = getComputedStyle(document.documentElement)
      term = new Terminal({
        cursorBlink: true,
        fontSize: 13,
        fontFamily: "var(--font-mono, ui-monospace, monospace)",
        theme: {
          background: styles.getPropertyValue("--nq-bg").trim() || undefined,
          foreground: styles.getPropertyValue("--nq-fg").trim() || undefined,
          cursor: styles.getPropertyValue("--nq-fg").trim() || undefined,
        },
      })
      const fit = new FitAddon()
      term.loadAddon(fit)
      term.open(hostRef.current)
      fit.fit()
      termRef.current = term

      const proto = window.location.protocol === "https:" ? "wss:" : "ws:"
      const qs = new URLSearchParams({ target, cols: String(term.cols), rows: String(term.rows) })
      const url = `${proto}//${window.location.host}/api/teams/${team.slug}/servers/${serverId}/terminal/ws?${qs.toString()}`
      const socket = new WebSocket(url)
      ws = socket

      socket.onopen = () => {
        if (!disposed) setStatus("connected")
      }
      socket.onmessage = (ev) => {
        try {
          const f = JSON.parse(ev.data as string) as Frame
          if (f.type === "term_data" && f.term_data) term?.write(bytesFromBase64(f.term_data))
          else if (f.type === "term_close") {
            setReason(f.term_reason ?? null)
            setStatus("closed")
          }
        } catch {
          // ignore malformed frames
        }
      }
      const onEnd = () => {
        if (!disposed) setStatus((s) => (s === "connected" || s === "connecting" ? "closed" : s))
      }
      socket.onclose = onEnd
      socket.onerror = onEnd

      term.onData((data) => {
        if (socket.readyState !== WebSocket.OPEN) return
        socket.send(JSON.stringify({ type: "term_data", term_data: base64FromBytes(new TextEncoder().encode(data)) }))
      })

      resizeObserver = new ResizeObserver(() => {
        if (disposed || !term) return
        fit.fit()
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: "term_resize", term_cols: term.cols, term_rows: term.rows }))
        }
      })
      resizeObserver.observe(hostRef.current)
    })()

    return () => {
      disposed = true
      resizeObserver?.disconnect()
      if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "term_close" }))
      ws?.close()
      term?.dispose()
      termRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [manage, serverId, target, team.slug, attempt])

  if (!manage) {
    return (
      <section aria-labelledby="terminal-h" data-testid="terminal">
        <SectionTitle id="terminal-h" title={t("servers.terminal.title")} hint={t("servers.terminal.hint")} />
        <EmptyState className="mt-3 rounded-lg border border-border bg-card" icon={SquareTerminalIcon} title={t("servers.terminal.noAccessTitle")} description={t("servers.terminal.noAccessHint")} />
      </section>
    )
  }

  return (
    <section aria-labelledby="terminal-h" data-testid="terminal" className="flex min-h-0 flex-1 flex-col">
      <SectionTitle
        id="terminal-h"
        title={t("servers.terminal.title")}
        hint={t("servers.terminal.hint")}
        action={
          <div className="flex items-center gap-3">
            <StatusDot status={status} />
            <Button size="sm" variant="secondary" onClick={() => setAttempt((n) => n + 1)}>
              <RefreshCwIcon />
              {t("servers.terminal.reconnect")}
            </Button>
          </div>
        }
      />
      {status === "closed" && reason ? (
        <p className="mt-3 rounded-lg border border-border bg-card px-4 py-2 text-sm text-muted-foreground">{t("servers.terminal.closedNotice", { reason })}</p>
      ) : null}
      <div className="mt-3 min-h-[28rem] flex-1 overflow-hidden rounded-lg border border-border bg-background p-3" dir="ltr">
        <div ref={hostRef} className="h-full min-h-[28rem] w-full" />
      </div>
    </section>
  )
}

function StatusDot({ status }: { status: Status }) {
  const { t } = useI18n()
  const color = status === "connected" ? "bg-nq-success" : status === "connecting" ? "bg-nq-warning" : "bg-nq-fg-muted"
  return (
    <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
      <span className={`size-2 rounded-full ${color}`} aria-hidden />
      {t(`servers.terminal.${status}`)}
    </span>
  )
}
