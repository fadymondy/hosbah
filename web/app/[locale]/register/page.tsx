"use client"

import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useState, type FormEvent } from "react"

import { AuthFrame } from "@/components/auth-frame"
import { FormError, useErrorText } from "@/components/form-error"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { apiFetch } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { safeNext } from "@/lib/next-param"

export default function RegisterPage() {
  const { t, locale } = useI18n()
  useDocumentTitle(t("auth.register.title"))
  const router = useRouter()
  const next = safeNext(useSearchParams().get("next"), locale)
  const errorText = useErrorText()

  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (password.length < 8) {
      setError(t("auth.register.passwordShort"))
      return
    }
    setBusy(true)
    setError(null)
    try {
      await apiFetch("/api/auth/register", { method: "POST", body: { email, password } })
      router.replace(next ?? `/${locale}`)
    } catch (err) {
      setError(errorText(err))
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
              minLength={8}
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <FieldDescription>{t("auth.register.passwordHint")}</FieldDescription>
          </Field>
          <Field>
            <Button type="submit" disabled={busy}>
              {busy ? t("common.working") : t("auth.register.submit")}
            </Button>
          </Field>
        </FieldGroup>
      </form>
    </AuthFrame>
  )
}
