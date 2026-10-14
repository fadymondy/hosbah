"use client"

import { Badge, EmptyState, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { Suspense, useEffect, useState } from "react"
import { InboxIcon, MessageSquareShareIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"

import { AIConnections } from "@/components/account/ai-connections"
import { ChangePassword } from "@/components/account/change-password"
import { ConnectedAccounts } from "@/components/account/connected-accounts"
import { DeleteAccount } from "@/components/account/delete-account"
import { TwoFactor } from "@/components/account/two-factor"
import { ErrorBand, LoadingRows, Notice, SectionTitle, TableBand } from "@/components/states"


import { apiFetch, type Report } from "@/lib/api"
import { MAHAAM_FEEDBACK } from "@/lib/config"
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
      <Suspense fallback={null}>
        <ConnectedAccounts />
      </Suspense>
      <AIConnections />
      <MyReports />
      <DeleteAccount />
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
      {MAHAAM_FEEDBACK ? (
        <Notice icon={<MessageSquareShareIcon />} title={t("account.reports.mahaamTitle")} data-testid="reports-mahaam">
          {t("account.reports.mahaamHint")}
        </Notice>
      ) : null}
      <ErrorBand message={error} />
      {reports === null ? (
        MAHAAM_FEEDBACK ? null : (
          <LoadingRows />
        )
      ) : MAHAAM_FEEDBACK && reports.length === 0 ? null : reports.length === 0 && !error ? (
        <EmptyState className="border-b" icon={InboxIcon} title={t("account.reports.emptyTitle")} description={t("account.reports.emptyHint")} />
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
