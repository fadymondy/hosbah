/** Where MCP clients reach the API directly (not through this app). Configurable per environment. */
export const MCP_ORIGIN = process.env.NEXT_PUBLIC_MCP_ORIGIN || "http://localhost:8710"
export const TEAM_MCP_ENDPOINT = `${MCP_ORIGIN}/api/mcp`
export const ADMIN_MCP_ENDPOINT = `${MCP_ORIGIN}/api/admin/mcp`

/**
 * Mahaam's Feedback SDK: "Report a problem" files an issue into Hosbah's Mahaam project. Both
 * values are public (the key only files issues, from the origins allowed on it). Unset: the
 * in-app report dialog is used instead.
 */
export const MAHAAM_URL = (process.env.NEXT_PUBLIC_MAHAAM_URL ?? "").replace(/\/+$/, "")
export const MAHAAM_FEEDBACK_KEY = process.env.NEXT_PUBLIC_MAHAAM_FEEDBACK_KEY ?? ""
export const MAHAAM_FEEDBACK = Boolean(MAHAAM_URL && MAHAAM_FEEDBACK_KEY)
