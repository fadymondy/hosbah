/**
 * Cloudy SDK for Node.js / TypeScript (Node 18+, Bun, Deno, edge runtimes — anything with fetch).
 *
 * It calls a team's MCP endpoint (POST /api/mcp) with a team token (cld_…), so every Cloudy tool is
 * available through `call`, with the token's scopes, the audit log and destructive-action approvals.
 *
 *   import { Cloudy } from "@cloudy/sdk"
 *   const cloudy = new Cloudy({ url: process.env.CLOUDY_URL!, token: process.env.CLOUDY_TOKEN! })
 *   await cloudy.deploy("shop", { wait: true })
 */

export interface CloudyOptions {
  url: string
  token: string
  fetch?: typeof fetch
}

export interface Tool {
  name: string
  description: string
  inputSchema: Record<string, unknown>
}

/** A tool refused (not found, missing scope, validation…). */
export class CloudyToolError extends Error {
  constructor(
    public tool: string,
    message: string,
  ) {
    super(`cloudy: ${tool}: ${message}`)
    this.name = "CloudyToolError"
  }
}

/** Transport or protocol failure (bad token, server error). */
export class CloudyApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(`cloudy: ${message} (HTTP ${status})`)
    this.name = "CloudyApiError"
  }
}

type Args = Record<string, unknown>

export class Cloudy {
  private url: string
  private token: string
  private fetchImpl: typeof fetch
  private id = 0

  constructor(opts: CloudyOptions) {
    this.url = opts.url.replace(/\/+$/, "")
    this.token = (opts.token ?? "").trim()
    this.fetchImpl = opts.fetch ?? globalThis.fetch
    if (!this.token) throw new Error("cloudy: no token (mint one under MCP tokens)")
  }

  private async rpc<T>(method: string, params: unknown): Promise<T> {
    const res = await this.fetchImpl(`${this.url}/api/mcp`, {
      method: "POST",
      headers: { Authorization: `Bearer ${this.token}`, "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: ++this.id, method, params }),
    })
    const text = await res.text()
    let body: { result?: T; error?: { message: string } }
    try {
      body = JSON.parse(text)
    } catch {
      throw new CloudyApiError(res.status, text.slice(0, 200) || res.statusText)
    }
    if (body.error) throw new CloudyApiError(res.status, body.error.message)
    if (!res.ok) throw new CloudyApiError(res.status, res.statusText)
    return body.result as T
  }

  /** What this token can call. */
  async tools(): Promise<Tool[]> {
    const r = await this.rpc<{ tools: Tool[] }>("tools/list", {})
    return r.tools
  }

  /** Run any tool; resolves to its text output, rejects with CloudyToolError on a refusal. */
  async call(tool: string, args: Args = {}): Promise<string> {
    const r = await this.rpc<{ content: { text: string }[]; isError?: boolean }>("tools/call", { name: tool, arguments: args })
    const text = (r.content ?? []).map((c) => c.text).join("")
    if (r.isError) throw new CloudyToolError(tool, text)
    return text
  }

  whoami() {
    return this.call("whoami")
  }
  servers() {
    return this.call("list_servers")
  }
  apps() {
    return this.call("list_apps")
  }
  deploy(app: string, opts: { wait?: boolean; ref?: string } = {}) {
    return this.call("deploy_app", { app, ...opts })
  }
  setEnv(app: string, name: string, value: string) {
    return this.call("set_env", { app, name, value })
  }
  addDomain(app: string, hostname: string, opts: { tls?: "auto" | "internal" | "off" } = {}) {
    return this.call("add_domain", { app, hostname, ...opts })
  }
  addProxyHost(server: string, hostname: string, upstream: string, opts: { tls?: "auto" | "internal" | "off"; skip_verify?: boolean } = {}) {
    return this.call("add_proxy_host", { server, hostname, upstream, ...opts })
  }
  connectServer(name: string, host: string, login: { user?: string; password?: string; private_key?: string; port?: number }) {
    return this.call("add_server", { name, method: "connect", host, ...login })
  }
}

export default Cloudy
