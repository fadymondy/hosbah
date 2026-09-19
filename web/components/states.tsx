"use client"

import type * as React from "react"
import type { ReactNode } from "react"
import { TriangleAlertIcon } from "lucide-react"

import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"

/*
Full-bleed page pieces, in the Mahaam layout: every band spans the page edge to edge and draws
its own bottom hairline (the PageBody header already draws the line above the first band).
*/

/** Skeleton rows in the same full-width band a table would take. */
export function LoadingRows({ rows = 3, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn("divide-y divide-line border-b border-line", className)} aria-busy>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex items-center gap-4 px-6 py-4">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-4 w-24" />
          <Skeleton className="ms-auto h-4 w-16" />
        </div>
      ))}
    </div>
  )
}

/** A full-width error band (the bleed equivalent of FormError). */
export function ErrorBand({ message }: { message: string | null }) {
  if (!message) return null
  return (
    <div role="alert" className="flex items-start gap-3 border-b border-line px-6 py-4 text-sm">
      <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-grid-danger" />
      <p className="text-grid-danger">{message}</p>
    </div>
  )
}

/** A full-width table band; the first and last columns get the page gutter. */
export function TableBand({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <div className={cn("border-b border-line [&_tr>*:first-child]:ps-6 [&_tr>*:last-child]:pe-6", className)}>
      {children}
    </div>
  )
}

/** A filters / tabs strip under the header. */
export function Strip({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn("flex flex-wrap items-center gap-2 border-b border-line px-6 py-3", className)}>{children}</div>
}

/** A section heading row: title, optional hint, optional action at the end. */
export function SectionTitle({
  title,
  hint,
  action,
  id,
  className,
}: {
  title: ReactNode
  hint?: ReactNode
  action?: ReactNode
  /** The heading id, for a section's aria-labelledby. */
  id?: string
  className?: string
}) {
  return (
    <div className={cn("flex flex-wrap items-end justify-between gap-3 border-b border-line px-6 pt-6 pb-3", className)}>
      <div className="min-w-0 space-y-1">
        <h2 id={id} className="text-base font-medium text-grid-fg">
          {title}
        </h2>
        {hint ? <p className="max-w-2xl text-xs text-grid-muted">{hint}</p> : null}
      </div>
      {action ? <div className="flex flex-wrap items-center gap-2">{action}</div> : null}
    </div>
  )
}

/** A full-width status band: icon, title, body; tone colours the icon and the start rule. */
export function Notice({
  icon,
  title,
  children,
  tone = "info",
  className,
  ...rest
}: {
  icon?: ReactNode
  title?: ReactNode
  children?: ReactNode
  tone?: "info" | "danger"
  className?: string
} & Omit<React.ComponentProps<"div">, "title" | "children">) {
  return (
    <div
      role={tone === "danger" ? "alert" : "status"}
      className={cn(
        "flex items-start gap-3 border-b border-line border-s-2 px-6 py-4 text-sm [&>svg]:mt-0.5 [&>svg]:size-4 [&>svg]:shrink-0",
        tone === "danger" ? "border-s-grid-danger [&>svg]:text-grid-danger" : "border-s-grid-brand bg-grid-soft [&>svg]:text-grid-brand",
        className,
      )}
      {...rest}
    >
      {icon}
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        {title ? <p className="font-medium text-grid-fg">{title}</p> : null}
        {children ? <div className="flex flex-col gap-3 text-grid-body">{children}</div> : null}
      </div>
    </div>
  )
}

/** A bordered section band: square, hairline at the bottom, padded like the page gutter. */
export function Band({ className, children }: { className?: string; children: ReactNode }) {
  return <section className={cn("border-b border-line px-6 py-5", className)}>{children}</section>
}

/** A settings row: label + hint on the start side, the control on the end side (Mahaam settings). */
export function SettingsRow({
  label,
  hint,
  htmlFor,
  children,
}: {
  label: ReactNode
  hint?: ReactNode
  htmlFor?: string
  children: ReactNode
}) {
  return (
    <div className="grid gap-2 border-b border-line px-6 py-5 md:grid-cols-[18rem_1fr] md:gap-8">
      <div>
        {htmlFor ? (
          <label htmlFor={htmlFor} className="text-sm font-medium text-grid-fg">
            {label}
          </label>
        ) : (
          <p className="text-sm font-medium text-grid-fg">{label}</p>
        )}
        {hint ? <p className="mt-1 text-xs text-grid-muted">{hint}</p> : null}
      </div>
      <div className="flex max-w-xl min-w-0 flex-col gap-3">{children}</div>
    </div>
  )
}
