"use client"

import { Button, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Card, type DataTableColumn } from "@fadymondy/nasaq/web"
import { useEffect, useState } from "react"
import { PlayIcon } from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { OperationLog } from "@/components/op-log"
import { useLoadedServer } from "@/components/server-tabs/context"
import { OpStatusBadge } from "@/components/server-status"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { SectionTitle } from "@/components/states"



import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { canOperate, opKey, PROVISION_STEPS, type Operation } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/** Typed operations on the server: run one, pick one to read its live log. */
export function ServerOperations() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const operate = canOperate(team.role)
  const { server, ops, setOps, setError, available, path } = useLoadedServer()
  const [selected, setSelected] = useState<string | null>(null)
  const [step, setStep] = useState<string>("")
  const [busy, setBusy] = useState(false)
  useEffect(() => setSelected((cur) => cur ?? ops[0]?.id ?? null), [ops])

  async function run(body: Record<string, unknown>) {
    setBusy(true)
    setError(null)
    try {
      const op = await apiFetch<Operation>(`${path}/operations`, { method: "POST", body })
      setOps((prev) => (prev.some((o) => o.id === op.id) ? prev : [op, ...prev]))
      setSelected(op.id)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  const enrolled = server.status === "enrolled"
  const selectedOp = ops.find((o) => o.id === selected) ?? null

  return (
      <section aria-labelledby="ops-h">
        <SectionTitle id="ops-h" title={t("servers.ops.title")} hint={t("servers.ops.description")} />

        {!enrolled ? (
          server.status !== "provisioning" ? (
            <p className="mt-3 rounded-lg border border-border bg-card px-4 py-3 text-sm text-muted-foreground">{t("servers.ops.notEnrolled")}</p>
          ) : null
        ) : !operate ? (
          <p className="mt-3 rounded-lg border border-border bg-card px-4 py-3 text-sm text-muted-foreground">{t("servers.ops.readOnly")}</p>
        ) : (
          <Card className="mt-3 flex-row flex-wrap items-center gap-2 px-4 py-3" data-testid="op-actions">
            <Button variant="primary" size="sm" disabled={busy} onClick={() => run({ kind: "system.info" })}>
              <PlayIcon />
              {t("servers.ops.systemInfo")}
            </Button>
            <Button size="sm" variant="secondary" disabled={busy} onClick={() => run({ kind: "docker.check" })}>
              <PlayIcon />
              {t("servers.ops.dockerCheck")}
            </Button>
            {manage ? (
              <>
                <Select value={step} onValueChange={(v) => setStep((v as string) ?? "")}>
                  <SelectTrigger className="w-48" aria-label={t("servers.ops.provision")}>
                    <SelectValue>{(v: string) => (v ? t(`servers.ops.steps.${opKey(v)}`) : t("servers.ops.chooseStep"))}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {PROVISION_STEPS.map((s) => (
                      <SelectItem key={s} value={s}>
                        {t(`servers.ops.steps.${opKey(s)}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button size="sm" variant="secondary" disabled={busy || !step} onClick={() => run({ kind: "provision.step", step })}>
                  {t("servers.ops.run")}
                </Button>
                {available && server.agent_version && available !== server.agent_version ? (
                  <Button size="sm" variant="secondary" disabled={busy} onClick={() => run({ kind: "agent.update" })}>
                    {t("servers.ops.update", { version: available })}
                  </Button>
                ) : null}
              </>
            ) : null}
          </Card>
        )}
        {enrolled && !server.online ? (
          <p className="mt-3 rounded-lg border border-border bg-card px-4 py-3 text-sm text-muted-foreground">{t("servers.ops.offlineHint")}</p>
        ) : null}

        <div className="mt-4 grid items-start gap-4 lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
          <div className="min-w-0">
            {ops.length === 0 ? (
              <Card className="px-5 py-8 text-center text-sm text-muted-foreground">{t("servers.ops.empty")}</Card>
            ) : (
              <NasaqTable
                data={ops}
                rowId={(o) => o.id}
                label={t("servers.ops.title")}
                rowLabel={(o) => t(`servers.ops.kinds.${opKey(o.kind)}`, undefined, o.kind)}
                onRowClick={(o) => setSelected(o.id)}
                pageSize={12}
                facets={[
                  {
                    column: "status",
                    title: t("servers.ops.status"),
                    options: [...new Set(ops.map((o) => o.status))].map((v) => ({ value: v, label: t(`servers.ops.statuses.${v}`, undefined, v) })),
                  },
                ]}
                columns={[
                  {
                    id: "kind",
                    header: t("servers.ops.kind"),
                    label: t("servers.ops.kind"),
                    sortValue: (o) => o.kind,
                    cell: (o) => (
                      <span className={cn("flex items-center gap-2", o.id === selected && "text-nq-action")} data-op={o.id} aria-current={o.id === selected ? "true" : undefined}>
                        <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", o.id === selected ? "bg-nq-action" : "bg-transparent")} />
                        <span>
                          <span className="font-medium">{t(`servers.ops.kinds.${opKey(o.kind)}`, undefined, o.kind)}</span>
                          {o.kind === "provision.step" && typeof o.params.step === "string" ? (
                            <span className="block text-caption text-muted-foreground">{t(`servers.ops.steps.${opKey(o.params.step)}`, undefined, o.params.step)}</span>
                          ) : null}
                        </span>
                      </span>
                    ),
                  },
                  { id: "status", header: t("servers.ops.status"), label: t("servers.ops.status"), filterValue: (o) => o.status, sortValue: (o) => o.status, cell: (o) => <OpStatusBadge status={o.status} /> },
                  {
                    id: "started",
                    header: t("servers.ops.started"),
                    label: t("servers.ops.started"),
                    sortValue: (o) => o.created_at,
                    cell: (o) => (
                      <span dir="ltr" className="text-caption text-muted-foreground">
                        {fmt(o.created_at)}
                      </span>
                    ),
                  },
                ] satisfies DataTableColumn<Operation>[]}
              />
            )}
          </div>
          <Card className="min-w-0 gap-0 overflow-hidden py-0">
            <OperationLog team={team.slug} serverId={server.id} op={selectedOp} />
          </Card>
        </div>
      </section>

  )
}
