"use client"

import { EnvPanel } from "@/components/apps/env"
import { useLoadedSite } from "@/components/site/context"
import { SectionTitle } from "@/components/states"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export default function Page() {
  return <Env />
}

function Env() {
  const { t } = useI18n()
  const { manage } = useTeam()
  const { path } = useLoadedSite()
  return (
    <section aria-labelledby="env-h">
      <SectionTitle id="env-h" title={t("site.nav.environment")} hint={t("site.environmentHint")} />
      <EnvPanel path={path} manage={manage} />
    </section>
  )
}
