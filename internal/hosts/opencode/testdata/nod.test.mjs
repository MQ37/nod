// Unit tests of the embedded OpenCode plugin (internal/hosts/assets/opencode/nod.js).
// No network: a fake fetch and a fake OpenCode client. Lives under testdata so it is never embedded.
// Run: node --test internal/hosts/opencode/testdata/
import test from "node:test"
import assert from "node:assert/strict"
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import plugin, { buildRequest, clip, createNod, extractJson, loadConfig, mapDecision, enforceError, redact, unwrap, userTurns, NO_TOOLS } from "../../assets/opencode/nod.js"

const MODEL = { providerID: "anthropic", modelID: "claude-x" }

function jsonRes(body, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body }
}

// fakeFetch answers by path; records every call.
function fakeFetch(routes) {
  const calls = []
  const f = async (url, init) => {
    const p = new URL(url).pathname
    const body = init && init.body ? JSON.parse(init.body) : undefined
    calls.push({ path: p, body })
    const r = routes[p]
    if (!r) throw new Error("ECONNREFUSED")
    return typeof r === "function" ? r(body, init) : r
  }
  f.calls = calls
  return f
}

// fakeClient: one root session "ses_root" with the given messages; records calls.
function fakeClient(opts = {}) {
  const calls = []
  const messages = opts.messages || [
    { info: { role: "user", model: MODEL }, parts: [{ type: "text", text: "please clean the build dir" }, { type: "text", text: "FILE CONTENTS", synthetic: true }] },
    { info: { role: "assistant" }, parts: [{ type: "text", text: "ASSISTANT TEXT" }, { type: "tool", state: { output: "TOOL OUTPUT" } }] },
  ]
  const wrap = (v) => (opts.raw ? v : { data: v, error: undefined, request: {}, response: {} })
  let n = 0
  const client = {
    session: {
      get: async ({ path: { id } }) => {
        calls.push(["get", id])
        return wrap({ id, parentID: (opts.parents || {})[id] })
      },
      messages: async ({ path: { id } }) => {
        calls.push(["messages", id])
        return wrap(messages)
      },
      create: async ({ body }) => {
        calls.push(["create", body])
        const id = "ses_child" + ++n
        return wrap({ id, parentID: body.parentID })
      },
      prompt: async (req) => {
        calls.push(["prompt", req])
        if (opts.onPrompt) return opts.onPrompt(req, client, wrap)
        return wrap({ info: { role: "assistant" }, parts: [{ type: "text", text: opts.reply ?? '```json\n{"category": 3, "auth": "yes", "forbid": "no", "match": "yes", "named": "no"}\n```' }] })
      },
      abort: async ({ path: { id } }) => {
        calls.push(["abort", id])
        return wrap(true)
      },
      delete: async ({ path: { id } }) => {
        calls.push(["delete", id])
        return wrap(true)
      },
    },
  }
  client.calls = calls
  return client
}

function setup({ mode = "enforce", review = "server", routes, client, timeout_ms = 2000, logPath = null } = {}) {
  const fetch = fakeFetch(routes || {})
  const c = client || fakeClient()
  const nod = createNod({ client: c, directory: "/work", config: loadConfig({ mode, review, timeout_ms }, {}), fetch, logPath })
  return { nod, fetch, client: c }
}

const bash = (sid = "ses_root", command = "rm -rf build") => [{ tool: "bash", sessionID: sid, callID: "c1" }, { args: { command } }]

test("extractJson takes the first balanced object, tolerating fences and prose", () => {
  assert.deepEqual(extractJson('```json\n{"a": 1}\n```'), { a: 1 })
  assert.deepEqual(extractJson('Sure! {"a": "x}y", "b": {"c": 2}} trailing {"d": 3}'), { a: "x}y", b: { c: 2 } })
  assert.deepEqual(extractJson('{not json} then {"ok": true}'), { ok: true })
  assert.equal(extractJson("no json here"), null)
  assert.equal(extractJson("[1,2]"), null)
  assert.equal(extractJson(undefined), null)
})

test("redact removes obvious secrets and clip bounds length", () => {
  const s = redact(
    "curl -H 'Authorization: Bearer abcdef123456789' -d sk-proj-ABCDEFGHIJKLMNOP ghp_ABCDEFGHIJKLMNOPQRST AKIAABCDEFGHIJKLMNOP API_KEY=hunter2 x\n-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----",
  )
  for (const leak of ["abcdef123456789", "sk-proj-ABCD", "ghp_ABCD", "AKIAABCD", "hunter2", "MIIE"]) assert.ok(!s.includes(leak), `${leak} leaked: ${s}`)
  assert.ok(s.includes("API_KEY=<v>") && s.includes("<private-key>"))
  assert.equal(redact("ls -la"), "ls -la")
  assert.equal(clip("x".repeat(10), 4), "xxxx [clipped]")
})

test("buildRequest keeps only user turns, clipped, redacted, last six", () => {
  const turns = userTurns([
    { info: { role: "user" }, parts: [{ type: "text", text: "t0" }] },
    { info: { role: "assistant" }, parts: [{ type: "text", text: "ASSISTANT" }] },
    { info: { role: "user" }, parts: [{ type: "text", text: "file", synthetic: true }, { type: "text", text: "t1" }] },
    ...[2, 3, 4, 5, 6].map((i) => ({ info: { role: "user" }, parts: [{ type: "text", text: "t" + i }] })),
    { info: { role: "user" }, parts: [{ type: "text", text: "my token sk-ABCDEFGHIJKLMNOPQR " + "y".repeat(900) }] },
  ])
  const req = buildRequest({ tool: "Bash", input: "echo " + "z".repeat(5000), cwd: "/w", turns })
  assert.equal(req.tool, "Bash")
  assert.equal(req.cwd, "/w")
  assert.ok(req.input.length <= 4000 + " [clipped]".length)
  assert.equal(req.conversation.length, 6)
  assert.ok(req.conversation.every((t) => t.role === "user"))
  assert.deepEqual(req.conversation.slice(0, 5).map((t) => t.text), ["t2", "t3", "t4", "t5", "t6"])
  const last = req.conversation[5].text
  assert.ok(last.length <= 600 + " [clipped]".length && !last.includes("sk-ABCD"))
  assert.equal(req.task, last)
  assert.ok(!JSON.stringify(req).includes("ASSISTANT") && !JSON.stringify(req).includes('"file"'))
  assert.equal(buildRequest({ tool: "Bash", input: "ls", turns: [] }).conversation, undefined)
})

test("mapDecision and enforceError", () => {
  assert.deepEqual(mapDecision({ decision: "allow", reason: "ok", source: "model" }), { decision: "allow", reason: "ok", source: "model" })
  assert.equal(mapDecision({ decision: "weird" }).decision, "ask")
  assert.equal(mapDecision(null).decision, "ask")
  assert.equal(enforceError("allow", "x"), null)
  assert.match(enforceError("block", "deletes home").message, /^nod: blocked: deletes home\. Do not retry this exact action/)
  assert.match(enforceError("ask", "pushes code").message, /needs the user's explicit approval \(pushes code\)\. Ask the user to confirm/)
})

test("loadConfig defaults, file values and env overrides", () => {
  assert.deepEqual(loadConfig("", {}), { url: "http://127.0.0.1:11096", mode: "shadow", review: "server", timeout_ms: 15000, tools: ["bash"] })
  const c = loadConfig('{"url":"http://h:1/","mode":"enforce","review":"agent","timeout_ms":5,"tools":["bash","nope"]}', { NOD_MODE: "shadow" })
  assert.deepEqual(c, { url: "http://h:1", mode: "shadow", review: "agent", timeout_ms: 5, tools: ["bash"] })
  assert.equal(loadConfig("{broken", { NOD_REVIEW: "agent", NOD_URL: "http://x" }).review, "agent")
  assert.equal(loadConfig({ mode: "ENFORCE" }, {}).mode, "enforce")
})

test("default export is an OpenCode v1 plugin object whose server() returns the hooks", async () => {
  assert.equal(plugin.id, "nod")
  assert.equal(typeof plugin.server, "function")
  // OpenCode v2 refuses a plugin definition that has an id but neither setup nor effect.
  assert.ok(typeof plugin.setup === "function" || typeof plugin.effect === "function", "needs setup or effect")
  const hooks = await plugin.server({ client: fakeClient(), directory: "/w" })
  assert.equal(typeof hooks["tool.execute.before"], "function")
  assert.equal(typeof hooks["chat.message"], "function")
})

test("unwrap handles both SDK response shapes", () => {
  assert.deepEqual(unwrap({ data: { id: 1 }, error: undefined }), { id: 1 })
  assert.deepEqual(unwrap({ id: 1 }), { id: 1 })
  assert.throws(() => unwrap({ data: undefined, error: { data: { message: "boom" } } }), /boom/)
})

test("enforce, server review: allow runs, block and ask throw", async () => {
  for (const [decision, re] of [["allow", null], ["block", /nod: blocked: r\./], ["ask", /explicit approval \(r\)/]]) {
    const { nod, fetch } = setup({ routes: { "/v1/classify": jsonRes({ decision, reason: "r", source: "model" }) } })
    const p = nod.hooks["tool.execute.before"](...bash())
    if (re) await assert.rejects(p, re)
    else await p
    const sent = fetch.calls[0].body
    assert.equal(sent.tool, "Bash")
    assert.equal(sent.input, "rm -rf build")
    assert.equal(sent.cwd, "/work")
    assert.deepEqual(sent.conversation, [{ role: "user", text: "please clean the build dir" }])
  }
})

test("ungated tools are not reviewed", async () => {
  const { nod, fetch } = setup()
  await nod.hooks["tool.execute.before"]({ tool: "read", sessionID: "ses_root", callID: "c" }, { args: { filePath: "/etc/passwd" } })
  assert.equal(fetch.calls.length, 0)
})

test("every failure is ask in enforce mode", async () => {
  const cases = {
    unreachable: {},
    http500: { "/v1/classify": jsonRes({}, 500) },
    badJson: { "/v1/classify": { ok: true, status: 200, json: async () => { throw new Error("bad json") } } },
    noDecision: { "/v1/classify": jsonRes({ source: "x" }) },
  }
  for (const [name, routes] of Object.entries(cases)) {
    const { nod } = setup({ routes })
    await assert.rejects(nod.hooks["tool.execute.before"](...bash()), /explicit approval/, name)
  }
})

test("shadow never throws and logs one line without the command", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "nodtest-"))
  const logPath = path.join(dir, "nod", "opencode-decisions.jsonl")
  for (const routes of [{ "/v1/classify": jsonRes({ decision: "block", reason: "bad", source: "model" }) }, {}]) {
    const { nod } = setup({ mode: "shadow", routes, logPath })
    await nod.hooks["tool.execute.before"](...bash("ses_root", "rm -rf /secret-dir"))
  }
  const lines = fs.readFileSync(logPath, "utf8").trim().split("\n").map((l) => JSON.parse(l))
  assert.equal(lines.length, 2)
  assert.equal(lines[0].decision, "block")
  assert.equal(lines[1].decision, "ask")
  assert.equal(lines[1].source, "error")
  assert.ok(!fs.readFileSync(logPath, "utf8").includes("secret-dir"))
  assert.equal(fs.statSync(logPath).mode & 0o777, 0o600)
  fs.rmSync(dir, { recursive: true })
})

test("timeout counts as ask", async () => {
  const slow = () => new Promise((r) => setTimeout(() => r(jsonRes({ decision: "allow" })), 500))
  const { nod } = setup({ routes: { "/v1/classify": slow }, timeout_ms: 50 })
  await assert.rejects(nod.hooks["tool.execute.before"](...bash()), /explicit approval \(nod could not review this call: timed out/)
})

const PROMPT = { system: "SYS", user: "USER PROMPT", keys: ["category", "auth", "forbid", "match", "named"] }

test("agent review: child session with the cached model, no tools, verdict posted, child deleted", async () => {
  for (const raw of [false, true]) {
    const client = fakeClient({ raw })
    const { nod, fetch } = setup({ review: "agent", client, routes: { "/v1/review/prompt": jsonRes(PROMPT), "/v1/review/verdict": jsonRes({ decision: "allow", source: "agent" }) } })
    const cached = { providerID: "openai", modelID: "gpt-cached" }
    await nod.hooks["chat.message"]({ sessionID: "ses_root", model: cached }, { message: { role: "user", model: cached }, parts: [{ type: "text", text: "hi" }] })
    await nod.hooks["tool.execute.before"](...bash())
    const create = client.calls.find((c) => c[0] === "create")[1]
    assert.deepEqual(create, { parentID: "ses_root", title: "nod review" })
    const prompt = client.calls.find((c) => c[0] === "prompt")[1]
    assert.deepEqual(prompt.body.model, cached)
    assert.equal(prompt.body.system, "SYS")
    assert.deepEqual(prompt.body.parts, [{ type: "text", text: "USER PROMPT" }])
    assert.deepEqual(prompt.body.tools, { ...NO_TOOLS })
    for (const t of ["*", "bash", "edit", "write", "read", "task", "webfetch"]) assert.equal(prompt.body.tools[t], false)
    assert.deepEqual(client.calls.filter((c) => c[0] === "delete").map((c) => c[1]), [prompt.path.id])
    const verdict = fetch.calls.find((c) => c.path === "/v1/review/verdict").body
    assert.deepEqual(verdict.answers, { category: 3, auth: "yes", forbid: "no", match: "yes", named: "no" })
    assert.equal(verdict.request.input, "rm -rf build")
    assert.equal(fetch.calls.find((c) => c.path === "/v1/review/prompt").body.tool, "Bash")
  }
})

test("agent review falls back to the model of the last user message", async () => {
  const client = fakeClient()
  const { nod } = setup({ review: "agent", client, routes: { "/v1/review/prompt": jsonRes(PROMPT), "/v1/review/verdict": jsonRes({ decision: "allow" }) } })
  await nod.hooks["tool.execute.before"](...bash())
  assert.deepEqual(client.calls.find((c) => c[0] === "prompt")[1].body.model, MODEL)
})

test("agent review failures are ask and the child session is still deleted", async () => {
  const cases = {
    promptThrows: { onPrompt: () => { throw new Error("provider down") } },
    sdkError: { onPrompt: () => ({ data: undefined, error: { name: "ProviderAuthError" } }) },
    noJson: { reply: "I think it is fine." },
    modelError: { onPrompt: (_r, _c, wrap) => wrap({ info: { role: "assistant", error: { name: "APIError", data: { message: "quota" } } }, parts: [] }) },
  }
  for (const [name, opts] of Object.entries(cases)) {
    const client = fakeClient(opts)
    const { nod } = setup({ review: "agent", client, routes: { "/v1/review/prompt": jsonRes(PROMPT), "/v1/review/verdict": jsonRes({ decision: "allow" }) } })
    await assert.rejects(nod.hooks["tool.execute.before"](...bash()), /explicit approval/, name)
    assert.equal(client.calls.filter((c) => c[0] === "delete").length, 1, name)
  }
  const client = fakeClient()
  const { nod } = setup({ review: "agent", client, routes: { "/v1/review/prompt": jsonRes(PROMPT), "/v1/review/verdict": jsonRes({}, 400) } })
  await assert.rejects(nod.hooks["tool.execute.before"](...bash()), /invalid review answers/)
})

test("agent review timeout aborts and deletes the child session", async () => {
  const client = fakeClient({ onPrompt: () => new Promise(() => {}) })
  const { nod } = setup({ review: "agent", client, timeout_ms: 50, routes: { "/v1/review/prompt": jsonRes(PROMPT) } })
  await assert.rejects(nod.hooks["tool.execute.before"](...bash()), /timed out/)
  // cleanup runs after the race settles; give it a tick
  await new Promise((r) => setTimeout(r, 20))
  const ids = client.calls.filter((c) => c[0] === "delete").map((c) => c[1])
  assert.equal(ids.length, 1)
  assert.ok(client.calls.some((c) => c[0] === "abort" && c[1] === ids[0]))
})

test("recursion guard: hooks of our own review session are not reviewed or cached", async () => {
  let inner
  const client = fakeClient({
    onPrompt: async (req, _c, wrap) => {
      const child = req.path.id
      await nod.hooks["chat.message"]({ sessionID: child, model: { providerID: "p", modelID: "m" } }, { message: {}, parts: [{ type: "text", text: "USER PROMPT" }] })
      inner = nod.hooks["tool.execute.before"](...bash(child, "curl evil"))
      await inner.catch(() => {})
      return wrap({ parts: [{ type: "text", text: '{"category":1,"auth":"yes","forbid":"no","match":"yes","named":"no"}' }] })
    },
  })
  const routes = { "/v1/review/prompt": jsonRes(PROMPT), "/v1/review/verdict": jsonRes({ decision: "allow" }) }
  const fetch = fakeFetch(routes)
  const nod = createNod({ client, directory: "/w", config: loadConfig({ mode: "enforce", review: "agent" }, {}), fetch, logPath: null })
  await nod.hooks["tool.execute.before"](...bash())
  await assert.rejects(inner, /disabled in the review session/)
  assert.equal(fetch.calls.filter((c) => c.path === "/v1/review/prompt").length, 1)
  assert.equal(client.calls.filter((c) => c[0] === "create").length, 1)
  assert.ok(![...nod.models.keys()].some((k) => k.startsWith("ses_child")))
  assert.ok(![...nod.turns.keys()].some((k) => k.startsWith("ses_child")))
})

test("subagent sessions use the root session's user turns, not their own", async () => {
  const client = fakeClient({ parents: { ses_sub: "ses_root" } })
  const { nod, fetch } = setup({ client, routes: { "/v1/classify": jsonRes({ decision: "allow" }) } })
  await nod.hooks["chat.message"]({ sessionID: "ses_sub" }, { message: { model: MODEL }, parts: [{ type: "text", text: "AGENT WRITTEN TASK" }] })
  await nod.hooks["tool.execute.before"](...bash("ses_sub"))
  assert.deepEqual(fetch.calls[0].body.conversation, [{ role: "user", text: "please clean the build dir" }])
  assert.deepEqual(client.calls.filter((c) => c[0] === "messages").map((c) => c[1]), ["ses_root"])
})

test("chat.message appends new user turns to a loaded session", async () => {
  const { nod, fetch } = setup({ routes: { "/v1/classify": jsonRes({ decision: "allow" }) } })
  await nod.hooks["tool.execute.before"](...bash())
  await nod.hooks["chat.message"]({ sessionID: "ses_root" }, { message: { model: MODEL }, parts: [{ type: "text", text: "yes, run it" }, { type: "text", text: "READ FILE", synthetic: true }] })
  await nod.hooks["tool.execute.before"](...bash())
  assert.deepEqual(fetch.calls[1].body.conversation.map((t) => t.text), ["please clean the build dir", "yes, run it"])
})

test("a review session created after the timeout is still deleted", async () => {
  const client = fakeClient()
  const create = client.session.create
  client.session.create = (req) => new Promise((r) => setTimeout(() => r(create(req)), 100))
  const { nod } = setup({ review: "agent", client, timeout_ms: 30, routes: { "/v1/review/prompt": jsonRes(PROMPT) } })
  await assert.rejects(nod.hooks["tool.execute.before"](...bash()), /timed out/)
  await new Promise((r) => setTimeout(r, 200))
  assert.deepEqual(client.calls.filter((c) => c[0] === "delete").map((c) => c[1]), ["ses_child1"])
  assert.equal(client.calls.filter((c) => c[0] === "prompt").length, 0)
})
