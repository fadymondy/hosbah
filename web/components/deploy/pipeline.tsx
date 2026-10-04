"use client"

import { CheckIcon, XIcon } from "lucide-react"

import type { DeploymentKind, DeploymentStatus, DeploymentStep } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import { cn } from "@/lib/utils"

/** The steps a deploy walks through, in order (rollback and restart are one step each). */
export const PIPELINE: DeploymentStep[] = ["provision", "build", "release", "start", "switch", "drain"]

/** Just what the pipeline needs, so a feed row and a full deployment both fit. */
export type PipelineItem = { status: DeploymentStatus; kind?: DeploymentKind | null; step?: DeploymentStep | null }

export const isActive = (d: { status: DeploymentStatus }) => d.status === "queued" || d.status === "running"

/** "1m 42s" from two instants. */
export function duration(from: string, to: string | null | undefined) {
  const ms = (to ? new Date(to).getTime() : Date.now()) - new Date(from).getTime()
  if (!(ms > 0)) return null
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s`
  return `${Math.floor(m / 60)}h ${m % 60}m`
}

/**
 * The deploy pipeline as a row of steps: done ones ticked, the current one pulsing, the one it
 * failed on crossed out in red. A rollback or restart shows its single step.
 */
export function Pipeline({ d }: { d: PipelineItem }) {
  const { t } = useI18n()
  const steps: DeploymentStep[] = d.kind === "rollback" || d.kind === "restart" ? [d.kind] : PIPELINE
  const at = d.step ? steps.indexOf(d.step) : -1
  return (
    <ol className="flex flex-wrap items-center gap-x-1 gap-y-1.5" aria-label={t("deploys2.pipeline")}>
      {steps.map((s, i) => {
        const state =
          d.status === "succeeded"
            ? "done"
            : at < 0
              ? d.status === "queued"
                ? "todo"
                : "unknown"
              : i < at
                ? "done"
                : i > at
                  ? "todo"
                  : d.status === "failed"
                    ? "failed"
                    : d.status === "cancelled"
                      ? "cancelled"
                      : "current"
        return (
          <li key={s} className="flex items-center gap-1">
            {i > 0 ? <span aria-hidden className={cn("h-px w-3", state === "done" || state === "current" || state === "failed" ? "bg-grid-muted" : "bg-line")} /> : null}
            <span
              className={cn(
                "inline-flex items-center gap-1 border px-1.5 py-0.5 text-[11px] whitespace-nowrap",
                state === "done" && "border-line text-grid-body",
                state === "current" && "border-grid-brand text-grid-fg",
                state === "failed" && "border-grid-danger bg-grid-danger/10 text-grid-danger",
                (state === "todo" || state === "unknown" || state === "cancelled") && "border-dashed border-line text-grid-muted",
              )}
            >
              {state === "done" ? <CheckIcon className="size-3 text-grid-ok" /> : null}
              {state === "current" ? <span aria-hidden className="size-1.5 animate-pulse rounded-full bg-grid-brand" /> : null}
              {state === "failed" ? <XIcon className="size-3" /> : null}
              {t(`apps.deploy.steps.${s}`, undefined, s)}
            </span>
          </li>
        )
      })}
    </ol>
  )
}
