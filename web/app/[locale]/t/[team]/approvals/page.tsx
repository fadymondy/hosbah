"use client"

/*
The approval queue (P4).

This page is the human half of the destructive-MCP gate: a token with the `destructive` scope
cannot delete anything while the team setting is on, and every attempt lands here instead.

Two things on this page are deliberate and easy to get wrong later:

  - Approving does NOT run the call. The approval is a one-shot permission the original caller
    then spends by retrying, so the page says so in as many words — an approver who expects the
    deletion to happen when they click would otherwise think the feature is broken.
  - The arguments shown are names only. The API redacts values before storing them, and nothing
    here un-redacts anything; if a value ever appears in this table it is a bug in the API, not
    something to fix by hiding it in the UI.
*/

import { Badge, Button, Checkbox, EmptyState, Field, FieldDescription, FieldLabel, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, toast } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { ShieldCheckIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, SettingsRow, TableBand } from "@/components/states"






import { ApiError, apiFetch, type ApprovalRequest, type Team } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import { useSession } from "@/lib/session"
import { useTeam } from "@/lib/team"

// argumentNames reads the redacted summary the API stored. It is JSON by construction, but this
// page renders whatever is in the database, so a malformed value degrades to "no arguments"
// rather than blanking the row.
function argumentNames(summary: string): string[] {
  try {
    const parsed = JSON.parse(summary) as { arguments?: unknown }
    return Array.isArray(parsed.arguments) ? parsed.arguments.map(String) : []
  } catch {
    return []
  }
}

const statusTone: Record<ApprovalRequest["status"], "success" | "neutral" | "danger" | "outline"> = {
  pending: "success",
  approved: "neutral",
  used: "outline",
  rejected: "danger",
  expired: "outline",
}

export default function ApprovalsPage() {
  const { t } = useI18n()
  const { team, manage, refresh } = useTeam()
  const session = useSession()
  useDocumentTitle(`${t("approvals.title")} — ${team.name}`)
  const fmt = useFormatDate()
  const errorText = useErrorText()

  const [requests, setRequests] = useState<ApprovalRequest[] | null>(null)
  const [enabled, setEnabled] = useState(Boolean(team.require_destructive_approval))
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(() => {
    apiFetch<{ requests: ApprovalRequest[]; enabled: boolean }>(`/api/teams/${team.slug}/approvals`)
      .then((r) => {
        setRequests(r.requests ?? [])
        setEnabled(Boolean(r.enabled))
      })
      .catch((err) => {
        setError(errorText(err))
        setRequests([])
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(load, [load])

  async function toggle(next: boolean) {
    setBusy("setting")
    setError(null)
    try {
      await apiFetch<Team>(`/api/teams/${team.slug}`, {
        method: "PATCH",
        body: { require_destructive_approval: next },
      })
      setEnabled(next)
      toast.success(t("settings.saved"))
      await session.refresh()
      await refresh()
      load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  async function decide(id: string, action: "approve" | "reject") {
    setBusy(id)
    setError(null)
    try {
      await apiFetch(`/api/teams/${team.slug}/approvals/${id}/${action}`, { method: "POST" })
      toast.success(action === "approve" ? t("approvals.approved") : t("approvals.rejected"))
      load()
    } catch (err) {
      // 404 here means the request expired or was decided while this page was open.
      if (err instanceof ApiError && err.status === 404) {
        setError(t("approvals.gone"))
        load()
      } else {
        setError(errorText(err))
      }
    } finally {
      setBusy(null)
    }
  }

  return (
    <PageBody title={t("approvals.title")} description={t("approvals.description")} bleed>
      <ErrorBand message={error} />

      <SettingsRow label={t("approvals.requireTitle")} hint={t("approvals.requireHint")}>
          <Field className="flex-row items-center">
            <Checkbox
              id="require-approval"
              checked={enabled}
              disabled={!manage || busy === "setting"}
              onCheckedChange={(v) => toggle(v === true)}
            />
            <div>
              <FieldLabel htmlFor="require-approval">
                {enabled ? t("approvals.requireOn") : t("approvals.requireOff")}
              </FieldLabel>
              {!manage ? <FieldDescription>{t("approvals.viewerNote")}</FieldDescription> : null}
            </div>
          </Field>
      </SettingsRow>

      {requests === null ? (
        <LoadingRows />
      ) : requests.length === 0 ? (
        <EmptyState className="border-b" icon={ShieldCheckIcon} title={enabled ? t("approvals.empty") : t("approvals.disabledNote")} description={enabled ? t("approvals.description") : t("approvals.emptyDisabled")} />
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("approvals.tool")}</TableHead>
                <TableHead>{t("approvals.resource")}</TableHead>
                <TableHead>{t("approvals.token")}</TableHead>
                <TableHead>{t("approvals.arguments")}</TableHead>
                <TableHead>{t("approvals.asked")}</TableHead>
                <TableHead>{t("approvals.expires")}</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {requests.map((r) => {
                const args = argumentNames(r.args_summary)
                return (
                  <TableRow key={r.id}>
                    <TableCell>
                      <span className="font-mono text-sm" dir="ltr">
                        {r.tool}
                      </span>
                      <div>
                        <Badge variant={statusTone[r.status]}>{t(`approvals.status.${r.status}`)}</Badge>
                      </div>
                    </TableCell>
                    <TableCell>
                      <span className="font-mono text-sm" dir="ltr">
                        {r.resource_name}
                      </span>
                      <div className="text-muted-foreground text-xs">{r.resource_type}</div>
                    </TableCell>
                    <TableCell>
                      <div>{r.token_name || "—"}</div>
                      <div className="text-muted-foreground font-mono text-xs" dir="ltr">
                        {r.token_id}
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground font-mono text-xs" dir="ltr">
                      {args.length ? args.join(", ") : "—"}
                    </TableCell>
                    <TableCell className="whitespace-nowrap">{fmt(r.created_at)}</TableCell>
                    <TableCell className="whitespace-nowrap">{fmt(r.expires_at)}</TableCell>
                    <TableCell>
                      {manage && r.status === "pending" ? (
                        <div className="flex gap-2">
                          <Button variant="primary" size="sm" disabled={busy === r.id} onClick={() => decide(r.id, "approve")}>
                            {t("approvals.approve")}
                          </Button>
                          <Button
                            size="sm"
                            variant="secondary"
                            disabled={busy === r.id}
                            onClick={() => decide(r.id, "reject")}
                          >
                            {t("approvals.reject")}
                          </Button>
                        </div>
                      ) : null}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </TableBand>
      )}
    </PageBody>
  )
}
