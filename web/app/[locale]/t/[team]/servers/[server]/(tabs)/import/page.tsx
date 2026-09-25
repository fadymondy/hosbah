"use client"

import { useLoadedServer } from "@/components/server-tabs/context"
import { Import } from "@/components/server-tabs/import"
import { useTeam } from "@/lib/team"

export default function Page() {
  const { manage } = useTeam()
  const { server } = useLoadedServer()
  return <Import serverId={server.id} manage={manage} />
}
