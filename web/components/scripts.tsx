"use client"

import { Badge, Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, Tabs, TabsList, TabsPanel, TabsTab, Textarea, toast, type DataTableColumn } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react"
import { HistoryIcon, PencilIcon, PlayIcon, PlusIcon, TerminalSquareIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { OperationLog } from "@/components/op-log"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { OpStatusBadge } from "@/components/server-status"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"











import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import type { BuiltinScript, Script, ScriptRun, ScriptRunServer } from "@/lib/scripts"
import { canOperate, type Operation, type OperationStatus, type Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

/**
 * The team's script library (MH-576 D5), its built-in catalogue of one-click host software
 * installs (D1), and runs (D4): one shell body, run as root on one or many servers. Reading is any
 * member; writing a script or starting a run is owner/admin; a run's own output is owner, admin or
 * deployer (like a schedule's output, it may hold secrets).
 */
export function TeamScripts() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  const operate = canOperate(team.role)
  const errorText = useErrorText()
  const base = `/api/teams/${team.slug}/scripts`

  const [scripts, setScripts] = useState<Script[] | null>(null)
  const [builtins, setBuiltins] = useState<BuiltinScript[] | null>(null)
  const [servers, setServers] = useState<Server[] | null>(null)
  const [runs, setRuns] = useState<ScriptRun[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [runTarget, setRunTarget] = useState<{ scriptId?: string; builtinName?: string } | null>(null)
  const [openRun, setOpenRun] = useState<{ run: ScriptRun; rows: ScriptRunServer[] } | null>(null)

  const load = useCallback(async () => {
    try {
      const [s, b, sv, r] = await Promise.all([
        apiFetch<{ scripts: Script[] }>(base),
        apiFetch<{ scripts: BuiltinScript[] }>(`${base}/builtin`),
        apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`),
        apiFetch<{ runs: ScriptRun[] }>(`${base}/runs`),
      ])
      setScripts(s.scripts ?? [])
      setBuiltins(b.scripts ?? [])
      setServers(sv.servers ?? [])
      setRuns(r.runs ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setScripts((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, team.slug])

  useEffect(() => {
    void load()
    const id = setInterval(load, 15_000)
    return () => clearInterval(id)
  }, [load])

  async function afterRunStarted(run: ScriptRun, rows: ScriptRunServer[]) {
    setRunTarget(null)
    setOpenRun({ run, rows })
    toast.success(t("scripts.started"))
    await load()
  }

  const scriptColumns = useMemo<DataTableColumn<Script>[]>(
    () => [
      {
        id: "name",
        header: t("scripts.name"),
        label: t("scripts.name"),
        sortValue: (s) => s.name,
        searchValue: (s) => `${s.name} ${s.description ?? ""}`,
        cell: (s) => (
          <span className="block min-w-0">
            <span dir="auto" className="block truncate font-medium">
              {s.name}
            </span>
            {s.description ? (
              <span dir="auto" className="block max-w-96 truncate text-caption text-muted-foreground">
                {s.description}
              </span>
            ) : null}
          </span>
        ),
      },
      { id: "run_as", header: t("scripts.runAs"), label: t("scripts.runAs"), sortValue: (s) => s.run_as, cell: (s) => <Badge variant="outline">{s.run_as}</Badge> },
      {
        id: "timeout",
        header: t("scripts.timeout"),
        label: t("scripts.timeout"),
        sortValue: (s) => s.timeout_seconds,
        className: "max-md:hidden",
        headerClassName: "max-md:hidden",
        cell: (s) => (
          <span dir="ltr" className="text-caption text-muted-foreground">
            {s.timeout_seconds}s
          </span>
        ),
      },
      {
        id: "actions",
        header: <span className="sr-only">{t("common.actions")}</span>,
        label: t("common.actions"),
        cell: (s) =>
          manage ? (
            <div className="flex flex-wrap justify-end gap-2">
              <Button variant="primary" size="sm" onClick={() => setRunTarget({ scriptId: s.id })}>
                <PlayIcon />
                {t("scripts.run")}
              </Button>
              <ScriptDialog path={base} script={s} onSaved={load} />
              <DeleteScript path={base} script={s} onDeleted={load} />
            </div>
          ) : null,
      },
    ],
    [t, manage, base, load],
  )
  const builtinColumns = useMemo<DataTableColumn<BuiltinScript>[]>(
    () => [
      {
        id: "name",
        header: t("scripts.name"),
        label: t("scripts.name"),
        sortValue: (b) => b.name,
        searchValue: (b) => `${b.name} ${b.description ?? ""}`,
        cell: (b) => (
          <span className="block min-w-0">
            <span className="block truncate font-medium">{b.name}</span>
            <span className="block max-w-[40rem] truncate text-caption text-muted-foreground">{b.description}</span>
          </span>
        ),
      },
    ],
    [t],
  )
  const enrolledServers = (servers ?? []).filter((s) => s.status === "enrolled")

  return (
    <>
      <section aria-labelledby="scripts-h">
        <SectionTitle
          id="scripts-h"
          title={t("scripts.title")}
          hint={t("scripts.hint")}
          action={manage ? <ScriptDialog path={base} onSaved={load} /> : null}
        />
        <ErrorBand message={error} />
        {scripts === null ? (
          <LoadingRows rows={3} />
        ) : scripts.length === 0 ? (
          <EmptyState className="border-b" icon={TerminalSquareIcon} title={t("scripts.emptyTitle")} description={t("scripts.emptyHint")} actions={<>{manage ? <ScriptDialog path={base} onSaved={load} /> : null}</>} />
        ) : (
          <NasaqTable
            data={scripts}
            columns={scriptColumns}
            rowId={(s) => s.id}
            label={t("scripts.title")}
            rowLabel={(s) => s.name}
            search={t("scripts.search")}
            empty={t("home.noMatch")}
          />
        )}
      </section>

      <section aria-labelledby="builtins-h">
        <SectionTitle id="builtins-h" title={t("scripts.builtins.title")} hint={t("scripts.builtins.hint")} />
        {builtins === null ? (
          <LoadingRows rows={3} />
        ) : (
          <NasaqTable
            data={builtins}
            columns={builtinColumns}
            rowId={(b) => b.id}
            label={t("scripts.builtins.title")}
            rowLabel={(b) => b.name}
            search={t("scripts.search")}
            rowActions={manage ? (b) => [{ id: "run", label: t("scripts.run"), icon: PlayIcon, onSelect: () => setRunTarget({ builtinName: b.id }) }] : undefined}
            empty={t("home.noMatch")}
          />
        )}
      </section>

      <section aria-labelledby="runs-h">
        <SectionTitle id="runs-h" title={t("scripts.runs.title")} hint={t("scripts.runs.hint")} />
        {runs === null ? (
          <LoadingRows rows={3} />
        ) : runs.length === 0 ? (
          <p className="border-b border-border px-6 py-6 text-sm text-muted-foreground">{t("scripts.runs.empty")}</p>
        ) : (
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("scripts.runs.of")}</TableHead>
                  <TableHead>{t("scripts.runs.status")}</TableHead>
                  <TableHead>{t("scripts.runs.when")}</TableHead>
                  <TableHead className="text-end">{t("common.actions")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {runs.map((r) => (
                  <RunRow key={r.id} run={r} base={base} scripts={scripts ?? []} canRead={operate} onOpen={setOpenRun} />
                ))}
              </TableBody>
            </Table>
          </TableBand>
        )}
      </section>

      {runTarget ? (
        <RunDialog
          path={base}
          scripts={scripts ?? []}
          builtins={builtins ?? []}
          servers={enrolledServers}
          target={runTarget}
          onClose={() => setRunTarget(null)}
          onStarted={afterRunStarted}
        />
      ) : null}
      {openRun ? <RunView team={team.slug} run={openRun.run} rows={openRun.rows} onClose={() => setOpenRun(null)} /> : null}
    </>
  )
}

function RunRow({
  run,
  base,
  scripts,
  canRead,
  onOpen,
}: {
  run: ScriptRun
  base: string
  scripts: Script[]
  canRead: boolean
  onOpen: (v: { run: ScriptRun; rows: ScriptRunServer[] }) => void
}) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [busy, setBusy] = useState(false)
  const name = run.builtin_name ? t(`scripts.builtins.names.${run.builtin_name}`, undefined, run.builtin_name) : scripts.find((s) => s.id === run.script_id)?.name || run.script_id

  async function open() {
    if (!canRead) return
    setBusy(true)
    try {
      const r = await apiFetch<{ run: ScriptRun; servers: ScriptRunServer[] }>(`${base}/runs/${run.id}`)
      onOpen({ run: r.run, rows: r.servers })
    } catch (err) {
      toast.error(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <TableRow>
      <TableCell dir="auto">{name}</TableCell>
      <TableCell>
        <OpStatusBadge status={run.status as OperationStatus} />
      </TableCell>
      <TableCell dir="ltr" className="text-start text-xs text-muted-foreground">
        {fmt(run.created_at)}
      </TableCell>
      <TableCell>
        <div className="flex justify-end">
          <Button size="sm" variant="secondary" disabled={busy || !canRead} onClick={() => void open()}>
            <HistoryIcon />
            {t("scripts.runs.view")}
          </Button>
        </div>
      </TableCell>
    </TableRow>
  )
}

/** A run's live per-server output: one tab per target server, each streamed over the same
 * team-scoped operation SSE every other operation log uses (never a bespoke stream). */
function RunView({ team, run, rows, onClose }: { team: string; run: ScriptRun; rows: ScriptRunServer[]; onClose: () => void }) {
  const { t } = useI18n()
  const [tab, setTab] = useState(rows[0]?.server_id ?? "")

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90dvh] max-w-3xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {t("scripts.runs.title")}
            <OpStatusBadge status={run.status as OperationStatus} />
          </DialogTitle>
          <DialogDescription>{t("scripts.runs.viewHint")}</DialogDescription>
        </DialogHeader>
        {rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("scripts.runs.empty")}</p>
        ) : (
          <Tabs value={tab} onValueChange={(v) => setTab(v as string)}>
            <TabsList>
              {rows.map((rs) => (
                <TabsTab key={rs.server_id} value={rs.server_id} dir="ltr">
                  {rs.server_id.slice(0, 8)}
                  <OpStatusBadge status={rs.status as OperationStatus} />
                </TabsTab>
              ))}
            </TabsList>
            {rows.map((rs) => (
              <TabsPanel key={rs.server_id} value={rs.server_id}>
                {rs.operation_id ? (
                  <OperationLog team={team} serverId={rs.server_id} op={runServerAsOperation(rs)} />
                ) : (
                  <pre dir="ltr" className="max-h-80 overflow-auto p-4 text-start font-mono text-xs whitespace-pre-wrap">
                    {rs.output || t("scripts.runs.noOutput")}
                  </pre>
                )}
              </TabsPanel>
            ))}
          </Tabs>
        )}
      </DialogContent>
    </Dialog>
  )
}

/** OperationLog only needs id/kind/status/timestamps to render — everything else comes live over
 * its own SSE subscription, so a script_run_servers row is enough to feed it without another fetch. */
function runServerAsOperation(rs: ScriptRunServer): Operation {
  return {
    id: rs.operation_id as string,
    server_id: rs.server_id,
    kind: "script.run",
    executor: "agent",
    status: rs.status as OperationStatus,
    params: {},
    error: null,
    exit_code: rs.exit_code,
    created_at: rs.created_at,
    started_at: rs.started_at,
    finished_at: rs.finished_at,
  }
}

/** Delete a script (its run history is kept). */
function DeleteScript({ path, script, onDeleted }: { path: string; script: Script; onDeleted: () => Promise<void> | void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      icon={<Trash2Icon />}
      label={t("common.delete")}
      title={t("scripts.deleteTitle", { name: script.name })}
      description={t("scripts.deleteBody")}
      confirmLabel={t("common.delete")}
      onConfirm={async () => {
        try {
          await apiFetch(`${path}/${script.id}`, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onDeleted()
      }}
    />
  )
}

/** Create or edit a script: name, description, body (a monospace editor, always LTR), run-as and
 * timeout. */
function ScriptDialog({ path, script, onSaved, ...controlled }: { path: string; script?: Script; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [body, setBody] = useState("")
  const [runAs, setRunAs] = useState<"root" | "app">("root")
  const [timeout, setTimeoutS] = useState("600")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setName(script?.name ?? "")
    setDescription(script?.description ?? "")
    setBody(script?.body ?? "#!/bin/bash\nset -euo pipefail\n\n")
    setRunAs(script?.run_as ?? "root")
    setTimeoutS(String(script?.timeout_seconds ?? 600))
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const bodyIn = { name: name.trim(), description: description.trim(), body, run_as: runAs, timeout_seconds: Number(timeout) || 600 }
    try {
      await apiFetch(script ? `${path}/${script.id}` : path, { method: script ? "PUT" : "POST", body: bodyIn })
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
        <DialogTrigger render={<Button size="sm" variant={script ? "secondary" : "primary"} />}>
          {script ? <PencilIcon /> : <PlusIcon />}
          {script ? t("lists.edit") : t("scripts.add")}
        </DialogTrigger>
      )}
      <DialogContent className="max-h-[90dvh] max-w-2xl overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{script ? script.name : t("scripts.add")}</DialogTitle>
            <DialogDescription>{t("scripts.dialogHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="scr-name">{t("scripts.name")}</FieldLabel>
              <Input id="scr-name" dir="auto" required maxLength={128} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="scr-desc">{t("scripts.description")}</FieldLabel>
              <Input id="scr-desc" dir="auto" maxLength={2000} value={description} onChange={(e) => setDescription(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="scr-body">{t("scripts.body")}</FieldLabel>
              <Textarea
                id="scr-body"
                dir="ltr"
                required
                rows={12}
                value={body}
                onChange={(e) => setBody(e.target.value)}
                className="min-h-56 font-mono text-xs"
              />
              <FieldDescription>{t("scripts.bodyHint")}</FieldDescription>
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="scr-runas">{t("scripts.runAs")}</FieldLabel>
                <Select value={runAs} onValueChange={(v) => setRunAs(v as "root" | "app")}>
                  <SelectTrigger id="scr-runas">
                    <SelectValue>{(v: string) => t(`scripts.runAsValues.${v}`, undefined, v)}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="root">{t("scripts.runAsValues.root")}</SelectItem>
                    <SelectItem value="app">{t("scripts.runAsValues.app")}</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel htmlFor="scr-to">{t("scripts.timeout")}</FieldLabel>
                <Input id="scr-to" dir="ltr" type="number" min={1} max={3600} required value={timeout} onChange={(e) => setTimeoutS(e.target.value)} />
              </Field>
            </div>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !name.trim() || !body.trim()}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Run a team script or a built-in on one or more servers, with the built-in's own params (an
 * allow list checked again server-side). */
function RunDialog({
  path,
  scripts,
  builtins,
  servers,
  target,
  onClose,
  onStarted,
}: {
  path: string
  scripts: Script[]
  builtins: BuiltinScript[]
  servers: Server[]
  target: { scriptId?: string; builtinName?: string }
  onClose: () => void
  onStarted: (run: ScriptRun, rows: ScriptRunServer[]) => Promise<void> | void
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const script = target.scriptId ? scripts.find((s) => s.id === target.scriptId) : undefined
  const builtin = target.builtinName ? builtins.find((b) => b.id === target.builtinName) : undefined
  const [selected, setSelected] = useState<string[]>([])
  const [params, setParams] = useState<Record<string, string>>(() => {
    const init: Record<string, string> = {}
    for (const p of builtin?.params ?? []) init[p.name] = p.default ?? ""
    return init
  })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const title = script?.name ?? (builtin ? t(`scripts.builtins.names.${builtin.id}`, undefined, builtin.name) : "")

  function toggle(id: string) {
    setSelected((cur) => (cur.includes(id) ? cur.filter((s) => s !== id) : [...cur, id]))
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (selected.length === 0) {
      setError(t("scripts.runDialog.pickServer"))
      return
    }
    setBusy(true)
    setError(null)
    try {
      const body: Record<string, unknown> = { servers: selected }
      if (script) body.script_id = script.id
      if (builtin) {
        body.builtin_name = builtin.id
        if (builtin.params?.length) body.params = params
      }
      const r = await apiFetch<{ run: ScriptRun; servers: ScriptRunServer[] }>(`${path}/run`, { method: "POST", body })
      await onStarted(r.run, r.servers)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("scripts.runDialog.title", { name: title })}</DialogTitle>
            <DialogDescription>{t("scripts.runDialog.hint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel>{t("scripts.runDialog.servers")}</FieldLabel>
              {servers.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t("scripts.runDialog.noServers")}</p>
              ) : (
                <div className="flex max-h-48 flex-col gap-2 overflow-y-auto border border-input p-3">
                  {servers.map((s) => (
                    <label key={s.id} className="flex items-center gap-2 text-sm">
                      <Checkbox checked={selected.includes(s.id)} onCheckedChange={() => toggle(s.id)} />
                      <span dir="ltr">{s.name}</span>
                    </label>
                  ))}
                </div>
              )}
            </Field>
            {builtin?.params?.map((p) => (
              <Field key={p.name}>
                <FieldLabel htmlFor={`param-${p.name}`}>{p.label || p.name}</FieldLabel>
                {p.enum?.length ? (
                  <Select value={params[p.name] ?? p.default ?? ""} onValueChange={(v) => setParams((cur) => ({ ...cur, [p.name]: v as string }))}>
                    <SelectTrigger id={`param-${p.name}`}>
                      <SelectValue>{(v: string) => v || p.default}</SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      {p.enum.map((v) => (
                        <SelectItem key={v} value={v}>
                          {v}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                ) : (
                  <Input
                    id={`param-${p.name}`}
                    dir="ltr"
                    value={params[p.name] ?? ""}
                    onChange={(e) => setParams((cur) => ({ ...cur, [p.name]: e.target.value }))}
                  />
                )}
              </Field>
            ))}
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || servers.length === 0}>
              {busy ? t("common.working") : t("scripts.run")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
