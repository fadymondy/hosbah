"use client"

import { Button, Field, FieldDescription, FieldLabel, Input } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useEffect, useState, type FormEvent } from "react"

import { CodeInput, RecoveryInput, RESEND_SECONDS } from "@/components/auth/code-input"
import { OAuthButtons, useOAuthErrorText } from "@/components/auth/oauth"
import { AuthFrame } from "@/components/auth-frame"
import { FormError, useErrorText } from "@/components/form-error"



import { ApiError } from "@/lib/api"
import { auth, authMessage, nextStep } from "@/lib/auth"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { safeNext } from "@/lib/next-param"

type Mode = "password" | "code"

/**
 * Sign in: a password or a code mailed to the address; then, when the account has an
 * authenticator, its code (or a recovery code). An unverified account is sent to verify first.
 */
export default function LoginPage() {
  const { t, locale } = useI18n()
  const router = useRouter()
  const params = useSearchParams()
  const next = safeNext(params.get("next"), locale)
  const errorText = useErrorText()
  const oauthErrorText = useOAuthErrorText()

  const [mode, setMode] = useState<Mode>("password")
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [codeSent, setCodeSent] = useState(false)
  const [otp, setOtp] = useState("")
  const [cooldown, setCooldown] = useState(0)
  const [challenge, setChallenge] = useState<string | null>(null)
  const [second, setSecond] = useState("")
  const [useRecovery, setUseRecovery] = useState(false)
  const [busy, setBusy] = useState(false)
  // A Google / GitHub round trip that failed comes back with ?oauth_error=<code>.
  const [error, setError] = useState<string | null>(() => oauthErrorText(params.get("oauth_error")))
  useDocumentTitle(challenge ? t("account.twoFactorTitle") : t("auth.login.title"))

  // A Google / GitHub sign-in of an account with two-factor comes back with #challenge=<id> (a
  // fragment: it never reaches a server log). Take it into the second step and drop it from the URL.
  useEffect(() => {
    const hash = new URLSearchParams(window.location.hash.replace(/^#/, ""))
    const id = hash.get("challenge")
    if (!id) return
    window.history.replaceState(null, "", window.location.pathname + window.location.search)
    setChallenge(id)
    setSecond("")
    setUseRecovery(false)
    setError(null)
  }, [])

  useEffect(() => {
    if (cooldown <= 0) return
    const id = setTimeout(() => setCooldown((c) => c - 1), 1000)
    return () => clearTimeout(id)
  }, [cooldown])

  const done = () => router.replace(next ?? `/${locale}`)
  const fail = (err: unknown) => setError(authMessage(err, t, errorText))

  /** A refusal that is really a next step: verify the address, or answer the second factor. */
  function follow(err: unknown): boolean {
    const step = nextStep(err, email.trim())
    if (step?.kind === "verify") {
      const q = new URLSearchParams({ email: step.email, ...(next ? { next } : {}) })
      router.push(`/${locale}/verify-email?${q}`)
      return true
    }
    if (step?.kind === "challenge") {
      setChallenge(step.challenge)
      setSecond("")
      setUseRecovery(false)
      setError(null)
      return true
    }
    return false
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      if (challenge) {
        await auth.challenge(challenge, useRecovery ? { recovery_code: second } : { code: second })
        done()
      } else if (mode === "password") {
        await auth.login(email.trim(), password, locale)
        done()
      } else if (!codeSent) {
        await auth.requestCode(email.trim(), locale)
        setCodeSent(true)
        setCooldown(RESEND_SECONDS)
      } else {
        await auth.signInWithCode(email.trim(), otp)
        done()
      }
    } catch (err) {
      if (challenge && err instanceof ApiError && err.reason === "challenge_expired") {
        // The second step lasts five minutes; after that the sign-in starts over.
        setChallenge(null)
        setSecond("")
      }
      if (!follow(err)) fail(err)
    } finally {
      setBusy(false)
    }
  }

  async function resend() {
    setError(null)
    try {
      await auth.requestCode(email.trim(), locale)
      setCooldown(RESEND_SECONDS)
    } catch (err) {
      fail(err)
    }
  }

  function switchMode() {
    setMode((m) => (m === "password" ? "code" : "password"))
    setCodeSent(false)
    setOtp("")
    setError(null)
  }

  const registerHref = `/${locale}/register${next ? `?next=${encodeURIComponent(next)}` : ""}`

  if (challenge) {
    return (
      <AuthFrame
        title={t("account.twoFactorTitle")}
        description={useRecovery ? t("account.recoveryIntro") : t("account.twoFactorIntro")}
      >
        <form onSubmit={onSubmit}>
          <div className="flex w-full flex-col gap-5">
            <FormError message={error} />
            <Field>
              <FieldLabel htmlFor={useRecovery ? "recovery" : "code"}>
                {useRecovery ? t("account.recoveryCode") : t("account.appCode")}
              </FieldLabel>
              {useRecovery ? (
                <RecoveryInput value={second} onChange={setSecond} />
              ) : (
                <CodeInput value={second} onChange={setSecond} autoFocus />
              )}
            </Field>
            <Field>
              <Button variant="primary" type="submit" disabled={busy || !second}>
                {busy ? t("common.working") : t("account.continue")}
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  setUseRecovery((v) => !v)
                  setSecond("")
                  setError(null)
                }}
              >
                {useRecovery ? t("account.useAppCode") : t("account.useRecovery")}
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  setChallenge(null)
                  setError(null)
                }}
              >
                {t("account.startOver")}
              </Button>
            </Field>
          </div>
        </form>
      </AuthFrame>
    )
  }

  return (
    <AuthFrame
      title={t("auth.login.title")}
      description={mode === "code" ? t("account.codeLoginIntro") : t("auth.login.description")}
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
        <div className="flex w-full flex-col gap-5">
          <FormError message={error} />
          <OAuthButtons next={next} />
          <Field>
            <FieldLabel htmlFor="email">{t("auth.email")}</FieldLabel>
            <Input
              id="email"
              type="email"
              dir="ltr"
              autoComplete="email"
              required
              readOnly={mode === "code" && codeSent}
              value={email}
              onChange={(e) => setEmail(e.target.value)}
            />
          </Field>

          {mode === "password" ? (
            <Field>
              <div className="flex items-center justify-between gap-2">
                <FieldLabel htmlFor="password">{t("auth.password")}</FieldLabel>
                <Link
                  href={`/${locale}/forgot-password`}
                  className="text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
                >
                  {t("account.forgotLink")}
                </Link>
              </div>
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </Field>
          ) : codeSent ? (
            <Field>
              <FieldLabel htmlFor="code">{t("account.code")}</FieldLabel>
              <CodeInput value={otp} onChange={setOtp} autoFocus />
              <FieldDescription>{t("account.codeSentTo", { email: email.trim() })}</FieldDescription>
            </Field>
          ) : null}

          <Field>
            <Button variant="primary" type="submit" disabled={busy}>
              {busy
                ? t("common.working")
                : mode === "code" && !codeSent
                  ? t("account.sendCode")
                  : t("auth.login.submit")}
            </Button>
            {mode === "code" && codeSent ? (
              <Button type="button" variant="ghost" disabled={cooldown > 0} onClick={() => void resend()}>
                {cooldown > 0 ? t("account.resendIn", { seconds: cooldown }) : t("account.resend")}
              </Button>
            ) : null}
            <Button type="button" variant="ghost" onClick={switchMode}>
              {mode === "password" ? t("account.useEmailCode") : t("account.usePassword")}
            </Button>
          </Field>
        </div>
      </form>
    </AuthFrame>
  )
}
