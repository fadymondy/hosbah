"use client"

import { ListOrderedIcon } from "lucide-react"

import { Planned } from "@/components/site/planned"

export default function Page() {
  return <Planned section="queues" icon={ListOrderedIcon} />
}
