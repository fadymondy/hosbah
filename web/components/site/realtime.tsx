"use client"

import Link from "next/link"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { RadioIcon, SparklesIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { useErrorText } from "@/components/form-error"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type Kind = "reverb" | "socketio" | "ws" | "go" | "custom"
type Config = { enabled: boolean; kind: Kind; process: string; port: number; path: string; whole_site?: boolean }
type Status = {
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
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const errorText = useErrorText()
  const { app, path } = useLoadedSite()
  const [st, setSt] = useState<Status | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [enabled, setEnabled] = useState(true)
  const [kind, setKind] = useState<Kind>("reverb")
  const [process, setProcess] = useState("")
  const [port, setPort] = useState("8080")
  const [route, setRoute] = useState("/app")

  const fill = (c: Partial<Config>) => {
    if (c.enabled !== undefined) setEnabled(c.enabled)
    if (c.kind) setKind(c.kind)
    if (c.process !== undefined) setProcess(c.process)
    if (c.port) setPort(String(c.port))
    if (c.path) setRoute(c.path)
  }

  const load = useCallback(async () => {
    try {
      const s = await apiFetch<Status>(`${path}/realtime`)
      setSt(s)
      fill(s.config ?? s.suggestion ?? {})
      setError(null)
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
  }, [load])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<Status & { applies: "now" | "next_deploy" }>(`${path}/realtime`, {
        method: "PUT",
        body: { enabled, kind, process: process.trim(), port: Number(port), path: route.trim() },
      })
      toast.success(r.applies === "now" ? t("site.realtime.appliedNow") : t("site.realtime.appliesNext"))
      await load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  const base = `/${locale}/t/${team.slug}/apps/${encodeURIComponent(app.name)}`
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
                <Badge variant={st.routed ? "default" : "outline"}>{st.routed ? t("site.realtime.routed") : t("site.realtime.notRouted")}</Badge>
                <Badge variant={st.process_running ? "secondary" : "outline"}>{st.process_running ? t("site.realtime.running") : t("site.realtime.notRunning")}</Badge>
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
              <p className="flex flex-wrap items-center gap-2 border-b border-line bg-grid-card px-6 py-3 text-sm">
                <SparklesIcon className="size-4 text-grid-brand" />
                {t("site.realtime.suggested", { kind: t(`site.realtime.kinds.${sg.kind}`) })}
                <span dir="ltr" className="font-mono text-xs text-grid-muted">
                  {sg.evidence.join(" · ")}
                </span>
              </p>
            ) : null}
            {st.hostnames.length && st.config ? (
              <div className="flex flex-col gap-2 border-b border-line px-6 py-4">
                <span className="grid-micro">{t("site.realtime.endpoints")}</span>
                {st.hostnames.map((h) => (
                  <CopyField key={h} value={h} />
                ))}
              </div>
            ) : null}
            <form onSubmit={save}>
              <fieldset disabled={!manage || busy} className="contents">
                <SettingsRow label={t("lists.kind")}>
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
                    <SelectTrigger className="w-full sm:w-72" aria-label={t("lists.kind")}>
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
                </SettingsRow>
                <SettingsRow label={t("site.process")} htmlFor="rt-proc" hint={t("site.realtime.processHint")}>
                  <Input id="rt-proc" dir="ltr" required list="rt-procs" placeholder="reverb" value={process} onChange={(e) => setProcess(e.target.value)} className="font-mono" />
                  <datalist id="rt-procs">
                    {st.declared_processes.map((p) => (
                      <option key={p} value={p.replace(/^worker:/, "")} />
                    ))}
                  </datalist>
                </SettingsRow>
                <SettingsRow label={t("lists.port")} htmlFor="rt-port" hint={t("site.realtime.portHint")}>
                  <Input id="rt-port" dir="ltr" type="number" min={1} max={65535} required value={port} onChange={(e) => setPort(e.target.value)} className="w-full sm:w-40" />
                </SettingsRow>
                <SettingsRow label={t("site.realtime.path")} htmlFor="rt-path" hint={t("site.realtime.pathHint")}>
                  <Input id="rt-path" dir="ltr" required value={route} onChange={(e) => setRoute(e.target.value)} className="font-mono" />
                </SettingsRow>
                <SettingsRow label={t("site.monitors.enabled")}>
                  <label className="flex items-center gap-2 text-sm">
                    <Checkbox checked={enabled} onCheckedChange={(v) => setEnabled(v === true)} />
                    {t("site.realtime.enabledHint")}
                  </label>
                </SettingsRow>
                {manage ? (
                  <div className="flex flex-wrap justify-end gap-2 border-b border-line px-6 py-4">
                    {st.config ? (
                      <ConfirmButton
                        size="default"
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
                          await load()
                        }}
                      />
                    ) : null}
                    <Button type="submit" disabled={busy || !process.trim()}>
                      {busy ? t("common.working") : t("common.save")}
                    </Button>
                  </div>
                ) : null}
              </fieldset>
            </form>
          </>
        )}
      </section>

      <section aria-labelledby="rtn-h">
        <SectionTitle id="rtn-h" title={t("site.realtime.notes")} />
        <ul className="flex flex-col gap-2 border-b border-line px-6 py-4 text-sm text-grid-body">
          <li className="flex items-start gap-2">
            <RadioIcon className="mt-0.5 size-4 shrink-0 text-grid-muted" />
            {t("site.realtime.noteListen")}
          </li>
          <li className="flex items-start gap-2">
            <RadioIcon className="mt-0.5 size-4 shrink-0 text-grid-muted" />
            {t("site.realtime.noteDeploy")}
          </li>
          <li className="flex items-start gap-2">
            <RadioIcon className="mt-0.5 size-4 shrink-0 text-grid-muted" />
            <span>
              {t("site.realtime.noteKeys")}{" "}
              <Link href={`${base}/environment`} className="text-grid-brand underline-offset-4 hover:underline">
                {t("site.nav.environment")}
              </Link>
            </span>
          </li>
          <li className="flex items-start gap-2">
            <RadioIcon className="mt-0.5 size-4 shrink-0 text-grid-muted" />
            {t("site.realtime.noteConnections")}
          </li>
        </ul>
      </section>
    </>
  )
}
