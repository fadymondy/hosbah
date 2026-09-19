/**
 * The one way the browser talks to the Hosbah API. Everything is same-origin
 * (/api/* is rewritten to the Go API by next.config.mjs), so the togo session
 * cookie travels by itself. Unsafe methods carry X-CSRF-Token, read from the
 * JS-readable `togo_csrf` cookie, fetched once from /api/auth/csrf if missing.
 */

export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message?: string) {
    super(message ?? code)
    this.status = status
    this.code = code
  }
}

const CSRF_COOKIE = "togo_csrf"
const UNSAFE = new Set(["POST", "PUT", "PATCH", "DELETE"])

function readCookie(name: string): string | null {
  if (typeof document === "undefined") return null
  const hit = document.cookie.split("; ").find((c) => c.startsWith(name + "="))
  return hit ? decodeURIComponent(hit.slice(name.length + 1)) : null
}

let csrfPromise: Promise<string | null> | null = null

async function csrfToken(): Promise<string | null> {
  const existing = readCookie(CSRF_COOKIE)
  if (existing) return existing
  csrfPromise ??= fetch("/api/auth/csrf", { credentials: "include" })
    .then(async (res) => {
      const body = (await res.json().catch(() => ({}))) as { csrf_token?: string }
      return readCookie(CSRF_COOKIE) ?? body.csrf_token ?? null
    })
    .catch(() => null)
    .finally(() => {
      csrfPromise = null
    })
  return csrfPromise
}

/** Pulls a human message out of `{error}` (togo) or `{title, detail, status}` (Huma). */
function errorFrom(status: number, body: unknown): ApiError {
  if (body && typeof body === "object") {
    const b = body as Record<string, unknown>
    if (typeof b.error === "string") return new ApiError(status, b.error)
    if (typeof b.detail === "string" || typeof b.title === "string") {
      const detail = typeof b.detail === "string" ? b.detail : String(b.title)
      return new ApiError(status, detail)
    }
    if (typeof b.message === "string") return new ApiError(status, b.message)
  }
  return new ApiError(status, `http_${status}`, `Request failed (${status})`)
}

export async function apiFetch<T = unknown>(
  path: string,
  init: { method?: string; body?: unknown; signal?: AbortSignal } = {},
): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase()
  const headers: Record<string, string> = { Accept: "application/json" }
  if (init.body !== undefined) headers["Content-Type"] = "application/json"
  if (UNSAFE.has(method)) {
    const token = await csrfToken()
    if (token) headers["X-CSRF-Token"] = token
  }

  const res = await fetch(path, {
    method,
    headers,
    credentials: "include",
    body: init.body === undefined ? undefined : JSON.stringify(init.body),
    signal: init.signal,
    cache: "no-store",
  })

  const text = await res.text()
  let body: unknown = null
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      body = text
    }
  }
  if (!res.ok) throw errorFrom(res.status, body)
  return body as T
}

// ── Contract types ──────────────────────────────────────────────────────────

export type Role = "owner" | "admin" | "deployer" | "viewer"
export const ROLES: Role[] = ["owner", "admin", "deployer", "viewer"]

export type User = { id: string | number; email: string; platform_admin: boolean }
export type TeamSummary = { id: string | number; name: string; slug: string; role: Role }
export type Me = { user: User; teams: TeamSummary[] }
export type Team = TeamSummary & { created_at?: string; require_destructive_approval?: boolean }

// ApprovalRequest is a destructive MCP call held for a person (P4). args_summary is redacted
// JSON — the tool, the resource and the argument NAMES, never a value.
export type ApprovalRequest = {
  id: string
  tool: string
  resource_type: string
  resource_name: string
  status: "pending" | "approved" | "used" | "rejected" | "expired"
  token_id: string
  token_name: string
  args_summary: string
  decided_by: string | null
  decided_at: string | null
  expires_at: string
  created_at: string
}

export type Member = { id: string | number; user_id: string | number; email: string; role: Role; created_at: string }
export type Invitation = {
  id: string | number
  email: string
  role: Role
  status: string
  expires_at: string
  created_at: string
}
export type AuditEntry = {
  id: string | number
  action: string
  subject_type: string
  subject_id: string | number | null
  causer_email: string | null
  ip: string | null
  created_at: string
  changes: unknown
}
export type McpToken = {
  id: string | number
  name: string
  scopes?: string[]
  last_used_at: string | null
  expires_at: string | null
  revoked_at: string | null
  created_at: string
}
export type Report = { key: string; title: string; status: string; created_at: string }

export const MCP_SCOPES = ["read", "deploy", "write", "secrets:read", "destructive"] as const

export function canManage(role: Role | undefined): boolean {
  return role === "owner" || role === "admin"
}
