"use client"

import { ServerMetrics } from "@/components/server-metrics"
import { useLoadedServer } from "@/components/server-tabs/context"

export default function ServerMetricsPage() {
  const { path } = useLoadedServer()
  return <ServerMetrics path={path} />
}
