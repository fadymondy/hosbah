"use client"

import { useLoadedServer } from "@/components/server-tabs/context"
import { Discovered } from "@/components/server-tabs/discovered"
import { useTeam } from "@/lib/team"

export default function Page() {
  const { manage } = useTeam()
  const { server } = useLoadedServer()
  return <Discovered services={server.facts?.services ?? []} serverId={server.id} manage={manage} />
}
