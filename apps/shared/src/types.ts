// Hand-written types for the slice of the Hosbah API the apps use. Verified against
// internal/api/*.go. src/openapi.d.ts (npm run gen:types) is the full machine-made reference.

export type Role = "owner" | "admin" | "deployer" | "viewer";
export type Locale = "ar" | "en";
export type DevicePlatform = "ios" | "android" | "windows" | "macos" | "linux";
export type DeviceApp = "mobile" | "desktop";

export const EVENT_NAMES = [
  "deploy.succeeded",
  "deploy.failed",
  "app.down",
  "app.up",
  "resource.high",
  "server.offline",
  "cert.expiring",
  "backup.failed",
] as const;
export type EventName = (typeof EVENT_NAMES)[number];

// ── auth / session ────────────────────────────────────────────────────────

export interface User {
  id: string;
  email: string;
  platform_admin?: boolean;
}

export interface LoginResult {
  token: string;
  user: User;
}

/** What the client hands to onSession whenever the credentials change. Persist all three. */
export interface Session {
  token: string;
  refreshToken: string | null;
  deviceId: string | null;
}

export interface Device {
  id: string;
  name: string;
  platform: DevicePlatform;
  app: DeviceApp;
  locale: Locale | string;
  app_version: string;
  push: boolean;
  created_at: string;
  last_seen_at: string | null;
}

export interface RegisterDeviceInput {
  name: string;
  platform: DevicePlatform;
  app: DeviceApp;
  locale?: Locale;
  app_version?: string;
}

export interface RefreshResult {
  token: string;
  refresh_token: string;
  device_id: string;
}

export type NotificationPrefs = Record<EventName, boolean>;

// ── teams ─────────────────────────────────────────────────────────────────

export interface TeamSummary {
  id: string;
  name: string;
  slug: string;
  role: Role;
  created_at: string;
  suspended: boolean;
}

export interface Me {
  user: User;
  teams: TeamSummary[];
}

// ── servers ───────────────────────────────────────────────────────────────

export interface ServerMetrics {
  cpu_percent: number;
  mem_used_bytes: number;
  mem_total_bytes: number;
  disk_used_bytes: number;
  disk_total_bytes: number;
  uptime_seconds: number;
  load1: number;
  at: string;
}

export interface Server {
  id: string;
  name: string;
  provider: "byo" | "proxmox";
  kind: "byo" | "vm" | "lxc";
  status: "pending" | "provisioning" | "enrolled" | "failed";
  online: boolean;
  hostname: string | null;
  ip_address: string | null;
  os: string | null;
  os_version: string | null;
  arch: string | null;
  agent_version: string | null;
  error: string | null;
  last_seen_at: string | null;
  created_at: string;
  color: string | null;
  icon: string | null;
  parent_server_id: string | null;
  metrics: ServerMetrics | null;
}

export interface ServersResponse {
  servers: Server[];
  agent_version: string;
}

export type MetricsRange = "1h" | "6h" | "24h" | "7d" | "30d";

export interface MetricPoint {
  at: string;
  cpu: number;
  mem_used: number;
  mem_total: number;
  disk_used: number;
  disk_total: number;
  load1: number;
  [extra: string]: unknown;
}

export interface MetricsSeries {
  range: MetricsRange;
  step_seconds: number;
  points: MetricPoint[];
}

// ── apps ──────────────────────────────────────────────────────────────────

export type AppStatus = "new" | "deploying" | "running" | "failed" | "stopped";
export type DeploymentStatus = "queued" | "running" | "succeeded" | "failed" | "cancelled";

export interface Deployment {
  id: string;
  app_id: string;
  number: number;
  ref: string;
  commit_sha: string | null;
  commit_message: string | null;
  trigger: "manual" | "hook" | "mcp" | "github";
  track: "fast" | "full" | null;
  kind: "deploy" | "rollback" | "restart";
  status: DeploymentStatus;
  step: string | null;
  error: string | null;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export interface AppDomain {
  id: string;
  hostname: string;
  tls: "auto" | "internal" | "off";
  verified: boolean;
  verified_at: string | null;
  created_at: string;
}

export interface App {
  id: string;
  name: string;
  title: string | null;
  description: string | null;
  color: string | null;
  icon: string | null;
  server_id: string;
  server_name?: string;
  repo_url: string | null;
  branch: string | null;
  runtime: string | null;
  source: "git" | "imported";
  imported_state: string | null;
  status: AppStatus;
  created_at: string;
  latest_deployment?: Deployment | null;
  domains?: AppDomain[];
}

export interface Release {
  id: string;
  version: number;
  image: string;
  commit_sha: string | null;
  status: "live" | "standby" | "pruned";
  deployment_id: string;
  activated_at: string | null;
  created_at: string;
}

export interface AppDetail {
  app: App;
  live_release: Release | null;
  previous_release: Release | null;
  domains: AppDomain[];
  processes: { name: string; command: string | null }[];
}

export interface LogLine {
  seq: number;
  /** "log" | "progress" | "status" (JSON key `event`). */
  event: string;
  stream?: string;
  line?: string;
  step?: number;
  total?: number;
  status?: string;
  exit_code?: number;
}

export interface DeploymentDetail {
  deployment: Deployment;
  logs: LogLine[];
}

export type DeploymentStreamEvent =
  | { type: "line"; line: LogLine }
  | { type: "status"; deployment: Deployment }
  | { type: "done"; status: DeploymentStatus }
  | { type: "gone" };

export type PowerAction = "start" | "stop";

// ── domains / certificates / backups ─────────────────────────────────────

export interface TeamDomain extends AppDomain {
  app_id: string;
  app_name: string;
  server_id: string;
}

export interface Certificate {
  id: string;
  hostname: string;
  source: "app" | "proxy";
  ok: boolean;
  issuer?: string;
  not_before?: string;
  not_after?: string;
  /** Absent when the certificate was never read. */
  days_left?: number;
  san_match: boolean;
  error?: string;
  last_checked_at?: string;
}

export interface BackupSummary {
  last_status: "pending" | "running" | "succeeded" | "failed" | null;
  last_at: string | null;
  last_error: string | null;
  last_restore_status: string | null;
  last_ok_at: string | null;
  last_ok_size: number | null;
  count: number;
  schedule: string | null;
  schedule_paused: boolean;
  next_run_at: string | null;
  target: "local" | "minio" | "s3" | null;
}

export interface DataService {
  id: string;
  name: string;
  kind: "postgres" | "mysql" | "redis" | "minio" | "mongodb";
  version: string;
  status: "new" | "provisioning" | "running" | "failed" | "stopped";
  server_id: string;
  error: string | null;
  source: "managed" | "imported";
  created_at: string;
  /** Present on the team list only; absent when the service has neither a backup nor a schedule. */
  backups?: BackupSummary;
}

export interface Backup {
  id: string;
  kind: "manual" | "scheduled";
  status: "pending" | "running" | "succeeded" | "failed";
  target: "local" | "minio" | "s3";
  size_bytes: number | null;
  error: string | null;
  restore_status: string | null;
  taken_at: string | null;
  created_at: string;
}

// ── notifications ────────────────────────────────────────────────────────

export interface AppNotification {
  id: string;
  kind: string;
  title: string;
  body: string | null;
  subject_type: string | null;
  subject_id: string | null;
  read_at: string | null;
  created_at: string;
}

export interface NotificationsResponse {
  notifications: AppNotification[];
  unread: number;
}

/** The `data` map of a push message. */
export interface PushData {
  team?: string;
  kind?: string;
  subject_type?: string;
  subject_id?: string;
  url?: string;
}
