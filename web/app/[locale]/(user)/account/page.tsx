"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { InboxIcon, ShieldCheckIcon } from "lucide-react"
import { toast } from "sonner"

import { PageBody } from "@/components/app-shell"
import { CopyField } from "@/components/copy-field"
import { FormError, useErrorText } from "@/components/form-error"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
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
      <MyReports />
    </PageBody>
  )
}

function CodeInput({ id, value, onChange }: { id: string; value: string; onChange: (v: string) => void }) {
  return (
    <Input
      id={id}
      dir="ltr"
      inputMode="numeric"
      autoComplete="one-time-code"
      pattern="[0-9]{6}"
      maxLength={6}
      required
      className="max-w-40 font-mono tracking-[0.4em]"
      value={value}
      onChange={(e) => onChange(e.target.value.replace(/\D/g, ""))}
    />
  )
}

function TwoFactor() {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [enabled, setEnabled] = useState<boolean | null>(null)
  const [enroll, setEnroll] = useState<{ secret: string; otpauth_url: string } | null>(null)
  const [code, setCode] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ enabled: boolean }>("/api/me/2fa")
      setEnabled(res.enabled)
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function start() {
    setBusy(true)
    setError(null)
    try {
      setEnroll(await apiFetch<{ secret: string; otpauth_url: string }>("/api/auth/2fa/enroll", { method: "POST" }))
      setCode("")
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function verify(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch("/api/auth/2fa/verify", { method: "POST", body: { code } })
      setEnroll(null)
      setCode("")
      toast.success(t("account.twofa.enabledToast"))
      await load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function disable(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch("/api/me/2fa/disable", { method: "POST", body: { code } })
      setCode("")
      toast.success(t("account.twofa.disabledToast"))
      await load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section>
      <SectionTitle
        title={
          <span className="flex flex-wrap items-center gap-2">
            <ShieldCheckIcon className="size-4 text-grid-brand" />
          {t("account.twofa.title")}
          {enabled === null ? null : enabled ? (
            <Badge>{t("account.twofa.on")}</Badge>
          ) : (
            <Badge variant="outline">{t("account.twofa.off")}</Badge>
          )}
          </span>
        }
        hint={t("account.twofa.description")}
      />
      <div className="border-b border-line px-6 py-5">
        <div className="flex max-w-xl flex-col gap-4">
        <FormError message={error} />
        {enabled === null && !error ? <Skeleton className="h-8 w-40" /> : null}

        {enabled === false && !enroll ? (
          <div>
            <Button onClick={start} disabled={busy}>
              {busy ? t("common.working") : t("account.twofa.enable")}
            </Button>
          </div>
        ) : null}

        {enabled === false && enroll ? (
          <form onSubmit={verify}>
            <FieldGroup>
              <Field>
                <FieldLabel>{t("account.twofa.secret")}</FieldLabel>
                <CopyField value={enroll.secret} />
                <FieldDescription>{t("account.twofa.secretHint")}</FieldDescription>
              </Field>
              <Field>
                <FieldLabel>{t("account.twofa.otpauth")}</FieldLabel>
                <CopyField value={enroll.otpauth_url} />
                <FieldDescription>{t("account.twofa.otpauthHint")}</FieldDescription>
              </Field>
              <Field>
                <FieldLabel htmlFor="verify-code">{t("auth.code")}</FieldLabel>
                <CodeInput id="verify-code" value={code} onChange={setCode} />
              </Field>
              <Field orientation="horizontal">
                <Button type="submit" disabled={busy || code.length !== 6}>
                  {busy ? t("common.working") : t("account.twofa.verify")}
                </Button>
                <Button type="button" variant="ghost" onClick={() => setEnroll(null)}>
                  {t("common.cancel")}
                </Button>
              </Field>
            </FieldGroup>
          </form>
        ) : null}

        {enabled === true ? (
          <form onSubmit={disable}>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="disable-code">{t("auth.code")}</FieldLabel>
                <CodeInput id="disable-code" value={code} onChange={setCode} />
                <FieldDescription>{t("account.twofa.disableHint")}</FieldDescription>
              </Field>
              <Field orientation="horizontal">
                <Button type="submit" variant="destructive" disabled={busy || code.length !== 6}>
                  {busy ? t("common.working") : t("account.twofa.disable")}
                </Button>
              </Field>
            </FieldGroup>
          </form>
        ) : null}
        </div>
      </div>
    </section>
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
