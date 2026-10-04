import type { Imported } from "@/lib/apps"
// Types for the P3 managed-data-services surface, matching internal/api/databases.go.

export type DataServiceKind = "postgres" | "mysql" | "redis" | "minio" | "mongodb"
export type DataServiceStatus = "new" | "provisioning" | "running" | "failed" | "stopped"

export interface DataService extends Imported {
  id: string
  name: string
  kind: DataServiceKind
  version: string
  status: DataServiceStatus
  server_id: string
  host: string | null
  port: number | null
  error: string | null
  has_credentials?: boolean
  credentials_required?: boolean
  created_at: string
  /** The team list only: the newest backup and the schedule; absent when it has neither. */
  backups?: BackupSummary
}

export interface BackupSummary {
  last_status: "pending" | "running" | "succeeded" | "failed" | null
  last_at: string | null
  last_error: string | null
  last_restore_status: string | null
  last_ok_at: string | null
  last_ok_size: number | null
  count: number
  schedule: string | null
  schedule_paused: boolean
  next_run_at: string | null
  target: "local" | "minio" | "s3" | null
}

export interface DatabaseRow {
  id: string
  name: string
  status: string
  error: string | null
}

export interface DatabaseUser {
  id: string
  name: string
  grants: "all" | "readwrite" | "readonly" | "none"
  status: string
  error: string | null
  database_id: string | null
}

export interface Backup {
  id: string
  kind: "manual" | "scheduled"
  status: "pending" | "running" | "succeeded" | "failed"
  target: "local" | "minio" | "s3"
  size_bytes: number | null
  snapshot_id: string | null
  restore_status: string | null
  restore_error: string | null
  error: string | null
  taken_at: string | null
  restore_tested_at: string | null
  created_at: string
}

export interface BackupSchedule {
  cron: string
  retention: number
  target: "local" | "minio" | "s3"
  restore_test: boolean
  paused: boolean
  next_run_at: string | null
  last_run_at: string | null
}

export interface DataServiceDetail {
  service: DataService
  databases: DatabaseRow[]
  users: DatabaseUser[]
  backups: Backup[]
  schedule: BackupSchedule | null
  remote_available: boolean
}

export function humanBytes(n: number | null | undefined): string {
  if (!n || n <= 0) return "—"
  const units = ["B", "KB", "MB", "GB", "TB"]
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`
}
