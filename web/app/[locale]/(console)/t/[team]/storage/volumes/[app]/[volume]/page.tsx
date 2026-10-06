"use client"

import { Alert, FileExplorer, type FileNode } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useParams } from "next/navigation"
import { useCallback, useEffect, useMemo, useState } from "react"
import { ArrowLeftIcon, ArrowRightIcon, PackageIcon } from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { apiFetch } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

interface Entry {
  key: string
  folder?: boolean
  size?: number
  modified?: string
}

const MAX_UPLOAD = 2 << 20

function leaf(key: string): string {
  const parts = key.replace(/\/$/, "").split("/")
  return parts[parts.length - 1]
}

function parentOf(key: string): string {
  const trimmed = key.replace(/\/$/, "")
  return trimmed.slice(0, trimmed.lastIndexOf("/") + 1)
}

/** One app volume's files on its server, in a file browser. Node ids are paths; "" is the volume's root. */
export default function VolumeFilesPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const errorText = useErrorText()
  const { app, volume } = useParams<{ app: string; volume: string }>()
  useDocumentTitle(`${volume} — ${team.name}`)
  const base = `/${locale}/t/${team.slug}`
  const api = `/api/teams/${team.slug}/apps/${app}/storage/volumes/${volume}/files`
  const Back = locale === "ar" ? ArrowRightIcon : ArrowLeftIcon

  const [listings, setListings] = useState<Record<string, Entry[]>>({})
  const [folder, setFolder] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [truncated, setTruncated] = useState(false)
  const [loading, setLoading] = useState(false)

  const list = useCallback(
    async (path: string) => {
      setLoading(true)
      try {
        const r = await apiFetch<{ entries: Entry[]; truncated: boolean }>(`${api}?${new URLSearchParams({ path })}`)
        setListings((l) => ({ ...l, [path]: r.entries ?? [] }))
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
    if (manage) void list("")
  }, [manage, list])

  useEffect(() => {
    if (manage && folder && !(folder in listings)) void list(folder)
  }, [manage, folder, listings, list])

  const nodes = useMemo(() => {
    const build = (path: string): FileNode[] =>
      (listings[path] ?? []).map((e) =>
        e.folder
          ? { id: e.key, name: leaf(e.key), kind: "folder", modifiedAt: e.modified || undefined, children: build(e.key) }
          : { id: e.key, name: leaf(e.key), kind: "file", size: e.size, modifiedAt: e.modified || undefined },
      )
    return build("")
  }, [listings])

  const upload = async (files: File[], into: string | null) => {
    const dir = into ?? ""
    for (const f of files) {
      if (f.size > MAX_UPLOAD) return { error: t("storage2.tooLarge", { name: f.name }) }
      const buf = new Uint8Array(await f.arrayBuffer())
      let bin = ""
      for (let i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode(...buf.subarray(i, i + 0x8000))
      try {
        await apiFetch(`${api}/content`, { method: "PUT", body: { path: dir + f.name, data: btoa(bin) } })
      } catch (err) {
        return { error: errorText(err) }
      }
    }
    await list(dir)
  }

  const createFolder = async (name: string, parent: string | null) => {
    const dir = parent ?? ""
    try {
      await apiFetch(`${api}/content`, { method: "PUT", body: { path: `${dir}${name}/` } })
    } catch (err) {
      return { error: errorText(err) }
    }
    await list(dir)
  }

  const remove = async (node: FileNode) => {
    try {
      await apiFetch(`${api}/content?${new URLSearchParams({ path: node.id })}`, { method: "DELETE" })
    } catch (err) {
      return { error: errorText(err) }
    }
    await list(parentOf(node.id))
  }

  const download = async (node: FileNode) => {
    try {
      const r = await apiFetch<{ data: string }>(`${api}/content?${new URLSearchParams({ path: node.id })}`)
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
          <PackageIcon className="size-6 text-muted-foreground" />
          <h1 className="text-2xl font-semibold">{volume}</h1>
        </div>
        <p className="text-sm text-muted-foreground">
          {t("storage2.kinds.volume")} ·{" "}
          <Link href={`${base}/apps/${encodeURIComponent(app)}/storage`} className="hover:underline">
            {app}
          </Link>
        </p>
      </div>

      {error ? <Alert tone="danger">{error}</Alert> : null}

      {!manage ? (
        <Alert tone="info">{t("storage2.ownersOnly")}</Alert>
      ) : (
        <>
          {truncated ? <Alert tone="info">{t("storage2.truncated")}</Alert> : null}
          <FileExplorer
            nodes={nodes}
            rootLabel={volume}
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
      )}
    </div>
  )
}
