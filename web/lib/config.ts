/** Where MCP clients reach the API directly (not through this app). Configurable per environment. */
export const MCP_ORIGIN = process.env.NEXT_PUBLIC_MCP_ORIGIN ?? "http://localhost:8710"
export const TEAM_MCP_ENDPOINT = `${MCP_ORIGIN}/api/mcp`
export const ADMIN_MCP_ENDPOINT = `${MCP_ORIGIN}/api/admin/mcp`
