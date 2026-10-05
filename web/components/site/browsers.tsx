"use client"

import { Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, toast, type DataTableRowAction } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { ExternalLinkIcon, GlobeIcon, MonitorIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"








import { apiFetch } from "@/lib/api"
import { humanBytes } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export type Browser = {
  id: string
  name: string
  kind: "headless" | "desktop"
  image: string
  memory_mb: number
  cpus: number
  max_sessions: number
  idle_timeout_seconds: number
  env_prefix: string
  env: string[]
  hostname: string | null
  status: "creating" | "ready" | "failed" | "removing"
  error: string | null
  running: boolean | null
  restarts: number | null
  memory_bytes: number | null
}

/**
 * Browsers: a headless Chromium the site drives over CDP (its port is open only on the server's
 * private address, behind an access token that reaches the site as sealed variables, from the
 * next deploy), or a full interactive desktop at its own hostname, opened by a team member —
 * Hosbah's own forward_auth guards it, re-checking team membership on every request.
 */
export function SiteBrowsers() {
  const { t } = useI18n()
  const { manage } = useTeam()
  const errorText = useErrorText()
  const { path } = useLoadedSite()
  const [items, setItems] = useState<Browser[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [removing, setRemoving] = useState<Browser | null>(null)
  const opener = useOpenBrowser(path)

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

  const rowActions = (b: Browser): DataTableRowAction[] => [
    ...(b.kind === "desktop"
      ? [{ id: "open", label: t("site.browsers.open"), icon: ExternalLinkIcon, disabled: !(b.status === "ready" && !!b.hostname) || opener.busy, onSelect: () => void opener.open(b) }]
      : []),
    ...(manage ? [{ id: "remove", label: t("site.storage.remove"), icon: Trash2Icon, danger: true, group: "danger", onSelect: () => setRemoving(b) }] : []),
  ]
  const add = manage ? <AddBrowser path={path} onSaved={load} /> : null

  return (
    <section aria-labelledby="br-h">
      <SectionTitle id="br-h" title={t("site.nav.browsers")} hint={t("site.planned.browsers.hint")} action={add} />
      <ErrorBand message={error} />
      {items === null ? (
        <LoadingRows rows={2} />
      ) : items.length === 0 ? (
        <EmptyState className="nq-empty" icon={GlobeIcon} title={t("site.browsers.emptyTitle")} description={t("site.browsers.emptyHint")} actions={<>{add}</>} />
      ) : (
        <NasaqTable
          data={items}
          rowId={(b) => b.id}
          rowLabel={(b) => b.name}
          label={t("site.nav.browsers")}
          search={t("site.browsers.search")}
          rowActions={rowActions}
          onRowClick={(b) => (b.kind === "desktop" && b.status === "ready" && b.hostname ? void opener.open(b) : undefined)}
          columns={[
            {
              id: "name",
              header: t("site.monitors.name"),
              cell: (b) => (
                <>
                  <span className="flex items-center gap-1.5">
                    {b.kind === "desktop" ? <MonitorIcon className="size-3.5 shrink-0 text-muted-foreground" /> : null}
                    <span dir="ltr" className="font-medium">
                      {b.name}
                    </span>
                  </span>
                  <div dir="ltr" className="max-w-56 truncate text-start font-mono text-xs text-muted-foreground" title={b.image}>
                    {b.image}
                  </div>
                </>
              ),
              sortValue: (b) => b.name,
              searchValue: (b) => [b.name, b.status, b.env_prefix].join(" "),
            },
            {
              id: "status",
              header: t("lists.status"),
              cell: (b) => (
                <>
                  <span className="flex flex-wrap items-center gap-1.5">
                    <Badge variant={b.status === "ready" ? "brand" : b.status === "failed" ? "danger" : "neutral"}>{t(`site.storage.status.${b.status}`)}</Badge>
                    {b.status === "ready" && b.running != null ? (
                      <Badge variant={b.running ? "neutral" : "outline"}>{b.running ? t("site.browsers.running") : t("site.browsers.stopped")}</Badge>
                    ) : null}
                  </span>
                  {b.error ? (
                    <p dir="auto" className="mt-1 max-w-xs text-xs text-nq-danger-text">
                      {b.error}
                    </p>
                  ) : null}
                  {b.restarts ? <p className="mt-1 text-xs text-muted-foreground">{t("site.browsers.restarts", { n: b.restarts })}</p> : null}
                </>
              ),
              sortValue: (b) => b.status,
            },
            {
              id: "limits",
              header: t("site.browsers.limits"),
              cell: (b) => (
                <div className="text-xs">
                  <span dir="ltr">
                    {b.memory_bytes != null ? `${humanBytes(b.memory_bytes)} / ` : ""}
                    {b.memory_mb} MB · {b.cpus} CPU
                  </span>
                  {b.kind === "headless" ? <div className="text-muted-foreground">{t("site.browsers.sessions", { n: b.max_sessions, idle: b.idle_timeout_seconds })}</div> : null}
                </div>
              ),
            },
            {
              id: "access",
              header: t("site.browsers.access"),
              cell: (b) =>
                b.kind === "desktop" ? (
                  b.hostname ? (
                    <span dir="ltr" className="block max-w-64 truncate font-mono text-xs text-muted-foreground" title={b.hostname}>
                      {b.hostname}
                    </span>
                  ) : (
                    <span className="text-xs text-muted-foreground">{t("site.browsers.noHostname")}</span>
                  )
                ) : (
                  <span dir="ltr" className="block max-w-64 truncate text-start font-mono text-xs text-muted-foreground">
                    {b.env.join(" ")}
                  </span>
                ),
            },
          ]}
        />
      )}
      {removing ? (
        <RemoveBrowser open onOpenChange={(v) => !v && setRemoving(null)} path={path} browser={removing} onRemoved={load} />
      ) : null}
      <ul className="flex flex-col gap-1.5 nq-band py-4 text-xs text-muted-foreground">
        <li>{t("site.browsers.noteConnect")}</li>
        <li>{t("site.browsers.notePrivate")}</li>
        <li>{t("site.browsers.noteDesktop")}</li>
      </ul>
    </section>
  )
}

/**
 * Add a browser: kind headless (a bare Chromium the app drives over CDP/WebSocket, default) or
 * desktop (a full interactive desktop at its own hostname, opened later from the list). Also
 * opened from the overview canvas.
 */
export function AddBrowser({ path, onSaved, ...controlled }: { path: string; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled)
  const [kind, setKind] = useState<"headless" | "desktop">("headless")
  const [name, setName] = useState("")
  const [memory, setMemory] = useState("1024")
  const [cpus, setCpus] = useState("1")
  const [sessions, setSessions] = useState("5")
  const [idle, setIdle] = useState("300")
  const [prefix, setPrefix] = useState("BROWSER")
  const [hostname, setHostname] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function onKindChange(k: "headless" | "desktop") {
    setKind(k)
    setMemory(k === "desktop" ? "2048" : "1024")
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/browsers`, {
        method: "POST",
        body:
          kind === "desktop"
            ? { name: name.trim(), kind, memory_mb: Number(memory) || 0, cpus: Number(cpus) || 0, hostname: hostname.trim().toLowerCase() || undefined }
            : {
                name: name.trim(),
                kind,
                memory_mb: Number(memory) || 0,
                cpus: Number(cpus) || 0,
                max_sessions: Number(sessions) || 0,
                idle_timeout_seconds: Number(idle) || 0,
                env_prefix: prefix.trim() || "BROWSER",
              },
      })
      toast.success(kind === "desktop" ? t("site.browsers.desktopCreating") : t("site.storage.appliesNext"))
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
      {fromCaller ? null : (
        <DialogTrigger render={<Button variant="primary" size="sm" />}>
          <PlusIcon />
          {t("site.browsers.add")}
        </DialogTrigger>
      )}
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("site.browsers.add")}</DialogTitle>
            <DialogDescription>{kind === "desktop" ? t("site.browsers.dialogHintDesktop") : t("site.browsers.dialogHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="br-kind">{t("site.browsers.kind")}</FieldLabel>
              <Select value={kind} onValueChange={(v) => v && onKindChange(v as "headless" | "desktop")}>
                <SelectTrigger id="br-kind">
                  <SelectValue>{(v: string) => (v === "desktop" ? t("site.browsers.kindDesktop") : t("site.browsers.kindHeadless"))}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="headless">{t("site.browsers.kindHeadless")}</SelectItem>
                  <SelectItem value="desktop">{t("site.browsers.kindDesktop")}</SelectItem>
                </SelectContent>
              </Select>
              <FieldDescription>{kind === "desktop" ? t("site.browsers.kindDesktopHint") : t("site.browsers.kindHeadlessHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="br-name">{t("site.monitors.name")}</FieldLabel>
              <Input id="br-name" dir="ltr" required pattern="[a-z][a-z0-9\-]{0,30}" placeholder="crawler" value={name} onChange={(e) => setName(e.target.value)} />
              <FieldDescription>{t("site.storage.nameHint")}</FieldDescription>
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="br-mem">{t("site.browsers.memory")}</FieldLabel>
                <Input
                  id="br-mem"
                  dir="ltr"
                  type="number"
                  min={kind === "desktop" ? 512 : 256}
                  max={kind === "desktop" ? 8192 : 4096}
                  step={64}
                  value={memory}
                  onChange={(e) => setMemory(e.target.value)}
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="br-cpu">{t("site.browsers.cpus")}</FieldLabel>
                <Input id="br-cpu" dir="ltr" type="number" min={0.25} max={4} step={0.25} value={cpus} onChange={(e) => setCpus(e.target.value)} />
              </Field>
              {kind === "headless" ? (
                <>
                  <Field>
                    <FieldLabel htmlFor="br-ses">{t("site.browsers.maxSessions")}</FieldLabel>
                    <Input id="br-ses" dir="ltr" type="number" min={1} max={20} value={sessions} onChange={(e) => setSessions(e.target.value)} />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="br-idle">{t("site.browsers.idle")}</FieldLabel>
                    <Input id="br-idle" dir="ltr" type="number" min={30} max={3600} value={idle} onChange={(e) => setIdle(e.target.value)} />
                  </Field>
                </>
              ) : null}
            </div>
            {kind === "headless" ? (
              <Field>
                <FieldLabel htmlFor="br-prefix">{t("site.storage.prefix")}</FieldLabel>
                <Input id="br-prefix" dir="ltr" pattern="[A-Z][A-Z0-9_]{0,20}" value={prefix} onChange={(e) => setPrefix(e.target.value.toUpperCase())} className="font-mono" />
                <FieldDescription dir="ltr">{`${prefix || "BROWSER"}_WS_ENDPOINT, ${prefix || "BROWSER"}_TOKEN`}</FieldDescription>
              </Field>
            ) : (
              <Field>
                <FieldLabel htmlFor="br-hostname">{t("site.browsers.hostname")}</FieldLabel>
                <Input id="br-hostname" dir="ltr" placeholder="browser.example.com" value={hostname} onChange={(e) => setHostname(e.target.value)} className="font-mono" />
                <FieldDescription>{t("site.browsers.hostnameHint")}</FieldDescription>
              </Field>
            )}
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

/**
 * Open a desktop browser: mints a one-time link and navigates a new tab to it right away — the
 * link is exchanged automatically for a session on the browser's own hostname, so it must be used
 * immediately, not stored or shared.
 */
/** Opens a desktop browser in a new tab, through a short-lived signed link. */
function useOpenBrowser(path: string) {
  const errorText = useErrorText()
  const [busy, setBusy] = useState(false)

  async function open(browser: Browser) {
    setBusy(true)
    try {
      const r = await apiFetch<{ url: string }>(`${path}/browsers/${browser.id}/open`, { method: "POST" })
      window.open(r.url, "_blank", "noopener,noreferrer")
    } catch (err) {
      toast.error(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return { busy, open }
}

/** Remove a browser: type its name. Also opened from the overview canvas. */
export function RemoveBrowser({ path, browser, onRemoved, ...controlled }: { path: string; browser: Browser; onRemoved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const name = browser.name
  const [typed, setTyped] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, () => {
    setTyped("")
    setError(null)
  })

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/browsers/${browser.id}`, { method: "DELETE", body: { confirm: name } })
      await onRemoved()
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
            <DialogDescription>{t("site.browsers.removeHint")}</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="brm-confirm">{t("site.storage.typeName", { name })}</FieldLabel>
            <Input id="brm-confirm" dir="ltr" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
          </Field>
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
