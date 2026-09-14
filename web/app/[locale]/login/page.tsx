"use client"

import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useState, type FormEvent } from "react"

import { AuthFrame } from "@/components/auth-frame"
import { FormError, useErrorText } from "@/components/form-error"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { apiFetch, ApiError } from "@/lib/api"
import { safeNext } from "@/lib/next-param"
import { useDocumentTitle, useI18n } from "@/lib/i18n"

export default function LoginPage() {
  const { t, locale } = useI18n()
  useDocumentTitle(t("auth.login.title"))
  const router = useRouter()
  const next = safeNext(useSearchParams().get("next"), locale)
  const errorText = useErrorText()

  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [code, setCode] = useState("")
  const [needCode, setNeedCode] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch("/api/auth/login", {
        method: "POST",
        body: needCode ? { email, password, code } : { email, password },
      })
      router.replace(next ?? `/${locale}`)
    } catch (err) {
      if (err instanceof ApiError && err.code === "totp_required") {
        // First pass: the account has 2FA, ask for the code. Second pass: the code was wrong.
        if (needCode) setError(t("auth.login.badCode"))
        setNeedCode(true)
        setCode("")
      } else {
        setError(errorText(err))
      }
    } finally {
      setBusy(false)
    }
  }

  const registerHref = `/${locale}/register${next ? `?next=${encodeURIComponent(next)}` : ""}`

  return (
    <AuthFrame
      title={needCode ? t("auth.login.codeTitle") : t("auth.login.title")}
      description={needCode ? t("auth.login.codeDescription") : t("auth.login.description")}
      footer={
        <>
          {t("auth.login.noAccount")}{" "}
          <Link href={registerHref} className="text-foreground underline underline-offset-4">
            {t("auth.register.title")}
          </Link>
        </>
      }
    >
      <form onSubmit={onSubmit}>
        <FieldGroup>
          <FormError message={error} />
          {!needCode ? (
            <>
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
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </Field>
            </>
          ) : (
            <Field>
              <FieldLabel htmlFor="code">{t("auth.code")}</FieldLabel>
              <Input
                id="code"
                dir="ltr"
                inputMode="numeric"
                autoComplete="one-time-code"
                pattern="[0-9]{6}"
                maxLength={6}
                required
                autoFocus
                className="font-mono tracking-[0.4em]"
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
              />
              <FieldDescription>
                <span dir="ltr">{email}</span>
              </FieldDescription>
            </Field>
          )}
          <Field>
            <Button type="submit" disabled={busy}>
              {busy ? t("common.working") : needCode ? t("auth.login.verify") : t("auth.login.submit")}
            </Button>
            {needCode ? (
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  setNeedCode(false)
                  setError(null)
                }}
              >
                {t("common.back")}
              </Button>
            ) : null}
          </Field>
        </FieldGroup>
      </form>
    </AuthFrame>
  )
}
