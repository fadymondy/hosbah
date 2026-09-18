import { test } from "node:test"
import assert from "node:assert/strict"
import { Cloudy, CloudyToolError, CloudyApiError } from "../dist/index.js"

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
    if (headers.Authorization !== "Bearer cld_ok") return { status: 401, body: { error: { message: "invalid token" } } }
    if (req.params.name === "deploy_app" && req.params.arguments.app === "shop")
      return { body: { result: { content: [{ type: "text", text: "Deployment #3 succeeded." }] } } }
    return { body: { result: { content: [{ type: "text", text: "app not found" }], isError: true } } }
  })
  const c = new Cloudy({ url: "https://c.test/", token: "cld_ok", fetch })
  assert.equal(await c.deploy("shop", { wait: true }), "Deployment #3 succeeded.")
  await assert.rejects(c.deploy("nope"), (e) => e instanceof CloudyToolError && e.message.includes("app not found"))
  const bad = new Cloudy({ url: "https://c.test", token: "cld_bad", fetch })
  await assert.rejects(bad.apps(), (e) => e instanceof CloudyApiError && e.status === 401)
  assert.throws(() => new Cloudy({ url: "https://c.test", token: "" }))
})
