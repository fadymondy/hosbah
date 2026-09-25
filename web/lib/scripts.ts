/** Contract types for the team's script library, its built-in catalogue, and runs (internal/api
 * scripts.go, internal/scripts/builtin) — MH-576 D1/D4/D5. */

export type Script = {
  id: string
  name: string
  description: string
  body: string
  default_args: Record<string, string>
  env_names: string[]
  run_as: "root" | "app"
  timeout_seconds: number
  created_by: string
  created_at: string
  updated_at: string
}

export type BuiltinParam = { name: string; label: string; enum?: string[]; default?: string }

export type BuiltinScript = {
  id: string
  name: string
  description: string
  icon: string
  run_as: "root" | "app"
  timeout_seconds: number
  params?: BuiltinParam[]
}

export type ScriptRunStatus = "pending" | "running" | "ok" | "failed"

export type ScriptRun = {
  id: string
  team_id: string
  script_id: string | null
  builtin_name: string | null
  params: string
  status: ScriptRunStatus
  created_by: string
  created_at: string
  updated_at: string
}

export type ScriptRunServerStatus = "pending" | "sent" | "running" | "ok" | "failed" | "timeout"

export type ScriptRunServer = {
  id: string
  team_id: string
  script_run_id: string
  server_id: string
  operation_id: string | null
  status: ScriptRunServerStatus
  exit_code: number | null
  output?: string
  started_at: string | null
  finished_at: string | null
  created_at: string
  updated_at: string
}

export function scriptRunTerminal(status: ScriptRunStatus | ScriptRunServerStatus): boolean {
  return status === "ok" || status === "failed" || status === "timeout"
}

/** A server's systemd .service units, and the result of starting/stopping/restarting/reloading one
 * (internal/agentproto services.go). */
export type ServiceUnit = {
  name: string
  description?: string
  load_state: string
  active_state: string
  sub_state: string
  enabled: boolean
}

export type ServiceAction = "start" | "stop" | "restart" | "reload"

export type ServiceActionResult = {
  unit: string
  active_state: string
  sub_state: string
}
