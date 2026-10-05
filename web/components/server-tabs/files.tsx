"use client"

import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldLabel, Card, Input, Textarea, toast, type DataTableColumn } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent, type KeyboardEvent } from "react"
import {
  ArrowUpIcon,
  FileIcon,
  FolderIcon,
  FolderPlusIcon,
  PencilIcon,
  RefreshCwIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { FormError, useErrorText } from "@/components/form-error"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"







import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { formatBytes } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/*
Files (MH-574 E10): a server's filesystem, browsed and edited through its agent (internal/agent/files.go,
internal/api/files.go). Every operation needs "manage" (owner/admin) since a file can hold a secret.
Paths are always shown dir="ltr", whatever the page's direction.
*/

type FSEntry = {
  name: string
  type: "file" | "dir" | "symlink" | "other"
  size: number
  mode: string
  mtime: string
  owner?: string
}

type FileContent = {
  path: string
  content: string
  size: number
  truncated: boolean
  binary: boolean
}

/** The parent of a clean absolute POSIX path ("/" for "/a" and for "/"). */
function parentOf(path: string): string {
  if (path === "/") return "/"
  const i = path.lastIndexOf("/")
  return i <= 0 ? "/" : path.slice(0, i)
}

/** dir + name, POSIX-joined ("/" stays "/x", not "//x"). */
function joinPath(dir: string, name: string): string {
  return dir === "/" ? `/${name}` : `${dir}/${name}`
}

/** The breadcrumb segments of a clean absolute path: [{name, path}, …], "/" first. */
function segmentsOf(path: string): { name: string; path: string }[] {
  const parts = path.split("/").filter(Boolean)
  const out = [{ name: "/", path: "/" }]
  let cur = ""
  for (const p of parts) {
    cur += `/${p}`
    out.push({ name: p, path: cur })
  }
  return out
}

function bytesFromBase64(b64: string): Uint8Array {
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

function base64FromText(text: string): string {
  const bytes = new TextEncoder().encode(text)
  let bin = ""
  for (const b of bytes) bin += String.fromCharCode(b)
  return btoa(bin)
}

/**
 * The Files tab: a breadcrumb path bar, the current directory's entries, and — when a file is
 * open — a text viewer/editor in place of the table (a directory and a file view never show at
 * once).
 */
export function ServerFiles({ serverId, manage }: { serverId: string; manage: boolean }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const base = `/api/teams/${team.slug}/servers/${serverId}/files`

  const [dir, setDir] = useState("/")
  const [pathInput, setPathInput] = useState("/")
  const [entries, setEntries] = useState<FSEntry[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const [file, setFile] = useState<FileContent | null>(null)
  const [fileError, setFileError] = useState<string | null>(null)
  const [fileLoading, setFileLoading] = useState(false)
  const [draft, setDraft] = useState("")
  const [saving, setSaving] = useState(false)

  const [busyName, setBusyName] = useState<string | null>(null)

  const list = useCallback(
    async (p: string) => {
      setLoading(true)
      setError(null)
      try {
        const r = await apiFetch<{ entries: FSEntry[] }>(`${base}?path=${encodeURIComponent(p)}`)
        setEntries((r.entries ?? []).slice().sort((a, b) => (a.type === b.type ? a.name.localeCompare(b.name) : a.type === "dir" ? -1 : 1)))
      } catch (err) {
        setError(errorText(err))
        setEntries([])
      } finally {
        setLoading(false)
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    },
    [base],
  )

  useEffect(() => {
    setPathInput(dir)
    void list(dir)
  }, [dir, list])

  function navigate(p: string) {
    setFile(null)
    setFileError(null)
    setDir(p || "/")
  }

  function onPathKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key !== "Enter") return
    e.preventDefault()
    const p = pathInput.trim()
    if (p) navigate(p.startsWith("/") ? p : `/${p}`)
  }

  async function openFile(entry: FSEntry) {
    const p = joinPath(dir, entry.name)
    setFileLoading(true)
    setFileError(null)
    setFile(null)
    try {
      const r = await apiFetch<{ path: string; content: string; size: number; truncated: boolean; binary: boolean }>(
        `${base}/content?path=${encodeURIComponent(p)}`,
      )
      const content = r.binary ? "" : new TextDecoder().decode(bytesFromBase64(r.content))
      setFile({ path: r.path, content, size: r.size, truncated: r.truncated, binary: r.binary })
      setDraft(content)
    } catch (err) {
      setFileError(errorText(err))
    } finally {
      setFileLoading(false)
    }
  }

  async function save() {
    if (!file) return
    setSaving(true)
    setFileError(null)
    try {
      await apiFetch(`${base}/content`, { method: "PUT", body: { path: file.path, content: base64FromText(draft) } })
      setFile({ ...file, content: draft, size: new TextEncoder().encode(draft).length, truncated: false })
      toast.success(t("servers.files.saved", { name: file.path.split("/").pop() ?? file.path }))
    } catch (err) {
      setFileError(errorText(err))
    } finally {
      setSaving(false)
    }
  }

  async function mkdir(name: string) {
    const p = joinPath(dir, name)
    await apiFetch(`${base}/mkdir`, { method: "POST", body: { path: p } })
    toast.success(t("servers.files.created", { name }))
    await list(dir)
  }

  async function rename(entry: FSEntry, name: string) {
    const from = joinPath(dir, entry.name)
    const to = joinPath(dir, name)
    setBusyName(entry.name)
    try {
      await apiFetch(`${base}/rename`, { method: "POST", body: { from, to } })
      toast.success(t("servers.files.renamed", { from: entry.name, to: name }))
      await list(dir)
    } finally {
      setBusyName(null)
    }
  }

  async function remove(entry: FSEntry) {
    const p = joinPath(dir, entry.name)
    const recursive = entry.type === "dir"
    try {
      await apiFetch(`${base}?path=${encodeURIComponent(p)}${recursive ? "&recursive=true" : ""}`, { method: "DELETE" })
    } catch (err) {
      throw new Error(errorText(err))
    }
    toast.success(t("servers.files.deleted", { name: entry.name }))
    await list(dir)
  }

  const crumbs = segmentsOf(dir)

  if (!manage) {
    return (
      <section aria-labelledby="files-h" data-testid="files">
        <SectionTitle id="files-h" title={t("servers.files.title")} hint={t("servers.files.hint")} />
        <EmptyState className="mt-3 rounded-lg border border-border bg-card" icon={FolderIcon} title={t("servers.files.noAccessTitle")} description={t("servers.files.noAccessHint")} />
      </section>
    )
  }

  return (
    <section aria-labelledby="files-h" data-testid="files">
      <SectionTitle
        id="files-h"
        title={t("servers.files.title")}
        hint={t("servers.files.hint")}
        action={
          file ? null : (
            <div className="flex items-center gap-2">
              <NewFolderButton onCreate={mkdir} />
              <Button size="sm" variant="secondary" onClick={() => void list(dir)} disabled={loading}>
                <RefreshCwIcon className={loading ? "animate-spin" : ""} />
                {t("servers.files.refresh")}
              </Button>
            </div>
          )
        }
      />

      {file ? (
        <FileViewer
          file={file}
          draft={draft}
          setDraft={setDraft}
          saving={saving}
          loading={fileLoading}
          error={fileError}
          onSave={() => void save()}
          onClose={() => {
            setFile(null)
            setFileError(null)
          }}
        />
      ) : (
        <>
          <Card className="flex-row flex-wrap items-center gap-2 px-3 py-2">
            <Button
              size="icon-sm"
              variant="secondary"
              aria-label={t("servers.files.up")}
              disabled={dir === "/"}
              onClick={() => navigate(parentOf(dir))}
            >
              <ArrowUpIcon className="rtl:-scale-x-100" />
            </Button>
            <nav aria-label={t("servers.files.title")} className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto" dir="ltr">
              {crumbs.map((c, i) => (
                <span key={c.path} className="flex shrink-0 items-center gap-1">
                  {i > 0 ? <span className="text-muted-foreground">/</span> : null}
                  <button
                    type="button"
                    onClick={() => navigate(c.path)}
                    className={`whitespace-nowrap px-1 text-sm hover:underline ${c.path === dir ? "font-medium text-foreground" : "text-muted-foreground"}`}
                  >
                    {c.name}
                  </button>
                </span>
              ))}
            </nav>
            <Input
              value={pathInput}
              onChange={(e) => setPathInput(e.target.value)}
              onKeyDown={onPathKeyDown}
              dir="ltr"
              className="w-64 font-mono text-xs"
              aria-label={t("servers.files.pathInput")}
              placeholder="/"
            />
          </Card>

          <ErrorBand message={error} />

          {loading && entries === null ? (
            <LoadingRows />
          ) : entries && entries.length === 0 ? (
            <EmptyState className="mt-3 rounded-lg border border-border bg-card" icon={FolderIcon} title={t("servers.files.emptyTitle")} description={t("servers.files.emptyHint")} />
          ) : entries ? (
            <NasaqTable
              data={entries}
              rowId={(e) => e.name}
              label={t("servers.files.title")}
              rowLabel={(e) => e.name}
              search={t("servers.files.columns.name")}
              empty={t("home.noMatch")}
              pageSize={50}
              defaultSort={{ id: "name", direction: "asc" }}
              onRowClick={(e) => (e.type === "dir" ? navigate(joinPath(dir, e.name)) : void openFile(e))}
              columns={[
                {
                  id: "name",
                  header: t("servers.files.columns.name"),
                  label: t("servers.files.columns.name"),
                  sortValue: (e) => `${e.type === "dir" ? 0 : 1}${e.name.toLowerCase()}`,
                  searchValue: (e) => e.name,
                  cell: (e) => (
                    <span className={cn("flex min-w-0 items-center gap-2 text-foreground", e.type === "dir" && "font-medium")} dir="ltr">
                      {e.type === "dir" ? <FolderIcon className="size-4 shrink-0 text-nq-action" /> : <FileIcon className="size-4 shrink-0 text-muted-foreground" />}
                      <span className="truncate">{e.name}</span>
                    </span>
                  ),
                },
                {
                  id: "size",
                  header: t("servers.files.columns.size"),
                  label: t("servers.files.columns.size"),
                  sortValue: (e) => (e.type === "dir" ? -1 : e.size),
                  cell: (e) => (
                    <span dir="ltr" className="font-mono text-caption text-muted-foreground">
                      {e.type === "dir" ? "—" : formatBytes(e.size)}
                    </span>
                  ),
                },
                {
                  id: "modified",
                  header: t("servers.files.columns.modified"),
                  label: t("servers.files.columns.modified"),
                  sortValue: (e) => e.mtime,
                  className: "max-md:hidden",
                  headerClassName: "max-md:hidden",
                  cell: (e) => (
                    <span dir="ltr" className="text-caption text-muted-foreground">
                      {fmt(e.mtime)}
                    </span>
                  ),
                },
                {
                  id: "mode",
                  header: t("servers.files.columns.mode"),
                  label: t("servers.files.columns.mode"),
                  className: "max-lg:hidden",
                  headerClassName: "max-lg:hidden",
                  cell: (e) => (
                    <span dir="ltr" className="font-mono text-caption text-muted-foreground">
                      {e.mode}
                    </span>
                  ),
                },
                {
                  id: "actions",
                  header: <span className="sr-only">{t("common.actions")}</span>,
                  label: t("common.actions"),
                  cell: (e) => (
                    <div className="flex flex-wrap justify-end gap-2" onClick={(ev) => ev.stopPropagation()}>
                      <RenameButton entry={e} busy={busyName === e.name} onRename={(name) => rename(e, name)} />
                      <ConfirmButton
                        size="sm"
                        icon={<Trash2Icon />}
                        label={t("common.delete")}
                        title={t(e.type === "dir" ? "servers.files.deleteFolderTitle" : "servers.files.deleteFileTitle", { name: e.name })}
                        description={t(e.type === "dir" ? "servers.files.deleteFolderBody" : "servers.files.deleteFileBody", { name: e.name })}
                        confirmLabel={t("common.delete")}
                        onConfirm={() => remove(e)}
                      />
                    </div>
                  ),
                },
              ] satisfies DataTableColumn<FSEntry>[]}
            />
          ) : null}
        </>
      )}
    </section>
  )
}

function FileViewer({
  file,
  draft,
  setDraft,
  saving,
  loading,
  error,
  onSave,
  onClose,
}: {
  file: FileContent
  draft: string
  setDraft: (v: string) => void
  saving: boolean
  loading: boolean
  error: string | null
  onSave: () => void
  onClose: () => void
}) {
  const { t } = useI18n()
  const dirty = !file.binary && !file.truncated && draft !== file.content

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card shadow-xs">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-5 py-3">
        <span dir="ltr" className="min-w-0 truncate font-mono text-sm text-foreground" title={file.path}>
          {file.path}
        </span>
        <div className="flex items-center gap-2">
          {file.binary || file.truncated ? null : (
            <Button variant="primary" size="sm" onClick={onSave} disabled={saving || !dirty}>
              {saving ? t("common.working") : t("servers.files.save")}
            </Button>
          )}
          <Button size="sm" variant="secondary" onClick={onClose}>
            <XIcon />
            {t("servers.files.close")}
          </Button>
        </div>
      </div>
      <ErrorBand message={error} />
      {loading ? (
        <LoadingRows />
      ) : file.binary ? (
        <p className="px-6 py-8 text-center text-sm text-muted-foreground">{t("servers.files.binaryNotice")}</p>
      ) : (
        <div className="px-6 pb-6">
          {file.truncated ? <p className="mb-2 text-xs text-muted-foreground">{t("servers.files.truncatedNotice", { size: formatBytes(file.size) })}</p> : null}
          <Textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            readOnly={file.truncated}
            dir="ltr"
            spellCheck={false}
            className="min-h-[28rem] font-mono text-xs"
          />
        </div>
      )}
    </div>
  )
}

function NewFolderButton({ onCreate }: { onCreate: (name: string) => Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!name.trim()) return
    setBusy(true)
    setError(null)
    try {
      await onCreate(name.trim())
      setOpen(false)
      setName("")
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
        if (!v) setError(null)
      }}
    >
      <DialogTrigger render={<Button size="sm" variant="secondary" />}>
        <FolderPlusIcon />
        {t("servers.files.newFolder")}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{t("servers.files.newFolderTitle")}</DialogTitle>
            <DialogDescription>{t("servers.files.newFolderHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5 py-4">
            <Field>
              <FieldLabel htmlFor="new-folder-name">{t("servers.files.columns.name")}</FieldLabel>
              <Input id="new-folder-name" value={name} onChange={(e) => setName(e.target.value)} dir="ltr" autoFocus />
            </Field>
            <FormError message={error} />
          </div>
          <DialogFooter>
            <Button variant="primary" type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : t("servers.files.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RenameButton({ entry, busy, onRename }: { entry: FSEntry; busy: boolean; onRename: (name: string) => Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState(entry.name)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!name.trim() || name === entry.name) return
    setSaving(true)
    setError(null)
    try {
      await onRename(name.trim())
      setOpen(false)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v)
        if (v) setName(entry.name)
        else setError(null)
      }}
    >
      <DialogTrigger render={<Button size="sm" variant="secondary" disabled={busy} />}>
        <PencilIcon />
        {t("servers.files.rename")}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{t("servers.files.renameTitle", { name: entry.name })}</DialogTitle>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5 py-4">
            <Field>
              <FieldLabel htmlFor="rename-name">{t("servers.files.columns.name")}</FieldLabel>
              <Input id="rename-name" value={name} onChange={(e) => setName(e.target.value)} dir="ltr" autoFocus />
            </Field>
            <FormError message={error} />
          </div>
          <DialogFooter>
            <Button variant="primary" type="submit" disabled={saving || !name.trim() || name === entry.name}>
              {saving ? t("common.working") : t("servers.files.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
