"use client"

import { useLoadedServer } from "@/components/server-tabs/context"
import { ServerTerminal } from "@/components/server-tabs/terminal"
import { useTeam } from "@/lib/team"

export default function Page() {
  const { manage } = useTeam()
  const { server } = useLoadedServer()
  return <ServerTerminal serverId={server.id} manage={manage} />
}
