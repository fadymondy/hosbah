"use client"

import { useLoadedServer } from "@/components/server-tabs/context"
import { ServerGitHub } from "@/components/server-github"
import { useTeam } from "@/lib/team"

export default function Page() {
  const { team, manage } = useTeam()
  return <ServerGitHub team={team.slug} serverId={useLoadedServer().server.id} manage={manage} />
}
