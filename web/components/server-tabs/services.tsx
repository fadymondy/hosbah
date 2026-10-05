"use client"

import { Badge, Button, Card, type DataTableColumn } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { PauseIcon, PlayIcon, RefreshCwIcon, RotateCwIcon, SquareIcon } from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"



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
  const base = `/api/teams/${team}/servers/${serverId}/services`

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ units: ServiceUnit[] }>(base)
      setUnits(r.units ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setUnits((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

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
        <p className="mt-3 rounded-lg border border-border bg-card px-4 py-3 text-sm text-muted-foreground">{t("servers.services.readOnly")}</p>
      </section>
    )
  }


  return (
    <section aria-labelledby="svc-h">
      <SectionTitle
        id="svc-h"
        title={t("servers.services.title")}
        hint={t("servers.services.hint")}
        action={
          <Button size="sm" variant="secondary" onClick={() => void load()}>
            <RefreshCwIcon />
            {t("common.refresh")}
          </Button>
        }
      />
      <ErrorBand message={error} />
      {units === null ? (
        <LoadingRows rows={5} />
      ) : units.length === 0 ? (
        <Card className="mt-3 px-5 py-8 text-center text-sm text-muted-foreground">{t("servers.services.empty")}</Card>
      ) : (
        <div className="mt-3">
          <NasaqTable
            data={units}
            rowId={(u) => u.name}
            label={t("servers.services.title")}
            rowLabel={(u) => u.name}
            search={t("servers.services.search")}
            empty={t("home.noMatch")}
            pageSize={25}
            facets={[
              { column: "state", title: t("servers.services.state"), options: [...new Set(units.map((u) => u.active_state))].map((v) => ({ value: v, label: v })) },
              {
                column: "enabled",
                title: t("servers.services.enabled"),
                options: [
                  { value: "yes", label: t("servers.services.enabledYes") },
                  { value: "no", label: t("servers.services.enabledNo") },
                ],
              },
            ]}
            rowActions={
              manage
                ? (u) =>
                    (
                      [
                        ["start", PlayIcon],
                        ["stop", SquareIcon],
                        ["restart", RotateCwIcon],
                        ["reload", PauseIcon],
                      ] as const
                    ).map(([action, icon]) => ({
                      id: action,
                      label: t(`servers.services.${action}`),
                      icon,
                      disabled: busy === u.name,
                      onSelect: () => void act(u, action),
                    }))
                : undefined
            }
            columns={[
              {
                id: "unit",
                header: t("servers.services.unit"),
                label: t("servers.services.unit"),
                sortValue: (u) => u.name,
                searchValue: (u) => [u.name, u.description, u.active_state, u.sub_state].join(" "),
                cell: (u) => (
                  <span className="block min-w-0">
                    <span dir="ltr" className="font-mono text-sm font-medium">
                      {u.name}
                    </span>
                    {u.description ? (
                      <span className="block max-w-xs truncate text-caption text-muted-foreground" dir="auto">
                        {u.description}
                      </span>
                    ) : null}
                  </span>
                ),
              },
              {
                id: "state",
                header: t("servers.services.state"),
                label: t("servers.services.state"),
                sortValue: (u) => u.active_state,
                filterValue: (u) => u.active_state,
                cell: (u) => <ServiceStateBadge active={u.active_state} sub={u.sub_state} load={u.load_state} />,
              },
              {
                id: "enabled",
                header: t("servers.services.enabled"),
                label: t("servers.services.enabled"),
                sortValue: (u) => (u.enabled ? 0 : 1),
                filterValue: (u) => (u.enabled ? "yes" : "no"),
                cell: (u) => (
                  <Badge variant={u.enabled ? "neutral" : "outline"}>{u.enabled ? t("servers.services.enabledYes") : t("servers.services.enabledNo")}</Badge>
                ),
              },
            ] satisfies DataTableColumn<ServiceUnit>[]}
          />
        </div>
      )}
    </section>
  )
}

function ServiceStateBadge({ active, sub, load }: { active: string; sub: string; load: string }) {
  const variant = active === "active" ? "success" : active === "failed" ? "danger" : "neutral"
  return (
    <span className="flex flex-col items-start gap-0.5">
      <Badge variant={variant}>{active}</Badge>
      <span dir="ltr" className="text-xs text-muted-foreground">
        {sub} · {load}
      </span>
    </span>
  )
}
