import { redirect } from "next/navigation"

// Apps, databases and proxy hosts live inside their server now (/servers/{id}/…); the team home lists the servers.
export default async function Page({ params }: { params: Promise<{ locale: string; team: string }> }) {
  const { locale, team } = await params
  redirect(`/${locale}/t/${team}`)
}
