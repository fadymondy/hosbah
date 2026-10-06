"use client"

import { ServerProxy } from "@/components/server-tabs/proxy"
import { useLoadedServer } from "@/components/server-tabs/context"

export default function Page() {
  return <ServerProxy serverId={useLoadedServer().server.id} />
}
