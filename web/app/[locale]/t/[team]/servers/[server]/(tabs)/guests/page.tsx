import { redirect } from "next/navigation"

/** Folded into Settings: an import sync pushes guests and discovered workloads into the apps list. */
export default async function Page({ params }: { params: Promise<{ locale: string; team: string; server: string }> }) {
  const { locale, team, server } = await params
  redirect(`/${locale}/t/${team}/servers/${server}/settings#import`)
}
