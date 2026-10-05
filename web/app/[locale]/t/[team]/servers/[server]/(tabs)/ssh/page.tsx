"use client"

import { ServerSSH } from "@/components/server-tabs/ssh"
import { useLoadedServer } from "@/components/server-tabs/context"

export default function Page() {
  return <ServerSSH serverId={useLoadedServer().server.id} />
}
