"use client"

import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useState, type FormEvent } from "react"

import { AuthFrame } from "@/components/auth-frame"
import { FormError, useErrorText } from "@/components/form-error"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { auth, authMessage, MIN_PASSWORD, nextStep } from "@/lib/auth"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { safeNext } from "@/lib/next-param"
import { invitationTokenFrom, usePublicSettings } from "@/lib/platform"

/**
 * Create an account. The server answers "email_unverified" and mails a code, so the visitor goes
 * on to verify the address (an invitation for the same address may already prove it, in which
 * case the server signs in directly). Closed or invite-only sign-up says so instead of a form.
 */
export default function RegisterPage() {
  const { t, locale } = useI18n()
  useDocumentTitle(t("auth.register.title"))
  const router = useRouter()
  const next = safeNext(useSearchParams().get("next"), locale)
  const errorText = useErrorText()
  const settings = usePublicSettings()
  const invitation = invitationTokenFrom(next)
  const blocked =
    settings?.signup_mode === "closed" ? "closed" : settings?.signup_mode === "invite_only" && !invitation ? "inviteOnly" : null

  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (password.length < MIN_PASSWORD) return setError(t("auth.register.passwordShort"))
    if (password !== confirm) return setError(t("account.passwordMismatch"))
    setBusy(true)
    try {
      await auth.register(email.trim(), password, locale, invitation)
      router.replace(next ?? `/${locale}`)
    } catch (err) {
      const step = nextStep(err, email.trim())
      if (step?.kind === "verify") {
        const q = new URLSearchParams({ email: step.email, ...(next ? { next } : {}) })
        router.push(`/${locale}/verify-email?${q}`)
        return
      }
      setError(authMessage(err, t, errorText))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthFrame
      title={t("auth.register.title")}
      description={t("auth.register.description")}
      footer={
        <>
          {t("auth.register.haveAccount")}{" "}
          <Link
            href={`/${locale}/login${next ? `?next=${encodeURIComponent(next)}` : ""}`}
            className="text-foreground underline underline-offset-4"
          >
            {t("auth.login.title")}
          </Link>
        </>
      }
    >
      {blocked ? (
        <div role="status" className="flex flex-col gap-2 border border-line bg-grid-card px-5 py-4">
          <p className="font-medium text-grid-fg">{t(`auth.register.${blocked}Title`)}</p>
          <p className="text-sm text-grid-muted">{t(`auth.register.${blocked}Body`)}</p>
          {settings?.support_email ? (
            <a
              href={`mailto:${settings.support_email}`}
              dir="ltr"
              className="text-sm text-grid-brand underline-offset-4 hover:underline"
            >
              {settings.support_email}
            </a>
          ) : null}
        </div>
      ) : (
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
              <FieldLabel htmlFor="password">{t("auth.password")}</FieldLabel>
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
              <FieldDescription>{t("auth.register.passwordHint")}</FieldDescription>
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
              <Button type="submit" disabled={busy}>
                {busy ? t("common.working") : t("auth.register.submit")}
              </Button>
            </Field>
          </FieldGroup>
        </form>
      )}
    </AuthFrame>
  )
}
