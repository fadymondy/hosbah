"use client"

import Link from "next/link"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { ArchiveIcon, HardDriveIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { FormError, useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { isImported } from "@/lib/apps"
import { humanBytes, type DataService } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type Volume = { id: string; name: string; mount_path: string; volume: string; mounted: boolean; bytes: number | null }
type Bucket = { id: string; service: string; bucket: string; access_key: string; env_prefix: string; env: string[]; status: "creating" | "ready" | "failed" | "removing"; error: string | null }

/**
 * Storage: what the site keeps outside its releases. Volumes are mounted into every release and
 * worker from the next deploy; buckets live on a MinIO service on the same server, each with a
 * key limited to that one bucket, injected as sealed S3_* variables.
 */
export function SiteStorage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const errorText = useErrorText()
  const { app, path } = useLoadedSite()
  const [volumes, setVolumes] = useState<Volume[] | null>(null)
  const [buckets, setBuckets] = useState<Bucket[]>([])
  const [minio, setMinio] = useState<DataService[]>([])
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ volumes: Volume[]; buckets: Bucket[] }>(`${path}/storage`)
      setVolumes(r.volumes ?? [])
      setBuckets(r.buckets ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setVolumes((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
    apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`)
      .then((r) => setMinio((r.services ?? []).filter((s) => s.server_id === app.server_id && s.kind === "minio" && !isImported(s))))
      .catch(() => setMinio([]))
  }, [load, team.slug, app.server_id])

  // A bucket is made by the server; look again while one is on its way.
  useEffect(() => {
    if (!buckets.some((b) => b.status === "creating" || b.status === "removing")) return
    const id = setInterval(load, 4000)
    return () => clearInterval(id)
  }, [buckets, load])

  return (
    <>
      <section aria-labelledby="vol-h">
        <SectionTitle
          id="vol-h"
          title={t("site.storage.volumes")}
          hint={t("site.storage.volumesHint")}
          action={manage ? <AddVolume path={path} onSaved={load} /> : null}
        />
        <ErrorBand message={error} />
        {volumes === null ? (
          <LoadingRows rows={2} />
        ) : volumes.length === 0 ? (
          <p className="flex items-center gap-2 border-b border-line px-6 py-6 text-sm text-grid-muted">
            <HardDriveIcon className="size-4" />
            {t("site.storage.noVolumes")}
          </p>
        ) : (
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("site.monitors.name")}</TableHead>
                  <TableHead>{t("site.storage.mountPath")}</TableHead>
                  <TableHead>{t("lists.status")}</TableHead>
                  <TableHead>{t("site.storage.size")}</TableHead>
                  {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {volumes.map((v) => (
                  <TableRow key={v.id}>
                    <TableCell dir="ltr" className="text-start font-medium">
                      {v.name}
                    </TableCell>
                    <TableCell dir="ltr" className="text-start font-mono text-xs">
                      {v.mount_path}
                    </TableCell>
                    <TableCell>
                      <Badge variant={v.mounted ? "default" : "outline"}>{v.mounted ? t("site.storage.mounted") : t("site.storage.nextDeploy")}</Badge>
                    </TableCell>
                    <TableCell dir="ltr" className="text-start text-xs tabular-nums">
                      {v.bytes == null ? "—" : `≈ ${humanBytes(v.bytes)}`}
                    </TableCell>
                    {manage ? (
                      <TableCell>
                        <div className="flex justify-end">
                          <RemoveDialog
                            name={v.name}
                            warn={v.mounted ? t("site.storage.volumeMounted") : t("site.storage.volumeDataHint")}
                            allowData={!v.mounted}
                            onRemove={async (deleteData) => {
                              await apiFetch(`${path}/storage/volumes/${v.id}`, { method: "DELETE", body: { confirm: v.name, delete_data: deleteData } })
                              await load()
                            }}
                          />
                        </div>
                      </TableCell>
                    ) : null}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableBand>
        )}
      </section>

      <section aria-labelledby="bkt-h">
        <SectionTitle
          id="bkt-h"
          title={t("site.storage.buckets")}
          hint={t("site.storage.bucketsHint")}
          action={manage && minio.length ? <AddBucket path={path} services={minio} onSaved={load} /> : null}
        />
        {minio.length === 0 && buckets.length === 0 ? (
          <p className="flex flex-wrap items-center gap-2 border-b border-line px-6 py-6 text-sm text-grid-muted">
            <ArchiveIcon className="size-4" />
            {t("site.storage.noMinio")}
            <Link href={`/${locale}/t/${team.slug}/servers/${app.server_id}/databases`} className="text-grid-brand underline-offset-4 hover:underline">
              {t("nav.databases")}
            </Link>
          </p>
        ) : buckets.length === 0 ? (
          <p className="border-b border-line px-6 py-6 text-sm text-grid-muted">{t("site.storage.noBuckets")}</p>
        ) : (
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("site.storage.bucket")}</TableHead>
                  <TableHead>{t("site.storage.service")}</TableHead>
                  <TableHead>{t("lists.status")}</TableHead>
                  <TableHead>{t("site.storage.variables")}</TableHead>
                  {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {buckets.map((b) => (
                  <TableRow key={b.id}>
                    <TableCell dir="ltr" className="text-start font-medium">
                      {b.bucket}
                    </TableCell>
                    <TableCell dir="ltr" className="text-start">
                      {b.service}
                    </TableCell>
                    <TableCell>
                      <Badge variant={b.status === "ready" ? "default" : b.status === "failed" ? "destructive" : "secondary"}>{t(`site.storage.status.${b.status}`)}</Badge>
                      {b.error ? (
                        <p dir="auto" className="mt-1 max-w-xs text-xs text-grid-danger">
                          {b.error}
                        </p>
                      ) : null}
                    </TableCell>
                    <TableCell dir="ltr" className="max-w-64 text-start font-mono text-[11px] text-grid-muted">
                      {b.env.join(" ")}
                    </TableCell>
                    {manage ? (
                      <TableCell>
                        <div className="flex justify-end">
                          <RemoveDialog
                            name={b.bucket}
                            warn={t("site.storage.bucketRemoveHint")}
                            allowData
                            allowForce
                            onRemove={async (deleteData, force) => {
                              await apiFetch(`${path}/storage/buckets/${b.id}`, { method: "DELETE", body: { confirm: b.bucket, delete_data: deleteData, force: deleteData && force } })
                              await load()
                            }}
                          />
                        </div>
                      </TableCell>
                    ) : null}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableBand>
        )}
        <p className="border-b border-line px-6 py-3 text-xs text-grid-muted">{t("site.storage.noBrowser")}</p>
      </section>
    </>
  )
}

function AddVolume({ path, onSaved }: { path: string; onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [mount, setMount] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/storage/volumes`, { method: "POST", body: { name: name.trim(), mount_path: mount.trim() } })
      toast.success(t("site.storage.appliesNext"))
      setOpen(false)
      setName("")
      setMount("")
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button size="sm" />}>
        <PlusIcon />
        {t("site.storage.addVolume")}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("site.storage.addVolume")}</DialogTitle>
            <DialogDescription>{t("site.storage.appliesNext")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="vol-name">{t("site.monitors.name")}</FieldLabel>
              <Input id="vol-name" dir="ltr" required pattern="[a-z][a-z0-9-]{0,30}" placeholder="uploads" value={name} onChange={(e) => setName(e.target.value)} />
              <FieldDescription>{t("site.storage.nameHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="vol-path">{t("site.storage.mountPath")}</FieldLabel>
              <Input id="vol-path" dir="ltr" required placeholder="/app/storage" value={mount} onChange={(e) => setMount(e.target.value)} className="font-mono" />
              <FieldDescription>{t("site.storage.pathHint")}</FieldDescription>
            </Field>
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function AddBucket({ path, services, onSaved }: { path: string; services: DataService[]; onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [service, setService] = useState(services[0]?.name ?? "")
  const [bucket, setBucket] = useState("")
  const [prefix, setPrefix] = useState("S3")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/storage/buckets`, { method: "POST", body: { service, bucket: bucket.trim(), env_prefix: prefix.trim() || "S3" } })
      toast.success(t("site.storage.appliesNext"))
      setOpen(false)
      setBucket("")
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button size="sm" />}>
        <PlusIcon />
        {t("site.storage.addBucket")}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("site.storage.addBucket")}</DialogTitle>
            <DialogDescription>{t("site.storage.bucketDialogHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel>{t("site.storage.service")}</FieldLabel>
              <Select value={service} onValueChange={(v) => v && setService(v as string)}>
                <SelectTrigger aria-label={t("site.storage.service")}>
                  <SelectValue>{(v: string) => v}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {services.map((s) => (
                    <SelectItem key={s.id} value={s.name}>
                      {s.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="bkt-name">{t("site.storage.bucket")}</FieldLabel>
              <Input id="bkt-name" dir="ltr" required minLength={3} maxLength={63} pattern="[a-z0-9][a-z0-9-]*[a-z0-9]" placeholder="my-site-uploads" value={bucket} onChange={(e) => setBucket(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="bkt-prefix">{t("site.storage.prefix")}</FieldLabel>
              <Input id="bkt-prefix" dir="ltr" pattern="[A-Z][A-Z0-9_]{0,20}" value={prefix} onChange={(e) => setPrefix(e.target.value.toUpperCase())} className="font-mono" />
              <FieldDescription dir="ltr">{`${prefix || "S3"}_ENDPOINT, ${prefix || "S3"}_BUCKET, ${prefix || "S3"}_ACCESS_KEY_ID, …`}</FieldDescription>
            </Field>
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !service}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Remove a volume or bucket: type its name; deleting its data is a separate, explicit choice. */
function RemoveDialog({
  name,
  warn,
  allowData,
  allowForce,
  onRemove,
}: {
  name: string
  warn: string
  allowData: boolean
  allowForce?: boolean
  onRemove: (deleteData: boolean, force: boolean) => Promise<void>
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState("")
  const [data, setData] = useState(false)
  const [force, setForce] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await onRemove(data, force)
      setOpen(false)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v)
        if (v) {
          setTyped("")
          setData(false)
          setForce(false)
          setError(null)
        }
      }}
    >
      <DialogTrigger render={<Button size="sm" variant="outline" />}>
        <Trash2Icon />
        {t("site.storage.remove")}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle dir="ltr">{name}</DialogTitle>
            <DialogDescription>{warn}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            {allowData ? (
              <label className="flex items-center gap-2 text-sm">
                <Checkbox checked={data} onCheckedChange={(v) => setData(v === true)} />
                {t("site.storage.deleteData")}
              </label>
            ) : null}
            {allowForce && data ? (
              <label className="flex items-center gap-2 text-sm text-grid-danger">
                <Checkbox checked={force} onCheckedChange={(v) => setForce(v === true)} />
                {t("site.storage.force")}
              </label>
            ) : null}
            <Field>
              <FieldLabel htmlFor="rm-confirm">{t("site.storage.typeName", { name })}</FieldLabel>
              <Input id="rm-confirm" dir="ltr" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
            </Field>
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" variant="destructive" disabled={busy || typed !== name}>
              {busy ? t("common.working") : t("site.storage.remove")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
