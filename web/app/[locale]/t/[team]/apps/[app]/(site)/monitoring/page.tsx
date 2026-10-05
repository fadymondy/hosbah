"use client"

import { SITE_TABS } from "@/components/site/sections"
import { SiteViews } from "@/components/site/views"

export default function Page() {
  return <SiteViews views={SITE_TABS.find((x) => x.key === "monitoring")?.views ?? []} />
}
