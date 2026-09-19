"use client"

import { useLoadedServer } from "@/components/server-tabs/context"
import { ServerTunnels } from "@/components/server-tunnels"
import { useTeam } from "@/lib/team"

export default function Page() {
  const { manage } = useTeam()
  return <ServerTunnels serverId={useLoadedServer().server.id} manage={manage} />
}
