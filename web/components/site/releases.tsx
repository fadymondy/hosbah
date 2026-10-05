"use client"

import { usePathname, useRouter } from "next/navigation"

import { ReleasesPanel } from "@/components/apps/releases"
import { useLoadedSite } from "@/components/site/context"
import { SectionTitle } from "@/components/states"

import { type Deployment } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam } from "@/lib/team"

/** The app's releases, each one a rollback target; a rollback opens the deployments view on it. */
export function SiteReleases() {
  const { t } = useI18n()
  const { team } = useTeam()
  const router = useRouter()
  const pathname = usePathname() ?? ""
  const { app, pushDeployment, path } = useLoadedSite()

  function rolledBack(d: Deployment) {
    pushDeployment(d)
    router.push(`${pathname}?view=deployments&d=${encodeURIComponent(d.id)}`)
  }

  return (
    <section aria-labelledby="rel-h">
      <SectionTitle id="rel-h" title={t("site.nav.releases")} />
      <ReleasesPanel path={path} operate={canOperate(team.role)} liveId={app.current_release_id} onRollback={rolledBack} />
    </section>
  )
}
