"use client"

import { ServerApps } from "@/components/server-tabs/apps"
import { useLoadedServer } from "@/components/server-tabs/context"

export default function Page() {
  return <ServerApps serverId={useLoadedServer().server.id} mode="panels" />
}
