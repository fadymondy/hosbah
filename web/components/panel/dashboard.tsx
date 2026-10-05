"use client"

import { Button } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { PlusIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ServerMetrics } from "@/components/server-metrics"
import { useLoadedServer } from "@/components/server-tabs/context"
import { RunningHere } from "@/components/server-tabs/running-here"
import { CardPieces } from "@/components/states"
import { useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam, useTeamBase } from "@/lib/team"

/** A panel's dashboard (MH-1141): the machine's status charts, then the customer's apps and databases. */
export function PanelDashboard() {
  const { t } = useI18n()
  const { team } = useTeam()
  const base = useTeamBase()
  const { server, path } = useLoadedServer()
  return (
    <PageBody
      bleed
      title={t("panel.dashboard.title")}
      description={t("panel.dashboard.description", { name: server.name })}
      actions={
        canOperate(team.role) ? (
          <Button variant="primary" nativeButton={false} render={<Link href={`${base}/apps/new?server=${server.id}`} />}>
            <PlusIcon />
            {t("panel.dashboard.newApp")}
          </Button>
        ) : null
      }
    >
      <CardPieces>
        <ServerMetrics path={path} />
        <RunningHere serverId={server.id} />
      </CardPieces>
    </PageBody>
  )
}
