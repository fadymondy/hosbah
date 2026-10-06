"use client"

import { ServerSFTP } from "@/components/server-tabs/sftp"
import { useLoadedServer } from "@/components/server-tabs/context"

export default function Page() {
  return <ServerSFTP serverId={useLoadedServer().server.id} />
}
