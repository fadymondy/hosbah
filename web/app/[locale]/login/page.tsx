"use client"

import { Button, Field, FieldLabel, SignInFlow, type OAuthProvider } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useEffect, useRef, useState, type FormEvent } from "react"

import { CodeInput, RecoveryInput } from "@/components/auth/code-input"
import { oauthHref, ProviderMark, useCircleXO, useCircleXOErrorText, useOAuthErrorText, useOAuthProviders } from "@/components/auth/oauth"
import { AuthFrame } from "@/components/auth-frame"
import { FormError, useErrorText } from "@/components/form-error"

import { ApiError } from "@/lib/api"
import { auth, authMessage, nextStep } from "@/lib/auth"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { safeNext } from "@/lib/next-param"

/**
 * Sign in, email first (Nasaq SignInFlow): the address, then its password or a code mailed to it; then,
 * when the account has an authenticator, its code (or a recovery code). An unverified account is sent to
 * verify first. A Google / GitHub / CircleXO sign-in that needs a second factor comes back with
 * #challenge= and answers it in the form below.
 */
export default function LoginPage() {
  const { t, locale } = useI18n()
  const router = useRouter()
  const params = useSearchParams()
  const next = safeNext(params.get("next"), locale)
  const errorText = useErrorText()
  const oauthErrorText = useOAuthErrorText()
  const circlexoErrorText = useCircleXOErrorText()
  const providers = useOAuthProviders()
  const circlexo = useCircleXO()

  const [email, setEmail] = useState("")
  const [challenge, setChallenge] = useState<string | null>(null)
  const [second, setSecond] = useState("")
  const [useRecovery, setUseRecovery] = useState(false)
  const [busy, setBusy] = useState(false)
  // The password step's second factor, answered inside SignInFlow.
  const pending = useRef<string | null>(null)
  // A Google / GitHub round trip that failed comes back with ?oauth_error=<code>.
  const [error, setError] = useState<string | null>(
    () => oauthErrorText(params.get("oauth_error")) ?? circlexoErrorText(params.get("circlexo_error")),
  )
  useDocumentTitle(challenge ? t("account.twoFactorTitle") : t("auth.login.title"))

  const oauthProviders: OAuthProvider[] = [
    ...(circlexo ? [{ id: "circlexo", label: t("auth.oauth.circlexo"), icon: null }] : []),
    ...(providers ?? []).map((p) => ({ id: p.id, label: t("auth.oauth.continueWith", { provider: p.name }), icon: <ProviderMark id={p.id} /> })),
  ]

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

  const done = () => router.replace(next ?? `/${locale}`)

  /** A refusal that is really a next step: verify the address, or answer the second factor. */
  function follow(err: unknown, address = email): boolean {
    const step = nextStep(err, address.trim())
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
    if (!challenge) return
    setError(null)
    setBusy(true)
    try {
      await auth.challenge(challenge, useRecovery ? { recovery_code: second } : { code: second })
      done()
    } catch (err) {
      if (err instanceof ApiError && err.reason === "challenge_expired") {
        // The second step lasts five minutes; after that the sign-in starts over.
        setChallenge(null)
        setSecond("")
      }
      setError(authMessage(err, t, errorText))
    } finally {
      setBusy(false)
    }
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
      description={t("auth.login.description")}
      footer={
        <>
          {t("auth.login.noAccount")} <Link href={registerHref}>{t("auth.register.title")}</Link>
        </>
      }
    >
      <div className="flex w-full flex-col gap-5">
        <FormError message={error} />
        <SignInFlow
          showRemember={false}
          oauthProviders={oauthProviders}
          onOAuth={(id) => {
            window.location.href =
              id === "circlexo" ? `/auth/circlexo/login${next ? `?return_to=${encodeURIComponent(next)}` : ""}` : oauthHref(id, { locale, next })
          }}
          onIdentify={() => ({ step: "password", alternatives: ["code"] })}
          onPassword={async ({ email: address, password }) => {
            setEmail(address)
            setError(null)
            try {
              await auth.login(address.trim(), password, locale)
              done()
            } catch (err) {
              const step = nextStep(err, address.trim())
              if (step?.kind === "challenge") {
                pending.current = step.challenge
                return { twoFactor: true }
              }
              if (follow(err, address)) return
              return { error: authMessage(err, t, errorText) }
            }
          }}
          onTwoFactor={async ({ code, method }) => {
            try {
              await auth.challenge(pending.current ?? "", method === "recovery" ? { recovery_code: code } : { code })
              done()
            } catch (err) {
              return { error: authMessage(err, t, errorText) }
            }
          }}
          onRequestCode={async (address) => {
            try {
              await auth.requestCode(address.trim(), locale)
            } catch (err) {
              return { error: authMessage(err, t, errorText) }
            }
          }}
          onCode={async ({ email: address, code }) => {
            setEmail(address)
            try {
              await auth.signInWithCode(address.trim(), code)
              done()
            } catch (err) {
              if (follow(err, address)) return
              return { error: authMessage(err, t, errorText) }
            }
          }}
          onRegister={() => router.push(registerHref)}
          forgotPassword={<Link href={`/${locale}/forgot-password`}>{t("account.forgotLink")}</Link>}
        />
      </div>
    </AuthFrame>
  )
}
