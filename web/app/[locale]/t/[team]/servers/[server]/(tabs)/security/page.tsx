"use client"

import { useParams } from "next/navigation"

import { ServerSecurity } from "@/components/server-tabs/security"
import { useTeam } from "@/lib/team"

export default function Page() {
  const { server: serverId } = useParams<{ server: string }>()
  const { team } = useTeam()
  return <ServerSecurity team={team.slug} serverId={serverId} />
}
