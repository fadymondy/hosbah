import { redirect } from "next/navigation"

/** The team's servers live on its home page (cards with live usage, search and filters). */
export default async function ServersPage({ params }: { params: Promise<{ locale: string; team: string }> }) {
  const { locale, team } = await params
  redirect(`/${locale}/t/${team}`)
}
