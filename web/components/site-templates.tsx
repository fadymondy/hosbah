"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { CheckCircle2Icon, EyeIcon, LayoutTemplateIcon, PencilIcon, PlusIcon, RocketIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { ErrorBand, LoadingRows, Notice, SectionTitle } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { apiFetch } from "@/lib/api"
import type { App, Deployment } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import type { Rendered, ServiceRefs, SiteTemplate } from "@/lib/site-templates"
import { useTeam } from "@/lib/team"

const BUILTIN_YAML = `# name: my-template
# description: a short description
# driver: laravel | nextjs | node-api | static-site | django | wordpress-docker
# repo: https://github.com/you/repo
# branch: main
# port: 8080
# health_path: /
# env:
#   APP_KEY: "{{random:32}}"
#   DATABASE_URL: "{{db.url}}"
# needs:
#   postgres: true
`

/**
 * MH-576 B11: YAML site templates. A gallery of Hosbah's built-in stack templates plus whatever
 * the team has saved, a YAML editor with the API's own validation errors surfaced inline, a
 * preview of what a template renders to, and a one-step "create app from template" flow that
 * reuses the same app-creation and env-setting code paths as the manual new-app page.
 */
export function TeamSiteTemplates() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  const errorText = useErrorText()
  const base = `/api/teams/${team.slug}/site-templates`

  const [templates, setTemplates] = useState<SiteTemplate[] | null>(null)
  const [servers, setServers] = useState<Server[] | null>(null)
  const [services, setServices] = useState<DataService[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const [preview, setPreview] = useState<SiteTemplate | null>(null)
  const [createFrom, setCreateFrom] = useState<SiteTemplate | null>(null)

  const load = useCallback(async () => {
    try {
      const [tpl, sv, ds] = await Promise.all([
        apiFetch<{ templates: SiteTemplate[] }>(base),
        apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`),
        apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`),
      ])
      setTemplates(tpl.templates ?? [])
      setServers((sv.servers ?? []).filter((s) => s.status === "enrolled"))
      setServices(ds.services ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setTemplates((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, team.slug])

  useEffect(() => {
    void load()
  }, [load])

  const shown = (templates ?? []).filter((s) => matches(q, s.name, s.description, s.driver))

  return (
    <>
      <section aria-labelledby="templates-h">
        <SectionTitle
          id="templates-h"
          title={t("siteTemplates.title")}
          hint={t("siteTemplates.hint")}
          action={manage ? <TemplateDialog path={base} onSaved={load} /> : null}
        />
        <ErrorBand message={error} />
        {templates === null ? (
          <LoadingRows rows={3} />
        ) : templates.length === 0 ? (
          <Empty className="border-b">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <LayoutTemplateIcon />
              </EmptyMedia>
              <EmptyTitle>{t("siteTemplates.emptyTitle")}</EmptyTitle>
              <EmptyDescription>{t("siteTemplates.emptyHint")}</EmptyDescription>
            </EmptyHeader>
            {manage ? <TemplateDialog path={base} onSaved={load} /> : null}
          </Empty>
        ) : (
          <>
            <ListSearch value={q} onChange={setQ} placeholder={t("siteTemplates.search")} shown={shown.length} total={templates.length} />
            <div className="grid gap-px border-b border-line bg-line sm:grid-cols-2 xl:grid-cols-3">
              {shown.map((tpl) => (
                <div key={tpl.id} className="flex flex-col gap-3 bg-grid-bg p-4">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <p dir="auto" className="font-medium text-grid-fg">
                        {tpl.name}
                      </p>
                      {tpl.description ? (
                        <p dir="auto" className="text-xs text-grid-muted">
                          {tpl.description}
                        </p>
                      ) : null}
                    </div>
                    {tpl.builtin ? <Badge variant="outline">{t("siteTemplates.builtin")}</Badge> : null}
                  </div>
                  <div>
                    <Badge variant="secondary">{tpl.driver}</Badge>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <Button size="sm" variant="outline" onClick={() => setPreview(tpl)}>
                      <EyeIcon />
                      {t("siteTemplates.preview")}
                    </Button>
                    {manage ? (
                      <Button size="sm" onClick={() => setCreateFrom(tpl)}>
                        <RocketIcon />
                        {t("siteTemplates.createApp")}
                      </Button>
                    ) : null}
                    {manage && !tpl.builtin ? <TemplateDialog path={base} template={tpl} onSaved={load} /> : null}
                    {manage && !tpl.builtin ? <DeleteTemplate path={base} template={tpl} onDeleted={load} /> : null}
                  </div>
                </div>
              ))}
            </div>
          </>
        )}
      </section>

      {preview ? (
        <PreviewDialog base={base} template={preview} services={services ?? []} onClose={() => setPreview(null)} />
      ) : null}
      {createFrom ? (
        <CreateFromTemplateDialog
          base={base}
          template={createFrom}
          servers={servers ?? []}
          services={services ?? []}
          onClose={() => setCreateFrom(null)}
        />
      ) : null}
    </>
  )
}

/** Delete a team's own template. Built-ins can never be deleted (the API 404s). */
function DeleteTemplate({ path, template, onDeleted }: { path: string; template: SiteTemplate; onDeleted: () => Promise<void> | void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      icon={<Trash2Icon />}
      label={t("common.delete")}
      title={t("siteTemplates.deleteTitle", { name: template.name })}
      description={t("siteTemplates.deleteBody")}
      confirmLabel={t("common.delete")}
      onConfirm={async () => {
        try {
          await apiFetch(`${path}/${template.id}`, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onDeleted()
      }}
    />
  )
}

/** Create or edit a team template — a raw YAML document, validated by the API itself (unknown
 * keys, size limit, driver name, all rejected server-side; errors come back as one 422 message). */
function TemplateDialog({
  path,
  template,
  onSaved,
  ...controlled
}: { path: string; template?: SiteTemplate; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [source, setSource] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setSource(template?.source_yaml ?? BUILTIN_YAML)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(template ? `${path}/${template.id}` : path, {
        method: template ? "PUT" : "POST",
        body: { source_yaml: source },
      })
      setOpen(false)
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {fromCaller ? null : (
        <DialogTrigger render={<Button size="sm" variant={template ? "outline" : "default"} />}>
          {template ? <PencilIcon /> : <PlusIcon />}
          {template ? t("lists.edit") : t("siteTemplates.add")}
        </DialogTrigger>
      )}
      <DialogContent className="max-h-[90dvh] max-w-2xl overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{template ? template.name : t("siteTemplates.add")}</DialogTitle>
            <DialogDescription>{t("siteTemplates.dialogHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="tpl-yaml">{t("siteTemplates.yaml")}</FieldLabel>
              <Textarea
                id="tpl-yaml"
                dir="ltr"
                required
                rows={18}
                value={source}
                onChange={(e) => setSource(e.target.value)}
                className="min-h-80 font-mono text-xs"
              />
              <FieldDescription>{t("siteTemplates.yamlHint")}</FieldDescription>
            </Field>
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !source.trim()}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** A team's chosen refs to already-provisioned data services, one per kind a template needs. A
 * template never provisions a service itself — it only wires up what already exists and is
 * running. */
function ServiceRefFields({
  services,
  refs,
  onChange,
}: {
  services: DataService[]
  refs: ServiceRefs
  onChange: (r: ServiceRefs) => void
}) {
  const { t } = useI18n()
  const kinds: { key: keyof ServiceRefs; kind: DataService["kind"] }[] = [
    { key: "postgres_service", kind: "postgres" },
    { key: "mysql_service", kind: "mysql" },
    { key: "redis_service", kind: "redis" },
  ]
  // A template only says WHICH kinds it needs (postgres/mysql/redis); it never says which of the
  // team's already-provisioned services to use, so always offer all three refs here — any ref the
  // template's driver doesn't actually need is simply ignored by the API.
  return (
    <div className="grid gap-4 sm:grid-cols-3">
      {kinds.map(({ key, kind }) => {
        const options = services.filter((s) => s.kind === kind && s.status === "running")
        return (
          <Field key={key}>
            <FieldLabel htmlFor={`svc-${key}`}>{t(`siteTemplates.services.${kind}`)}</FieldLabel>
            <Select value={refs[key] ?? ""} onValueChange={(v) => onChange({ ...refs, [key]: v as string })}>
              <SelectTrigger id={`svc-${key}`}>
                <SelectValue placeholder={t("siteTemplates.services.none")}>{(v: string) => v || t("siteTemplates.services.none")}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="">{t("siteTemplates.services.none")}</SelectItem>
                {options.map((s) => (
                  <SelectItem key={s.id} value={s.id}>
                    {s.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        )
      })}
    </div>
  )
}

/** Preview what a template will render to — driver, repo, branch, port, health path and env var
 * names (secret values stay hidden here; the API only reveals them once an app is actually
 * created from the same call that generates them). */
function PreviewDialog({
  base,
  template,
  services,
  onClose,
}: {
  base: string
  template: SiteTemplate
  services: DataService[]
  onClose: () => void
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [refs, setRefs] = useState<ServiceRefs>({})
  const [rendered, setRendered] = useState<Rendered | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const run = useCallback(async () => {
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<Rendered>(`${base}/${template.id}/preview`, { method: "POST", body: refs })
      setRendered(r)
    } catch (err) {
      setError(errorText(err))
      setRendered(null)
    } finally {
      setBusy(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, template.id, refs])

  useEffect(() => {
    void run()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90dvh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("siteTemplates.previewTitle", { name: template.name })}</DialogTitle>
          <DialogDescription>{t("siteTemplates.previewHint")}</DialogDescription>
        </DialogHeader>
        <ServiceRefFields services={services} refs={refs} onChange={(r) => { setRefs(r); void run() }} />
        <ErrorBand message={error} />
        {busy && !rendered ? (
          <LoadingRows rows={3} />
        ) : rendered ? (
          <div className="flex flex-col gap-3 text-sm">
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
              <dt className="text-grid-muted">{t("siteTemplates.driver")}</dt>
              <dd dir="ltr">{rendered.driver.label}</dd>
              <dt className="text-grid-muted">{t("apps.repo")}</dt>
              <dd dir="ltr" className="truncate">{rendered.repo_url}</dd>
              <dt className="text-grid-muted">{t("apps.branch")}</dt>
              <dd dir="ltr">{rendered.branch}</dd>
              <dt className="text-grid-muted">{t("apps.port")}</dt>
              <dd dir="ltr">{rendered.port}</dd>
              <dt className="text-grid-muted">{t("apps.healthPath")}</dt>
              <dd dir="ltr">{rendered.health_path}</dd>
            </dl>
            <div>
              <p className="mb-1 font-medium">{t("siteTemplates.env")}</p>
              <ul className="flex flex-col gap-1 font-mono text-xs" dir="ltr">
                {rendered.env.map((e) => (
                  <li key={e.name} className="flex items-center gap-2">
                    <span>{e.name}</span>
                    {e.secret ? (
                      <Badge variant="outline">{t("siteTemplates.secret")}</Badge>
                    ) : (
                      <span className="truncate text-grid-muted">{e.value}</span>
                    )}
                  </li>
                ))}
              </ul>
            </div>
            {rendered.notes ? <Notice icon={<CheckCircle2Icon />} title={t("siteTemplates.notes")}>{rendered.notes}</Notice> : null}
          </div>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("common.close")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** Apply a template: pick a name, target server, optional branch override and service refs, then
 * create the app (and set its env) in one call — the same CreateApp/SetEnv path the manual
 * new-app page uses. */
function CreateFromTemplateDialog({
  base,
  template,
  servers,
  services,
  onClose,
}: {
  base: string
  template: SiteTemplate
  servers: Server[]
  services: DataService[]
  onClose: () => void
}) {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const [name, setName] = useState("")
  const [server, setServer] = useState(servers[0]?.id ?? "")
  const [branch, setBranch] = useState("")
  const [refs, setRefs] = useState<ServiceRefs>({})
  const [deploy, setDeploy] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [created, setCreated] = useState<{ app: App; deploy_hook_url: string; deployment?: Deployment } | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const body: Record<string, unknown> = { name: name.trim(), server, deploy, ...refs }
      if (branch.trim()) body.branch = branch.trim()
      const r = await apiFetch<{ app: App; deploy_hook_url: string; deployment?: Deployment }>(`${base}/${template.id}/apply`, {
        method: "POST",
        body,
      })
      setCreated(r)
      toast.success(t("siteTemplates.created"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  if (created) {
    return (
      <Dialog open onOpenChange={(v) => !v && onClose()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("siteTemplates.created")}</DialogTitle>
            <DialogDescription>{t("siteTemplates.createdHint", { name: created.app.name })}</DialogDescription>
          </DialogHeader>
          <CopyField value={created.deploy_hook_url} />
          <DialogFooter>
            <Button render={<a href={`/${locale}/t/${team.slug}/apps/${created.app.id}`} />}>{t("siteTemplates.viewApp")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    )
  }

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("siteTemplates.createFromTitle", { name: template.name })}</DialogTitle>
            <DialogDescription>{t("siteTemplates.createFromHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="tplapp-name">{t("apps.name")}</FieldLabel>
              <Input id="tplapp-name" dir="ltr" required maxLength={40} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="tplapp-server">{t("apps.server")}</FieldLabel>
              {servers.length === 0 ? (
                <p className="text-sm text-grid-muted">{t("siteTemplates.noServers")}</p>
              ) : (
                <Select value={server} onValueChange={(v) => setServer((v as string) ?? "")}>
                  <SelectTrigger id="tplapp-server">
                    <SelectValue>{(v: string) => servers.find((s) => s.id === v)?.name ?? v}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {servers.map((s) => (
                      <SelectItem key={s.id} value={s.id}>
                        {s.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </Field>
            <Field>
              <FieldLabel htmlFor="tplapp-branch">{t("apps.branch")}</FieldLabel>
              <Input id="tplapp-branch" dir="ltr" placeholder="main" maxLength={200} value={branch} onChange={(e) => setBranch(e.target.value)} />
            </Field>
            <ServiceRefFields services={services} refs={refs} onChange={setRefs} />
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={deploy} onCheckedChange={(v) => setDeploy(!!v)} />
              {t("siteTemplates.deployNow")}
            </label>
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !name.trim() || servers.length === 0}>
              {busy ? t("common.working") : t("siteTemplates.createApp")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
