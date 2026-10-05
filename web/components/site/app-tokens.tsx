"use client"

import { SettingsSection, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows } from "@/components/states"
import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"

type AppToken = { id: string; label: string; created_by_email: string; created_at: string; last_used_at: string | null }

/**
 * The app's integration tokens (hba_…, MH-1153): minted by an outside service through the
 * integrations API, scoped to this app only. Listed without their secrets; revoking one is final.
 */
export function SiteAppTokens() {
  const { t } = useI18n()
  const errorText = useErrorText()
  const fmt = useFormatDate()
  const { path } = useLoadedSite()
  const [tokens, setTokens] = useState<AppToken[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setTokens((await apiFetch<{ tokens: AppToken[] | null }>(`${path}/tokens`)).tokens ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <SettingsSection title={t("panel.appTokens.title")} description={t("panel.appTokens.hint")}>
      <ErrorBand message={error} />
      {tokens === null ? (
        error ? null : <LoadingRows rows={2} />
      ) : tokens.length === 0 ? (
        <p className="px-4 py-3 text-sm text-muted-foreground">{t("panel.appTokens.empty")}</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("panel.appTokens.label")}</TableHead>
              <TableHead>{t("panel.appTokens.createdBy")}</TableHead>
              <TableHead>{t("panel.appTokens.created")}</TableHead>
              <TableHead>{t("panel.appTokens.lastUsed")}</TableHead>
              <TableHead className="text-end" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {tokens.map((tk) => (
              <TableRow key={tk.id}>
                <TableCell className="font-medium">{tk.label}</TableCell>
                <TableCell>
                  <span dir="ltr" className="text-sm">
                    {tk.created_by_email}
                  </span>
                </TableCell>
                <TableCell className="text-muted-foreground">{fmt(tk.created_at)}</TableCell>
                <TableCell className="text-muted-foreground">{tk.last_used_at ? fmt(tk.last_used_at) : t("panel.appTokens.never")}</TableCell>
                <TableCell className="text-end">
                  <ConfirmButton
                    icon={<Trash2Icon />}
                    label={t("panel.appTokens.revoke")}
                    title={t("panel.appTokens.revokeTitle", { label: tk.label })}
                    description={t("panel.appTokens.revokeHint")}
                    confirmLabel={t("panel.appTokens.revoke")}
                    onConfirm={async () => {
                      try {
                        await apiFetch(`${path}/tokens/${tk.id}`, { method: "DELETE" })
                      } catch (err) {
                        throw new Error(errorText(err))
                      }
                      await load()
                    }}
                  />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </SettingsSection>
  )
}
