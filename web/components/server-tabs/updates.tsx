"use client"

import { Badge, Button, Checkbox, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { PackageIcon, RefreshCwIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { useLoadedServer } from "@/components/server-tabs/context"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow } from "@/components/states"




import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import type { UpdateStatus } from "@/lib/servers"

/** Upgradable packages, the upgrade button, and the automatic-security-updates toggle (MH-578 C8). */
export function ServerUpdates({ manage }: { manage: boolean }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const fmt = useFormatDate()
  const { path } = useLoadedServer()
  const [status, setStatus] = useState<UpdateStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [checking, setChecking] = useState(false)
  const [upgrading, setUpgrading] = useState(false)
  const [togglingAuto, setTogglingAuto] = useState(false)

  const load = useCallback(async () => {
    try {
      setStatus(await apiFetch<UpdateStatus>(`${path}/updates`))
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
  }, [load])

  async function checkNow() {
    setChecking(true)
    setError(null)
    try {
      setStatus(await apiFetch<UpdateStatus>(`${path}/updates/check`, { method: "POST" }))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setChecking(false)
    }
  }

  async function upgrade() {
    setUpgrading(true)
    setError(null)
    try {
      await apiFetch(`${path}/updates/upgrade`, { method: "POST", body: {} })
      await load()
    } catch (err) {
      setError(errorText(err))
      throw err
    } finally {
      setUpgrading(false)
    }
  }

  async function setAuto(enabled: boolean) {
    setTogglingAuto(true)
    setError(null)
    try {
      await apiFetch(`${path}/updates/auto`, { method: "PUT", body: { enabled } })
      await load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setTogglingAuto(false)
    }
  }

  return (
    <>
      <SectionTitle
        title={t("servers.updates.title")}
        hint={t("servers.updates.hint")}
        action={
          <Button variant="secondary" size="sm" onClick={() => void checkNow()} disabled={checking}>
            <RefreshCwIcon className={checking ? "animate-spin" : ""} />
            {checking ? t("common.working") : t("servers.updates.checkNow")}
          </Button>
        }
      />
      <ErrorBand message={error} />
      {status === null ? (
        <LoadingRows rows={2} />
      ) : (
        <>
          <SettingsRow
            label={t("servers.updates.upgradable")}
            hint={status.last_checked_at ? t("servers.updates.lastChecked", { when: fmt(status.last_checked_at) }) : t("servers.updates.neverChecked")}
          >
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant={status.upgradable_count > 0 ? "neutral" : "outline"}>
                {t("servers.updates.count", { n: status.upgradable_count })}
              </Badge>
              {status.reboot_required ? (
                <Badge variant="outline" className="text-nq-warning-text">
                  {t("servers.updates.rebootRequired")}
                </Badge>
              ) : null}
              {status.last_upgraded_at ? (
                <span dir="ltr" className="text-xs text-muted-foreground">
                  {t("servers.updates.lastUpgraded", { when: fmt(status.last_upgraded_at) })}
                </span>
              ) : null}
            </div>
            {status.packages.length > 0 ? (
              <div className="max-h-64 overflow-y-auto border border-border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t("servers.updates.package")}</TableHead>
                      <TableHead>{t("servers.updates.current")}</TableHead>
                      <TableHead>{t("servers.updates.candidate")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {status.packages.map((p) => (
                      <TableRow key={p.name}>
                        <TableCell dir="ltr" className="font-mono text-xs">
                          {p.name}
                        </TableCell>
                        <TableCell dir="ltr" className="font-mono text-xs text-muted-foreground">
                          {p.current || "—"}
                        </TableCell>
                        <TableCell dir="ltr" className="font-mono text-xs">
                          {p.candidate || "—"}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            ) : null}
            {manage ? (
              <div className="flex justify-end">
                <ConfirmButton
                  label={t("servers.updates.upgrade")}
                  title={t("servers.updates.upgradeTitle")}
                  description={t("servers.updates.upgradeHint", { n: status.upgradable_count })}
                  confirmLabel={t("servers.updates.upgrade")}
                  onConfirm={upgrade}
                  icon={<PackageIcon />}
                />
              </div>
            ) : null}
          </SettingsRow>
          <SettingsRow
            label={t("servers.updates.autoTitle")}
            hint={status.auto_supported ? t("servers.updates.autoHint") : t("servers.updates.autoUnsupported")}
            htmlFor="srv-auto-updates"
          >
            <div className="flex items-center gap-2">
              <Checkbox
                id="srv-auto-updates"
                checked={status.auto_security_updates}
                disabled={!manage || !status.auto_supported || togglingAuto || upgrading}
                onCheckedChange={(v) => void setAuto(v === true)}
              />
              <label htmlFor="srv-auto-updates" className="text-sm text-nq-fg-body">
                {status.auto_supported
                  ? status.auto_security_updates
                    ? t("servers.updates.autoOn")
                    : t("servers.updates.autoOff")
                  : t("servers.updates.unsupported")}
              </label>
            </div>
          </SettingsRow>
        </>
      )}
    </>
  )
}
