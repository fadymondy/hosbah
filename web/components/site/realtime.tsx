"use client"

import { Badge, Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, toast } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react"
import { RadioIcon, SparklesIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow } from "@/components/states"







import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useTeam, useTeamBase } from "@/lib/team"

type Kind = "reverb" | "socketio" | "ws" | "go" | "custom"
type Config = { enabled: boolean; kind: Kind; process: string; port: number; path: string; whole_site?: boolean }
export type RealtimeStatus = {
  config: Config | null
  suggestion: (Omit<Config, "enabled"> & { confidence: "detected" | "likely" | "unknown"; evidence: string[] }) | null
  routed: boolean
  process_running: boolean
  declared_processes: string[]
  hostnames: string[]
  connections: number | null
  connections_reason?: string | null
  note?: string | null
}

const KINDS: Kind[] = ["reverb", "socketio", "ws", "go", "custom"]
const DEFAULTS: Record<Kind, { port: number; path: string }> = {
  reverb: { port: 8080, path: "/app" },
  socketio: { port: 3001, path: "/socket.io" },
  ws: { port: 8080, path: "/ws" },
  go: { port: 8080, path: "/ws" },
  custom: { port: 8080, path: "/ws" },
}

/**
 * Realtime: the site's socket server runs as one of its worker processes; Caddy sends a path of
 * the site's hostnames to it with websocket settings. Keys and allowed origins are the site's own
 * environment variables, so they are set there, not stored twice.
 */
export function SiteRealtime() {
  const { t } = useI18n()
  const { manage } = useTeam()
  const teamBase = useTeamBase()
  const errorText = useErrorText()
  const { app, path } = useLoadedSite()
  const [st, setSt] = useState<RealtimeStatus | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setSt(await apiFetch<RealtimeStatus>(`${path}/realtime`))
      setError(null)
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
  }, [load])

  const base = `${teamBase}/apps/${encodeURIComponent(app.name)}`
  const sg = st?.suggestion

  return (
    <>
      <section aria-labelledby="rt-h">
        <SectionTitle
          id="rt-h"
          title={t("site.nav.realtime")}
          hint={t("site.planned.realtime.hint")}
          action={
            st?.config ? (
              <span className="flex flex-wrap items-center gap-2">
                <Badge variant={st.routed ? "brand" : "outline"}>{st.routed ? t("site.realtime.routed") : t("site.realtime.notRouted")}</Badge>
                <Badge variant={st.process_running ? "neutral" : "outline"}>{st.process_running ? t("site.realtime.running") : t("site.realtime.notRunning")}</Badge>
              </span>
            ) : null
          }
        />
        <ErrorBand message={error} />
        {st === null ? (
          error ? null : <LoadingRows rows={3} />
        ) : (
          <>
            {!st.config && sg && sg.confidence !== "unknown" ? (
              <p className="flex flex-wrap items-center gap-2 nq-band bg-card py-3 text-sm">
                <SparklesIcon className="size-4 text-nq-brand" />
                {t("site.realtime.suggested", { kind: t(`site.realtime.kinds.${sg.kind}`) })}
                <span dir="ltr" className="font-mono text-xs text-muted-foreground">
                  {sg.evidence.join(" · ")}
                </span>
              </p>
            ) : null}
            {st.hostnames.length && st.config ? (
              <div className="flex flex-col gap-2 nq-band py-4">
                <span className="eyebrow">{t("site.realtime.endpoints")}</span>
                {st.hostnames.map((h) => (
                  <CopyField key={h} value={h} />
                ))}
              </div>
            ) : null}
            <RealtimeForm path={path} st={st} manage={manage} onSaved={load} onError={setError} />
          </>
        )}
      </section>

      <section aria-labelledby="rtn-h">
        <SectionTitle id="rtn-h" title={t("site.realtime.notes")} />
        <ul className="flex flex-col gap-2 nq-band py-4 text-sm text-nq-fg-body">
          <li className="flex items-start gap-2">
            <RadioIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            {t("site.realtime.noteListen")}
          </li>
          <li className="flex items-start gap-2">
            <RadioIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            {t("site.realtime.noteDeploy")}
          </li>
          <li className="flex items-start gap-2">
            <RadioIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            <span>
              {t("site.realtime.noteKeys")}{" "}
              <Link href={`${base}/environment`} className="text-nq-brand underline-offset-4 hover:underline">
                {t("site.nav.environment")}
              </Link>
            </span>
          </li>
          <li className="flex items-start gap-2">
            <RadioIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            {t("site.realtime.noteConnections")}
          </li>
        </ul>
      </section>
    </>
  )
}

/** Remove the realtime route (plain confirmation). Also opened from the overview canvas. */
export function RemoveRealtime({ path, onRemoved, size, ...controlled }: { path: string; onRemoved: () => Promise<void> | void; size?: "sm" | "md" } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      {...controlled}
      size={size}
      icon={<Trash2Icon />}
      label={t("site.storage.remove")}
      title={t("site.realtime.removeTitle")}
      description={t("site.realtime.removeBody")}
      confirmLabel={t("site.storage.remove")}
      onConfirm={async () => {
        try {
          await apiFetch(`${path}/realtime`, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onRemoved()
      }}
    />
  )
}

/**
 * The realtime configuration form, filled from the saved configuration or else the suggestion.
 * layout "section" is the Realtime page's rows; "dialog" is the overview canvas's add form.
 */
export function RealtimeForm({
  path,
  st,
  manage,
  onSaved,
  onError,
  layout = "section",
  onCancel,
}: {
  path: string
  st: RealtimeStatus
  manage: boolean
  onSaved: () => Promise<void> | void
  onError: (message: string | null) => void
  layout?: "section" | "dialog"
  onCancel?: () => void
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [busy, setBusy] = useState(false)
  const [enabled, setEnabled] = useState(true)
  const [kind, setKind] = useState<Kind>("reverb")
  const [process, setProcess] = useState("")
  const [port, setPort] = useState("8080")
  const [route, setRoute] = useState("/app")

  useEffect(() => {
    const c: Partial<Config> = st.config ?? st.suggestion ?? {}
    if (c.enabled !== undefined) setEnabled(c.enabled)
    if (c.kind) setKind(c.kind)
    if (c.process !== undefined) setProcess(c.process)
    if (c.port) setPort(String(c.port))
    if (c.path) setRoute(c.path)
  }, [st])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    onError(null)
    try {
      const r = await apiFetch<RealtimeStatus & { applies: "now" | "next_deploy" }>(`${path}/realtime`, {
        method: "PUT",
        body: { enabled, kind, process: process.trim(), port: Number(port), path: route.trim() },
      })
      toast.success(r.applies === "now" ? t("site.realtime.appliedNow") : t("site.realtime.appliesNext"))
      await onSaved()
    } catch (err) {
      onError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  const fields = (
    <>
      <Row layout={layout} label={t("lists.kind")}>
        <Select
          value={kind}
          onValueChange={(v) => {
            if (!v) return
            const k = v as Kind
            setKind(k)
            if (!st.config) {
              setPort(String(DEFAULTS[k].port))
              setRoute(DEFAULTS[k].path)
            }
          }}
        >
          <SelectTrigger className={layout === "section" ? "w-full sm:w-72" : "w-full"} aria-label={t("lists.kind")}>
            <SelectValue>{(v: string) => t(`site.realtime.kinds.${v}`)}</SelectValue>
          </SelectTrigger>
          <SelectContent>
            {KINDS.map((k) => (
              <SelectItem key={k} value={k}>
                {t(`site.realtime.kinds.${k}`)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Row>
      <Row layout={layout} label={t("site.process")} htmlFor={`rt-proc-${layout}`} hint={t("site.realtime.processHint")}>
        <Input
          id={`rt-proc-${layout}`}
          dir="ltr"
          required
          list={`rt-procs-${layout}`}
          placeholder="reverb"
          value={process}
          onChange={(e) => setProcess(e.target.value)}
          className="font-mono"
        />
        <datalist id={`rt-procs-${layout}`}>
          {st.declared_processes.map((p) => (
            <option key={p} value={p.replace(/^worker:/, "")} />
          ))}
        </datalist>
      </Row>
      <Row layout={layout} label={t("lists.port")} htmlFor={`rt-port-${layout}`} hint={t("site.realtime.portHint")}>
        <Input
          id={`rt-port-${layout}`}
          dir="ltr"
          type="number"
          min={1}
          max={65535}
          required
          value={port}
          onChange={(e) => setPort(e.target.value)}
          className={layout === "section" ? "w-full sm:w-40" : "w-full"}
        />
      </Row>
      <Row layout={layout} label={t("site.realtime.path")} htmlFor={`rt-path-${layout}`} hint={t("site.realtime.pathHint")}>
        <Input id={`rt-path-${layout}`} dir="ltr" required value={route} onChange={(e) => setRoute(e.target.value)} className="font-mono" />
      </Row>
      <Row layout={layout} label={t("site.monitors.enabled")}>
        <label className="flex items-center gap-2 text-sm">
          <Checkbox checked={enabled} onCheckedChange={(v) => setEnabled(v === true)} />
          {t("site.realtime.enabledHint")}
        </label>
      </Row>
    </>
  )

  return (
    <form onSubmit={save} className={layout === "dialog" ? "flex flex-col gap-4" : undefined}>
      <fieldset disabled={!manage || busy} className="contents">
        {layout === "dialog" ? <div className="flex w-full flex-col gap-5">{fields}</div> : fields}
        {manage ? (
          <div className={layout === "section" ? "flex flex-wrap justify-end gap-2 nq-band py-4" : "flex flex-wrap justify-end gap-2"}>
            {onCancel ? (
              <Button type="button" variant="secondary" onClick={onCancel}>
                {t("common.cancel")}
              </Button>
            ) : null}
            {st.config && layout === "section" ? <RemoveRealtime path={path} size="md" onRemoved={onSaved} /> : null}
            <Button variant="primary" type="submit" disabled={busy || !process.trim()}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </div>
        ) : null}
      </fieldset>
    </form>
  )
}

/** One field of RealtimeForm: a settings row on the page, a stacked field in a dialog. */
function Row({ layout, label, hint, htmlFor, children }: { layout: "section" | "dialog"; label: string; hint?: string; htmlFor?: string; children: ReactNode }) {
  return layout === "section" ? (
    <SettingsRow label={label} hint={hint} htmlFor={htmlFor}>
      {children}
    </SettingsRow>
  ) : (
    <Field>
      <FieldLabel htmlFor={htmlFor}>{label}</FieldLabel>
      {children}
      {hint ? <FieldDescription>{hint}</FieldDescription> : null}
    </Field>
  )
}

/** The realtime form in a dialog, opened from the overview canvas. */
export function RealtimeDialog({ path, onSaved, ...controlled }: { path: string; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [st, setSt] = useState<RealtimeStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen } = useDialogOpen(controlled, () => {
    setSt(null)
    setError(null)
    apiFetch<RealtimeStatus>(`${path}/realtime`)
      .then(setSt)
      .catch((err) => setError(errorText(err)))
  })

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("site.nav.realtime")}</DialogTitle>
          <DialogDescription>{t("site.planned.realtime.hint")}</DialogDescription>
        </DialogHeader>
        {st ? (
          <RealtimeForm
            path={path}
            st={st}
            manage
            layout="dialog"
            onError={setError}
            onCancel={() => setOpen(false)}
            onSaved={async () => {
              setOpen(false)
              await onSaved()
            }}
          />
        ) : error ? null : (
          <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
        )}
        <FormError message={error} />
      </DialogContent>
    </Dialog>
  )
}
