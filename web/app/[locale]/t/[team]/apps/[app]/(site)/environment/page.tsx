"use client"

import { EnvPanel } from "@/components/apps/env"
import { useLoadedSite } from "@/components/site/context"
import { useTeam } from "@/lib/team"

export default function Page() {
  const { manage } = useTeam()
  const { path } = useLoadedSite()
  return <EnvPanel path={path} manage={manage} />
}
