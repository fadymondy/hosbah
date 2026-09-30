"use client"

import { createContext, useContext, useState } from "react"
import { BaseEdge, EdgeLabelRenderer, getBezierPath, Handle, Position, type Edge, type EdgeProps, type Node, type NodeProps } from "@xyflow/react"
import {
  ActivityIcon,
  AppWindowIcon,
  ArchiveIcon,
  BellRingIcon,
  BlocksIcon,
  CalendarClockIcon,
  CloudIcon,
  CogIcon,
  DatabaseIcon,
  GlobeIcon,
  HardDriveIcon,
  NetworkIcon,
  PlusIcon,
  RadioIcon,
  UsersIcon,
  XIcon,
  type LucideIcon,
} from "lucide-react"

import type { BoxData, NodeKind, Removal, Status } from "@/components/site/canvas/model"
import { useI18n } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export type BoxNode = Node<BoxData, "box">
export type LinkEdge = Edge<{ removal: Removal | null }, "link">

export const KIND_ICON: Record<NodeKind, LucideIcon> = {
  visitors: UsersIcon,
  domain: CloudIcon,
  caddy: NetworkIcon,
  app: AppWindowIcon,
  worker: CogIcon,
  realtime: RadioIcon,
  db: DatabaseIcon,
  volume: HardDriveIcon,
  bucket: ArchiveIcon,
  browser: GlobeIcon,
  install: BlocksIcon,
  monitor: BellRingIcon,
  schedule: CalendarClockIcon,
}

export const STATUS_DOT: Record<Status, string> = {
  ok: "bg-nq-success",
  warn: "bg-nq-warning",
  error: "bg-nq-danger",
  busy: "bg-nq-brand animate-pulse",
  off: "bg-nq-fg-muted",
}

/** What boxes and edges ask of the canvas around them. */
export type CanvasActions = {
  manage: boolean
  openPalette: () => void
  remove: (removal: Removal) => void
  labels: { add: string; remove: string; disconnect: string }
}
export const CanvasCtx = createContext<CanvasActions | null>(null)

/** A status dot with its words for screen readers and a tooltip. */
export function StatusDot({ status, label, className }: { status: Status | null; label: string | null; className?: string }) {
  if (!status) return null
  return (
    <span className={cn("inline-flex shrink-0 items-center", className)} title={label ?? undefined}>
      <span aria-hidden className={cn("size-2 rounded-full", STATUS_DOT[status])} />
      <span className="sr-only">{label}</span>
    </span>
  )
}

/**
 * One box. The canvas is always left to right, so handles are physical: things arrive on the left
 * and leave on the right. Only the app's right handle starts a connection; every box's left handle
 * can be hovered with one (so a refusal shows), and only a database that is not connected yet takes it.
 *
 * kind "domain" stands for every domain of the site at once (model.ts's DOMAINS_BOX_ID), drawn as a
 * stack of cards offset behind each other — the front card is data.domains[0] plus a "+N" chip —
 * that expands into the full list on hover or click/focus of the chip, each with its own remove.
 */
export function BoxCard({ data, selected }: NodeProps<BoxNode>) {
  const ctx = useContext(CanvasCtx)
  const { t } = useI18n()
  const Icon = data.kind === "monitor" && data.lines[0]?.startsWith("http") ? ActivityIcon : KIND_ICON[data.kind]
  const manage = !!ctx?.manage
  const stack = data.kind === "domain" ? (data.domains ?? []) : []
  const extra = stack.length > 1 ? stack.length - 1 : 0
  const [expanded, setExpanded] = useState(false)

  return (
    <div
      className="group relative w-[216px]"
      onMouseEnter={() => extra > 0 && setExpanded(true)}
      onMouseLeave={() => extra > 0 && setExpanded(false)}
    >
      {extra > 0 ? (
        <>
          <div aria-hidden className="absolute inset-0 -z-10 translate-x-3 translate-y-3 rtl:-translate-x-3 border border-border bg-card opacity-30" />
          <div aria-hidden className="absolute inset-0 -z-10 translate-x-1.5 translate-y-1.5 rtl:-translate-x-1.5 border border-border bg-card opacity-50" />
        </>
      ) : null}
      <div
        className={cn(
          "relative w-[216px] border bg-card px-3 py-2.5 text-start shadow-xs transition-colors hover:bg-secondary",
          data.kind === "app" ? "border-nq-brand" : "border-border",
          data.dimmed && "border-dashed bg-background opacity-80",
          selected && "ring-2 ring-nq-brand ring-offset-1 ring-offset-nq-bg",
        )}
      >
        <Handle
          type="target"
          position={Position.Left}
          isConnectable={manage && data.kind !== "app" && data.kind !== "visitors"}
          className={cn("hosbah-handle", manage && data.connect === "target" ? "hosbah-handle-open" : "hosbah-handle-hidden")}
        />
        <div className="flex min-w-0 items-center gap-2">
          <Icon aria-hidden className="size-4 shrink-0 text-nq-brand" />
          <span dir="auto" className="eyebrow min-w-0 flex-1 truncate">
            {data.label}
          </span>
          <StatusDot status={data.status} label={data.statusLabel} />
        </div>
        <div className="mt-1 flex min-w-0 items-center gap-1.5">
          <div dir="auto" className="min-w-0 flex-1 truncate text-sm font-medium text-foreground" title={data.title}>
            {data.title}
          </div>
          {extra > 0 ? (
            <button
              type="button"
              dir="ltr"
              aria-expanded={expanded}
              aria-label={t("apps.domains.moreCount", { count: extra })}
              onClick={(e) => {
                e.stopPropagation()
                setExpanded((v) => !v)
              }}
              className="nodrag nopan shrink-0 rounded-full bg-secondary px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground hover:bg-secondary/80 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
            >
              +{extra}
            </button>
          ) : null}
        </div>
        {data.lines.map((l, n) => (
          <div key={n} dir="ltr" className="truncate text-start font-mono text-xs text-muted-foreground" title={l}>
            {l}
          </div>
        ))}
        {manage && data.removal ? (
          <button
            type="button"
            tabIndex={-1}
            aria-label={data.removal.kind === "detach" ? ctx!.labels.disconnect : ctx!.labels.remove}
            title={data.removal.kind === "detach" ? ctx!.labels.disconnect : ctx!.labels.remove}
            onClick={(e) => {
              e.stopPropagation()
              ctx!.remove(data.removal!)
            }}
            className="nodrag nopan absolute -top-2.5 -right-2.5 hidden size-5 items-center justify-center border border-border bg-card text-muted-foreground group-hover:flex hover:text-nq-danger-text focus-visible:flex"
          >
            <XIcon className="size-3" />
          </button>
        ) : null}
        <Handle
          type="source"
          position={Position.Right}
          isConnectable={manage && data.connect === "source"}
          className={cn("hosbah-handle", manage && data.connect === "source" ? "hosbah-handle-open" : "hosbah-handle-hidden")}
        />
        {manage && data.kind === "app" ? (
          <button
            type="button"
            aria-label={ctx!.labels.add}
            title={ctx!.labels.add}
            onClick={(e) => {
              e.stopPropagation()
              ctx!.openPalette()
            }}
            className="nodrag nopan absolute top-1/2 -right-11 flex size-6 -translate-y-1/2 items-center justify-center border border-nq-brand bg-card text-nq-brand hover:bg-secondary"
          >
            <PlusIcon className="size-3.5" />
          </button>
        ) : null}
      </div>
      {extra > 0 && expanded ? (
        <div className="nodrag nopan absolute start-0 top-full z-50 mt-1 max-h-56 w-[216px] overflow-y-auto border border-border bg-card shadow-lg">
          {stack.map((d) => (
            <div key={d.id} className="flex items-center gap-2 border-b border-border px-3 py-2 last:border-b-0">
              <StatusDot status={d.verified ? "ok" : "warn"} label={t(d.verified ? "apps.domains.verified" : "apps.domains.unverified")} />
              <span dir="ltr" className="min-w-0 flex-1 truncate font-mono text-xs" title={d.hostname}>
                {d.hostname}
              </span>
              {manage ? (
                <button
                  type="button"
                  aria-label={ctx!.labels.remove}
                  title={ctx!.labels.remove}
                  onClick={(e) => {
                    e.stopPropagation()
                    ctx!.remove({ kind: "domain", domain: d })
                  }}
                  className="shrink-0 text-muted-foreground hover:text-nq-danger-text"
                >
                  <XIcon className="size-3" />
                </button>
              ) : null}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  )
}

/** An edge; a selected one that can be undone shows a remove button at its middle. */
export function LinkLine({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, selected, data, markerEnd, style }: EdgeProps<LinkEdge>) {
  const ctx = useContext(CanvasCtx)
  const [path, labelX, labelY] = getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition })
  const removal = data?.removal ?? null
  return (
    <>
      <BaseEdge id={id} path={path} markerEnd={markerEnd} style={style} />
      {selected && removal && ctx?.manage ? (
        <EdgeLabelRenderer>
          <button
            type="button"
            aria-label={removal.kind === "detach" ? ctx.labels.disconnect : ctx.labels.remove}
            title={removal.kind === "detach" ? ctx.labels.disconnect : ctx.labels.remove}
            onClick={(e) => {
              e.stopPropagation()
              ctx.remove(removal)
            }}
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
            className="nodrag nopan pointer-events-auto absolute flex size-6 items-center justify-center border border-border bg-card text-muted-foreground hover:text-nq-danger-text"
          >
            <XIcon className="size-3.5" />
          </button>
        </EdgeLabelRenderer>
      ) : null}
    </>
  )
}
