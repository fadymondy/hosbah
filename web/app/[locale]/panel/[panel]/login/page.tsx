"use client"

import { useParams, useSearchParams } from "next/navigation"

import { PanelLogin } from "@/components/panel/auth"

export default function PanelLoginPage() {
  const { panel } = useParams<{ panel: string }>()
  const params = useSearchParams()
  return <PanelLogin panelId={panel} next={params.get("next")} />
}
