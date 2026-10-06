"use client"

import { Alert, Button, EmptyState, FileExplorer, Status, type FileNode } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useParams, useSearchParams } from "next/navigation"
import { useCallback, useEffect, useMemo, useState } from "react"
import { ArrowLeftIcon, ArrowRightIcon, ContainerIcon } from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { apiFetch } from "@/lib/api"
import type { DataService } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import type { Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

interface Entry {
  key: string
  folder?: boolean
  size?: number
  modified?: string
}

const MAX_UPLOAD = 2 << 20

/** "bucket/" or "bucket/a/b/" (folders) and "bucket/a/b.txt" (objects) → bucket and key. */
function split(id: string): { bucket: string; key: string } {
  const i = id.indexOf("/")
  return { bucket: id.slice(0, i), key: id.slice(i + 1) }
}

function leaf(key: string): string {
  const parts = key.replace(/\/$/, "").split("/")
  return parts[parts.length - 1]
}

/** One MinIO service: what it is, and its buckets in a file browser (MH-1113). */
export default function StorageDetailsPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const errorText = useErrorText()
  const { service: ref } = useParams<{ service: string }>()
  const search = useSearchParams()
  const base = `/${locale}/t/${team.slug}`
  const api = `/api/teams/${team.slug}/data-services/${ref}/objects`
  const Back = locale === "ar" ? ArrowRightIcon : ArrowLeftIcon

  const [svc, setSvc] = useState<DataService | null | undefined>(undefined)
  const [server, setServer] = useState<Server | null>(null)
  const [error, setError] = useState<string | null>(null)
  // Folder id ("" for the bucket list) → its loaded entries.
  const [listings, setListings] = useState<Record<string, Entry[]>>({})
  const [truncated, setTruncated] = useState(false)
  const [folder, setFolder] = useState<string | null>(() => {
    const b = search.get("bucket")
    return b ? `${b}/` : null
  })
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`)
      .then((r) => {
        const s = (r.services ?? []).find((x) => (x.id === ref || x.name === ref) && x.kind === "minio") ?? null
        setSvc(s)
        if (s)
          apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`)
            .then((rs) => setServer((rs.servers ?? []).find((x) => x.id === s.server_id) ?? null))
            .catch(() => {})
      })
      .catch((err) => {
        setError(errorText(err))
        setSvc(null)
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug, ref])

  const running = svc?.status === "running"

  const list = useCallback(
    async (id: string) => {
      setLoading(true)
      try {
        const { bucket, key } = id ? split(id) : { bucket: "", key: "" }
        const qs = new URLSearchParams()
        if (bucket) qs.set("bucket", bucket)
        if (key) qs.set("prefix", key)
        const r = await apiFetch<{ entries: Entry[]; truncated: boolean }>(`${api}?${qs}`)
        setListings((l) => ({ ...l, [id]: r.entries ?? [] }))
        if (r.truncated) setTruncated(true)
        setError(null)
      } catch (err) {
        setError(errorText(err))
      } finally {
        setLoading(false)
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [api],
  )

  useEffect(() => {
    if (running) void list("")
  }, [running, list])

  useEffect(() => {
    if (running && folder && !(folder in listings)) void list(folder)
  }, [running, folder, listings, list])

  const nodes = useMemo(() => {
    const build = (id: string): FileNode[] =>
      (listings[id] ?? []).map((e) => {
        if (id === "") {
          const fid = `${e.key}/`
          return { id: fid, name: e.key, kind: "folder", modifiedAt: e.modified || undefined, children: build(fid) }
        }
        const { bucket } = split(id)
        const nid = `${bucket}/${e.key}`
        return e.folder
          ? { id: nid, name: leaf(e.key), kind: "folder", children: build(nid) }
          : { id: nid, name: leaf(e.key), kind: "file", size: e.size, modifiedAt: e.modified || undefined }
      })
    return build("")
  }, [listings])

  const refresh = (id: string | null) => (id ? list(id) : list(""))

  const upload = async (files: File[], into: string | null) => {
    if (!into) return { error: t("storage2.pickBucket") }
    const { bucket, key } = split(into)
    for (const f of files) {
      if (f.size > MAX_UPLOAD) return { error: t("storage2.tooLarge", { name: f.name }) }
      const buf = new Uint8Array(await f.arrayBuffer())
      let bin = ""
      for (let i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode(...buf.subarray(i, i + 0x8000))
      try {
        await apiFetch(`${api}/content`, { method: "PUT", body: { bucket, key: key + f.name, data: btoa(bin) } })
      } catch (err) {
        return { error: errorText(err) }
      }
    }
    await refresh(into)
  }

  const createFolder = async (name: string, parent: string | null) => {
    if (!parent) return { error: t("storage2.pickBucket") }
    const { bucket, key } = split(parent)
    try {
      // S3 has no folders: a placeholder object makes one exist.
      await apiFetch(`${api}/content`, { method: "PUT", body: { bucket, key: `${key}${name}/.keep`, data: "" } })
    } catch (err) {
      return { error: errorText(err) }
    }
    await refresh(parent)
  }

  const parentOf = (id: string) => {
    const trimmed = id.replace(/\/$/, "")
    return trimmed.slice(0, trimmed.lastIndexOf("/") + 1)
  }

  const remove = async (node: FileNode) => {
    const { bucket, key } = split(node.id)
    if (!key) return { error: t("storage2.noBucketDelete") }
    const qs = new URLSearchParams({ bucket, key })
    if (node.kind === "folder") qs.set("recursive", "true")
    try {
      await apiFetch(`${api}/content?${qs}`, { method: "DELETE" })
    } catch (err) {
      return { error: errorText(err) }
    }
    await refresh(parentOf(node.id))
  }

  const download = async (node: FileNode) => {
    const { bucket, key } = split(node.id)
    try {
      const r = await apiFetch<{ data: string }>(`${api}/content?${new URLSearchParams({ bucket, key })}`)
      const bin = atob(r.data)
      const bytes = new Uint8Array(bin.length)
      for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
      const url = URL.createObjectURL(new Blob([bytes]))
      const a = document.createElement("a")
      a.href = url
      a.download = node.name
      a.click()
      URL.revokeObjectURL(url)
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex flex-col gap-2">
        <Link href={`${base}/storage`} className="flex items-center gap-1 text-sm text-muted-foreground hover:underline">
          <Back className="size-4" />
          {t("nav.storage")}
        </Link>
        <div className="flex flex-wrap items-center gap-3">
          <ContainerIcon className="size-6 text-muted-foreground" />
          <h1 className="text-2xl font-semibold">{svc?.name ?? ref}</h1>
          {svc ? (
            <Status tone={running ? "success" : svc.status === "failed" ? "danger" : "neutral"}>
              {t(`databases.statuses.${svc.status}`)}
            </Status>
          ) : null}
        </div>
        {svc ? (
          <p className="flex flex-wrap gap-x-4 text-sm text-muted-foreground">
            <span>
              {t("storage2.kinds.service")} · MinIO {svc.version}
            </span>
            {server ? (
              <Link href={`${base}/servers/${server.id}`} className="hover:underline">
                {server.name}
              </Link>
            ) : null}
            {svc.host && svc.port ? (
              <span dir="ltr">
                {svc.host}:{svc.port}
              </span>
            ) : null}
          </p>
        ) : null}
      </div>

      {error ? <Alert tone="danger">{error}</Alert> : null}

      {svc === null ? (
        <EmptyState
          icon={ContainerIcon}
          title={t("storage2.notFound")}
          actions={
            <Button variant="secondary" nativeButton={false} render={<Link href={`${base}/storage`} />}>
              {t("nav.storage")}
            </Button>
          }
        />
      ) : svc && !running ? (
        <Alert tone="warning">{t("storage2.notRunning")}</Alert>
      ) : svc && !manage ? (
        <Alert tone="info">{t("storage2.ownersOnly")}</Alert>
      ) : svc ? (
        <>
          {truncated ? <Alert tone="info">{t("storage2.truncated")}</Alert> : null}
          <FileExplorer
            nodes={nodes}
            rootLabel={t("storage2.buckets")}
            folderId={folder}
            onFolderChange={setFolder}
            loading={loading && !((folder ?? "") in listings)}
            onUpload={upload}
            onCreateFolder={createFolder}
            onDelete={remove}
            onDownload={(n) => void download(n)}
          />
          <p className="text-xs text-muted-foreground">{t("storage2.limitHint")}</p>
        </>
      ) : null}
    </div>
  )
}
