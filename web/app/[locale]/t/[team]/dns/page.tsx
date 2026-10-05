import { redirect } from "next/navigation"

/** DNS zones now live on each domain's page (/domains/{domain}); the old address goes to the list. */
export default async function DNSZonesPage({ params }: { params: Promise<{ locale: string; team: string }> }) {
  const { locale, team } = await params
  redirect(`/${locale}/t/${team}/domains`)
}
