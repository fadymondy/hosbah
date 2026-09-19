"use client"

import { CalendarClockIcon } from "lucide-react"

import { Planned } from "@/components/site/planned"

export default function Page() {
  return <Planned section="schedules" icon={CalendarClockIcon} />
}
