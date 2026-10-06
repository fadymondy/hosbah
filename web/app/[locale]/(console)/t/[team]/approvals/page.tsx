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

import { Badge, Checkbox, EmptyState, Field, FieldDescription, FieldLabel, toast, type DataTableColumn, type DataTableRowAction } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { CheckIcon, ShieldCheckIcon, XIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, SettingsRow } from "@/components/states"






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

  const columns: DataTableColumn<ApprovalRequest>[] = [
    {
      id: "tool",
      header: t("approvals.tool"),
      label: t("approvals.tool"),
      sortValue: (r) => r.tool,
      searchValue: (r) => `${r.tool} ${r.resource_name} ${r.resource_type} ${r.token_name ?? ""}`,
      cell: (r) => (
        <span className="flex flex-col items-start gap-1">
          <span className="font-mono text-sm" dir="ltr">
            {r.tool}
          </span>
          <Badge variant={statusTone[r.status]}>{t(`approvals.status.${r.status}`)}</Badge>
        </span>
      ),
    },
    {
      id: "status",
      header: t("approvals.statusLabel"),
      label: t("approvals.statusLabel"),
      defaultHidden: true,
      filterValue: (r) => r.status,
      sortValue: (r) => r.status,
      cell: (r) => t(`approvals.status.${r.status}`),
    },
    {
      id: "resource",
      header: t("approvals.resource"),
      label: t("approvals.resource"),
      sortValue: (r) => r.resource_name,
      cell: (r) => (
        <span className="block">
          <span className="block font-mono text-sm" dir="ltr">
            {r.resource_name}
          </span>
          <span className="block text-caption text-muted-foreground">{r.resource_type}</span>
        </span>
      ),
    },
    {
      id: "token",
      header: t("approvals.token"),
      label: t("approvals.token"),
      sortValue: (r) => r.token_name ?? "",
      className: "max-lg:hidden",
      headerClassName: "max-lg:hidden",
      cell: (r) => (
        <span className="block">
          <span className="block">{r.token_name || "—"}</span>
          <span className="block font-mono text-caption text-muted-foreground" dir="ltr">
            {r.token_id}
          </span>
        </span>
      ),
    },
    {
      id: "arguments",
      header: t("approvals.arguments"),
      label: t("approvals.arguments"),
      className: "max-lg:hidden",
      headerClassName: "max-lg:hidden",
      cell: (r) => {
        const args = argumentNames(r.args_summary)
        return (
          <span className="font-mono text-caption text-muted-foreground" dir="ltr">
            {args.length ? args.join(", ") : "—"}
          </span>
        )
      },
    },
    {
      id: "asked",
      header: t("approvals.asked"),
      label: t("approvals.asked"),
      sortValue: (r) => r.created_at,
      cell: (r) => <span className="whitespace-nowrap text-caption">{fmt(r.created_at)}</span>,
    },
    {
      id: "expires",
      header: t("approvals.expires"),
      label: t("approvals.expires"),
      sortValue: (r) => r.expires_at,
      className: "max-md:hidden",
      headerClassName: "max-md:hidden",
      cell: (r) => <span className="whitespace-nowrap text-caption">{fmt(r.expires_at)}</span>,
    },
  ]

  const actions = (r: ApprovalRequest): DataTableRowAction[] =>
    r.status === "pending"
      ? [
          { id: "approve", label: t("approvals.approve"), icon: CheckIcon, disabled: busy === r.id, onSelect: () => void decide(r.id, "approve") },
          { id: "reject", label: t("approvals.reject"), icon: XIcon, danger: true, disabled: busy === r.id, onSelect: () => void decide(r.id, "reject") },
        ]
      : []

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
        <div className="flex flex-col gap-3 px-6 py-6">
          <NasaqTable
            data={requests}
            columns={columns}
            rowId={(r) => r.id}
            label={t("approvals.title")}
            rowLabel={(r) => `${r.tool} ${r.resource_name}`}
            search={t("approvals.tool")}
            facets={[{ column: "status", title: t("approvals.statusLabel"), options: [...new Set(requests.map((r) => r.status))].map((v) => ({ value: v, label: t(`approvals.status.${v}`) })) }]}
            rowActions={manage ? actions : undefined}
            defaultSort={{ id: "asked", direction: "desc" }}
            empty={t("home.noMatch")}
          />
        </div>
      )}
    </PageBody>
  )
}
