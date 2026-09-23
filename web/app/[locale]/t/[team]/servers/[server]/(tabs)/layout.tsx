"use client"

import { useParams } from "next/navigation"
import type { ReactNode } from "react"
import { AlertCircleIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ServerProvider, useServerPage } from "@/components/server-tabs/context"
import { ServerHeader } from "@/components/server-tabs/header"
import { useProvideServerScope } from "@/components/shell/scope"
import { ErrorBand, LoadingRows, Notice } from "@/components/states"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export default function ServerLayout({ children }: { children: ReactNode }) {
  const { server: serverId } = useParams<{ server: string }>()
  return (
    <ServerProvider serverId={serverId}>
      <Frame serverId={serverId}>{children}</Frame>
    </ServerProvider>
  )
}

function Frame({ serverId, children }: { serverId: string; children: ReactNode }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const { server, missing, error } = useServerPage()
  useDocumentTitle(`${server?.name ?? t("nav.servers")} — ${team.name}`)
  // The shell's breadcrumb and sidebar show this server.
  useProvideServerScope(
    server
      ? { id: server.id, name: server.name, status: server.status, online: server.online, color: server.color, icon: server.icon, logo_url: server.logo_url }
      : null,
  )

  if (missing) {
    return (
      <PageBody title={t("nav.servers")} bleed>
        <Notice icon={<AlertCircleIcon />} title={t("errors.notFound")} />
      </PageBody>
    )
  }
  return (
    <div className="flex flex-col">
      <ServerHeader serverId={serverId} />
      <ErrorBand message={error} />
      {server ? children : <LoadingRows rows={4} />}
    </div>
  )
}
