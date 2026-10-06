"use client"

import { PageBody } from "@/components/app-shell"
import { TeamProducts } from "@/components/products"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/** SaaS-ify any Docker app (MH-576 D7): a product definition per app/recipe/repo, per-customer
 * instances with their own hostname and env, suspend/resume/destroy, one-at-a-time upgrades with
 * a health gate, and a product-scoped API key for the team's own billing/signup system. */
export default function ProductsPage() {
  const { t } = useI18n()
  const { team } = useTeam()
  useDocumentTitle(`${t("nav.products")} — ${team.name}`)

  return (
    <PageBody title={t("nav.products")} description={t("products.hint")} bleed>
      <TeamProducts />
    </PageBody>
  )
}
