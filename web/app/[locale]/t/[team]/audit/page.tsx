"use client"

import { useEffect, useState } from "react"
import { ScrollTextIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { FormError, useErrorText } from "@/components/form-error"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch, type AuditEntry } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export default function AuditPage() {
  const { t } = useI18n()
  const { team } = useTeam()
  useDocumentTitle(`${t("nav.audit")} — ${team.name}`)
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [entries, setEntries] = useState<AuditEntry[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<{ entries: AuditEntry[] }>(`/api/teams/${team.slug}/audit`)
      .then((r) => setEntries(r.entries ?? []))
      .catch((err) => {
        setError(errorText(err))
        setEntries([])
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  return (
    <PageBody title={t("nav.audit")} description={t("audit.description")}>
      <FormError message={error} />
      {entries === null ? (
        <Skeleton className="h-8 w-full" />
      ) : entries.length === 0 && !error ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <ScrollTextIcon />
            </EmptyMedia>
            <EmptyTitle>{t("audit.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("audit.emptyHint")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : entries.length ? (
        <div className="border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("audit.when")}</TableHead>
                <TableHead>{t("audit.action")}</TableHead>
                <TableHead>{t("audit.subject")}</TableHead>
                <TableHead>{t("audit.by")}</TableHead>
                <TableHead>{t("audit.ip")}</TableHead>
                <TableHead>{t("audit.changes")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((e) => (
                <TableRow key={e.id} className="align-top">
                  <TableCell dir="ltr" className="text-start whitespace-nowrap">
                    {fmt(e.created_at)}
                  </TableCell>
                  <TableCell>
                    <code dir="ltr" className="font-mono text-xs">
                      {e.action}
                    </code>
                  </TableCell>
                  <TableCell>
                    <code dir="ltr" className="font-mono text-xs">
                      {e.subject_type}
                      {e.subject_id != null ? `#${e.subject_id}` : ""}
                    </code>
                  </TableCell>
                  <TableCell dir="ltr" className="text-start">
                    {e.causer_email ?? t("audit.system")}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs">
                    {e.ip ?? "—"}
                  </TableCell>
                  <TableCell className="max-w-72">
                    {e.changes && Object.keys(e.changes as object).length ? (
                      <details>
                        <summary className="cursor-pointer text-xs text-muted-foreground">{t("audit.show")}</summary>
                        <pre dir="ltr" className="mt-2 overflow-x-auto text-start font-mono text-xs">
                          {JSON.stringify(e.changes, null, 2)}
                        </pre>
                      </details>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}
    </PageBody>
  )
}
