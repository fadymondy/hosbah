"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { GlobeIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { FormError, useErrorText } from "@/components/form-error"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { humanBytes } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type Browser = {
  id: string
  name: string
  image: string
  memory_mb: number
  cpus: number
  max_sessions: number
  idle_timeout_seconds: number
  env_prefix: string
  env: string[]
  status: "creating" | "ready" | "failed" | "removing"
  error: string | null
  running: boolean | null
  restarts: number | null
  memory_bytes: number | null
}

/**
 * Browsers: headless Chromium on the site's server, driven over CDP. Its port is open only on
 * the server's private address, behind an access token; the address and token reach the site as
 * sealed variables, from the next deploy.
 */
export function SiteBrowsers() {
  const { t } = useI18n()
  const { manage } = useTeam()
  const errorText = useErrorText()
  const { path } = useLoadedSite()
  const [items, setItems] = useState<Browser[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ browsers: Browser[] }>(`${path}/browsers`)
      setItems(r.browsers ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setItems((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
  }, [load])

  // The server pulls an image and starts a container; look again while that is going on.
  useEffect(() => {
    if (!items?.some((b) => b.status === "creating" || b.status === "removing")) return
    const id = setInterval(load, 5000)
    return () => clearInterval(id)
  }, [items, load])

  const shown = (items ?? []).filter((b) => matches(q, b.name, b.status, b.env_prefix))
  const add = manage ? <AddBrowser path={path} onSaved={load} /> : null

  return (
    <section aria-labelledby="br-h">
      <SectionTitle id="br-h" title={t("site.nav.browsers")} hint={t("site.planned.browsers.hint")} action={add} />
      <ErrorBand message={error} />
      {items === null ? (
        <LoadingRows rows={2} />
      ) : items.length === 0 ? (
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <GlobeIcon />
            </EmptyMedia>
            <EmptyTitle>{t("site.browsers.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("site.browsers.emptyHint")}</EmptyDescription>
          </EmptyHeader>
          {add}
        </Empty>
      ) : (
        <>
          <ListSearch value={q} onChange={setQ} placeholder={t("site.browsers.search")} shown={shown.length} total={items.length} />
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("site.monitors.name")}</TableHead>
                  <TableHead>{t("lists.status")}</TableHead>
                  <TableHead>{t("site.browsers.limits")}</TableHead>
                  <TableHead>{t("site.storage.variables")}</TableHead>
                  {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((b) => (
                  <TableRow key={b.id}>
                    <TableCell>
                      <span dir="ltr" className="font-medium">
                        {b.name}
                      </span>
                      <div dir="ltr" className="max-w-56 truncate text-start font-mono text-[11px] text-grid-muted" title={b.image}>
                        {b.image}
                      </div>
                    </TableCell>
                    <TableCell>
                      <span className="flex flex-wrap items-center gap-1.5">
                        <Badge variant={b.status === "ready" ? "default" : b.status === "failed" ? "destructive" : "secondary"}>{t(`site.storage.status.${b.status}`)}</Badge>
                        {b.status === "ready" && b.running != null ? (
                          <Badge variant={b.running ? "secondary" : "outline"}>{b.running ? t("site.browsers.running") : t("site.browsers.stopped")}</Badge>
                        ) : null}
                      </span>
                      {b.error ? (
                        <p dir="auto" className="mt-1 max-w-xs text-xs text-grid-danger">
                          {b.error}
                        </p>
                      ) : null}
                      {b.restarts ? <p className="mt-1 text-xs text-grid-muted">{t("site.browsers.restarts", { n: b.restarts })}</p> : null}
                    </TableCell>
                    <TableCell className="text-xs">
                      <span dir="ltr">
                        {b.memory_bytes != null ? `${humanBytes(b.memory_bytes)} / ` : ""}
                        {b.memory_mb} MB · {b.cpus} CPU
                      </span>
                      <div className="text-grid-muted">{t("site.browsers.sessions", { n: b.max_sessions, idle: b.idle_timeout_seconds })}</div>
                    </TableCell>
                    <TableCell dir="ltr" className="max-w-64 text-start font-mono text-[11px] text-grid-muted">
                      {b.env.join(" ")}
                    </TableCell>
                    {manage ? (
                      <TableCell>
                        <div className="flex justify-end">
                          <RemoveBrowser
                            name={b.name}
                            onRemove={async () => {
                              await apiFetch(`${path}/browsers/${b.id}`, { method: "DELETE", body: { confirm: b.name } })
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
        </>
      )}
      <ul className="flex flex-col gap-1.5 border-b border-line px-6 py-4 text-xs text-grid-muted">
        <li>{t("site.browsers.noteConnect")}</li>
        <li>{t("site.browsers.notePrivate")}</li>
        <li>{t("site.browsers.noteViewer")}</li>
      </ul>
    </section>
  )
}

function AddBrowser({ path, onSaved }: { path: string; onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [memory, setMemory] = useState("1024")
  const [cpus, setCpus] = useState("1")
  const [sessions, setSessions] = useState("5")
  const [idle, setIdle] = useState("300")
  const [prefix, setPrefix] = useState("BROWSER")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/browsers`, {
        method: "POST",
        body: {
          name: name.trim(),
          memory_mb: Number(memory) || 0,
          cpus: Number(cpus) || 0,
          max_sessions: Number(sessions) || 0,
          idle_timeout_seconds: Number(idle) || 0,
          env_prefix: prefix.trim() || "BROWSER",
        },
      })
      toast.success(t("site.storage.appliesNext"))
      setOpen(false)
      setName("")
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
        {t("site.browsers.add")}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("site.browsers.add")}</DialogTitle>
            <DialogDescription>{t("site.browsers.dialogHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="br-name">{t("site.monitors.name")}</FieldLabel>
              <Input id="br-name" dir="ltr" required pattern="[a-z][a-z0-9-]{0,30}" placeholder="crawler" value={name} onChange={(e) => setName(e.target.value)} />
              <FieldDescription>{t("site.storage.nameHint")}</FieldDescription>
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="br-mem">{t("site.browsers.memory")}</FieldLabel>
                <Input id="br-mem" dir="ltr" type="number" min={256} max={4096} step={64} value={memory} onChange={(e) => setMemory(e.target.value)} />
              </Field>
              <Field>
                <FieldLabel htmlFor="br-cpu">{t("site.browsers.cpus")}</FieldLabel>
                <Input id="br-cpu" dir="ltr" type="number" min={0.25} max={4} step={0.25} value={cpus} onChange={(e) => setCpus(e.target.value)} />
              </Field>
              <Field>
                <FieldLabel htmlFor="br-ses">{t("site.browsers.maxSessions")}</FieldLabel>
                <Input id="br-ses" dir="ltr" type="number" min={1} max={20} value={sessions} onChange={(e) => setSessions(e.target.value)} />
              </Field>
              <Field>
                <FieldLabel htmlFor="br-idle">{t("site.browsers.idle")}</FieldLabel>
                <Input id="br-idle" dir="ltr" type="number" min={30} max={3600} value={idle} onChange={(e) => setIdle(e.target.value)} />
              </Field>
            </div>
            <Field>
              <FieldLabel htmlFor="br-prefix">{t("site.storage.prefix")}</FieldLabel>
              <Input id="br-prefix" dir="ltr" pattern="[A-Z][A-Z0-9_]{0,20}" value={prefix} onChange={(e) => setPrefix(e.target.value.toUpperCase())} className="font-mono" />
              <FieldDescription dir="ltr">{`${prefix || "BROWSER"}_WS_ENDPOINT, ${prefix || "BROWSER"}_TOKEN`}</FieldDescription>
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

function RemoveBrowser({ name, onRemove }: { name: string; onRemove: () => Promise<void> }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await onRemove()
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
            <DialogDescription>{t("site.browsers.removeHint")}</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="brm-confirm">{t("site.storage.typeName", { name })}</FieldLabel>
            <Input id="brm-confirm" dir="ltr" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
          </Field>
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
