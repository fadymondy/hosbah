"use client"

import { Button, Input } from "@fadymondy/nasaq/web"
import { useState, type FormEvent } from "react"
import { toast } from "sonner"

import { useErrorText } from "@/components/form-error"
import { ErrorBand, SectionTitle, SettingsRow } from "@/components/states"


import { ApiError } from "@/lib/api"
import { auth, MIN_PASSWORD } from "@/lib/auth"
import { useI18n } from "@/lib/i18n"

/** Change the password with the current one (forgotten it? the sign-in page resets it by email). */
export function ChangePassword() {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (next.length < MIN_PASSWORD) return setError(t("auth.register.passwordShort"))
    if (next !== confirm) return setError(t("account.passwordMismatch"))
    setBusy(true)
    try {
      await auth.changePassword(current, next)
      setCurrent("")
      setNext("")
      setConfirm("")
      toast.success(t("account.passwordChanged"))
    } catch (err) {
      setError(err instanceof ApiError && err.status === 403 ? t("account.currentWrong") : errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section aria-labelledby="password-title">
      <SectionTitle id="password-title" title={t("account.passwordTitle")} hint={t("account.passwordHint")} className="border-t" />
      <form onSubmit={submit}>
        <ErrorBand message={error} />
        <SettingsRow label={t("account.currentPassword")} htmlFor="current-password">
          <Input
            id="current-password"
            type="password"
            autoComplete="current-password"
            required
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        </SettingsRow>
        <SettingsRow label={t("account.newPassword")} htmlFor="new-password" hint={t("auth.register.passwordHint")}>
          <Input
            id="new-password"
            type="password"
            autoComplete="new-password"
            minLength={MIN_PASSWORD}
            maxLength={72}
            required
            value={next}
            onChange={(e) => setNext(e.target.value)}
          />
        </SettingsRow>
        <SettingsRow label={t("account.confirmPassword")} htmlFor="confirm-password">
          <Input
            id="confirm-password"
            type="password"
            autoComplete="new-password"
            required
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        </SettingsRow>
        <div className="flex justify-end border-b border-border px-6 py-4">
          <Button variant="primary" type="submit" disabled={busy || !current || !next}>
            {busy ? t("common.working") : t("account.changePassword")}
          </Button>
        </div>
      </form>
    </section>
  )
}
