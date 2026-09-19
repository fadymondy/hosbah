"use client"

import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useEffect, useState, type FormEvent } from "react"

import { CodeInput, RESEND_SECONDS } from "@/components/auth/code-input"
import { AuthFrame } from "@/components/auth-frame"
import { FormError, useErrorText } from "@/components/form-error"
import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { auth, authMessage, nextStep } from "@/lib/auth"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { safeNext } from "@/lib/next-param"

/** Enter the code mailed to a new address. Verifying signs in (or asks for the second factor). */
export default function VerifyEmailPage() {
  const { t, locale } = useI18n()
  useDocumentTitle(t("account.verifyTitle"))
  const router = useRouter()
  const params = useSearchParams()
  const next = safeNext(params.get("next"), locale)
  const errorText = useErrorText()

  const [email, setEmail] = useState(params.get("email") ?? "")
  const [code, setCode] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [note, setNote] = useState<string | null>(null)
  // The server just sent one when it sent the visitor here, so resending waits a minute.
  const [cooldown, setCooldown] = useState(RESEND_SECONDS)

  useEffect(() => {
    if (cooldown <= 0) return
    const id = setTimeout(() => setCooldown((c) => c - 1), 1000)
    return () => clearTimeout(id)
  }, [cooldown])

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const r = await auth.verifyEmail(email.trim(), code)
      // Verified and signed in; without a session, sign in by hand.
      router.replace(r.token || r.user ? (next ?? `/${locale}`) : `/${locale}/login`)
    } catch (err) {
      // An account with an authenticator finishes on the sign-in page's second step.
      if (nextStep(err, email.trim())?.kind === "challenge") {
        router.replace(`/${locale}/login${next ? `?next=${encodeURIComponent(next)}` : ""}`)
        return
      }
      setError(authMessage(err, t, errorText))
    } finally {
      setBusy(false)
    }
  }

  async function resend() {
    setError(null)
    try {
      await auth.resendVerification(email.trim(), locale)
      setNote(t("account.codeResent"))
      setCooldown(RESEND_SECONDS)
    } catch (err) {
      setError(authMessage(err, t, errorText))
    }
  }

  return (
    <AuthFrame
      title={t("account.verifyTitle")}
      description={t("account.verifyIntro")}
      footer={
        <Link href={`/${locale}/login`} className="text-foreground underline underline-offset-4">
          {t("account.backToLogin")}
        </Link>
      }
    >
      <form onSubmit={onSubmit}>
        <FieldGroup>
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
            <FieldLabel htmlFor="code">{t("account.code")}</FieldLabel>
            <CodeInput value={code} onChange={setCode} autoFocus />
          </Field>
          {note ? (
            <p role="status" className="text-sm text-muted-foreground">
              {note}
            </p>
          ) : null}
          <Field>
            <Button type="submit" disabled={busy || code.length !== 6}>
              {busy ? t("common.working") : t("account.verify")}
            </Button>
            <Button type="button" variant="ghost" disabled={!email || cooldown > 0} onClick={() => void resend()}>
              {cooldown > 0 ? t("account.resendIn", { seconds: cooldown }) : t("account.resend")}
            </Button>
          </Field>
        </FieldGroup>
      </form>
    </AuthFrame>
  )
}
