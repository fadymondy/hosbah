"use client"

// Cloudy's host adapter for the feedback SDK. The SDK itself is the GENERATED copy in
// lib/feedback (npm run feedback:sync, from fadymondy.com-v2/packages/feedback-sdk) — never
// edit it here. This file only supplies Cloudy's strings, its /api/feedback JSON transport and
// the current team.

import { useState } from "react"
import { MessageSquareWarningIcon } from "lucide-react"
import { toast } from "sonner"

import { SidebarMenuButton } from "@/components/ui/sidebar"
import { ReportDialog, type FeedbackSubmission } from "@/lib/feedback/react"
import { apiFetch, ApiError } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useOptionalTeam } from "@/lib/team"

// The fields the Cloudy API contract names for POST /api/feedback.
const CONTRACT_FIELDS = ["title", "body", "issue_type", "page_url", "route", "user_agent", "viewport"] as const

export function ReportProblem() {
  const { t, locale } = useI18n()
  const team = useOptionalTeam()?.team
  const [open, setOpen] = useState(false)

  async function send(submission: FeedbackSubmission) {
    const team_id = team?.id
    const full = { ...submission, meta: undefined, team_id }
    let result: { key?: string }
    try {
      result = await apiFetch<{ key?: string }>("/api/feedback", { method: "POST", body: full })
    } catch (err) {
      // The contract lists only the text fields. If the API rejects the SDK's capture fields
      // (screenshot, selector, console_log, network_log), file the report without them rather
      // than losing it — and say so.
      if (!(err instanceof ApiError) || (err.status !== 422 && err.status !== 400)) throw err
      const slim: Record<string, unknown> = { team_id }
      for (const k of CONTRACT_FIELDS) slim[k] = submission[k]
      result = await apiFetch<{ key?: string }>("/api/feedback", { method: "POST", body: slim })
      toast.warning(t("feedback.capturesDropped"))
    }
    toast.success(t("feedback.filed", { key: result.key ?? "—" }))
  }

  return (
    <>
      <SidebarMenuButton tooltip={t("feedback.launcher")} onClick={() => setOpen(true)}>
        <MessageSquareWarningIcon />
        <span>{t("feedback.launcher")}</span>
      </SidebarMenuButton>
      <ReportDialog
        open={open}
        onOpenChange={setOpen}
        locale={locale}
        types={["bug", "feature"]}
        typeLabels={{ bug: t("feedback.types.bug"), feature: t("feedback.types.feature") }}
        onSubmit={send}
        idPrefix="cloudy-feedback"
      />
    </>
  )
}
