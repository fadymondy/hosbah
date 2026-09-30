"use client"

import { Button, Checkbox, EmptyState, Input, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, Textarea } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { EyeIcon, InfoIcon, KeyRoundIcon, PencilIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import type { Controlled } from "@/components/dialog-state"
import { useErrorText } from "@/components/form-error"
import type { BuildConfig } from "@/components/site/build-config"
import { ErrorBand, LoadingRows, Notice, SectionTitle, SettingsRow, TableBand } from "@/components/states"






import { apiFetch } from "@/lib/api"
import { ENV_NAME, type EnvVar } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"

/**
 * Remove one variable (from the next deploy or restart). Also how the overview canvas disconnects a
 * database: by removing the variable that points at it.
 */
export function RemoveEnvVar({ path, name, onRemoved, ...controlled }: { path: string; name: string; onRemoved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      {...controlled}
      label={t("apps.env.remove")}
      title={t("apps.env.removeTitle", { name })}
      description={t("apps.env.removeHint")}
      confirmLabel={t("apps.env.remove")}
      onConfirm={async () => {
        try {
          await apiFetch(`${path}/env/${encodeURIComponent(name)}`, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onRemoved()
      }}
    />
  )
}

/** The app's environment: names only, values write-only (owners/admins can reveal one, audited). */
export function EnvPanel({ path, manage }: { path: string; manage: boolean }) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [vars, setVars] = useState<EnvVar[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [name, setName] = useState("")
  const [value, setValue] = useState("")
  const [busy, setBusy] = useState(false)
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  // MH-579 gap A: which of these variables are also exposed at build time (as --build-arg), fetched
  // from the app's build settings (deploy-config/build) so the toggle lives right next to the value
  // it applies to.
  const buildConfigPath = `${path}/deploy-config/build`
  const [buildConfig, setBuildConfig] = useState<BuildConfig | null>(null)
  const [buildBusy, setBuildBusy] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ variables: EnvVar[] }>(`${path}/env`)
      setVars(r.variables ?? [])
    } catch (err) {
      setError(errorText(err))
      setVars((v) => v ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  const loadBuildConfig = useCallback(async () => {
    try {
      setBuildConfig(await apiFetch<BuildConfig>(buildConfigPath))
    } catch {
      // Read-only members may not have deploy access to this endpoint's PUT, but GET is any-member;
      // a failure here just hides the build-time column instead of failing the whole page.
      setBuildConfig(null)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [buildConfigPath])

  useEffect(() => {
    void load()
    void loadBuildConfig()
  }, [load, loadBuildConfig])

  async function toggleBuildTime(varName: string, on: boolean) {
    if (!buildConfig) return
    setBuildBusy(varName)
    setError(null)
    try {
      const build_env = on ? [...buildConfig.build_env, varName] : buildConfig.build_env.filter((n) => n !== varName)
      const r = await apiFetch<BuildConfig>(buildConfigPath, {
        method: "PUT",
        body: { dockerfile_path: buildConfig.dockerfile_path, build_context: buildConfig.build_context, build_env },
      })
      setBuildConfig(r)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBuildBusy(null)
    }
  }

  const nameOK = ENV_NAME.test(name)

  async function save(e: FormEvent) {
    e.preventDefault()
    if (!nameOK) return
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/env/${encodeURIComponent(name)}`, { method: "PUT", body: { value } })
      setRevealed(({ [name]: _, ...rest }) => rest)
      setName("")
      setValue("")
      await load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function reveal(n: string) {
    setError(null)
    try {
      const r = await apiFetch<{ name: string; value: string }>(`${path}/env/${encodeURIComponent(n)}/reveal`, {
        method: "POST",
      })
      setRevealed((cur) => ({ ...cur, [n]: r.value }))
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <div data-testid="env">
      <Notice icon={<InfoIcon />}>{t("apps.env.applies")}</Notice>
      {buildConfig ? <Notice icon={<InfoIcon />}>{t("apps.env.buildTimeHint")}</Notice> : null}
      <ErrorBand message={error} />
      {vars === null ? (
        <LoadingRows />
      ) : vars.length === 0 ? (
        <EmptyState className="border-b" icon={KeyRoundIcon} title={t("apps.env.empty")} />
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("apps.env.name")}</TableHead>
                <TableHead>{t("apps.env.value")}</TableHead>
                <TableHead>{t("apps.env.updated")}</TableHead>
                {buildConfig ? <TableHead title={t("apps.env.buildTimeHint")}>{t("apps.env.buildTime")}</TableHead> : null}
                {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {vars.map((v) => (
                <TableRow key={v.name} data-var={v.name}>
                  <TableCell dir="ltr" className="text-start font-mono text-xs">
                    {v.name}
                  </TableCell>
                  <TableCell dir="ltr" className="max-w-80 text-start font-mono text-xs break-all whitespace-pre-wrap">
                    {v.name in revealed ? revealed[v.name] : "••••••••"}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start text-xs">
                    {fmt(v.updated_at)}
                  </TableCell>
                  {buildConfig ? (
                    <TableCell>
                      <Checkbox
                        checked={buildConfig.build_env.includes(v.name)}
                        onCheckedChange={(val) => toggleBuildTime(v.name, !!val)}
                        disabled={!manage || buildBusy === v.name}
                        aria-label={t("apps.env.buildTime")}
                      />
                    </TableCell>
                  ) : null}
                  {manage ? (
                    <TableCell>
                      <div className="flex justify-end gap-2">
                        {!(v.name in revealed) ? (
                          <Button size="sm" variant="secondary" onClick={() => reveal(v.name)} title={t("apps.env.revealHint")}>
                            <EyeIcon />
                            {t("apps.env.reveal")}
                          </Button>
                        ) : null}
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() => {
                            setName(v.name)
                            setValue("")
                          }}
                        >
                          <PencilIcon />
                          {t("apps.env.edit")}
                        </Button>
                        <RemoveEnvVar path={path} name={v.name} onRemoved={load} />
                      </div>
                    </TableCell>
                  ) : null}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      )}
      {manage ? (
        <form onSubmit={save}>
          <SectionTitle title={t("apps.env.setTitle")} hint={t("apps.env.revealHint")} />
          <SettingsRow label={t("apps.env.name")} htmlFor="env-name" hint={t("apps.env.nameHint")}>
            <Input
              id="env-name"
              dir="ltr"
              required
              maxLength={128}
              value={name}
              placeholder="DATABASE_URL"
              onChange={(e) => setName(e.target.value)}
              aria-invalid={name !== "" && !nameOK}
              autoComplete="off"
            />
          </SettingsRow>
          <SettingsRow
            label={t("apps.env.value")}
            htmlFor="env-value"
            hint={vars?.some((v) => v.name === name) ? t("apps.env.overwrite") : undefined}
          >
            <Textarea
              id="env-value"
              dir="ltr"
              rows={3}
              value={value}
              maxLength={32768}
              onChange={(e) => setValue(e.target.value)}
              className="font-mono text-xs"
              autoComplete="off"
            />
          </SettingsRow>
          <div className="flex justify-end border-b border-border px-6 py-4">
            <Button variant="primary" type="submit" disabled={busy || !nameOK}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </div>
        </form>
      ) : (
        <p className="border-b border-border px-6 py-4 text-sm text-muted-foreground">{t("common.readOnly")}</p>
      )}
    </div>
  )
}
