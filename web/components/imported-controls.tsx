"use client"

import { useCallback, useState, type FormEvent, type ReactNode } from "react"
import {
  ArchiveIcon,
  CameraIcon,
  PlayIcon,
  PowerIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  RotateCwIcon,
  SquareIcon,
  Trash2Icon,
} from "lucide-react"
import { toast } from "sonner"

import { Facts } from "@/components/admin/admin-list"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { OperationLog } from "@/components/op-log"
import { ErrorBand, SectionTitle, SettingsRow } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { apiFetch } from "@/lib/api"
import type { Imported } from "@/lib/apps"
import { humanBytes } from "@/lib/databases"
import { useFormatDate, useI18n } from "@/lib/i18n"
import type { Operation } from "@/lib/servers"

/*
Controls for an imported workload (a Proxmox guest or a container Hosbah found on a server): power,
stats, logs, and for guests resize / snapshots / backup, for containers pull-and-recreate, and
destroying the real workload. Every action is a signed, audited operation the server's agent runs;
its live log shows in the band below the controls.
*/

type Queued = { operation: Operation }

/** Runs a workload action and keeps its operation, so its live log shows in place. */
export function useWorkloadActions(path: string, serverId: string) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [op, setOp] = useState<Operation | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const run = useCallback(
    async (key: string, suffix: string, init: { method?: string; body?: unknown } = { method: "POST" }) => {
      setBusy(key)
      setError(null)
      try {
        const r = await apiFetch<Queued | Operation>(`${path}${suffix}`, { method: init.method ?? "POST", body: init.body })
        const queued = "operation" in (r as Queued) ? (r as Queued).operation : (r as Operation)
        if (queued?.id) setOp(queued)
        toast.success(t("controls.queued"))
        return queued
      } catch (err) {
        setError(errorText(err))
        throw err
      } finally {
        setBusy(null)
      }
    },
    [path, t, errorText],
  )

  const log = op ? (
    <section aria-label={t("controls.lastOperation")}>
      <SectionTitle title={t("controls.lastOperation")} hint={op.kind} />
      <div className="border-b border-line">
        <OperationLog team={path.split("/")[3] ?? ""} serverId={serverId} op={op} />
      </div>
    </section>
  ) : null

  return { run, busy, error, setError, log }
}

/** Start / stop / shutdown / restart, shaped by the current state. */
export function PowerControls({
  item,
  run,
  busy,
  guest,
}: {
  item: Imported
  run: (key: string, suffix: string, init?: { method?: string; body?: unknown }) => Promise<unknown>
  busy: string | null
  guest: boolean
}) {
  const { t } = useI18n()
  const running = item.imported_state !== "stopped"
  const power = async (action: string) => {
    await run(`power:${action}`, "/power", { method: "POST", body: { action } }).catch(() => {})
  }
  return (
    <div className="flex flex-wrap gap-2">
      {!running ? (
        <Button disabled={!!busy} onClick={() => power("start")}>
          <PlayIcon />
          {t("controls.start")}
        </Button>
      ) : (
        <>
          <ConfirmButton
            size="default"
            icon={<RotateCwIcon />}
            label={t("controls.restart")}
            title={t("controls.restartTitle")}
            description={t("controls.restartBody")}
            confirmLabel={t("controls.restart")}
            onConfirm={() => power("restart")}
          />
          {guest ? (
            <ConfirmButton
              size="default"
              icon={<PowerIcon />}
              label={t("controls.shutdown")}
              title={t("controls.shutdownTitle")}
              description={t("controls.shutdownBody")}
              confirmLabel={t("controls.shutdown")}
              onConfirm={() => power("shutdown")}
            />
          ) : null}
          <ConfirmButton
            size="default"
            icon={<SquareIcon />}
            label={guest ? t("controls.forceStop") : t("controls.stop")}
            title={t("controls.stopTitle")}
            description={guest ? t("controls.forceStopBody") : t("controls.stopBody")}
            confirmLabel={guest ? t("controls.forceStop") : t("controls.stop")}
            onConfirm={() => power("stop")}
          />
        </>
      )}
    </div>
  )
}

type Stats = {
  status: string
  cpu_percent: number
  cpus: number
  mem_used_bytes: number
  mem_total_bytes: number
  disk_used_bytes: number
  disk_total_bytes: number
  uptime_seconds: number
}

function uptime(s: number) {
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`
}

/** Live CPU, memory, disk and uptime, asked of the agent on demand. */
export function WorkloadStats({ path }: { path: string }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [stats, setStats] = useState<Stats | null | "none">(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function load() {
    setLoading(true)
    setError(null)
    try {
      const r = await apiFetch<{ stats: Stats | null }>(`${path}/stats?wait=20`)
      setStats(r.stats ?? "none")
    } catch (err) {
      setError(errorText(err))
    } finally {
      setLoading(false)
    }
  }

  const pct = (u: number, total: number) => (total > 0 ? ` (${Math.round((u / total) * 100)}%)` : "")
  return (
    <section aria-labelledby="stats-h">
      <SectionTitle
        id="stats-h"
        title={t("controls.stats")}
        hint={t("controls.statsHint")}
        action={
          <Button variant="outline" size="sm" disabled={loading} onClick={() => void load()}>
            <RefreshCwIcon className={loading ? "animate-spin" : ""} />
            {stats ? t("controls.refresh") : t("controls.loadStats")}
          </Button>
        }
      />
      <ErrorBand message={error} />
      {loading && !stats ? (
        <div className="grid grid-cols-2 gap-px border-b border-line bg-line lg:grid-cols-4">
          {[0, 1, 2, 3].map((i) => (
            <div key={i} className="bg-grid-bg px-6 py-4">
              <Skeleton className="h-10 w-full" />
            </div>
          ))}
        </div>
      ) : stats === "none" ? (
        <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{t("controls.statsNone")}</p>
      ) : stats ? (
        <Facts
          items={[
            [t("controls.cpu"), <span key="c" dir="ltr" className="font-mono">{stats.cpu_percent.toFixed(1)}%{stats.cpus ? ` · ${stats.cpus} vCPU` : ""}</span>],
            [t("imported.facts.memory"), <span key="m" dir="ltr" className="font-mono">{humanBytes(stats.mem_used_bytes)} / {humanBytes(stats.mem_total_bytes)}{pct(stats.mem_used_bytes, stats.mem_total_bytes)}</span>],
            [t("imported.facts.disk"), <span key="d" dir="ltr" className="font-mono">{humanBytes(stats.disk_used_bytes)} / {humanBytes(stats.disk_total_bytes)}{pct(stats.disk_used_bytes, stats.disk_total_bytes)}</span>],
            [t("controls.uptime"), <span key="u" dir="ltr" className="font-mono">{stats.uptime_seconds ? uptime(stats.uptime_seconds) : "—"}</span>],
          ]}
        />
      ) : null}
    </section>
  )
}

/** Recent container logs, optionally followed for a while; the lines stream into the operation log. */
export function WorkloadLogs({ run, busy }: { run: (key: string, suffix: string, init?: { method?: string; body?: unknown }) => Promise<unknown>; busy: string | null }) {
  const { t } = useI18n()
  const [tail, setTail] = useState("200")
  const [follow, setFollow] = useState("0")
  function submit(e: FormEvent) {
    e.preventDefault()
    run("logs", "/workload-logs", { method: "POST", body: { tail: Number(tail) || 200, follow_seconds: Number(follow) || 0 } }).catch(() => {})
  }
  return (
    <form onSubmit={submit}>
      <SectionTitle title={t("controls.logs")} hint={t("controls.logsHint")} />
      <div className="flex flex-wrap items-end gap-3 border-b border-line px-6 py-4">
        <label className="flex flex-col gap-1 text-xs text-grid-muted">
          {t("controls.tail")}
          <Input type="number" min={1} max={2000} dir="ltr" className="w-28 font-mono" value={tail} onChange={(e) => setTail(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1 text-xs text-grid-muted">
          {t("controls.follow")}
          <Input type="number" min={0} max={300} dir="ltr" className="w-28 font-mono" value={follow} onChange={(e) => setFollow(e.target.value)} />
        </label>
        <Button type="submit" disabled={!!busy}>
          {busy === "logs" ? t("common.working") : t("controls.fetchLogs")}
        </Button>
      </div>
    </form>
  )
}

/** Guest resources: vCPUs, memory, and growing the root disk (disks only grow). */
export function ResizeGuest({ run, busy, item }: { run: (key: string, suffix: string, init?: { method?: string; body?: unknown }) => Promise<unknown>; busy: string | null; item: Imported }) {
  const { t } = useI18n()
  const [cores, setCores] = useState(item.origin?.cpus ? String(item.origin.cpus) : "")
  const [memory, setMemory] = useState(item.origin?.mem_bytes ? String(Math.round(item.origin.mem_bytes / 1048576)) : "")
  const [grow, setGrow] = useState("")
  function submit(e: FormEvent) {
    e.preventDefault()
    const body: Record<string, number> = {}
    if (cores && Number(cores) !== item.origin?.cpus) body.cores = Number(cores)
    if (memory && Number(memory) !== Math.round((item.origin?.mem_bytes ?? 0) / 1048576)) body.memory_mb = Number(memory)
    if (grow && Number(grow) > 0) body.disk_grow_gb = Number(grow)
    if (!Object.keys(body).length) return
    run("resize", "/resize", { method: "POST", body }).catch(() => {})
  }
  return (
    <form onSubmit={submit}>
      <SectionTitle title={t("controls.resize")} hint={t("controls.resizeHint")} className="border-t" />
      <SettingsRow label={t("controls.cores")} htmlFor="rs-cores">
        <Input id="rs-cores" type="number" min={1} max={256} dir="ltr" className="w-28 font-mono" value={cores} onChange={(e) => setCores(e.target.value)} />
      </SettingsRow>
      <SettingsRow label={t("controls.memoryMb")} htmlFor="rs-mem">
        <Input id="rs-mem" type="number" min={128} step={128} dir="ltr" className="w-36 font-mono" value={memory} onChange={(e) => setMemory(e.target.value)} />
      </SettingsRow>
      <SettingsRow label={t("controls.diskGrow")} htmlFor="rs-disk" hint={t("controls.diskGrowHint")}>
        <Input id="rs-disk" type="number" min={0} dir="ltr" className="w-28 font-mono" placeholder="0" value={grow} onChange={(e) => setGrow(e.target.value)} />
      </SettingsRow>
      <div className="flex justify-end border-b border-line px-6 py-4">
        <Button type="submit" disabled={!!busy}>
          {busy === "resize" ? t("common.working") : t("controls.applyResize")}
        </Button>
      </div>
    </form>
  )
}

type Snapshot = { name: string; description?: string; parent?: string; snaptime?: number; vmstate?: boolean }

/** Guest snapshots: list, take, roll back, delete (delete needs the site's name typed). */
export function GuestSnapshots({
  path,
  name,
  run,
  busy,
  admin,
}: {
  path: string
  name: string
  run: (key: string, suffix: string, init?: { method?: string; body?: unknown }) => Promise<unknown>
  busy: string | null
  admin: boolean
}) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [list, setList] = useState<Snapshot[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [snapName, setSnapName] = useState("")
  const [note, setNote] = useState("")

  async function load() {
    setLoading(true)
    setError(null)
    try {
      const r = await apiFetch<{ snapshots: Snapshot[] | null }>(`${path}/snapshots?wait=20`)
      setList((r.snapshots ?? []).filter((s) => s.name !== "current"))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setLoading(false)
    }
  }

  function create(e: FormEvent) {
    e.preventDefault()
    run("snap", "/snapshots", { method: "POST", body: { name: snapName.trim(), description: note.trim() || undefined } })
      .then(() => {
        setSnapName("")
        setNote("")
      })
      .catch(() => {})
  }

  const validName = /^[A-Za-z][A-Za-z0-9_-]{1,39}$/.test(snapName.trim())
  return (
    <section aria-labelledby="snaps-h">
      <SectionTitle
        id="snaps-h"
        title={t("controls.snapshots")}
        hint={t("controls.snapshotsHint")}
        className="border-t"
        action={
          <Button variant="outline" size="sm" disabled={loading} onClick={() => void load()}>
            <RefreshCwIcon className={loading ? "animate-spin" : ""} />
            {list ? t("controls.refresh") : t("controls.loadSnapshots")}
          </Button>
        }
      />
      <ErrorBand message={error} />
      {list === null ? null : list.length === 0 ? (
        <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{t("controls.noSnapshots")}</p>
      ) : (
        <ul className="divide-y divide-line border-b border-line">
          {list.map((s) => (
            <li key={s.name} className="flex flex-wrap items-center justify-between gap-3 px-6 py-3">
              <span className="flex min-w-0 flex-col">
                <span dir="ltr" className="font-mono text-sm text-grid-fg">
                  {s.name}
                </span>
                <span className="text-xs text-grid-muted">
                  {s.description ? `${s.description} · ` : ""}
                  <span dir="ltr">{s.snaptime ? fmt(new Date(s.snaptime * 1000).toISOString()) : ""}</span>
                </span>
              </span>
              <span className="flex gap-2">
                <ConfirmButton
                  icon={<RotateCcwIcon />}
                  label={t("controls.rollback")}
                  title={t("controls.rollbackTitle", { snap: s.name })}
                  description={t("controls.rollbackBody", { snap: s.name, name })}
                  confirmLabel={t("controls.rollback")}
                  onConfirm={async () => {
                    await run(`rb:${s.name}`, `/snapshots/${encodeURIComponent(s.name)}/rollback`)
                  }}
                />
                {admin ? (
                  <ConfirmButton
                    icon={<Trash2Icon />}
                    label={t("controls.deleteSnapshot")}
                    title={t("controls.deleteSnapshotTitle", { snap: s.name })}
                    description={t("controls.deleteSnapshotBody", { snap: s.name })}
                    confirmLabel={t("controls.deleteSnapshot")}
                    onConfirm={async () => {
                      await run(`del:${s.name}`, `/snapshots/${encodeURIComponent(s.name)}`, { method: "DELETE", body: { confirm: name } })
                      setList((l) => (l ? l.filter((x) => x.name !== s.name) : l))
                    }}
                  />
                ) : null}
              </span>
            </li>
          ))}
        </ul>
      )}
      <form onSubmit={create} className="flex flex-wrap items-end gap-3 border-b border-line px-6 py-4">
        <label className="flex flex-col gap-1 text-xs text-grid-muted">
          {t("controls.snapshotName")}
          <Input dir="ltr" className="w-48 font-mono" placeholder="before-upgrade" value={snapName} onChange={(e) => setSnapName(e.target.value)} />
        </label>
        <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-grid-muted">
          {t("controls.snapshotNote")}
          <Input value={note} maxLength={200} onChange={(e) => setNote(e.target.value)} />
        </label>
        <Button type="submit" disabled={!!busy || !validName}>
          <CameraIcon />
          {busy === "snap" ? t("common.working") : t("controls.takeSnapshot")}
        </Button>
      </form>
    </section>
  )
}

/** A full backup of the guest (vzdump) to the node's backup storage. */
export function GuestBackup({ run, busy }: { run: (key: string, suffix: string, init?: { method?: string; body?: unknown }) => Promise<unknown>; busy: string | null }) {
  const { t } = useI18n()
  const [storage, setStorage] = useState("")
  return (
    <section>
      <SectionTitle title={t("controls.backup")} hint={t("controls.backupHint")} className="border-t" />
      <div className="flex flex-wrap items-end gap-3 border-b border-line px-6 py-4">
        <label className="flex flex-col gap-1 text-xs text-grid-muted">
          {t("controls.storage")}
          <Input dir="ltr" className="w-48 font-mono" placeholder={t("controls.storageDefault")} value={storage} onChange={(e) => setStorage(e.target.value)} />
        </label>
        <Button
          variant="outline"
          disabled={!!busy}
          onClick={() => run("backup", "/backup", { method: "POST", body: storage.trim() ? { storage: storage.trim() } : {} }).catch(() => {})}
        >
          <ArchiveIcon />
          {busy === "backup" ? t("common.working") : t("controls.backupNow")}
        </Button>
      </div>
    </section>
  )
}

/** Pull the container's image again and recreate it with the same configuration. */
export function RecreateContainer({ run }: { run: (key: string, suffix: string, init?: { method?: string; body?: unknown }) => Promise<unknown> }) {
  const { t } = useI18n()
  return (
    <section>
      <SectionTitle title={t("controls.recreate")} hint={t("controls.recreateHint")} className="border-t" />
      <div className="flex border-b border-line px-6 py-4">
        <ConfirmButton
          size="default"
          icon={<RefreshCwIcon />}
          label={t("controls.recreateNow")}
          title={t("controls.recreateTitle")}
          description={t("controls.recreateBody")}
          confirmLabel={t("controls.recreateNow")}
          onConfirm={async () => {
            await run("recreate", "/recreate")
          }}
        />
      </div>
    </section>
  )
}

/**
 * The danger zone: remove the record only (the workload keeps running), or destroy the workload
 * itself — stopping and deleting the guest or container, with its data if purged. Destroy needs
 * the name typed and an admin; the API refuses it for Hosbah's own workloads.
 */
export function DangerZone({
  name,
  admin,
  guest,
  onForget,
  onDestroy,
}: {
  name: string
  admin: boolean
  guest: boolean
  onForget: () => Promise<void>
  onDestroy: (purge: boolean) => Promise<void>
}) {
  const { t } = useI18n()
  const [typed, setTyped] = useState("")
  const [purge, setPurge] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  return (
    <section aria-labelledby="danger-h">
      <SectionTitle id="danger-h" title={t("controls.danger")} className="border-t" />
      <SettingsRow label={t("imported.removeTitle")} hint={t("imported.removeHint")}>
        <div>
          <ConfirmButton
            size="default"
            icon={<Trash2Icon />}
            label={t("imported.remove")}
            title={t("imported.removeConfirmTitle", { name })}
            description={t("imported.removeConfirmBody", { name })}
            confirmLabel={t("imported.remove")}
            onConfirm={onForget}
          />
        </div>
      </SettingsRow>
      {admin ? (
        <SettingsRow
          label={<span className="text-grid-danger">{t("controls.destroy")}</span>}
          hint={guest ? t("controls.destroyGuestHint") : t("controls.destroyContainerHint")}
        >
          <ErrorBand message={error} />
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)} />
            {guest ? t("controls.purgeGuest") : t("controls.purgeContainer")}
          </label>
          <label className="flex flex-col gap-1 text-xs text-grid-muted">
            {t("controls.typeName", { name })}
            <Input dir="ltr" className="max-w-64 font-mono" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
          </label>
          <div>
            <Button
              variant="destructive"
              disabled={busy || typed !== name}
              onClick={async () => {
                setBusy(true)
                setError(null)
                try {
                  await onDestroy(purge)
                } catch (err) {
                  setError(err instanceof Error ? err.message : String(err))
                } finally {
                  setBusy(false)
                }
              }}
            >
              <Trash2Icon />
              {busy ? t("common.working") : t("controls.destroyNow")}
            </Button>
          </div>
        </SettingsRow>
      ) : null}
    </section>
  )
}

/** The database's admin login, so Hosbah can manage its databases, users and backups. */
export function DatabaseCredentials({ path, onSaved }: { path: string; onSaved: () => void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [database, setDatabase] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<{ verified: boolean; version?: string; detail?: string }>(`${path}/credentials`, {
        method: "POST",
        body: { username: username.trim(), password, database: database.trim() || undefined },
      })
      toast.success(r.verified ? t("controls.credentialsVerified", { version: r.version ?? "" }) : t("controls.credentialsSaved"))
      setPassword("")
      onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit}>
      <SectionTitle title={t("controls.credentials")} hint={t("controls.credentialsHint")} className="border-t" />
      <ErrorBand message={error} />
      <SettingsRow label={t("controls.dbUser")} htmlFor="cr-user">
        <Input id="cr-user" dir="ltr" required autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} />
      </SettingsRow>
      <SettingsRow label={t("controls.dbPassword")} htmlFor="cr-pass">
        <Input id="cr-pass" type="password" dir="ltr" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
      </SettingsRow>
      <SettingsRow label={t("controls.dbName")} htmlFor="cr-db" hint={t("controls.dbNameHint")}>
        <Input id="cr-db" dir="ltr" autoComplete="off" value={database} onChange={(e) => setDatabase(e.target.value)} />
      </SettingsRow>
      <div className="flex justify-end border-b border-line px-6 py-4">
        <Button type="submit" disabled={busy || !username.trim()}>
          {busy ? t("controls.checking") : t("controls.saveCredentials")}
        </Button>
      </div>
    </form>
  )
}

export function Section({ children }: { children: ReactNode }) {
  return <div className="flex flex-col">{children}</div>
}
