"use client"

import { useParams } from "next/navigation"
import type { ReactNode } from "react"

import { PanelShell } from "@/components/panel/shell"

export default function PanelLayout({ children }: { children: ReactNode }) {
  const { panel } = useParams<{ panel: string }>()
  return <PanelShell panelId={panel}>{children}</PanelShell>
}
