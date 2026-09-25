"use client"

import { useCallback, useEffect, useState } from "react"
import { RefreshCwIcon, ShieldCheckIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, TableBand } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import type { SSLCertificate } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/** The team's TLS certificates: every app domain and proxy host set to auto-TLS, watched by the
 * control plane's certcheck sweep (MH-580 J6). Any member can see it; "check now" re-dials every
 * hostname instead of waiting for the next sweep. */
export default function CertificatesPage() {
  const { t } = useI18n()
  const { team } = useTeam()
  useDocumentTitle(`${t("nav.certificates")} — ${team.name}`)
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [certs, setCerts] = useState<SSLCertificate[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [checking, setChecking] = useState(false)

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ certificates: SSLCertificate[] }>(`/api/teams/${team.slug}/certificates`)
      setCerts(res.certificates ?? [])
    } catch (err) {
      setError(errorText(err))
      setCerts([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug])

  useEffect(() => {
    void load()
  }, [load])

  async function checkNow() {
    setChecking(true)
    setError(null)
    try {
      const res = await apiFetch<{ certificates: SSLCertificate[] }>(`/api/teams/${team.slug}/certificates/check`, { method: "POST" })
      setCerts(res.certificates ?? [])
    } catch (err) {
      setError(errorText(err))
    } finally {
      setChecking(false)
    }
  }

  return (
    <PageBody
      title={t("nav.certificates")}
      description={t("certificates.description")}
      actions={
        <Button variant="outline" onClick={() => void checkNow()} disabled={checking}>
          <RefreshCwIcon className={checking ? "animate-spin" : ""} />
          {checking ? t("common.working") : t("certificates.checkNow")}
        </Button>
      }
      bleed
    >
      <ErrorBand message={error} />
      {certs === null ? (
        <LoadingRows />
      ) : certs.length === 0 ? (
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <ShieldCheckIcon />
            </EmptyMedia>
            <EmptyTitle>{t("certificates.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("certificates.emptyHint")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("certificates.hostname")}</TableHead>
                <TableHead>{t("certificates.issuer")}</TableHead>
                <TableHead>{t("certificates.expires")}</TableHead>
                <TableHead>{t("certificates.san")}</TableHead>
                <TableHead>{t("certificates.lastChecked")}</TableHead>
                <TableHead className="text-end">{t("certificates.status")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {certs.map((c) => (
                <TableRow key={c.id}>
                  <TableCell>
                    <span dir="ltr" className="font-medium">
                      {c.hostname}
                    </span>
                    <div className="text-xs text-muted-foreground">{t(`certificates.sources.${c.source}`)}</div>
                  </TableCell>
                  <TableCell className="text-sm text-grid-body">{c.issuer || "—"}</TableCell>
                  <TableCell>
                    {c.not_after ? (
                      <div className="flex flex-col gap-1">
                        <span dir="ltr" className="text-sm">
                          {fmt(c.not_after)}
                        </span>
                        <DaysLeftBadge daysLeft={c.days_left} ok={c.ok} />
                      </div>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                  <TableCell>
                    {c.not_after ? (
                      <Badge variant="outline" className={c.san_match ? "text-grid-ok" : "text-grid-danger"}>
                        {c.san_match ? t("certificates.sanMatch") : t("certificates.sanMismatch")}
                      </Badge>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start text-sm text-grid-muted">
                    {fmt(c.last_checked_at)}
                  </TableCell>
                  <TableCell className="text-end">
                    {c.ok ? (
                      <Badge variant="outline" className="text-grid-ok">
                        {t("certificates.ok")}
                      </Badge>
                    ) : (
                      <Badge variant="outline" className="text-grid-danger" title={c.error}>
                        {c.error ? t("certificates.error") : t("certificates.unknown")}
                      </Badge>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      )}
    </PageBody>
  )
}

/** Green above 14 days, amber at 14 or fewer, red at 7 or fewer or when the last check failed. */
function DaysLeftBadge({ daysLeft, ok }: { daysLeft: number | undefined; ok: boolean }) {
  const { t } = useI18n()
  if (daysLeft === undefined) return null
  const tone = !ok || daysLeft <= 7 ? "text-grid-danger" : daysLeft <= 14 ? "text-grid-warn" : "text-grid-ok"
  return (
    <Badge variant="outline" className={cn("w-fit", tone)}>
      {daysLeft >= 0 ? t("certificates.daysLeft", { n: daysLeft }) : t("certificates.expired")}
    </Badge>
  )
}
