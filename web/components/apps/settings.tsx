"use client"

import { useRouter } from "next/navigation"
import { useEffect, useState, type FormEvent } from "react"
import { AlertTriangleIcon, InfoIcon } from "lucide-react"

import { CopyField } from "@/components/copy-field"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, Notice, SectionTitle, SettingsRow } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
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
  const [branch, setBranch] = useState(app.branch ?? "")
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
    <div data-testid="settings">
      <form onSubmit={save}>
        <SectionTitle title={t("apps.settings.title")} hint={!manage ? t("common.readOnly") : undefined} />
        <SettingsRow label={t("apps.branch")} htmlFor="set-branch">
          <Input id="set-branch" dir="ltr" disabled={!manage} maxLength={200} value={branch} onChange={(e) => setBranch(e.target.value)} />
        </SettingsRow>
        <SettingsRow label={t("apps.port")} htmlFor="set-port">
          <Input
            id="set-port"
            dir="ltr"
            type="number"
            min={1}
            max={65535}
            disabled={!manage}
            value={port}
            onChange={(e) => setPort(e.target.value)}
          />
        </SettingsRow>
        <SettingsRow label={t("apps.healthPath")} htmlFor="set-hpath">
          <Input
            id="set-hpath"
            dir="ltr"
            maxLength={200}
            disabled={!manage}
            value={healthPath}
            onChange={(e) => setHealthPath(e.target.value)}
          />
        </SettingsRow>
        <SettingsRow label={t("apps.healthTimeout")} htmlFor="set-htimeout">
          <Input
            id="set-htimeout"
            dir="ltr"
            type="number"
            min={5}
            max={600}
            disabled={!manage}
            value={healthTimeout}
            onChange={(e) => setHealthTimeout(e.target.value)}
          />
        </SettingsRow>
        <SettingsRow label={t("apps.settings.pause")} htmlFor="set-paused" hint={t("apps.settings.pauseHint")}>
          <Checkbox id="set-paused" checked={paused} disabled={!manage} onCheckedChange={(v) => setPaused(!!v)} />
        </SettingsRow>
        <ErrorBand message={error} />
        {manage ? (
          <div className="flex flex-wrap items-center justify-end gap-3 border-b border-line px-6 py-4">
            {saved ? (
              <p role="status" className="text-sm text-grid-muted">
                {t("apps.settings.saved")}
              </p>
            ) : null}
            <Button type="submit" disabled={busy}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </div>
        ) : null}
      </form>

      <section data-testid="deploy-hook">
        <SectionTitle title={t("apps.hook.title")} hint={t("apps.hook.hint")} />
        <SettingsRow label={t("apps.hook.title")} hint={manage ? t("apps.hook.rotateHint") : undefined}>
          {hook ? (
            <>
              <CopyField value={hook} />
              <p className="flex items-start gap-2 text-xs text-grid-body">
                <AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0 text-grid-brand" />
                {t("apps.hook.once")}
              </p>
            </>
          ) : (
            <CopyField value={detail.deploy_hook_hint} />
          )}
          {manage ? (
            <div>
              <Button variant="outline" onClick={rotate}>
                {t("apps.hook.rotate")}
              </Button>
            </div>
          ) : null}
        </SettingsRow>
        <ErrorBand message={hookErr} />
      </section>

      <section data-testid="integrations">
        <SectionTitle title={t("apps.settings.github")} />
        {integrations === null ? null : integrations.github_app.configured ? (
          <p className="border-b border-line px-6 py-4 text-sm">
            {t("apps.settings.githubConfigured")}{" "}
            {integrations.github_app.install_url ? (
              <a href={integrations.github_app.install_url} target="_blank" rel="noreferrer" className="underline underline-offset-4">
                {t("apps.settings.githubInstall")}
              </a>
            ) : null}
          </p>
        ) : (
          <Notice icon={<InfoIcon />} title={t("apps.settings.githubMissing")}>
            {t("apps.settings.githubMissingHint")}
          </Notice>
        )}
        {integrations ? (
          <p dir="ltr" className="border-b border-line px-6 py-3 text-start font-mono text-xs text-grid-muted">
            railpack {integrations.railpack_version} · caddy {integrations.caddy_version}
          </p>
        ) : null}
      </section>

      {manage ? <DeleteApp path={path} name={app.name} team={team} /> : null}
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
    <section className="border-s-2 border-s-grid-danger">
      <SettingsRow label={t("apps.settings.deleteTitle")} hint={t("apps.settings.deleteHint")}>
        <Dialog
          open={open}
          onOpenChange={(o) => {
            setOpen(o)
            setTyped("")
            setError(null)
          }}
        >
          <DialogTrigger render={<Button variant="destructive" className="w-fit" />}>{t("apps.settings.delete")}</DialogTrigger>
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
      </SettingsRow>
    </section>
  )
}
