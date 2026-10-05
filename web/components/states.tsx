"use client"

import { Skeleton } from "@fadymondy/nasaq/web"
import type * as React from "react"
import { createContext, useContext, type ReactNode } from "react"
import { TriangleAlertIcon } from "lucide-react"

import { cn } from "@/lib/utils"

/*
Full-bleed page pieces, in the Mahaam layout: every band spans the page edge to edge and draws
its own bottom hairline (the PageBody header already draws the line above the first band).
*/

/**
 * Card mode: inside a CardPieces provider (the server pages) the same pieces render as Nasaq cards on a
 * padded page instead of full-bleed bands. Rows that follow each other join into one card.
 */
const CardMode = createContext(false)
/** `bare` drops the page padding, for pieces inside a container that already insets them (a settings page). */
export function CardPieces({ children, bare }: { children: ReactNode; bare?: boolean }) {
  return (
    <CardMode.Provider value={true}>
      <div data-cards className={cn("[&_section>*+*]:mt-3 [&>*+*]:mt-4", !bare && "px-4 py-6 sm:px-6")}>{children}</div>
    </CardMode.Provider>
  )
}
export const useCardMode = () => useContext(CardMode)

/** Card chrome; consecutive joined pieces (`data-join`) share one card. */
const CARD =
  "overflow-hidden rounded-card border border-border bg-card shadow-xs [[data-join]+&]:mt-0! [[data-join]+&]:rounded-t-none [[data-join]+&]:border-t-0 [&:has(+[data-join])]:rounded-b-none"

/** Skeleton rows in the same full-width band a table would take. */
export function LoadingRows({ rows = 3, className }: { rows?: number; className?: string }) {
  const card = useCardMode()
  return (
    <div className={cn("divide-y divide-border", card ? CARD : "border-b border-border", className)} aria-busy>
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
  const card = useCardMode()
  if (!message) return null
  return (
    <div
      role="alert"
      className={cn(
        "flex items-start gap-3 text-sm",
        card ? "rounded-card border border-nq-danger/40 bg-nq-danger/5 px-4 py-3" : "border-b border-border px-6 py-4",
      )}
    >
      <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-nq-danger-text" />
      <p className="text-nq-danger-text">{message}</p>
    </div>
  )
}

/** A full-width table band; the first and last columns get the page gutter. */
export function TableBand({ className, children }: { className?: string; children: ReactNode }) {
  const card = useCardMode()
  return (
    <div
      data-join={card ? "" : undefined}
      className={cn(
        card ? cn(CARD, "[&_tr>*:first-child]:ps-4 [&_tr>*:last-child]:pe-4") : "border-b border-border [&_tr>*:first-child]:ps-6 [&_tr>*:last-child]:pe-6",
        className,
      )}
    >
      {children}
    </div>
  )
}

/** A filters / tabs strip under the header. */
export function Strip({ className, children }: { className?: string; children: ReactNode }) {
  const card = useCardMode()
  return <div className={cn("flex flex-wrap items-center gap-2", card ? "" : "border-b border-border px-6 py-3", className)}>{children}</div>
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
  const card = useCardMode()
  return (
    <div className={cn("flex flex-wrap items-end justify-between gap-3", card ? "pt-2" : "border-b border-border px-6 pt-6 pb-3", className)}>
      <div className="min-w-0 space-y-1">
        <h2 id={id} className={cn("text-foreground", card ? "text-h4 font-semibold" : "text-base font-medium")}>
          {title}
        </h2>
        {hint ? <p className="max-w-2xl text-xs text-muted-foreground">{hint}</p> : null}
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
  const card = useCardMode()
  return (
    <div
      role={tone === "danger" ? "alert" : "status"}
      className={cn(
        card ? "rounded-card border border-border border-s-4 px-4 py-3 shadow-xs" : "border-b border-border border-s-2 px-6 py-4",
        "flex items-start gap-3 text-sm [&>svg]:mt-0.5 [&>svg]:size-4 [&>svg]:shrink-0",
        tone === "danger" ? "border-s-nq-danger [&>svg]:text-nq-danger-text" : "border-s-nq-brand bg-secondary [&>svg]:text-nq-brand",
        className,
      )}
      {...rest}
    >
      {icon}
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        {title ? <p className="font-medium text-foreground">{title}</p> : null}
        {children ? <div className="flex flex-col gap-3 text-nq-fg-body">{children}</div> : null}
      </div>
    </div>
  )
}

/** A bordered section band: square, hairline at the bottom, padded like the page gutter. */
export function Band({ className, children }: { className?: string; children: ReactNode }) {
  const card = useCardMode()
  return <section className={cn(card ? cn(CARD, "p-5") : "border-b border-border px-6 py-5", className)}>{children}</section>
}

/** The save bar under settings rows: joined to them as the card's last row, actions at the end. */
export function SettingsFooter({ children }: { children: ReactNode }) {
  const card = useCardMode()
  return (
    <div
      data-join={card ? "" : undefined}
      className={cn("flex flex-wrap items-center justify-end gap-2", card ? cn(CARD, "bg-secondary/40 px-5 py-3") : "border-b border-border px-6 py-4")}
    >
      {children}
    </div>
  )
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
  const card = useCardMode()
  return (
    <div
      data-join={card ? "" : undefined}
      className={cn("grid gap-2 md:grid-cols-[18rem_1fr] md:gap-8", card ? cn(CARD, "px-5 py-5") : "border-b border-border px-6 py-5")}
    >
      <div>
        {htmlFor ? (
          <label htmlFor={htmlFor} className="text-sm font-medium text-foreground">
            {label}
          </label>
        ) : (
          <p className="text-sm font-medium text-foreground">{label}</p>
        )}
        {hint ? <p className="mt-1 text-xs text-muted-foreground">{hint}</p> : null}
      </div>
      <div className="flex max-w-xl min-w-0 flex-col gap-3">{children}</div>
    </div>
  )
}
