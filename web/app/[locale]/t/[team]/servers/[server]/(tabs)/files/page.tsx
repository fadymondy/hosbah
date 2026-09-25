"use client"

import { useLoadedServer } from "@/components/server-tabs/context"
import { ServerFiles } from "@/components/server-tabs/files"
import { useTeam } from "@/lib/team"

export default function Page() {
  const { manage } = useTeam()
  const { server } = useLoadedServer()
  return <ServerFiles serverId={server.id} manage={manage} />
}
