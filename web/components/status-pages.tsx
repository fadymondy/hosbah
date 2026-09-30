"use client"

import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Badge, Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, Field, FieldDescription, FieldLabel, Input, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { ExternalLinkIcon, GaugeIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"







import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"

import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

export type StatusPage = {
  id: string
  slug: string
  title: string
  monitor_ids: string[]
  published: boolean
}
type PickableMonitor = { id: string; label: string }

/**
 * Team settings "Status pages" (MH-575 H3): a published, read-only page at /status/{slug} showing a
 * chosen set of the team's monitors. Owner or admin manage; any member reads (matching
 * internal/api/status_pages.go). Monitors belong to apps, so the picker loads every app's monitors.
 */
export function StatusPagesSection({ team, manage }: { team: string; manage: boolean }) {
  const { t, locale } = useI18n()
  const errorText = useErrorText()
  const [pages, setPages] = useState<StatusPage[] | null>(null)
  const [monitors, setMonitors] = useState<PickableMonitor[]>([])
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ status_pages: StatusPage[] }>(`/api/teams/${team}/status-pages`)
      setPages(r.status_pages ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setPages((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team])

  const loadMonitors = useCallback(async () => {
    try {
      const apps = await apiFetch<{ apps: { id: string; name: string }[] }>(`/api/teams/${team}/apps`)
      const lists = await Promise.all(
        (apps.apps ?? []).map(async (a) => {
          try {
            const m = await apiFetch<{ monitors: { id: string; name: string }[] }>(`/api/teams/${team}/apps/${a.id}/monitors`)
            return (m.monitors ?? []).map((mon) => ({ id: mon.id, label: `${a.name} / ${mon.name}` }))
          } catch {
            return []
          }
        }),
      )
      setMonitors(lists.flat())
    } catch {
      setMonitors([])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team])

  useEffect(() => {
    void load()
    void loadMonitors()
  }, [load, loadMonitors])

  const shown = (pages ?? []).filter((p) => matches(q, p.title, p.slug))
  const add = manage ? <StatusPageDialog team={team} monitors={monitors} onSaved={load} /> : null

  return (
    <section aria-labelledby="statuspages-h">
      <SectionTitle id="statuspages-h" title={t("settings.statusPagesTitle")} hint={t("settings.statusPagesHint")} action={add} />
      <ErrorBand message={error} />
      {pages === null ? (
        <LoadingRows rows={2} />
      ) : pages.length === 0 ? (
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <GaugeIcon />
            </EmptyMedia>
            <EmptyTitle>{t("statusPages.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("statusPages.emptyHint")}</EmptyDescription>
          </EmptyHeader>
          {add}
        </Empty>
      ) : (
        <>
          <ListSearch value={q} onChange={setQ} placeholder={t("statusPages.search")} shown={shown.length} total={pages.length} />
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("statusPages.title")}</TableHead>
                  <TableHead>{t("statusPages.slug")}</TableHead>
                  <TableHead>{t("lists.status")}</TableHead>
                  <TableHead className="text-end">{t("common.actions")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((p) => (
                  <TableRow key={p.id}>
                    <TableCell>
                      <span dir="auto" className="font-medium">
                        {p.title}
                      </span>
                    </TableCell>
                    <TableCell dir="ltr" className="text-start font-mono text-xs text-muted-foreground">
                      /status/{p.slug}
                    </TableCell>
                    <TableCell>
                      <Badge variant={p.published ? "brand" : "outline"}>{p.published ? t("statusPages.published") : t("statusPages.unpublished")}</Badge>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap justify-end gap-2">
                        {p.published ? (
                          <Button size="sm" variant="secondary" nativeButton={false} render={<a href={`/${locale}/status/${p.slug}`} target="_blank" rel="noreferrer" />}>
                            <ExternalLinkIcon />
                            {t("statusPages.view")}
                          </Button>
                        ) : null}
                        {manage ? (
                          <>
                            <StatusPageDialog team={team} monitors={monitors} page={p} onSaved={load} />
                            <DeletePage team={team} page={p} onDeleted={load} />
                          </>
                        ) : null}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableBand>
        </>
      )}
    </section>
  )
}

function DeletePage({ team, page: p, onDeleted, ...controlled }: { team: string; page: StatusPage; onDeleted: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      {...controlled}
      icon={<Trash2Icon />}
      label={t("common.delete")}
      title={t("statusPages.deleteTitle", { name: p.title })}
      description={t("statusPages.deleteBody")}
      confirmLabel={t("common.delete")}
      onConfirm={async () => {
        try {
          await apiFetch(`/api/teams/${team}/status-pages/${p.id}`, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onDeleted()
      }}
    />
  )
}

function StatusPageDialog({
  team,
  monitors,
  page,
  onSaved,
  ...controlled
}: { team: string; monitors: PickableMonitor[]; page?: StatusPage; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [slug, setSlug] = useState("")
  const [title, setTitle] = useState("")
  const [ids, setIds] = useState<string[]>([])
  const [published, setPublished] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setSlug(page?.slug ?? "")
    setTitle(page?.title ?? "")
    setIds(page?.monitor_ids ?? [])
    setPublished(page?.published ?? false)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const body: Record<string, unknown> = { title: title.trim(), monitor_ids: ids, published }
    if (!page) body.slug = slug.trim()
    try {
      await apiFetch(page ? `/api/teams/${team}/status-pages/${page.id}` : `/api/teams/${team}/status-pages`, {
        method: page ? "PATCH" : "POST",
        body,
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
        <DialogTrigger render={<Button size="sm" variant={page ? "secondary" : "primary"} />}>
          {page ? <PencilIcon /> : <PlusIcon />}
          {page ? t("lists.edit") : t("statusPages.add")}
        </DialogTrigger>
      )}
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{page ? page.title : t("statusPages.add")}</DialogTitle>
            <DialogDescription>{t("statusPages.monitorsHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="sp-title">{t("statusPages.title")}</FieldLabel>
              <Input id="sp-title" dir="auto" required maxLength={120} value={title} onChange={(e) => setTitle(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="sp-slug">{t("statusPages.slug")}</FieldLabel>
              <Input
                id="sp-slug"
                dir="ltr"
                required
                maxLength={63}
                pattern="[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?"
                disabled={!!page}
                value={slug}
                onChange={(e) => setSlug(e.target.value.toLowerCase())}
              />
              <FieldDescription>{t("statusPages.slugHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel>{t("statusPages.monitors")}</FieldLabel>
              {monitors.length === 0 ? (
                <FieldDescription>{t("statusPages.noMonitors")}</FieldDescription>
              ) : (
                <div className="flex max-h-48 flex-col gap-2 overflow-y-auto">
                  {monitors.map((m) => (
                    <label key={m.id} className="flex items-center gap-2 text-sm">
                      <Checkbox
                        checked={ids.includes(m.id)}
                        onCheckedChange={(v) => setIds((cur) => (v === true ? [...cur, m.id] : cur.filter((x) => x !== m.id)))}
                      />
                      <span dir="auto">{m.label}</span>
                    </label>
                  ))}
                </div>
              )}
            </Field>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={published} onCheckedChange={(v) => setPublished(v === true)} />
              {t("statusPages.published")}
            </label>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !title.trim() || !slug.trim()}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
