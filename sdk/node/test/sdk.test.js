import { test } from "node:test"
import assert from "node:assert/strict"
import { Hosbah, HosbahToolError, HosbahApiError } from "../dist/index.js"

function fakeFetch(handler) {
  return async (url, init) => {
    const req = JSON.parse(init.body)
    const { status = 200, body } = handler(url, init.headers, req)
    return new Response(JSON.stringify(body), { status })
  }
}

test("call, helpers and errors", async () => {
  const fetch = fakeFetch((url, headers, req) => {
    assert.equal(url, "https://c.test/api/mcp")
    if (headers.Authorization !== "Bearer hbu_ok") return { status: 401, body: { error: { message: "invalid token" } } }
    if (req.params.name === "deploy_app" && req.params.arguments.app === "shop")
      return { body: { result: { content: [{ type: "text", text: "Deployment #3 succeeded." }] } } }
    return { body: { result: { content: [{ type: "text", text: "app not found" }], isError: true } } }
  })
  const c = new Hosbah({ url: "https://c.test/", token: "hbu_ok", fetch })
  assert.equal(await c.deploy("shop", { wait: true }), "Deployment #3 succeeded.")
  await assert.rejects(c.deploy("nope"), (e) => e instanceof HosbahToolError && e.message.includes("app not found"))
  const bad = new Hosbah({ url: "https://c.test", token: "hbu_bad", fetch })
  await assert.rejects(bad.apps(), (e) => e instanceof HosbahApiError && e.status === 401)
  assert.throws(() => new Hosbah({ url: "https://c.test", token: "" }))
})

test("call follows continue: lines until the wait is over", async () => {
  let calls = 0
  const fetch = fakeFetch((url, headers, req) => {
    calls++
    const next = 'continue: wait_deployment {"app":"shop","deployment":"4","until":"2026-09-25T12:00:00Z"}\n'
    if (req.params.name === "deploy_app") return { body: { result: { content: [{ type: "text", text: "Deploy #4 of shop queued.\n" + next }] } } }
    if (calls === 2) return { body: { result: { content: [{ type: "text", text: "#4 is running (step release).\n" + next }] } } }
    return { body: { result: { content: [{ type: "text", text: "✗ #4 failed: health check\n" }], isError: true } } }
  })
  const c = new Hosbah({ url: "https://c.test", token: "hbu_ok", fetch })
  await assert.rejects(c.deploy("shop", { wait: true }), (e) => e instanceof HosbahToolError && e.tool === "deploy_app" && e.detail === "Deploy #4 of shop queued.\n✗ #4 failed: health check\n")
  assert.equal(calls, 3)
})
