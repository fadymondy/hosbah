"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { CopyIcon, ShieldCheckIcon, ShieldOffIcon } from "lucide-react"
import { toast } from "sonner"

import { CodeInput, RecoveryInput } from "@/components/auth/code-input"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { auth, authMessage } from "@/lib/auth"
import { useI18n } from "@/lib/i18n"

type Status = { enabled: boolean; recovery_codes_left: number; available: boolean }
type Enrollment = { secret: string; otpauth_url: string; qr: string }

/**
 * Two-factor authentication (an authenticator app), as on fadymondy.com: set up with a QR code
 * and a confirming code; ten single-use recovery codes are shown once; new codes and turning it
 * off both need a current code (turning off also accepts a recovery code).
 */
export function TwoFactor() {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [status, setStatus] = useState<Status | null>(null)
  const [enroll, setEnroll] = useState<Enrollment | null>(null)
  const [codes, setCodes] = useState<string[] | null>(null)
  const [code, setCode] = useState("")
  const [recovery, setRecovery] = useState("")
  const [panel, setPanel] = useState<"none" | "disable" | "regenerate">("none")
  const [useRecovery, setUseRecovery] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setStatus(await auth.twoFactor.status())
    } catch (err) {
      setError(errorText(err))
    }
  }, [errorText])

  useEffect(() => {
    void load()
  }, [load])

  const fail = (err: unknown) => setError(authMessage(err, t, errorText))

  async function run(fn: () => Promise<void>) {
    setBusy(true)
    setError(null)
    try {
      await fn()
    } catch (err) {
      fail(err)
    } finally {
      setBusy(false)
    }
  }

  const start = () =>
    run(async () => {
      setEnroll(await auth.twoFactor.enroll())
      setCode("")
    })

  const confirm = (e: FormEvent) => {
    e.preventDefault()
    return run(async () => {
      const r = await auth.twoFactor.confirm(code)
      setEnroll(null)
      setCodes(r.recovery_codes)
      setCode("")
      toast.success(t("account.twofa.enabledToast"))
      await load()
    })
  }

  const disable = (e: FormEvent) => {
    e.preventDefault()
    return run(async () => {
      await auth.twoFactor.disable(useRecovery ? { recovery_code: recovery } : { code })
      setPanel("none")
      setCode("")
      setRecovery("")
      setCodes(null)
      toast.success(t("account.twofa.disabledToast"))
      await load()
    })
  }

  const regenerate = (e: FormEvent) => {
    e.preventDefault()
    return run(async () => {
      const r = await auth.twoFactor.newRecoveryCodes(code)
      setCodes(r.recovery_codes)
      setPanel("none")
      setCode("")
      await load()
    })
  }

  return (
    <section aria-labelledby="twofa-title">
      <SectionTitle
        id="twofa-title"
        title={t("account.twofa.title")}
        hint={t("account.twofa.description")}
        action={
          status ? (
            <Badge variant={status.enabled ? "default" : "outline"}>
              {status.enabled ? t("account.twofa.on") : t("account.twofa.off")}
            </Badge>
          ) : null
        }
      />
      <ErrorBand message={error} />

      {!status ? (
        <LoadingRows rows={2} />
      ) : !status.available ? (
        <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{t("account.twofaUnavailable")}</p>
      ) : codes ? (
        <RecoveryCodes codes={codes} onDone={() => setCodes(null)} />
      ) : enroll ? (
        <form onSubmit={confirm}>
          <SettingsRow label={t("account.scanTitle")} hint={t("account.twofa.secretHint")}>
            <div className="flex flex-wrap items-start gap-5">
              {/* eslint-disable-next-line @next/next/no-img-element -- a server-made data: URL */}
              <img
                src={enroll.qr}
                alt={t("account.qrAlt")}
                width={168}
                height={168}
                className="border border-line bg-white p-2 [image-rendering:pixelated]"
              />
              <div className="flex min-w-0 flex-1 flex-col gap-2">
                <span className="grid-micro">{t("account.twofa.secret")}</span>
                <code dir="ltr" className="break-all border border-line bg-grid-card px-3 py-2 font-mono text-sm">
                  {enroll.secret}
                </code>
                <a href={enroll.otpauth_url} className="text-sm text-grid-brand underline-offset-4 hover:underline">
                  {t("account.twofa.otpauthHint")}
                </a>
              </div>
            </div>
          </SettingsRow>
          <SettingsRow label={t("account.appCode")} htmlFor="code" hint={t("account.confirmHint")}>
            <div className="max-w-48">
              <CodeInput value={code} onChange={setCode} autoFocus />
            </div>
          </SettingsRow>
          <div className="flex justify-end gap-2 border-b border-line px-6 py-4">
            <Button type="button" variant="ghost" onClick={() => setEnroll(null)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || code.length !== 6}>
              {busy ? t("common.working") : t("account.twofa.verify")}
            </Button>
          </div>
        </form>
      ) : !status.enabled ? (
        <div className="flex border-b border-line px-6 py-4">
          <Button onClick={() => void start()} disabled={busy}>
            <ShieldCheckIcon />
            {t("account.twofa.enable")}
          </Button>
        </div>
      ) : panel === "none" ? (
        <>
          <SettingsRow label={t("account.recoveryLeftLabel")} hint={t("account.recoveryLeftHint")}>
            <span className="font-mono text-xl tabular-nums">{status.recovery_codes_left}</span>
          </SettingsRow>
          <div className="flex flex-wrap gap-2 border-b border-line px-6 py-4">
            <Button variant="outline" onClick={() => setPanel("regenerate")}>
              {t("account.newRecoveryCodes")}
            </Button>
            <Button variant="outline" onClick={() => setPanel("disable")}>
              <ShieldOffIcon />
              {t("account.twofa.disable")}
            </Button>
          </div>
        </>
      ) : (
        <form onSubmit={panel === "disable" ? disable : regenerate}>
          <SettingsRow
            label={panel === "disable" && useRecovery ? t("account.recoveryCode") : t("account.appCode")}
            htmlFor={panel === "disable" && useRecovery ? "recovery" : "code"}
            hint={panel === "disable" ? t("account.twofa.disableHint") : t("account.regenerateHint")}
          >
            <div className="max-w-48">
              {panel === "disable" && useRecovery ? (
                <RecoveryInput value={recovery} onChange={setRecovery} />
              ) : (
                <CodeInput value={code} onChange={setCode} autoFocus />
              )}
            </div>
            {panel === "disable" ? (
              <button
                type="button"
                className="self-start text-xs text-grid-muted underline-offset-4 hover:text-grid-fg hover:underline"
                onClick={() => setUseRecovery((v) => !v)}
              >
                {useRecovery ? t("account.useAppCode") : t("account.useRecovery")}
              </button>
            ) : null}
          </SettingsRow>
          <div className="flex justify-end gap-2 border-b border-line px-6 py-4">
            <Button type="button" variant="ghost" onClick={() => setPanel("none")}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" variant={panel === "disable" ? "destructive" : "default"} disabled={busy}>
              {busy
                ? t("common.working")
                : panel === "disable"
                  ? t("account.twofa.disable")
                  : t("account.newRecoveryCodes")}
            </Button>
          </div>
        </form>
      )}
    </section>
  )
}

/** Ten single-use codes, shown once: copy them, or download them as a text file. */
function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const { t } = useI18n()
  const text = codes.join("\n")
  return (
    <div className="border-b border-line px-6 py-5">
      <p className="mb-1 font-medium text-grid-fg">{t("account.recoveryTitle")}</p>
      <p className="mb-4 max-w-2xl text-sm text-grid-muted">{t("account.recoveryHint")}</p>
      <ol dir="ltr" className="mb-4 grid max-w-md grid-cols-2 gap-px border border-line bg-line font-mono text-sm">
        {codes.map((c) => (
          <li key={c} className="bg-grid-card px-3 py-2 text-center tracking-widest">
            {c}
          </li>
        ))}
      </ol>
      <div className="flex flex-wrap gap-2">
        <Button
          variant="outline"
          onClick={() => {
            void navigator.clipboard.writeText(text)
            toast.success(t("account.copied"))
          }}
        >
          <CopyIcon />
          {t("account.copyCodes")}
        </Button>
        <Button
          variant="outline"
          nativeButton={false}
          render={<a href={`data:text/plain;charset=utf-8,${encodeURIComponent(text + "\n")}`} download="hosbah-recovery-codes.txt" />}
        >
          {t("account.downloadCodes")}
        </Button>
        <Button onClick={onDone}>{t("account.savedThem")}</Button>
      </div>
    </div>
  )
}
