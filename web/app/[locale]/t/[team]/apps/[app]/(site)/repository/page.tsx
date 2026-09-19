"use client"

import { GitBranchIcon } from "lucide-react"

import { Planned } from "@/components/site/planned"

export default function Page() {
  return <Planned section="repository" icon={GitBranchIcon} />
}
