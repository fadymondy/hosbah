"use client"

import { Button, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useEffect, useState } from "react"
import { PlayIcon } from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { OperationLog } from "@/components/op-log"
import { useLoadedServer } from "@/components/server-tabs/context"
import { OpStatusBadge } from "@/components/server-status"
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
            <p className="border-b border-border px-6 py-3 text-sm text-muted-foreground">{t("servers.ops.notEnrolled")}</p>
          ) : null
        ) : !operate ? (
          <p className="border-b border-border px-6 py-3 text-sm text-muted-foreground">{t("servers.ops.readOnly")}</p>
        ) : (
          <div className="flex flex-wrap items-center gap-2 border-b border-border px-6 py-3" data-testid="op-actions">
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
          </div>
        )}
        {enrolled && !server.online ? (
          <p className="border-b border-border px-6 py-3 text-sm text-muted-foreground">{t("servers.ops.offlineHint")}</p>
        ) : null}

        <div className="grid border-b border-border lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
          <div className="min-w-0 border-b border-border lg:border-e lg:border-b-0 [&_tr>*:first-child]:ps-6">
            {ops.length === 0 ? (
              <p className="px-6 py-4 text-sm text-muted-foreground">{t("servers.ops.empty")}</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("servers.ops.kind")}</TableHead>
                    <TableHead>{t("servers.ops.status")}</TableHead>
                    <TableHead>{t("servers.ops.started")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {ops.map((o) => (
                    <TableRow
                      key={o.id}
                      data-op={o.id}
                      aria-selected={o.id === selected}
                      className={cn("cursor-pointer", o.id === selected && "bg-secondary")}
                      onClick={() => setSelected(o.id)}
                    >
                      <TableCell>
                        <button type="button" className="text-start font-medium" onClick={() => setSelected(o.id)}>
                          {t(`servers.ops.kinds.${opKey(o.kind)}`, undefined, o.kind)}
                          {o.kind === "provision.step" && typeof o.params.step === "string" ? (
                            <span className="block text-xs font-normal text-muted-foreground">
                              {t(`servers.ops.steps.${opKey(o.params.step)}`, undefined, o.params.step)}
                            </span>
                          ) : null}
                        </button>
                      </TableCell>
                      <TableCell>
                        <OpStatusBadge status={o.status} />
                      </TableCell>
                      <TableCell dir="ltr" className="text-start text-xs">
                        {fmt(o.created_at)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>
          <OperationLog team={team.slug} serverId={server.id} op={selectedOp} />
        </div>
      </section>

  )
}
