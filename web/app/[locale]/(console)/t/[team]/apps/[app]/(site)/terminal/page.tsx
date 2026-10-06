"use client"

import { useLoadedSite } from "@/components/site/context"
import { ServerTerminal } from "@/components/server-tabs/terminal"
import { useTeam } from "@/lib/team"

/** A shell inside the app's live web container (deployer and up), the same terminal as a server's. */
export default function Page() {
  const { app } = useLoadedSite()
  const { manage } = useTeam()
  return <ServerTerminal key={app.id} serverId={app.server_id} manage={manage} target={`app:${app.id}/web`} />
}
