"use client"

import { Button, Field, FieldDescription, FieldLabel, Input, SettingsSection } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useState, type FormEvent } from "react"
import { ExternalLinkIcon, LogOutIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { FormError, useErrorText } from "@/components/form-error"
import { panelApi } from "@/components/panel/auth"
import { usePanelViewer } from "@/components/panel/shell"
import { useI18n } from "@/lib/i18n"

/**
 * A panel's account page (MH-1141). A panel account changes its password and signs out here; a Hosbah
 * member is pointed at the console, where their own sign-in is managed.
 */
export function PanelAccountPage() {
  const { t, locale } = useI18n()
  const viewer = usePanelViewer()
  // A customer from an app link has no account page: the panel leads them back to their app.
  if (!viewer || viewer.kind === "app") return null

  if (viewer.kind === "member") {
    return (
      <PageBody title={t("panel.account.title")}>
        <SettingsSection
          title={t("panel.account.consoleTitle")}
          description={t("panel.account.consoleHint")}
          actions={
            <Button variant="secondary" size="sm" nativeButton={false} render={<Link href={`/${locale}/account`} />}>
              <ExternalLinkIcon />
              {t("panel.account.openConsole")}
            </Button>
          }
        >
          <p dir="ltr" className="px-4 py-3 font-mono text-sm text-muted-foreground">
            {viewer.email}
          </p>
        </SettingsSection>
      </PageBody>
    )
  }

  return (
    <PageBody title={t("panel.account.title")} description={t("panel.account.description")}>
      <SettingsSection title={t("panel.account.email")}>
        <p dir="ltr" className="px-4 py-3 font-mono text-sm text-foreground">
          {viewer.account.email}
        </p>
      </SettingsSection>
      <ChangePassword panelId={viewer.panelId} />
      <SettingsSection
        title={t("panel.account.signOut")}
        description={t("panel.account.signOutHint")}
        actions={
          <Button variant="secondary" size="sm" onClick={() => void viewer.signOut()}>
            <LogOutIcon />
            {t("panel.account.signOut")}
          </Button>
        }
      >
        {null}
      </SettingsSection>
    </PageBody>
  )
}

function ChangePassword({ panelId }: { panelId: string }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setDone(false)
    try {
      await panelApi.changePassword(panelId, current, next)
      setCurrent("")
      setNext("")
      setDone(true)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <SettingsSection title={t("panel.account.password")} description={t("panel.account.passwordHint")}>
      <form onSubmit={submit} className="flex max-w-md flex-col gap-4 px-4 py-4">
        <Field>
          <FieldLabel htmlFor="panel-current">{t("panel.account.current")}</FieldLabel>
          <Input id="panel-current" type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor="panel-new">{t("panel.account.new")}</FieldLabel>
          <Input id="panel-new" type="password" autoComplete="new-password" minLength={10} required value={next} onChange={(e) => setNext(e.target.value)} />
          <FieldDescription>{t("panel.accounts.passwordHint")}</FieldDescription>
        </Field>
        <FormError message={error} />
        {done ? (
          <p role="status" className="text-sm text-nq-success">
            {t("panel.account.saved")}
          </p>
        ) : null}
        <div>
          <Button variant="primary" type="submit" disabled={busy}>
            {busy ? t("common.working") : t("panel.account.save")}
          </Button>
        </div>
      </form>
    </SettingsSection>
  )
}
