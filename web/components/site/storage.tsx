"use client"

import { Badge, Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, toast } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { ArchiveIcon, FolderOpenIcon, HardDriveIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"








import { apiFetch } from "@/lib/api"
import { isImported } from "@/lib/apps"
import { humanBytes, type DataService } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import { useInPanel, useTeam, useTeamBase, useServerHref } from "@/lib/team"

export type Volume = { id: string; name: string; mount_path: string; volume: string; mounted: boolean; bytes: number | null }
export type Bucket = { id: string; service: string; bucket: string; access_key: string; env_prefix: string; env: string[]; status: "creating" | "ready" | "failed" | "removing"; error: string | null }

/**
 * Storage: what the site keeps outside its releases. Volumes are mounted into every release and
 * worker from the next deploy; buckets live on a MinIO service on the same server, each with a
 * key limited to that one bucket, injected as sealed S3_* variables.
 */
export function SiteStorage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  const teamBase = useTeamBase()
  const serverHref = useServerHref()
  // The file browsers are team pages; a server user's panel has none, so its rows do not link.
  const browse = !useInPanel()
  const errorText = useErrorText()
  const { app, path } = useLoadedSite()
  const [volumes, setVolumes] = useState<Volume[] | null>(null)
  const [buckets, setBuckets] = useState<Bucket[]>([])
  const [minio, setMinio] = useState<DataService[]>([])
  const [error, setError] = useState<string | null>(null)
  const [removingVolume, setRemovingVolume] = useState<Volume | null>(null)
  const [removingBucket, setRemovingBucket] = useState<Bucket | null>(null)
  const router = useRouter()
  const volumeHref = (v: Volume) => `${teamBase}/storage/volumes/${encodeURIComponent(app.name)}/${encodeURIComponent(v.name)}`
  const bucketHref = (b: Bucket) => `${teamBase}/storage/${encodeURIComponent(b.service)}?bucket=${encodeURIComponent(b.bucket)}`

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
          <p className="flex items-center gap-2 nq-band py-6 text-sm text-muted-foreground">
            <HardDriveIcon className="size-4" />
            {t("site.storage.noVolumes")}
          </p>
        ) : (
          <NasaqTable
            data={volumes}
            rowId={(v) => v.id}
            rowLabel={(v) => v.name}
            label={t("site.storage.volumes")}
            onRowClick={browse ? (v) => router.push(volumeHref(v)) : undefined}
            rowActions={(v) => [
              ...(browse ? [{ id: "browse", label: t("site.storage.browse"), icon: FolderOpenIcon, onSelect: () => router.push(volumeHref(v)) }] : []),
              ...(manage ? [{ id: "remove", label: t("site.storage.remove"), icon: Trash2Icon, danger: true, group: "danger", onSelect: () => setRemovingVolume(v) }] : []),
            ]}
            columns={[
              {
                id: "name",
                header: t("site.monitors.name"),
                cell: (v) =>
                  browse ? (
                    <Link dir="ltr" href={volumeHref(v)} className="font-medium hover:underline">
                      {v.name}
                    </Link>
                  ) : (
                    <span dir="ltr" className="font-medium">
                      {v.name}
                    </span>
                  ),
                sortValue: (v) => v.name,
              },
              {
                id: "mount",
                header: t("site.storage.mountPath"),
                cell: (v) => (
                  <span dir="ltr" className="font-mono text-xs">
                    {v.mount_path}
                  </span>
                ),
              },
              {
                id: "status",
                header: t("lists.status"),
                cell: (v) => <Badge variant={v.mounted ? "brand" : "outline"}>{v.mounted ? t("site.storage.mounted") : t("site.storage.nextDeploy")}</Badge>,
              },
              {
                id: "size",
                header: t("site.storage.size"),
                cell: (v) => (
                  <span dir="ltr" className="text-xs tabular-nums">
                    {v.bytes == null ? "—" : `≈ ${humanBytes(v.bytes)}`}
                  </span>
                ),
                sortValue: (v) => v.bytes ?? -1,
              },
            ]}
          />
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
          <p className="flex flex-wrap items-center gap-2 nq-band py-6 text-sm text-muted-foreground">
            <ArchiveIcon className="size-4" />
            {t("site.storage.noMinio")}
            <Link href={serverHref(app.server_id, "databases")} className="text-nq-brand underline-offset-4 hover:underline">
              {t("nav.databases")}
            </Link>
          </p>
        ) : buckets.length === 0 ? (
          <p className="nq-band py-6 text-sm text-muted-foreground">{t("site.storage.noBuckets")}</p>
        ) : (
          <NasaqTable
            data={buckets}
            rowId={(b) => b.id}
            rowLabel={(b) => b.bucket}
            label={t("site.storage.buckets")}
            onRowClick={browse ? (b) => router.push(bucketHref(b)) : undefined}
            rowActions={(b) => [
              ...(browse ? [{ id: "browse", label: t("site.storage.browse"), icon: FolderOpenIcon, onSelect: () => router.push(bucketHref(b)) }] : []),
              ...(manage ? [{ id: "remove", label: t("site.storage.remove"), icon: Trash2Icon, danger: true, group: "danger", onSelect: () => setRemovingBucket(b) }] : []),
            ]}
            columns={[
              {
                id: "bucket",
                header: t("site.storage.bucket"),
                cell: (b) =>
                  browse ? (
                    <Link dir="ltr" href={bucketHref(b)} className="font-medium hover:underline">
                      {b.bucket}
                    </Link>
                  ) : (
                    <span dir="ltr" className="font-medium">
                      {b.bucket}
                    </span>
                  ),
                sortValue: (b) => b.bucket,
              },
              { id: "service", header: t("site.storage.service"), cell: (b) => <span dir="ltr">{b.service}</span>, sortValue: (b) => b.service },
              {
                id: "status",
                header: t("lists.status"),
                cell: (b) => (
                  <>
                    <Badge variant={b.status === "ready" ? "brand" : b.status === "failed" ? "danger" : "neutral"}>{t(`site.storage.status.${b.status}`)}</Badge>
                    {b.error ? (
                      <p dir="auto" className="mt-1 max-w-xs text-xs text-nq-danger-text">
                        {b.error}
                      </p>
                    ) : null}
                  </>
                ),
              },
              {
                id: "vars",
                header: t("site.storage.variables"),
                cell: (b) => (
                  <span dir="ltr" className="block max-w-64 text-start font-mono text-xs text-muted-foreground">
                    {b.env.join(" ")}
                  </span>
                ),
              },
            ]}
          />
        )}
        <p className="nq-band py-3 text-xs text-muted-foreground">{t("site.storage.noBrowser")}</p>
      </section>
      {removingVolume ? <RemoveVolume open onOpenChange={(v) => !v && setRemovingVolume(null)} path={path} volume={removingVolume} onRemoved={load} /> : null}
      {removingBucket ? <RemoveBucket open onOpenChange={(v) => !v && setRemovingBucket(null)} path={path} bucket={removingBucket} onRemoved={load} /> : null}
    </>
  )
}

/** Add a volume (mounted from the next deploy). Also opened from the overview canvas. */
export function AddVolume({ path, onSaved, ...controlled }: { path: string; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled)
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
      {fromCaller ? null : (
        <DialogTrigger render={<Button variant="primary" size="sm" />}>
          <PlusIcon />
          {t("site.storage.addVolume")}
        </DialogTrigger>
      )}
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("site.storage.addVolume")}</DialogTitle>
            <DialogDescription>{t("site.storage.appliesNext")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="vol-name">{t("site.monitors.name")}</FieldLabel>
              <Input id="vol-name" dir="ltr" required pattern="[a-z][a-z0-9\-]{0,30}" placeholder="uploads" value={name} onChange={(e) => setName(e.target.value)} />
              <FieldDescription>{t("site.storage.nameHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="vol-path">{t("site.storage.mountPath")}</FieldLabel>
              <Input id="vol-path" dir="ltr" required placeholder="/app/storage" value={mount} onChange={(e) => setMount(e.target.value)} className="font-mono" />
              <FieldDescription>{t("site.storage.pathHint")}</FieldDescription>
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Add a bucket on one of the server's MinIO services. Also opened from the overview canvas. */
export function AddBucket({ path, services, onSaved, ...controlled }: { path: string; services: DataService[]; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled)
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
      {fromCaller ? null : (
        <DialogTrigger render={<Button variant="primary" size="sm" />}>
          <PlusIcon />
          {t("site.storage.addBucket")}
        </DialogTrigger>
      )}
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("site.storage.addBucket")}</DialogTitle>
            <DialogDescription>{t("site.storage.bucketDialogHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
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
              <Input id="bkt-name" dir="ltr" required minLength={3} maxLength={63} pattern="[a-z0-9][a-z0-9\-]*[a-z0-9]" placeholder="my-site-uploads" value={bucket} onChange={(e) => setBucket(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="bkt-prefix">{t("site.storage.prefix")}</FieldLabel>
              <Input id="bkt-prefix" dir="ltr" pattern="[A-Z][A-Z0-9_]{0,20}" value={prefix} onChange={(e) => setPrefix(e.target.value.toUpperCase())} className="font-mono" />
              <FieldDescription dir="ltr">{`${prefix || "S3"}_ENDPOINT, ${prefix || "S3"}_BUCKET, ${prefix || "S3"}_ACCESS_KEY_ID, …`}</FieldDescription>
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !service}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Remove a volume (typed name; its data only when it is not mounted). Also opened from the overview canvas. */
export function RemoveVolume({ path, volume: v, onRemoved, ...controlled }: { path: string; volume: Volume; onRemoved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  return (
    <RemoveDialog
      {...controlled}
      name={v.name}
      warn={v.mounted ? t("site.storage.volumeMounted") : t("site.storage.volumeDataHint")}
      allowData={!v.mounted}
      onRemove={async (deleteData) => {
        await apiFetch(`${path}/storage/volumes/${v.id}`, { method: "DELETE", body: { confirm: v.name, delete_data: deleteData } })
        await onRemoved()
      }}
    />
  )
}

/** Remove a bucket (typed name; its data, even when not empty, only on request). Also opened from the overview canvas. */
export function RemoveBucket({ path, bucket: b, onRemoved, ...controlled }: { path: string; bucket: Bucket; onRemoved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  return (
    <RemoveDialog
      {...controlled}
      name={b.bucket}
      warn={t("site.storage.bucketRemoveHint")}
      allowData
      allowForce
      onRemove={async (deleteData, force) => {
        await apiFetch(`${path}/storage/buckets/${b.id}`, { method: "DELETE", body: { confirm: b.bucket, delete_data: deleteData, force: deleteData && force } })
        await onRemoved()
      }}
    />
  )
}

/** Remove a volume or bucket: type its name; deleting its data is a separate, explicit choice. */
function RemoveDialog({
  name,
  warn,
  allowData,
  allowForce,
  onRemove,
  ...controlled
}: {
  name: string
  warn: string
  allowData: boolean
  allowForce?: boolean
  onRemove: (deleteData: boolean, force: boolean) => Promise<void>
} & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [typed, setTyped] = useState("")
  const [data, setData] = useState(false)
  const [force, setForce] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, () => {
    setTyped("")
    setData(false)
    setForce(false)
    setError(null)
  })

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
    <Dialog open={open} onOpenChange={setOpen}>
      {fromCaller ? null : (
        <DialogTrigger render={<Button size="sm" variant="secondary" />}>
          <Trash2Icon />
          {t("site.storage.remove")}
        </DialogTrigger>
      )}
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle dir="ltr">{name}</DialogTitle>
            <DialogDescription>{warn}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            {allowData ? (
              <label className="flex items-center gap-2 text-sm">
                <Checkbox checked={data} onCheckedChange={(v) => setData(v === true)} />
                {t("site.storage.deleteData")}
              </label>
            ) : null}
            {allowForce && data ? (
              <label className="flex items-center gap-2 text-sm text-nq-danger-text">
                <Checkbox checked={force} onCheckedChange={(v) => setForce(v === true)} />
                {t("site.storage.force")}
              </label>
            ) : null}
            <Field>
              <FieldLabel htmlFor="rm-confirm">{t("site.storage.typeName", { name })}</FieldLabel>
              <Input id="rm-confirm" dir="ltr" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" variant="danger" disabled={busy || typed !== name}>
              {busy ? t("common.working") : t("site.storage.remove")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
