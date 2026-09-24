"use client"

import { useLoadedServer } from "@/components/server-tabs/context"
import { Guests } from "@/components/server-tabs/guests"

export default function Page() {
  const { server } = useLoadedServer()
  return <Guests guests={server.guests ?? []} />
}
