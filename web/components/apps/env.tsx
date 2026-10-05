"use client"

import {
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  EmptyState,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  Textarea,
  useConfirm,
  type DataTableColumn,
  type DataTableRowAction,
} from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { EyeIcon, InfoIcon, KeyRoundIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import type { Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import type { BuildConfig } from "@/components/site/build-config"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, Notice, SectionTitle } from "@/components/states"
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
  const confirm = useConfirm()
  const [vars, setVars] = useState<EnvVar[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  // The variable being set in the dialog: "" for a new one, a name to change that one's value.
  const [editing, setEditing] = useState<string | null>(null)
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

  async function saved(name: string) {
    setRevealed(({ [name]: _, ...rest }) => rest)
    await load()
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

  async function remove(n: string) {
    const ok = await confirm({ title: t("apps.env.removeTitle", { name: n }), description: t("apps.env.removeHint"), confirmLabel: t("apps.env.remove") })
    if (!ok) return
    setError(null)
    try {
      await apiFetch(`${path}/env/${encodeURIComponent(n)}`, { method: "DELETE" })
      await load()
    } catch (err) {
      setError(errorText(err))
    }
  }

  const columns: DataTableColumn<EnvVar>[] = [
    {
      id: "name",
      header: t("apps.env.name"),
      cell: (v) => (
        <span dir="ltr" className="font-mono text-xs">
          {v.name}
        </span>
      ),
      sortValue: (v) => v.name,
      searchValue: (v) => v.name,
    },
    {
      id: "value",
      header: t("apps.env.value"),
      cell: (v) => (
        <span dir="ltr" className="block max-w-80 font-mono text-xs break-all whitespace-pre-wrap">
          {v.name in revealed ? revealed[v.name] : "••••••••"}
        </span>
      ),
    },
    { id: "updated", header: t("apps.env.updated"), cell: (v) => <span dir="ltr" className="text-xs">{fmt(v.updated_at)}</span>, sortValue: (v) => v.updated_at },
  ]
  if (buildConfig) {
    columns.push({
      id: "build",
      header: <span title={t("apps.env.buildTimeHint")}>{t("apps.env.buildTime")}</span>,
      label: t("apps.env.buildTime"),
      cell: (v) => (
        <Checkbox
          checked={buildConfig.build_env.includes(v.name)}
          onCheckedChange={(val) => toggleBuildTime(v.name, !!val)}
          disabled={!manage || buildBusy === v.name}
          aria-label={t("apps.env.buildTime")}
        />
      ),
    })
  }
  const rowActions = (v: EnvVar): DataTableRowAction[] => [
    ...(v.name in revealed ? [] : [{ id: "reveal", label: t("apps.env.reveal"), icon: EyeIcon, onSelect: () => void reveal(v.name) }]),
    { id: "edit", label: t("apps.env.edit"), icon: PencilIcon, onSelect: () => setEditing(v.name) },
    { id: "remove", label: t("apps.env.remove"), icon: Trash2Icon, danger: true, group: "danger", onSelect: () => void remove(v.name) },
  ]

  return (
    <div data-testid="env" data-stack>
      <SectionTitle
        title={t("site.nav.environment")}
        hint={t("site.environmentHint")}
        action={
          manage ? (
            <Button size="sm" variant="primary" onClick={() => setEditing("")}>
              <PlusIcon />
              {t("apps.env.add")}
            </Button>
          ) : null
        }
      />
      <Notice icon={<InfoIcon />}>{t("apps.env.applies")}</Notice>
      {buildConfig ? <Notice icon={<InfoIcon />}>{t("apps.env.buildTimeHint")}</Notice> : null}
      <ErrorBand message={error} />
      {vars === null ? (
        <LoadingRows />
      ) : vars.length === 0 ? (
        <EmptyState className="rounded-card border border-dashed border-border" icon={KeyRoundIcon} title={t("apps.env.empty")} />
      ) : (
        <NasaqTable
          data={vars}
          columns={columns}
          rowId={(v) => v.name}
          rowLabel={(v) => v.name}
          label={t("site.nav.environment")}
          search={vars.length > 8 ? t("apps.env.search") : undefined}
          rowActions={manage ? rowActions : undefined}
          onRowClick={manage ? (v) => setEditing(v.name) : undefined}
        />
      )}
      {!manage ? <p className="px-1 text-sm text-muted-foreground">{t("common.readOnly")}</p> : null}
      {editing !== null ? (
        <EnvDialog path={path} name={editing} exists={(n) => !!vars?.some((v) => v.name === n)} onClose={() => setEditing(null)} onSaved={saved} />
      ) : null}
    </div>
  )
}

/** Set one variable: a new one (name and value), or a new value for an existing one. */
function EnvDialog({
  path,
  name: fixed,
  exists,
  onClose,
  onSaved,
}: {
  path: string
  /** "" for a new variable. */
  name: string
  exists: (name: string) => boolean
  onClose: () => void
  onSaved: (name: string) => Promise<void>
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [name, setName] = useState(fixed)
  const [value, setValue] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const nameOK = ENV_NAME.test(name)

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!nameOK) return
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/env/${encodeURIComponent(name)}`, { method: "PUT", body: { value } })
      onClose()
      await onSaved(name)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{fixed ? t("apps.env.editTitle", { name: fixed }) : t("apps.env.add")}</DialogTitle>
            <DialogDescription>{t("apps.env.applies")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="env-name">{t("apps.env.name")}</FieldLabel>
              <Input
                id="env-name"
                dir="ltr"
                required
                maxLength={128}
                value={name}
                placeholder="DATABASE_URL"
                disabled={!!fixed}
                onChange={(e) => setName(e.target.value)}
                aria-invalid={name !== "" && !nameOK}
                autoComplete="off"
                className="font-mono"
              />
              <FieldDescription>{!fixed && exists(name) ? t("apps.env.overwrite") : t("apps.env.nameHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="env-value">{t("apps.env.value")}</FieldLabel>
              <Textarea
                id="env-value"
                dir="ltr"
                rows={4}
                value={value}
                maxLength={32768}
                onChange={(e) => setValue(e.target.value)}
                className="font-mono text-xs"
                autoComplete="off"
              />
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !nameOK}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
