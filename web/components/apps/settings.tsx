"use client"

import { useRouter } from "next/navigation"
import { useEffect, useState, type FormEvent } from "react"
import { AlertTriangleIcon, InfoIcon } from "lucide-react"

import { CopyField } from "@/components/copy-field"
import { FormError, useErrorText } from "@/components/form-error"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { apiFetch } from "@/lib/api"
import type { App, AppDetail, Integrations } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"

export function SettingsPanel({
  team,
  path,
  detail,
  manage,
  onSaved,
}: {
  team: string
  path: string
  detail: AppDetail
  manage: boolean
  onSaved: (a: App) => void
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const app = detail.app
  const [branch, setBranch] = useState(app.branch)
  const [port, setPort] = useState(String(app.port))
  const [healthPath, setHealthPath] = useState(app.healthcheck_path)
  const [healthTimeout, setHealthTimeout] = useState(String(app.healthcheck_timeout))
  const [paused, setPaused] = useState(app.autodeploy_paused)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [hook, setHook] = useState<string | null>(null)
  const [hookErr, setHookErr] = useState<string | null>(null)
  const [integrations, setIntegrations] = useState<Integrations | null>(null)

  useEffect(() => {
    apiFetch<Integrations>(`/api/teams/${team}/integrations`)
      .then(setIntegrations)
      .catch(() => {})
  }, [team])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setSaved(false)
    try {
      const a = await apiFetch<App>(path, {
        method: "PATCH",
        body: {
          branch: branch.trim(),
          port: Number(port),
          healthcheck_path: healthPath.trim(),
          healthcheck_timeout: Number(healthTimeout),
          autodeploy_paused: paused,
        },
      })
      onSaved(a)
      setSaved(true)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function rotate() {
    setHookErr(null)
    try {
      const r = await apiFetch<{ url: string }>(`${path}/deploy-hook`, { method: "POST" })
      setHook(r.url)
    } catch (err) {
      setHookErr(errorText(err))
    }
  }

  return (
    <div className="grid gap-6 lg:grid-cols-2" data-testid="settings">
      <form onSubmit={save} className="flex flex-col gap-4 border p-4">
        <h3 className="font-medium">{t("apps.settings.title")}</h3>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="set-branch">{t("apps.branch")}</FieldLabel>
            <Input id="set-branch" dir="ltr" disabled={!manage} maxLength={200} value={branch} onChange={(e) => setBranch(e.target.value)} />
          </Field>
          <div className="grid gap-4 sm:grid-cols-3">
            <Field>
              <FieldLabel htmlFor="set-port">{t("apps.port")}</FieldLabel>
              <Input id="set-port" dir="ltr" type="number" min={1} max={65535} disabled={!manage} value={port} onChange={(e) => setPort(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="set-hpath">{t("apps.healthPath")}</FieldLabel>
              <Input id="set-hpath" dir="ltr" maxLength={200} disabled={!manage} value={healthPath} onChange={(e) => setHealthPath(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="set-htimeout">{t("apps.healthTimeout")}</FieldLabel>
              <Input id="set-htimeout" dir="ltr" type="number" min={5} max={600} disabled={!manage} value={healthTimeout} onChange={(e) => setHealthTimeout(e.target.value)} />
            </Field>
          </div>
          <Field orientation="horizontal">
            <Checkbox id="set-paused" checked={paused} disabled={!manage} onCheckedChange={(v) => setPaused(!!v)} />
            <div className="flex flex-col gap-1">
              <FieldLabel htmlFor="set-paused">{t("apps.settings.pause")}</FieldLabel>
              <FieldDescription>{t("apps.settings.pauseHint")}</FieldDescription>
            </div>
          </Field>
          <FormError message={error} />
          {saved ? (
            <p role="status" className="text-sm text-muted-foreground">
              {t("apps.settings.saved")}
            </p>
          ) : null}
          {manage ? (
            <div>
              <Button type="submit" disabled={busy}>
                {busy ? t("common.working") : t("common.save")}
              </Button>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">{t("common.readOnly")}</p>
          )}
        </FieldGroup>
      </form>

      <div className="flex flex-col gap-6">
        <section className="flex flex-col gap-3 border p-4" data-testid="deploy-hook">
          <h3 className="font-medium">{t("apps.hook.title")}</h3>
          <p className="text-sm text-muted-foreground">{t("apps.hook.hint")}</p>
          {hook ? (
            <>
              <CopyField value={hook} />
              <Alert>
                <AlertTriangleIcon />
                <AlertDescription>{t("apps.hook.once")}</AlertDescription>
              </Alert>
            </>
          ) : (
            <CopyField value={detail.deploy_hook_hint} />
          )}
          <FormError message={hookErr} />
          {manage ? (
            <div>
              <Button size="sm" variant="outline" onClick={rotate}>
                {t("apps.hook.rotate")}
              </Button>
              <p className="pt-2 text-xs text-muted-foreground">{t("apps.hook.rotateHint")}</p>
            </div>
          ) : null}
        </section>

        <section className="flex flex-col gap-3 border p-4" data-testid="integrations">
          <h3 className="font-medium">{t("apps.settings.github")}</h3>
          {integrations === null ? null : integrations.github_app.configured ? (
            <p className="text-sm">
              {t("apps.settings.githubConfigured")}{" "}
              {integrations.github_app.install_url ? (
                <a
                  href={integrations.github_app.install_url}
                  target="_blank"
                  rel="noreferrer"
                  className="underline underline-offset-4"
                >
                  {t("apps.settings.githubInstall")}
                </a>
              ) : null}
            </p>
          ) : (
            <Alert>
              <InfoIcon />
              <AlertTitle>{t("apps.settings.githubMissing")}</AlertTitle>
              <AlertDescription>{t("apps.settings.githubMissingHint")}</AlertDescription>
            </Alert>
          )}
          {integrations ? (
            <p dir="ltr" className="text-start font-mono text-xs text-muted-foreground">
              railpack {integrations.railpack_version} · caddy {integrations.caddy_version}
            </p>
          ) : null}
        </section>

        {manage ? <DeleteApp path={path} name={app.name} team={team} /> : null}
      </div>
    </div>
  )
}

function DeleteApp({ path, name, team }: { path: string; name: string; team: string }) {
  const { t, locale } = useI18n()
  const router = useRouter()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function run() {
    setBusy(true)
    setError(null)
    try {
      await apiFetch(path, { method: "DELETE", body: { confirm: typed } })
      setOpen(false)
      router.push(`/${locale}/t/${team}/apps`)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-3 border border-destructive/40 p-4">
      <h3 className="font-medium">{t("apps.settings.deleteTitle")}</h3>
      <p className="text-sm text-muted-foreground">{t("apps.settings.deleteHint")}</p>
      <Dialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o)
          setTyped("")
          setError(null)
        }}
      >
        <DialogTrigger render={<Button variant="destructive" size="sm" className="w-fit" />}>
          {t("apps.settings.delete")}
        </DialogTrigger>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("apps.settings.deleteTitle")}</DialogTitle>
            <DialogDescription>{t("apps.settings.deleteConfirm", { name })}</DialogDescription>
          </DialogHeader>
          <Input dir="ltr" value={typed} onChange={(e) => setTyped(e.target.value)} aria-label={t("apps.name")} autoComplete="off" />
          {error ? (
            <p role="alert" className="text-sm text-destructive" dir="auto">
              {error}
            </p>
          ) : null}
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" disabled={busy || typed !== name} onClick={run}>
              {busy ? t("common.working") : t("apps.settings.delete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  )
}
