"use client"

import { AuthFooter, AuthLayout, Button, Field, FieldDescription, FieldLabel, Input, SignInFlow } from "@fadymondy/nasaq/web"
import { useRouter } from "next/navigation"
import { useEffect, useState, type CSSProperties, type FormEvent, type ReactNode } from "react"

import { CodeInput } from "@/components/auth/code-input"
import { FormError } from "@/components/form-error"
import { LanguageMenu, ThemeToggle } from "@/components/preferences"
import { ServerMark } from "@/components/server-mark"
import { apiFetch, ApiError } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"

/** What a panel shows before sign-in: the server's own name and mark (GET /api/panel/{id}/brand). */
export type PanelBrand = { id: string; name: string; color: string | null; icon: string | null; logo_url: string | null }
export type PanelAccount = { id: string; email: string }

/** The panel's brand; null when there is no such panel, undefined while loading. */
export function usePanelBrand(panelId: string): PanelBrand | null | undefined {
  const [brand, setBrand] = useState<PanelBrand | null | undefined>(undefined)
  useEffect(() => {
    let live = true
    apiFetch<PanelBrand>(`/api/panel/${encodeURIComponent(panelId)}/brand`)
      .then((b) => live && setBrand(b))
      .catch(() => live && setBrand(null))
    return () => {
      live = false
    }
  }, [panelId])
  return brand
}

/** The panel's colour as the theme's brand and action colour, so buttons and focus rings follow it. */
export function brandStyle(color: string | null | undefined): CSSProperties | undefined {
  return color ? ({ "--nq-brand": color, "--nq-action": color } as CSSProperties) : undefined
}

export const panelApi = {
  me: (id: string) => apiFetch<{ account: PanelAccount }>(`/api/panel/${encodeURIComponent(id)}/me`),
  login: (id: string, email: string, password: string) =>
    apiFetch<{ account: PanelAccount }>(`/api/panel/${encodeURIComponent(id)}/login`, { method: "POST", body: { email, password } }),
  logout: (id: string) => apiFetch(`/api/panel/${encodeURIComponent(id)}/logout`, { method: "POST", body: {} }),
  forgot: (id: string, email: string) => apiFetch(`/api/panel/${encodeURIComponent(id)}/password/forgot`, { method: "POST", body: { email } }),
  reset: (id: string, email: string, code: string, password: string) =>
    apiFetch<{ account: PanelAccount }>(`/api/panel/${encodeURIComponent(id)}/password/reset`, { method: "POST", body: { email, code, password } }),
  changePassword: (id: string, current: string, password: string) =>
    apiFetch(`/api/panel/${encodeURIComponent(id)}/password`, { method: "POST", body: { current, password } }),
}

/** The brand side of the panel's sign-in: only the server's mark, name and colour, nothing of Hosbah. */
function BrandPanel({ brand }: { brand: PanelBrand }) {
  return (
    <div className="relative flex flex-1 flex-col items-center justify-center gap-5 text-center">
      <ServerMark server={brand} size={22} />
      <p dir="auto" className="text-h1 text-foreground">
        {brand.name}
      </p>
    </div>
  )
}

/**
 * A panel's own sign-in (MH-1141): email first, then the password of a panel account of this server.
 * Panel accounts sign in here only; the Hosbah console refuses them.
 */
export function PanelLogin({ panelId, next }: { panelId: string; next?: string | null }) {
  const { t, locale } = useI18n()
  const router = useRouter()
  const brand = usePanelBrand(panelId)
  const [forgot, setForgot] = useState(false)
  useDocumentTitle(brand?.name ?? t("panel.login.title"))

  if (brand === undefined) return null
  if (brand === null) return <PanelFrame>{t("panel.notFound")}</PanelFrame>
  const home = () => router.replace(next && next.startsWith(`/${locale}/panel/${panelId}`) ? next : `/${locale}/panel/${panelId}`)

  return (
    <AuthLayout
      variant="split"
      style={brandStyle(brand.color)}
      panel={<BrandPanel brand={brand} />}
      mark={
        <span className="lg:hidden">
          <ServerMark server={brand} size={10} />
        </span>
      }
      title={forgot ? t("panel.forgot.title") : t("panel.login.title")}
      description={forgot ? t("panel.forgot.description") : t("panel.login.description", { name: brand.name })}
      origin={false}
      footer={
        <AuthFooter
          end={
            <>
              <LanguageMenu />
              <ThemeToggle />
            </>
          }
        />
      }
    >
      {forgot ? (
        <PanelReset panelId={panelId} onDone={home} onBack={() => setForgot(false)} />
      ) : (
        <SignInFlow
          showRemember={false}
          onIdentify={() => ({ step: "password", alternatives: [] })}
          onPassword={async ({ email, password }) => {
            try {
              await panelApi.login(panelId, email.trim(), password)
              home()
            } catch (err) {
              return { error: message(err) }
            }
          }}
          forgotPassword={
            <button type="button" className="text-nq-action hover:underline" onClick={() => setForgot(true)}>
              {t("panel.forgot.link")}
            </button>
          }
        />
      )}
    </AuthLayout>
  )
}

const message = (err: unknown) => (err instanceof ApiError || err instanceof Error ? err.message : String(err))

/** Forgot password: the address, then the mailed code and a new password, which also signs in. */
function PanelReset({ panelId, onDone, onBack }: { panelId: string; onDone: () => void; onBack: () => void }) {
  const { t } = useI18n()
  const [email, setEmail] = useState("")
  const [sent, setSent] = useState(false)
  const [code, setCode] = useState("")
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function send(e?: FormEvent) {
    e?.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await panelApi.forgot(panelId, email.trim())
      setSent(true)
    } catch (err) {
      setError(message(err))
    } finally {
      setBusy(false)
    }
  }

  async function reset(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await panelApi.reset(panelId, email.trim(), code.trim(), password)
      onDone()
    } catch (err) {
      setError(message(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={sent ? reset : send} className="flex w-full flex-col gap-5">
      <FormError message={error} />
      {sent ? (
        <>
          <p role="status" className="text-sm text-muted-foreground">
            {t("panel.forgot.sent", { email: email.trim() })}
          </p>
          <Field>
            <FieldLabel htmlFor="panel-code">{t("panel.forgot.code")}</FieldLabel>
            <CodeInput value={code} onChange={setCode} autoFocus />
          </Field>
          <Field>
            <FieldLabel htmlFor="panel-new-password">{t("panel.forgot.password")}</FieldLabel>
            <Input id="panel-new-password" type="password" autoComplete="new-password" minLength={10} required value={password} onChange={(e) => setPassword(e.target.value)} />
            <FieldDescription>{t("panel.accounts.passwordHint")}</FieldDescription>
          </Field>
          <Button variant="primary" type="submit" disabled={busy || code.length < 6}>
            {busy ? t("common.working") : t("panel.forgot.submit")}
          </Button>
          <Button type="button" variant="ghost" disabled={busy} onClick={() => void send()}>
            {t("panel.forgot.resend")}
          </Button>
        </>
      ) : (
        <>
          <Field>
            <FieldLabel htmlFor="panel-forgot-email">{t("panel.accounts.email")}</FieldLabel>
            <Input id="panel-forgot-email" type="email" dir="ltr" autoComplete="email" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
          </Field>
          <Button variant="primary" type="submit" disabled={busy}>
            {busy ? t("common.working") : t("panel.forgot.send")}
          </Button>
        </>
      )}
      <Button type="button" variant="ghost" onClick={onBack}>
        {t("panel.forgot.back")}
      </Button>
    </form>
  )
}

function PanelFrame({ children }: { children: ReactNode }) {
  return <div className="flex min-h-dvh items-center justify-center p-6 text-center text-muted-foreground">{children}</div>
}
