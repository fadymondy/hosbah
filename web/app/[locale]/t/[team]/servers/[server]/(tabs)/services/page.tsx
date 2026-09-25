"use client"

import { useParams } from "next/navigation"

import { ServerServices } from "@/components/server-tabs/services"
import { useTeam } from "@/lib/team"

export default function Page() {
  const { server: serverId } = useParams<{ server: string }>()
  const { team } = useTeam()
  return <ServerServices team={team.slug} serverId={serverId} />
}
