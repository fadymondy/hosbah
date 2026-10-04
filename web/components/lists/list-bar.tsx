"use client"

import type { ReactNode } from "react"
import { SearchIcon } from "lucide-react"

import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"

/** A small segmented control for one filter (the empty value is "all"). */
export function FilterPills<T extends string>({
  value,
  onChange,
  options,
  label,
}: {
  value: T
  onChange: (v: T) => void
  options: [T, string, number?][]
  label: string
}) {
  return (
    <div role="radiogroup" aria-label={label} className="flex max-w-full overflow-x-auto border border-line">
      {options.map(([v, text, count]) => (
        <button
          key={v || "all"}
          type="button"
          role="radio"
          aria-checked={value === v}
          onClick={() => onChange(v)}
          className={cn(
            "flex shrink-0 items-center gap-1.5 px-2.5 py-1 text-xs whitespace-nowrap",
            value === v ? "bg-grid-soft font-medium text-grid-fg" : "text-grid-muted hover:bg-grid-soft hover:text-grid-fg",
          )}
        >
          {text}
          {count !== undefined ? <span className="tabular-nums text-grid-muted">{count}</span> : null}
        </button>
      ))}
    </div>
  )
}

/** The bar above a team-wide list: a search box, then its filters and sort at the end. */
export function ListBar({
  q,
  onQ,
  placeholder,
  children,
}: {
  q: string
  onQ: (q: string) => void
  placeholder: string
  children?: ReactNode
}) {
  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-line px-6 py-3">
      <div className="relative w-full sm:max-w-xs">
        <SearchIcon className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-grid-muted" />
        <Input type="search" value={q} onChange={(e) => onQ(e.target.value)} placeholder={placeholder} aria-label={placeholder} className="ps-8" />
      </div>
      {children ? <div className="flex flex-wrap items-center gap-2 sm:ms-auto">{children}</div> : null}
    </div>
  )
}

export type Tone = "ok" | "progress" | "fail" | "idle"

/** The one colour language for every status: green ok, brand (pulsing) in progress, red failed, muted idle. */
export function StatusDot({ tone, label, className }: { tone: Tone; label?: string; className?: string }) {
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-xs whitespace-nowrap", className)}>
      <span
        aria-hidden
        className={cn(
          "size-2 shrink-0 rounded-full",
          tone === "ok" && "bg-grid-ok",
          tone === "progress" && "animate-pulse bg-grid-brand",
          tone === "fail" && "bg-grid-danger",
          tone === "idle" && "bg-grid-muted/50",
        )}
      />
      {label ? <span className={tone === "fail" ? "text-grid-danger" : "text-grid-body"}>{label}</span> : null}
    </span>
  )
}

export function appTone(status: string): Tone {
  if (status === "running" || status === "succeeded") return "ok"
  if (status === "deploying" || status === "queued" || status === "provisioning" || status === "creating") return "progress"
  if (status === "failed" || status === "missing") return "fail"
  return "idle"
}

/** "Sorted by" as a native select: small, keyboard-friendly and right in RTL. */
export function SortSelect<T extends string>({
  value,
  onChange,
  options,
  label,
}: {
  value: T
  onChange: (v: T) => void
  options: [T, string][]
  label: string
}) {
  return (
    <label className="flex items-center gap-2 text-xs text-grid-muted">
      <span className="sr-only sm:not-sr-only">{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value as T)}
        className="h-7 border border-line bg-grid-card px-2 text-xs text-grid-fg"
      >
        {options.map(([v, text]) => (
          <option key={v} value={v}>
            {text}
          </option>
        ))}
      </select>
    </label>
  )
}
