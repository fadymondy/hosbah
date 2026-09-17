"use client"

import type { LogLine as OpLogLine } from "@/lib/servers"

/** Contract types for apps, deployments, releases, env, domains (internal/api apps.go). */

export type AppStatus = "new" | "deploying" | "running" | "failed" | "stopped"
export type DeploymentStatus = "queued" | "running" | "succeeded" | "failed" | "cancelled"
export type DeploymentKind = "deploy" | "rollback" | "restart"
export type DeploymentTrigger = "manual" | "hook" | "mcp" | "github"
export type DeploymentStep = "provision" | "build" | "release" | "start" | "switch" | "drain" | "rollback" | "restart"
export type TLSMode = "auto" | "internal" | "off"

export type Deployment = {
  id: string
  app_id: string
  number: number
  ref: string
  commit_sha: string | null
  commit_message: string | null
  trigger: DeploymentTrigger
  kind: DeploymentKind
  status: DeploymentStatus
  step: DeploymentStep | null
  image: string | null
  runtime: string | null
  error: string | null
  release_id: string | null
  target_release_id: string | null
  cancel_requested: boolean
  created_at: string
  started_at: string | null
  finished_at: string | null
}

export type Domain = {
  id: string
  hostname: string
  tls: TLSMode
  verified: boolean
  verified_at: string | null
  verify_name: string
  verify_value: string
  created_at: string
}

export type App = {
  id: string
  name: string
  server_id: string
  server_name?: string
  repo_url: string
  branch: string
  runtime: string | null
  port: number
  healthcheck_path: string
  healthcheck_timeout: number
  autodeploy_paused: boolean
  status: AppStatus
  current_release_id: string | null
  created_at: string
  latest_deployment?: Deployment
  domains?: Domain[]
}

export type Release = {
  id: string
  version: number
  image: string
  commit_sha: string | null
  status: "live" | "standby" | "pruned"
  deployment_id: string
  activated_at: string | null
  created_at: string
}

export type AppDetail = {
  app: App
  live_release: Release | null
  previous_release: Release | null
  domains: Domain[]
  processes: { name: string; command: string | null }[]
  deploy_hook_hint: string
}

export type EnvVar = { name: string; updated_at: string }

export type DeployLogLine = OpLogLine & { op?: string }

export type DetectResult = {
  commit: string
  message: string
  runtime: "dockerfile" | "railpack"
  provider: string
  start_command: string
  cloudy_toml: unknown
}

export type Integrations = {
  github_app: { configured: boolean; slug?: string; install_url?: string }
  apps_domain: { configured: boolean }
  railpack_version: string
  caddy_version: string
}

export type Notification = {
  id: string
  kind: string
  title: string
  body: string | null
  subject_type: string | null
  subject_id: string | null
  read_at: string | null
  created_at: string
}

export const APP_NAME = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/
export const ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/

export function isActive(d: Pick<Deployment, "status">): boolean {
  return d.status === "queued" || d.status === "running"
}

/** "railpack:node" → "Railpack · node"; "dockerfile" → "Dockerfile". */
export function runtimeLabel(runtime: string | null | undefined): string | null {
  if (!runtime) return null
  const [kind, provider] = runtime.split(":")
  const name = kind === "dockerfile" ? "Dockerfile" : kind === "railpack" ? "Railpack" : kind
  return provider ? `${name} · ${provider}` : name
}

export function shortSha(sha: string | null | undefined): string {
  return sha ? sha.slice(0, 7) : "—"
}

export function appPath(team: string, app: string): string {
  return `/api/teams/${encodeURIComponent(team)}/apps/${encodeURIComponent(app)}`
}
