"use client"

import { createContext, useContext } from "react"
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
  ok: "bg-grid-ok",
  warn: "bg-grid-warn",
  error: "bg-grid-danger",
  busy: "bg-grid-brand animate-pulse",
  off: "bg-grid-muted",
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
 */
export function BoxCard({ data, selected }: NodeProps<BoxNode>) {
  const ctx = useContext(CanvasCtx)
  const Icon = data.kind === "monitor" && data.lines[0]?.startsWith("http") ? ActivityIcon : KIND_ICON[data.kind]
  const manage = !!ctx?.manage
  return (
    <div
      className={cn(
        "group relative w-[216px] border bg-grid-card px-3 py-2.5 text-start shadow-xs transition-colors hover:bg-grid-soft",
        data.kind === "app" ? "border-[var(--grid-brand)]" : "border-line",
        data.dimmed && "border-dashed bg-grid-bg opacity-80",
        selected && "ring-2 ring-[var(--grid-brand)] ring-offset-1 ring-offset-[var(--grid-bg)]",
      )}
    >
      <Handle
        type="target"
        position={Position.Left}
        isConnectable={manage && data.kind !== "app" && data.kind !== "visitors"}
        className={cn("hosbah-handle", manage && data.connect === "target" ? "hosbah-handle-open" : "hosbah-handle-hidden")}
      />
      <div className="flex min-w-0 items-center gap-2">
        <Icon aria-hidden className="size-4 shrink-0 text-grid-brand" />
        <span dir="auto" className="grid-micro min-w-0 flex-1 truncate">
          {data.label}
        </span>
        <StatusDot status={data.status} label={data.statusLabel} />
      </div>
      <div dir="auto" className="mt-1 truncate text-sm font-medium text-grid-fg" title={data.title}>
        {data.title}
      </div>
      {data.lines.map((l, n) => (
        <div key={n} dir="ltr" className="truncate text-start font-mono text-xs text-grid-muted" title={l}>
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
          className="nodrag nopan absolute -top-2.5 -right-2.5 hidden size-5 items-center justify-center border border-line bg-grid-card text-grid-muted group-hover:flex hover:text-grid-danger focus-visible:flex"
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
          className="nodrag nopan absolute top-1/2 -right-11 flex size-6 -translate-y-1/2 items-center justify-center border border-[var(--grid-brand)] bg-grid-card text-grid-brand hover:bg-grid-soft"
        >
          <PlusIcon className="size-3.5" />
        </button>
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
            className="nodrag nopan pointer-events-auto absolute flex size-6 items-center justify-center border border-line bg-grid-card text-grid-muted hover:text-grid-danger"
          >
            <XIcon className="size-3.5" />
          </button>
        </EdgeLabelRenderer>
      ) : null}
    </>
  )
}
