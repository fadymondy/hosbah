import { redirect } from "next/navigation"

// Proxy hosts are listed with the app domains on the team Domains page.
export default async function Page({ params }: { params: Promise<{ locale: string; team: string }> }) {
  const { locale, team } = await params
  redirect(`/${locale}/t/${team}/domains`)
}
