#!/usr/bin/env node
// A scripted MCP connector: walks Hosbah's OAuth flow the way claude.ai or ChatGPT do, without a
// browser, and never prints a token. Signs in with an emailed sign-in code (two runs).
//
//   node scripts/mcp-oauth-check.mjs start  <base> <email>                 # discovery + DCR, emails a sign-in code
//   node scripts/mcp-oauth-check.mjs finish <base> <email> <code> [team]  # consent (team slug/name, default: the
//                                                                          # suggested one), token, MCP calls,
//                                                                          # refresh, revoke
//
// State between the runs (cookies, client id) is kept in $MCP_CHECK_STATE (default /tmp/mcp-oauth-check.json, 0600).

import { createHash, randomBytes } from "node:crypto"
import { chmodSync, existsSync, readFileSync, writeFileSync } from "node:fs"

const [, , phase, baseArg, email, code, teamArg] = process.argv
if (!["start", "finish"].includes(phase) || !baseArg || !email) {
  console.error("usage: mcp-oauth-check.mjs start <base> <email> | finish <base> <email> <code> [team]")
  process.exit(2)
}
const base = baseArg.replace(/\/+$/, "")
const statePath = process.env.MCP_CHECK_STATE ?? "/tmp/mcp-oauth-check.json"
const state = phase === "finish" && existsSync(statePath) ? JSON.parse(readFileSync(statePath, "utf8")) : { cookies: {} }
const save = () => {
  writeFileSync(statePath, JSON.stringify(state))
  chmodSync(statePath, 0o600)
}
const ok = (msg) => console.log(`ok   ${msg}`)
const fail = (msg) => {
  console.log(`FAIL ${msg}`)
  process.exit(1)
}
const redact = (tok) => (tok ? `${tok.slice(0, 4)}… (${tok.length} chars)` : "(none)")

// --- a cookie-jar session for the consent API ---
async function session(method, path, body) {
  const headers = { "Content-Type": "application/json", Cookie: Object.entries(state.cookies).map(([k, v]) => `${k}=${v}`).join("; ") }
  if (method !== "GET") headers["X-CSRF-Token"] = state.cookies.togo_csrf ?? ""
  const res = await fetch(base + path, { method, headers, body: body ? JSON.stringify(body) : undefined, redirect: "manual" })
  for (const c of res.headers.getSetCookie?.() ?? []) {
    const [pair] = c.split(";")
    const i = pair.indexOf("=")
    state.cookies[pair.slice(0, i).trim()] = pair.slice(i + 1).trim()
  }
  const text = await res.text()
  let json = null
  try {
    json = JSON.parse(text)
  } catch {}
  return { status: res.status, json, text }
}

async function form(path, params) {
  const res = await fetch(base + path, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams(params) })
  const text = await res.text()
  let json = null
  try {
    json = JSON.parse(text)
  } catch {}
  return { status: res.status, json }
}

async function mcp(token, method, params) {
  const res = await fetch(base + "/api/mcp", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
  })
  const json = await res.json().catch(() => null)
  return { status: res.status, json }
}
const toolText = (r) => (r.json?.result?.content ?? []).map((c) => c.text).join("")

if (phase === "start") {
  // 1. The 401 points at the protected-resource metadata.
  const res = await fetch(base + "/api/mcp", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" })
  const challenge = res.headers.get("www-authenticate") ?? ""
  const m = challenge.match(/resource_metadata="([^"]+)"/)
  if (res.status !== 401 || !m) fail(`401 challenge: ${res.status} ${challenge}`)
  ok(`401 WWW-Authenticate: ${challenge}`)
  const prm = await (await fetch(m[1])).json()
  if (prm.resource !== base + "/api/mcp" || !prm.authorization_servers?.length) fail(`protected resource metadata: ${JSON.stringify(prm)}`)
  ok(`protected resource: ${prm.resource} -> ${prm.authorization_servers.join(", ")}`)
  const as = await (await fetch(prm.authorization_servers[0] + "/.well-known/oauth-authorization-server")).json()
  for (const k of ["issuer", "authorization_endpoint", "token_endpoint", "registration_endpoint", "revocation_endpoint"]) if (!as[k]) fail(`AS metadata lacks ${k}`)
  if (!as.code_challenge_methods_supported?.includes("S256")) fail("no S256")
  ok(`authorization server: issuer ${as.issuer}, grants ${as.grant_types_supported.join(" ")}`)
  state.as = as

  // 2. Dynamic client registration, as a public client.
  const redirect = "http://127.0.0.1:53682/callback"
  const reg = await fetch(as.registration_endpoint, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ client_name: "Hosbah OAuth check", redirect_uris: [redirect], grant_types: ["authorization_code", "refresh_token"], response_types: ["code"], token_endpoint_auth_method: "none" }),
  })
  const client = await reg.json()
  if (reg.status !== 201 || !client.client_id) fail(`registration: ${reg.status} ${JSON.stringify(client)}`)
  ok(`registered client ${client.client_id.slice(0, 8)}… (public, auth method ${client.token_endpoint_auth_method})`)
  state.client = { id: client.client_id, redirect }

  // 3. A session for the consent page: CSRF cookie, then an emailed sign-in code.
  await session("GET", "/api/auth/csrf")
  const req = await session("POST", "/api/auth/code/request", { email, locale: "en" })
  if (req.status >= 300) fail(`sign-in code request: ${req.status} ${req.text}`)
  ok(`sign-in code requested for ${email}; run "finish" with the code from the email`)
  save()
} else {
  if (!code || !state.client) fail("run start first, then finish with the emailed code")
  const v = await session("POST", "/api/auth/code/verify", { email, code })
  if (v.status !== 200) fail(`sign-in: ${v.status} ${v.text}`)
  ok("signed in")

  // 4. The consent page's data: the teams.
  const verifier = randomBytes(48).toString("base64url")
  const challenge = createHash("sha256").update(verifier).digest("base64url")
  const resource = base + "/api/mcp"
  const q = new URLSearchParams({ client_id: state.client.id, redirect_uri: state.client.redirect, scope: "read deploy write", resource })
  const consent = await session("GET", `/api/oauth/authorize?${q}`)
  if (consent.status !== 200 || consent.json?.kind !== "mcp") fail(`consent: ${consent.status} ${consent.text}`)
  console.log("     teams on the consent page:")
  for (const t of consent.json.teams) console.log(`       ${t.default ? "*" : " "} ${t.name} (${t.slug}) servers=${t.servers} apps=${t.apps} role=${t.role}${t.can_connect ? "" : " [cannot connect]"}`)
  const team = teamArg ? consent.json.teams.find((t) => t.slug === teamArg || t.name === teamArg || t.id === teamArg) : consent.json.teams.find((t) => t.default)
  if (!team) fail(`no such team: ${teamArg ?? "(default)"}`)
  ok(`picking ${team.name} (${team.slug})`)

  // 5. Allow → the redirect carries the code.
  const state_ = randomBytes(8).toString("hex")
  const dec = await session("POST", "/api/oauth/authorize", {
    response_type: "code", client_id: state.client.id, redirect_uri: state.client.redirect, state: state_, code_challenge: challenge,
    code_challenge_method: "S256", resource, team: team.id, scopes: consent.json.default_scopes, allow: true,
  })
  const redirect = new URL(dec.json?.redirect ?? "http://x/")
  if (dec.status !== 200 || redirect.searchParams.get("state") !== state_ || !redirect.searchParams.get("code")) fail(`authorize: ${dec.status} ${dec.text}`)
  ok("consent granted, code received")

  // 6. The token endpoint.
  const tok = await form(new URL(state.as.token_endpoint).pathname, {
    grant_type: "authorization_code", code: redirect.searchParams.get("code"), redirect_uri: state.client.redirect, code_verifier: verifier, client_id: state.client.id, resource,
  })
  if (tok.status !== 200) fail(`token: ${tok.status} ${JSON.stringify(tok.json)}`)
  ok(`token: access ${redact(tok.json.access_token)}, refresh ${redact(tok.json.refresh_token)}, expires_in ${tok.json.expires_in}, scope "${tok.json.scope}"`)

  // 7. MCP calls with it.
  const init = await mcp(tok.json.access_token, "initialize", { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "check", version: "1" } })
  if (init.status !== 200) fail(`initialize: ${init.status}`)
  ok(`initialize: ${init.json.result.serverInfo.name} ${init.json.result.protocolVersion}`)
  console.log(toolText(await mcp(tok.json.access_token, "tools/call", { name: "whoami", arguments: {} })).replace(/^/gm, "     "))
  console.log(toolText(await mcp(tok.json.access_token, "tools/call", { name: "list_teams", arguments: {} })).replace(/^/gm, "     "))
  const apps = toolText(await mcp(tok.json.access_token, "tools/call", { name: "list_apps", arguments: {} }))
  const want = (process.env.MCP_CHECK_APPS ?? "").split(",").filter(Boolean)
  const lines = apps.split("\n").filter((l) => !want.length || want.some((w) => l.includes(w)))
  console.log(`     list_apps (${apps.split("\n").filter(Boolean).length} lines${want.length ? `, showing ${want.join(", ")}` : ""}):`)
  console.log(lines.join("\n").replace(/^/gm, "       "))
  for (const w of want) if (!apps.includes(w)) fail(`list_apps has no ${w}`)

  // 8. Refresh rotates; the old access token stops working.
  const ref = await form(new URL(state.as.token_endpoint).pathname, { grant_type: "refresh_token", refresh_token: tok.json.refresh_token, client_id: state.client.id })
  if (ref.status !== 200 || ref.json.access_token === tok.json.access_token) fail(`refresh: ${ref.status} ${JSON.stringify(ref.json)}`)
  const old = await mcp(tok.json.access_token, "initialize", {})
  const fresh = await mcp(ref.json.access_token, "tools/call", { name: "whoami", arguments: {} })
  if (old.status !== 401 || fresh.status !== 200) fail(`after refresh: old ${old.status}, new ${fresh.status}`)
  ok("refresh rotated both tokens; the old access token now gets 401")

  // 9. Revoke: the connection ends.
  if (process.env.MCP_CHECK_KEEP !== "1") {
    const rv = await form(new URL(state.as.revocation_endpoint).pathname, { token: ref.json.refresh_token, client_id: state.client.id })
    const after = await mcp(ref.json.access_token, "initialize", {})
    if (rv.status !== 200 || after.status !== 401) fail(`revoke: ${rv.status}, then ${after.status}`)
    ok("revoked; the access token now gets 401")
  }
  await session("POST", "/api/auth/logout")
  save()
}
