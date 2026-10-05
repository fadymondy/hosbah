"use client"

import { DeployView, type DeployStep } from "@fadymondy/nasaq/web"
import { useEffect, useMemo, useState } from "react"

import { PIPELINE } from "@/components/deploy/pipeline"
import type { FeedItem } from "@/components/home/deployments-feed"
import { render } from "@/components/op-log"
import type { DeployLogLine } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import { useEventStream } from "@/lib/servers"

export const RUN_STATUS = { queued: "pending", running: "running", succeeded: "success", failed: "failed", cancelled: "cancelled" } as const

/** Which pipeline step an agent operation's lines belong to. */
const STEP_OF_OP: Record<string, string> = {
  "app.detect": "build",
  "app.build": "build",
  "app.fast_build": "build",
  "app.release": "release",
  "app.start": "start",
  "route.set": "switch",
  "app.drain": "drain",
  "app.rollback": "rollback",
}

/**
 * One deployment's steps in Nasaq's DeployView, each with its own output: the deployment log is
 * streamed and split by the operation each line came from; the control plane's own lines go to the
 * step running at the time.
 */
export function DeployRun({ team, d }: { team: string; d: FeedItem }) {
  const { t } = useI18n()
  const [lines, setLines] = useState<DeployLogLine[]>([])
  useEffect(() => setLines([]), [d.id])
  useEventStream(`/api/teams/${encodeURIComponent(team)}/apps/${encodeURIComponent(d.app.name)}/deployments/${d.id}/stream`, {
    line: (x) => {
      const l = x as DeployLogLine
      setLines((prev) => (prev.some((p) => p.seq === l.seq) ? prev : [...prev, l].sort((a, b) => a.seq - b.seq)))
    },
  })

  const steps = useMemo(() => {
    const names: string[] = d.kind === "rollback" || d.kind === "restart" ? [d.kind] : PIPELINE
    const byStep = new Map<string, DeployLogLine[]>()
    let current = names[0]
    for (const l of lines) {
      const step = l.op ? STEP_OF_OP[l.op] : undefined
      if (step && names.includes(step)) current = step
      byStep.set(current, [...(byStep.get(current) ?? []), l])
    }
    const at = d.step ? names.indexOf(d.step) : -1
    return names.map((id, i): DeployStep => {
      let status: DeployStep["status"] = "pending"
      if (d.status === "succeeded") status = "success"
      else if (d.status !== "queued" && at >= 0) {
        if (i < at) status = "success"
        else if (i === at) status = RUN_STATUS[d.status] as DeployStep["status"]
        else if (d.status !== "running") status = "skipped"
      }
      const own = byStep.get(id) ?? []
      const first = own[0]?.at ? Date.parse(own[0].at) : NaN
      const last = own.at(-1)?.at ? Date.parse(own.at(-1)!.at) : NaN
      return {
        id,
        name: t(`apps.deploy.steps.${id}`, undefined, id),
        status,
        startedAt: Number.isNaN(first) ? (status === "running" ? (d.started_at ?? d.created_at) : undefined) : first,
        durationMs: status !== "running" && !Number.isNaN(first) && !Number.isNaN(last) ? last - first : undefined,
        logs: own.length ? own.map(render).join("\n") : undefined,
        error: status === "failed" ? (d.error ?? undefined) : undefined,
      }
    })
  }, [lines, d, t])

  return (
    <DeployView
      className="rounded-none border-0 bg-transparent shadow-none [&_[data-slot=deploy-step]_.ps-11]:pt-3"
      status={RUN_STATUS[d.status as keyof typeof RUN_STATUS]}
      steps={steps}
      defaultExpanded={d.status === "failed" && d.step ? [d.step] : []}
    />
  )
}
