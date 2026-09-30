"use client"

import { Button, Field, FieldLabel, Input } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useState, type FormEvent } from "react"

import { CodeInput } from "@/components/auth/code-input"
import { AuthFrame } from "@/components/auth-frame"
import { FormError, useErrorText } from "@/components/form-error"



import { auth, authMessage, MIN_PASSWORD } from "@/lib/auth"
import { useDocumentTitle, useI18n } from "@/lib/i18n"

/**
 * Forgot password: the address, then the mailed code with a new password, then done. The answer
 * never says whether an address has an account; a reset signs every other session out.
 */
export default function ForgotPasswordPage() {
  const { t, locale } = useI18n()
  useDocumentTitle(t("account.forgotTitle"))
  const errorText = useErrorText()
  const [step, setStep] = useState<"email" | "reset" | "done">("email")
  const [email, setEmail] = useState("")
  const [code, setCode] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function sendCode(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      await auth.forgotPassword(email.trim(), locale)
      setStep("reset")
    } catch (err) {
      setError(authMessage(err, t, errorText))
    } finally {
      setBusy(false)
    }
  }

  async function reset(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (password.length < MIN_PASSWORD) return setError(t("auth.register.passwordShort"))
    if (password !== confirm) return setError(t("account.passwordMismatch"))
    setBusy(true)
    try {
      await auth.resetPassword(email.trim(), code, password)
      setStep("done")
    } catch (err) {
      setError(authMessage(err, t, errorText))
    } finally {
      setBusy(false)
    }
  }

  const back = (
    <Link href={`/${locale}/login`} className="text-foreground underline underline-offset-4">
      {t("account.backToLogin")}
    </Link>
  )

  if (step === "done") {
    return (
      <AuthFrame title={t("account.forgotTitle")} description={t("account.resetDone")}>
        <Button variant="primary" nativeButton={false} render={<Link href={`/${locale}/login`} />}>
          {t("auth.login.title")}
        </Button>
      </AuthFrame>
    )
  }

  return (
    <AuthFrame
      title={t("account.forgotTitle")}
      description={step === "email" ? t("account.forgotIntro") : t("account.forgotSent", { email: email.trim() })}
      footer={back}
    >
      {step === "email" ? (
        <form onSubmit={sendCode}>
          <div className="flex w-full flex-col gap-5">
            <FormError message={error} />
            <Field>
              <FieldLabel htmlFor="email">{t("auth.email")}</FieldLabel>
              <Input
                id="email"
                type="email"
                dir="ltr"
                autoComplete="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </Field>
            <Field>
              <Button variant="primary" type="submit" disabled={busy}>
                {busy ? t("common.working") : t("account.sendCode")}
              </Button>
            </Field>
          </div>
        </form>
      ) : (
        <form onSubmit={reset}>
          <div className="flex w-full flex-col gap-5">
            <FormError message={error} />
            <Field>
              <FieldLabel htmlFor="code">{t("account.code")}</FieldLabel>
              <CodeInput value={code} onChange={setCode} autoFocus />
            </Field>
            <Field>
              <FieldLabel htmlFor="password">{t("account.newPassword")}</FieldLabel>
              <Input
                id="password"
                type="password"
                autoComplete="new-password"
                minLength={MIN_PASSWORD}
                maxLength={72}
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="confirm">{t("account.confirmPassword")}</FieldLabel>
              <Input
                id="confirm"
                type="password"
                autoComplete="new-password"
                required
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
              />
            </Field>
            <Field>
              <Button variant="primary" type="submit" disabled={busy || code.length !== 6}>
                {busy ? t("common.working") : t("account.setPassword")}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setStep("email")}>
                {t("account.useAnotherEmail")}
              </Button>
            </Field>
          </div>
        </form>
      )}
    </AuthFrame>
  )
}
