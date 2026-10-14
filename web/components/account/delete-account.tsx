"use client"

import { Button, Input } from "@fadymondy/nasaq/web"
import { useEffect, useState } from "react"
import { TriangleAlertIcon } from "lucide-react"

import { CodeInput } from "@/components/auth/code-input"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { Notice, SectionTitle, SettingsRow } from "@/components/states"

import { ApiError, apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useSession } from "@/lib/session"

/**
 * Delete the account (DELETE /api/me). The API wants one proof besides the session: the password, or a
 * 6-digit code mailed to the account's address (accounts that sign in with Google, Apple or GitHub only).
 * Teams only you are in go with the account if they have no servers left; a team with other members
 * needs another owner first — the API says which (sole_owner / team_has_servers).
 */
export function DeleteAccount() {
  const { t, locale } = useI18n()
  const { me } = useSession()
  const errorText = useErrorText()
  const [hasPassword, setHasPassword] = useState<boolean | null>(null)
  const [password, setPassword] = useState("")
  const [code, setCode] = useState("")
  const [sent, setSent] = useState(false)
  const [sending, setSending] = useState(false)
  const [sendError, setSendError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<{ has_password: boolean }>("/api/me/identities")
      .then((r) => setHasPassword(r.has_password))
      .catch(() => setHasPassword(true))
  }, [])

  async function sendCode() {
    setSending(true)
    setSendError(null)
    try {
      await apiFetch("/api/me/delete/code", { method: "POST", body: { locale } })
      setSent(true)
    } catch (err) {
      setSendError(errorText(err))
    } finally {
      setSending(false)
    }
  }

  async function remove() {
    const proof = hasPassword && password ? { password } : { code: code.trim() }
    try {
      await apiFetch("/api/me", { method: "DELETE", body: proof })
    } catch (err) {
      const reason = err instanceof ApiError ? err.reason : undefined
      throw new Error(reason ? t(`account.deleteAccount.errors.${reason}`, undefined, errorText(err)) : errorText(err))
    }
    // The session is gone with the account.
    window.location.assign(`/${locale}/login`)
  }

  const ready = (hasPassword && password.length > 0) || code.trim().length === 6

  return (
    <section aria-labelledby="delete-account-title" data-testid="delete-account">
      <SectionTitle id="delete-account-title" title={t("account.deleteAccount.title")} hint={t("account.deleteAccount.hint")} className="mt-6 border-t" />
      <Notice icon={<TriangleAlertIcon />}>{t("account.deleteAccount.dialogBody")}</Notice>
      {hasPassword ? (
        <SettingsRow label={t("account.deleteAccount.proofPassword")} htmlFor="delete-password">
          <Input id="delete-password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </SettingsRow>
      ) : null}
      {hasPassword !== null ? (
        <SettingsRow
          label={t("account.deleteAccount.proofCode", { email: me.user.email })}
          hint={sent ? t("account.deleteAccount.codeSent", { email: me.user.email }) : (sendError ?? undefined)}
        >
          {sent ? (
            <CodeInput id="delete-code" value={code} onChange={setCode} />
          ) : (
            <Button variant="secondary" type="button" disabled={sending} onClick={() => void sendCode()}>
              {sending ? t("common.working") : t("account.deleteAccount.sendCode")}
            </Button>
          )}
        </SettingsRow>
      ) : null}
      <div className="flex justify-end border-b border-border px-6 py-4">
        {ready ? (
          <ConfirmButton
            size="md"
            label={t("account.deleteAccount.button")}
            title={t("account.deleteAccount.dialogTitle")}
            description={t("account.deleteAccount.dialogBody")}
            confirmLabel={t("account.deleteAccount.confirm")}
            onConfirm={remove}
          />
        ) : (
          <Button variant="secondary" disabled>
            {t("account.deleteAccount.button")}
          </Button>
        )}
      </div>
    </section>
  )
}
