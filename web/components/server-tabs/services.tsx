"use client"

import { useCallback, useEffect, useState } from "react"
import { PauseIcon, PlayIcon, RefreshCwIcon, RotateCwIcon, SquareIcon } from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import type { ServiceAction, ServiceUnit } from "@/lib/scripts"
import { useTeam } from "@/lib/team"

/** systemd .service units of one server (MH-576 D3): a read-only list plus start/stop/restart/
 * reload of one unit. The agent's own unit is always refused, on both sides. */
export function ServerServices({ team, serverId }: { team: string; serverId: string }) {
  const { t } = useI18n()
  const { team: teamCtx, manage } = useTeam()
  const operate = canOperate(teamCtx.role)
  const errorText = useErrorText()
  const [units, setUnits] = useState<ServiceUnit[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const base = `/api/teams/${team}/servers/${serverId}/services`

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ units: ServiceUnit[] }>(`${base}${q.trim() ? `?query=${encodeURIComponent(q.trim())}` : ""}`)
      setUnits(r.units ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setUnits((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base, q])

  useEffect(() => {
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  async function act(unit: ServiceUnit, action: ServiceAction) {
    setBusy(unit.name)
    setError(null)
    try {
      await apiFetch(`${base}/action`, { method: "POST", body: { unit: unit.name, action } })
      await load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  if (!operate) {
    return (
      <section aria-labelledby="svc-h">
        <SectionTitle id="svc-h" title={t("servers.services.title")} hint={t("servers.services.hint")} />
        <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{t("servers.services.readOnly")}</p>
      </section>
    )
  }

  const shown = (units ?? []).filter((u) => matches(q, u.name, u.description, u.active_state, u.sub_state))

  return (
    <section aria-labelledby="svc-h">
      <SectionTitle
        id="svc-h"
        title={t("servers.services.title")}
        hint={t("servers.services.hint")}
        action={
          <Button size="sm" variant="outline" onClick={() => void load()}>
            <RefreshCwIcon />
            {t("common.refresh")}
          </Button>
        }
      />
      <ErrorBand message={error} />
      {units === null ? (
        <LoadingRows rows={5} />
      ) : units.length === 0 ? (
        <p className="border-b border-line px-6 py-6 text-sm text-grid-muted">{t("servers.services.empty")}</p>
      ) : (
        <>
          <ListSearch value={q} onChange={setQ} placeholder={t("servers.services.search")} shown={shown.length} total={units.length} />
          <div className="border-b border-line [&_tr>*:first-child]:ps-6 [&_tr>*:last-child]:pe-6">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("servers.services.unit")}</TableHead>
                  <TableHead>{t("servers.services.state")}</TableHead>
                  <TableHead>{t("servers.services.enabled")}</TableHead>
                  {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((u) => (
                  <TableRow key={u.name}>
                    <TableCell>
                      <span dir="ltr" className="font-mono text-sm">
                        {u.name}
                      </span>
                      {u.description ? <div className="max-w-xs truncate text-xs text-grid-muted" dir="auto">{u.description}</div> : null}
                    </TableCell>
                    <TableCell>
                      <ServiceStateBadge active={u.active_state} sub={u.sub_state} load={u.load_state} />
                    </TableCell>
                    <TableCell>
                      <Badge variant={u.enabled ? "secondary" : "outline"}>
                        {u.enabled ? t("servers.services.enabledYes") : t("servers.services.enabledNo")}
                      </Badge>
                    </TableCell>
                    {manage ? (
                      <TableCell>
                        <div className="flex flex-wrap justify-end gap-2">
                          <Button size="sm" variant="outline" disabled={busy === u.name} onClick={() => void act(u, "start")}>
                            <PlayIcon />
                            {t("servers.services.start")}
                          </Button>
                          <Button size="sm" variant="outline" disabled={busy === u.name} onClick={() => void act(u, "stop")}>
                            <SquareIcon />
                            {t("servers.services.stop")}
                          </Button>
                          <Button size="sm" variant="outline" disabled={busy === u.name} onClick={() => void act(u, "restart")}>
                            <RotateCwIcon />
                            {t("servers.services.restart")}
                          </Button>
                          <Button size="sm" variant="outline" disabled={busy === u.name} onClick={() => void act(u, "reload")}>
                            <PauseIcon />
                            {t("servers.services.reload")}
                          </Button>
                        </div>
                      </TableCell>
                    ) : null}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </>
      )}
    </section>
  )
}

function ServiceStateBadge({ active, sub, load }: { active: string; sub: string; load: string }) {
  const variant = active === "active" ? "default" : active === "failed" ? "destructive" : "secondary"
  return (
    <span className="flex flex-col items-start gap-0.5">
      <Badge variant={variant}>{active}</Badge>
      <span dir="ltr" className="text-xs text-grid-muted">
        {sub} · {load}
      </span>
    </span>
  )
}
