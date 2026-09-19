"use client"

import { useParams, useRouter } from "next/navigation"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { DatabaseIcon, HardDriveDownloadIcon, KeyRoundIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, Notice, SectionTitle, TableBand } from "@/components/states"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
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
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import type { App } from "@/lib/apps"
import { humanBytes, type DataServiceDetail } from "@/lib/databases"
import { isImported } from "@/lib/apps"
import { ImportedDatabaseControls, ImportedDatabaseView } from "@/components/imported-view"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

import { statusVariant } from "../page"

export default function DataServicePage() {
  const { service: ref } = useParams<{ service: string }>()
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const router = useRouter()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [detail, setDetail] = useState<DataServiceDetail | null>(null)
  const [error, setError] = useState<string | null>(null)
  useDocumentTitle(`${ref} — ${team.name}`)

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<DataServiceDetail>(`/api/teams/${team.slug}/data-services/${ref}`)
      setDetail(res)
      setError(null)
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug, ref])

  useEffect(() => {
    void load()
    const id = setInterval(load, 6_000)
    return () => clearInterval(id)
  }, [load])

  if (error && !detail)
    return (
      <PageBody title={String(ref)} bleed>
        <ErrorBand message={error} />
      </PageBody>
    )
  if (!detail) {
    return (
      <PageBody title={String(ref)} bleed>
        <LoadingRows rows={4} />
      </PageBody>
    )
  }

  const s = detail.service
  // An imported database is shown read-only: Hosbah has no credentials for it and never changes it.
  // An imported database without its admin login: power/stats/logs and the credentials form. With
  // it, the full database tools below apply, with the workload controls on top.
  const imported = isImported(s)
  if (imported && !(s as { has_credentials?: boolean }).has_credentials)
    return <ImportedDatabaseView service={s} onChanged={() => void load()} />
  const isRedis = s.kind === "redis"

  const actions = manage ? (
    <ConfirmButton
      size="default"
      label={t("common.delete")}
      title={t("databases.deleteTitle", { name: s.name })}
      description={t("databases.deleteHint")}
      confirmLabel={t("common.delete")}
      icon={<Trash2Icon />}
      onConfirm={async () => {
        await apiFetch(`/api/teams/${team.slug}/data-services/${s.id}`, { method: "DELETE" })
        router.push(`/${locale}/t/${team.slug}/databases`)
      }}
    />
  ) : null

  return (
    <PageBody
      title={s.name}
      eyebrow={imported ? t("imported.eyebrowDatabase") : undefined}
      description={`${s.kind} ${s.version}`}
      actions={actions}
      bleed
    >
      {imported ? <ImportedDatabaseControls service={s} onChanged={() => void load()} /> : null}
      <div className="flex flex-col">
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-6 py-3">
          <Badge variant={statusVariant(s.status)}>{t(`databases.statuses.${s.status}`)}</Badge>
          {s.host && s.port ? (
            <span className="text-sm text-muted-foreground" dir="ltr">
              {t("databases.endpoint")}: {s.host}:{s.port}
            </span>
          ) : null}
        </div>
        {s.status === "failed" && s.error ? (
          <Notice tone="danger" title={t("databases.provisionFailed")}>
            <span dir="ltr">{s.error}</span>
          </Notice>
        ) : null}

        {!isRedis ? (
          <Section title={t("databases.databasesTitle")} action={manage ? <CreateDbDialog teamSlug={team.slug} serviceRef={s.id} onDone={load} /> : null}>
            {detail.databases.length === 0 ? (
              <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{t("databases.noDatabases")}</p>
            ) : (
              <TableBand>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t("databases.dbName")}</TableHead>
                      <TableHead>{t("databases.status")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {detail.databases.map((d) => (
                      <TableRow key={d.id}>
                        <TableCell dir="ltr">{d.name}</TableCell>
                        <TableCell>{d.status}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </TableBand>
            )}
            {detail.users.length > 0 ? (
              <TableBand>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t("databases.user")}</TableHead>
                      <TableHead>{t("databases.grants")}</TableHead>
                      <TableHead>{t("databases.status")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {detail.users.map((u) => (
                      <TableRow key={u.id}>
                        <TableCell dir="ltr">{u.name}</TableCell>
                        <TableCell>{u.grants}</TableCell>
                        <TableCell>{u.status}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </TableBand>
            ) : null}
          </Section>
        ) : null}

        {manage ? (
          <Section title={t("databases.connectionTitle")}>
            <div className="border-b border-line px-6 py-5">
              <div className="max-w-2xl">
                <ConnectionPanel teamSlug={team.slug} serviceRef={s.id} canAttach={isRedis || detail.users.length > 0} />
              </div>
            </div>
          </Section>
        ) : null}

        <Section
          title={t("databases.backupsTitle")}
          action={
            manage && s.status === "running" ? (
              <Button
                size="sm"
                onClick={async () => {
                  await apiFetch(`/api/teams/${team.slug}/data-services/${s.id}/backups`, { method: "POST" })
                  await load()
                }}
              >
                <HardDriveDownloadIcon />
                {t("databases.backupNow")}
              </Button>
            ) : null
          }
        >
          {detail.backups.length === 0 ? (
            <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{t("databases.noBackups")}</p>
          ) : (
            <TableBand>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("databases.backupKind")}</TableHead>
                    <TableHead>{t("databases.status")}</TableHead>
                    <TableHead>{t("databases.size")}</TableHead>
                    <TableHead>{t("databases.restoreTest")}</TableHead>
                    <TableHead>{t("databases.taken")}</TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {detail.backups.map((b) => (
                    <TableRow key={b.id}>
                      <TableCell>{t(`databases.backupKinds.${b.kind}`)}</TableCell>
                      <TableCell>
                        <Badge variant={b.status === "succeeded" ? "default" : b.status === "failed" ? "destructive" : "secondary"}>{b.status}</Badge>
                      </TableCell>
                      <TableCell dir="ltr">{humanBytes(b.size_bytes)}</TableCell>
                      <TableCell>
                        {b.restore_status && b.restore_status !== "untested" ? (
                          <Badge variant={b.restore_status === "pass" ? "default" : b.restore_status === "fail" ? "destructive" : "secondary"}>
                            {t(`databases.restoreStatuses.${b.restore_status}`)}
                          </Badge>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </TableCell>
                      <TableCell className="text-muted-foreground">{b.taken_at ? fmt(b.taken_at) : "—"}</TableCell>
                      <TableCell className="text-end">
                        {manage && b.status === "succeeded" ? (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={b.restore_status === "testing"}
                            onClick={async () => {
                              await apiFetch(`/api/teams/${team.slug}/data-services/${s.id}/backups/${b.id}/restore-test`, { method: "POST" })
                              await load()
                            }}
                          >
                            {t("databases.runRestoreTest")}
                          </Button>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableBand>
          )}
        </Section>

        {manage ? (
          <Section title={t("databases.scheduleTitle")}>
            <div className="border-b border-line px-6 py-5">
              <div className="max-w-2xl">
                <ScheduleForm teamSlug={team.slug} serviceRef={s.id} schedule={detail.schedule} remoteAvailable={detail.remote_available} onDone={load} />
              </div>
            </div>
          </Section>
        ) : null}
      </div>
    </PageBody>
  )
}

function Section({ title, action, children }: { title: string; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section>
      <SectionTitle
        title={
          <span className="flex items-center gap-2">
            <DatabaseIcon className="size-4 text-grid-muted" aria-hidden />
            {title}
          </span>
        }
        action={action}
      />
      {children}
    </section>
  )
}

function CreateDbDialog({ teamSlug, serviceRef, onDone }: { teamSlug: string; serviceRef: string; onDone: () => void | Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [user, setUser] = useState("")
  const [grants, setGrants] = useState("all")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!name) return
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`/api/teams/${teamSlug}/data-services/${serviceRef}/databases`, {
        method: "POST",
        body: { name, user: user || undefined, grants },
      })
      setOpen(false)
      setName("")
      setUser("")
      await onDone()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button size="sm" variant="outline" />}>
        <PlusIcon />
        {t("databases.addDatabase")}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{t("databases.addDatabase")}</DialogTitle>
            <DialogDescription>{t("databases.addDatabaseHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup className="py-4">
            <Field>
              <FieldLabel htmlFor="new-db">{t("databases.dbName")}</FieldLabel>
              <Input id="new-db" value={name} onChange={(e) => setName(e.target.value)} placeholder="app" dir="ltr" autoFocus />
            </Field>
            <Field>
              <FieldLabel htmlFor="new-user">{t("databases.user")}</FieldLabel>
              <Input id="new-user" value={user} onChange={(e) => setUser(e.target.value)} placeholder={t("databases.userOptional")} dir="ltr" />
            </Field>
            <Field>
              <FieldLabel>{t("databases.grants")}</FieldLabel>
              <Select value={grants} onValueChange={(v) => v && setGrants(v)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {["all", "readwrite", "readonly"].map((g) => (
                    <SelectItem key={g} value={g}>
                      {g}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <FormError message={error} />
          </FieldGroup>
          <DialogFooter>
            <Button type="submit" disabled={busy || !name}>
              {busy ? t("common.working") : t("databases.createDb")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function ConnectionPanel({ teamSlug, serviceRef, canAttach }: { teamSlug: string; serviceRef: string; canAttach: boolean }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [url, setUrl] = useState<string | null>(null)
  const [apps, setApps] = useState<App[]>([])
  const [app, setApp] = useState("")
  const [attached, setAttached] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    apiFetch<{ apps: App[] }>(`/api/teams/${teamSlug}/apps`)
      .then((r) => setApps(r.apps ?? []))
      .catch(() => setApps([]))
  }, [teamSlug])

  const reveal = async () => {
    setError(null)
    try {
      const res = await apiFetch<{ url: string }>(`/api/teams/${teamSlug}/data-services/${serviceRef}/reveal-url`, { method: "POST", body: {} })
      setUrl(res.url)
    } catch (err) {
      setError(errorText(err))
    }
  }

  const attach = async () => {
    if (!app) return
    setError(null)
    try {
      const res = await apiFetch<{ variable: string }>(`/api/teams/${teamSlug}/data-services/${serviceRef}/attach`, { method: "POST", body: { app } })
      setAttached(res.variable)
    } catch (err) {
      setError(errorText(err))
    }
  }

  if (!canAttach) {
    return <p className="text-sm text-muted-foreground">{t("databases.createDbFirst")}</p>
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-2">
        <p className="text-sm text-muted-foreground">{t("databases.revealHint")}</p>
        {url ? (
          <CopyField value={url} />
        ) : (
          <div>
            <Button size="sm" variant="outline" onClick={reveal}>
              <KeyRoundIcon />
              {t("databases.reveal")}
            </Button>
          </div>
        )}
      </div>
      <div className="flex flex-col gap-2 border-t border-line pt-4">
        <FieldLabel>{t("databases.attachTitle")}</FieldLabel>
        <p className="text-sm text-muted-foreground">{t("databases.attachHint")}</p>
        <div className="flex flex-wrap items-center gap-2">
          <div className="min-w-56">
            <Select value={app} onValueChange={(v) => v && setApp(v)}>
              <SelectTrigger>
                <SelectValue placeholder={t("databases.chooseApp")} />
              </SelectTrigger>
              <SelectContent>
                {apps.map((a) => (
                  <SelectItem key={a.id} value={a.id}>
                    {a.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <Button size="sm" onClick={attach} disabled={!app}>
            {t("databases.attach")}
          </Button>
        </div>
        {attached ? (
          <Alert>
            <AlertDescription>
              {t("databases.attachedDone", { variable: attached })}
            </AlertDescription>
          </Alert>
        ) : null}
      </div>
      <FormError message={error} />
    </div>
  )
}

function ScheduleForm({
  teamSlug,
  serviceRef,
  schedule,
  remoteAvailable,
  onDone,
}: {
  teamSlug: string
  serviceRef: string
  schedule: DataServiceDetail["schedule"]
  remoteAvailable: boolean
  onDone: () => void | Promise<void>
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [cron, setCron] = useState(schedule?.cron ?? "0 3 * * *")
  const [retention, setRetention] = useState(schedule?.retention ?? 7)
  const [target, setTarget] = useState(schedule?.target ?? "local")
  const [restoreTest, setRestoreTest] = useState(schedule?.restore_test ?? true)
  const [paused, setPaused] = useState(schedule?.paused ?? false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setSaved(false)
    try {
      await apiFetch(`/api/teams/${teamSlug}/data-services/${serviceRef}/schedule`, {
        method: "PUT",
        body: { cron, retention, target, restore_test: restoreTest, paused },
      })
      setSaved(true)
      await onDone()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor="cron">{t("databases.cron")}</FieldLabel>
          <Input id="cron" value={cron} onChange={(e) => setCron(e.target.value)} dir="ltr" placeholder="0 3 * * *" />
        </Field>
        <Field>
          <FieldLabel htmlFor="retention">{t("databases.retention")}</FieldLabel>
          <Input id="retention" type="number" min={1} max={3650} value={retention} onChange={(e) => setRetention(Number(e.target.value))} dir="ltr" />
        </Field>
        <Field>
          <FieldLabel>{t("databases.target")}</FieldLabel>
          <Select value={target} onValueChange={(v) => v && setTarget(v)}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="local">{t("databases.targetLocal")}</SelectItem>
              <SelectItem value="s3" disabled={!remoteAvailable}>
                {remoteAvailable ? "S3" : t("databases.s3NotConfigured")}
              </SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </div>
      <label className="flex items-center gap-2 text-sm">
        <Checkbox checked={restoreTest} onCheckedChange={(v) => setRestoreTest(Boolean(v))} />
        {t("databases.restoreTestEnabled")}
      </label>
      <label className="flex items-center gap-2 text-sm">
        <Checkbox checked={paused} onCheckedChange={(v) => setPaused(Boolean(v))} />
        {t("databases.paused")}
      </label>
      {schedule?.next_run_at ? (
        <p className="text-sm text-muted-foreground">{t("databases.nextRun", { time: schedule.next_run_at })}</p>
      ) : null}
      <FormError message={error} />
      <div className="flex items-center gap-2">
        <Button type="submit" disabled={busy}>
          {busy ? t("common.working") : t("databases.saveSchedule")}
        </Button>
        {saved ? <span className="text-sm text-primary">{t("common.done")}</span> : null}
      </div>
    </form>
  )
}
