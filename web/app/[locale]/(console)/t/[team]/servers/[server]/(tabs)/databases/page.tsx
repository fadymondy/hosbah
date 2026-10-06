"use client"

import { ServerDatabases } from "@/components/server-tabs/databases"
import { useLoadedServer } from "@/components/server-tabs/context"

export default function Page() {
  return <ServerDatabases serverId={useLoadedServer().server.id} />
}
