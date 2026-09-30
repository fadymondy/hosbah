"use client"

import { Button, EmptyState, Input, Skeleton } from "@fadymondy/nasaq/web"
import type { ReactNode } from "react"
import { ChevronLeftIcon, ChevronRightIcon, SearchIcon, TriangleAlertIcon } from "lucide-react"





import { ApiError } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

/** The strip above a list: search at the start, filters and actions at the end (Mahaam's tab strip). */
export function ListToolbar({
  q,
  onQ,
  placeholder,
  children,
}: {
  q?: string
  onQ?: (v: string) => void
  placeholder?: string
  children?: ReactNode
}) {
  const { t } = useI18n()
  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-border px-6 py-3">
      {onQ ? (
        <div className="relative w-full max-w-xs">
          <SearchIcon className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            value={q}
            onChange={(e) => onQ(e.target.value)}
            placeholder={placeholder ?? t("admin.list.search")}
            aria-label={placeholder ?? t("admin.list.search")}
            className="ps-8"
          />
        </div>
      ) : null}
      <div className="ms-auto flex flex-wrap items-center gap-2">{children}</div>
    </div>
  )
}

/** Loading rows in the list's own shape, full width between hairlines. */
export function LoadingRows({ rows = 5 }: { rows?: number }) {
  return (
    <div className="divide-y divide-border border-b border-border" aria-busy>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex items-center gap-4 px-6 py-4">
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-4 w-24" />
          <Skeleton className="ms-auto h-4 w-16" />
        </div>
      ))}
    </div>
  )
}

export function ListError({ error }: { error: unknown }) {
  const { t } = useI18n()
  const message =
    error instanceof ApiError ? (error.status >= 500 ? t("admin.list.unavailable") : error.message) : t("admin.list.network")
  return (
    <div role="alert" className="flex items-start gap-3 border-b border-border px-6 py-4 text-sm">
      <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-nq-danger-text" />
      <p className="text-nq-danger-text">{message}</p>
    </div>
  )
}

export function ListEmpty({ title, body }: { title: string; body?: string }) {
  return (
    <EmptyState className="border-b" title={title} description={body ? body : undefined} />
  )
}

/** "1–25 of 140" with previous/next; the arrows follow the reading direction. */
export function Pager({
  offset,
  pageSize,
  total,
  onOffset,
}: {
  offset: number
  pageSize: number
  total: number
  onOffset: (o: number) => void
}) {
  const { t } = useI18n()
  if (total <= pageSize && offset === 0) return null
  const from = total === 0 ? 0 : offset + 1
  const to = Math.min(offset + pageSize, total)
  return (
    <div className="flex items-center justify-between gap-4 border-b border-border px-6 py-3 text-sm text-muted-foreground">
      <span className="tabular-nums">{t("admin.list.range", { from, to, total })}</span>
      <div className="flex items-center gap-1">
        <Button
          variant="secondary"
          size="icon-sm"
          disabled={offset === 0}
          onClick={() => onOffset(Math.max(0, offset - pageSize))}
          aria-label={t("admin.list.previous")}
        >
          <ChevronLeftIcon className="rtl:-scale-x-100" />
        </Button>
        <Button
          variant="secondary"
          size="icon-sm"
          disabled={to >= total}
          onClick={() => onOffset(offset + pageSize)}
          aria-label={t("admin.list.next")}
        >
          <ChevronRightIcon className="rtl:-scale-x-100" />
        </Button>
      </div>
    </div>
  )
}

/** A label/value grid for detail pages (Mahaam's overview facts row). */
export function Facts({ items }: { items: [ReactNode, ReactNode][] }) {
  return (
    <dl className="grid grid-cols-1 divide-y divide-border border-b border-border text-sm sm:grid-cols-2 sm:divide-y-0 lg:grid-cols-4 [&>div]:sm:border-e [&>div]:border-border [&>div:last-child]:border-e-0">
      {items.map(([label, value], i) => (
        <div key={i} className="px-6 py-4">
          <dt className="eyebrow mb-1">{label}</dt>
          <dd className="text-foreground">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

/** A section title between hairlines, inside a full-bleed page. */
export function SectionTitle({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4 px-6 pt-6 pb-3">
      <h2 className="text-base font-medium">{children}</h2>
      {action}
    </div>
  )
}

/** A compact filter select for list toolbars; "" means every value. */
export function FilterSelect({
  value,
  onValue,
  label,
  options,
}: {
  value: string
  onValue: (v: string) => void
  label: string
  options: { value: string; label: string }[]
}) {
  const { t } = useI18n()
  const all = [{ value: "", label: t("admin.list.all", { what: label }) }, ...options]
  return (
    <select
      value={value}
      onChange={(e) => onValue(e.target.value)}
      aria-label={label}
      className="h-8 border border-border bg-background px-2 text-sm text-foreground focus-visible:outline-2 focus-visible:outline-[var(--nq-action)]"
    >
      {all.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  )
}

/** A server's state as a square dot and a word (online, or its lifecycle status). */
export function ServerStatus({ online, status }: { online: boolean; status: string }) {
  const { t } = useI18n()
  return (
    <span className="inline-flex items-center gap-2">
      <span
        aria-hidden
        className={online ? "size-2 bg-nq-success" : "size-2 bg-nq-fg-muted opacity-60"}
      />
      <span className={online ? "text-foreground" : "text-muted-foreground"}>
        {online ? t("admin.servers.online") : t(`admin.servers.status.${status}`, undefined, status)}
      </span>
    </span>
  )
}
