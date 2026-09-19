"use client"

import { useEffect, useState } from "react"
import { InboxIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { Badge } from "@/components/ui/badge"
import { ChangePassword } from "@/components/account/change-password"
import { TwoFactor } from "@/components/account/two-factor"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch, type Report } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import { useSession } from "@/lib/session"

export default function AccountPage() {
  const { t } = useI18n()
  const { me } = useSession()
  useDocumentTitle(t("account.title"))
  return (
    <PageBody title={t("account.title")} description={<span dir="ltr">{me.user.email}</span>} bleed>
      <TwoFactor />
      <ChangePassword />
      <MyReports />
    </PageBody>
  )
}

function MyReports() {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [reports, setReports] = useState<Report[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<{ reports: Report[] }>("/api/me/reports")
      .then((r) => setReports(r.reports ?? []))
      .catch((err) => {
        setError(errorText(err))
        setReports([])
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <section>
      <SectionTitle title={t("account.reports.title")} className="mt-6 border-t" />
      <ErrorBand message={error} />
      {reports === null ? (
        <LoadingRows />
      ) : reports.length === 0 && !error ? (
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <InboxIcon />
            </EmptyMedia>
            <EmptyTitle>{t("account.reports.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("account.reports.emptyHint")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : reports.length ? (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("account.reports.key")}</TableHead>
                <TableHead>{t("account.reports.titleCol")}</TableHead>
                <TableHead>{t("account.reports.status")}</TableHead>
                <TableHead>{t("account.reports.filed")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {reports.map((r) => (
                <TableRow key={r.key}>
                  <TableCell dir="ltr" className="text-start font-mono">
                    {r.key}
                  </TableCell>
                  <TableCell dir="auto">{r.title}</TableCell>
                  <TableCell>
                    <Badge variant="outline">{t(`account.reports.statuses.${r.status}`, undefined, r.status)}</Badge>
                  </TableCell>
                  <TableCell dir="ltr" className="text-start">
                    {fmt(r.created_at)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      ) : null}
    </section>
  )
}
