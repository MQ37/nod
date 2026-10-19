// Unit tests of the embedded OpenCode plugin (internal/hosts/assets/opencode/nod.js).
// No network: a fake fetch and a fake OpenCode client. Lives under testdata so it is never embedded.
// Run: node --test internal/hosts/opencode/testdata/
import test from "node:test"
import assert from "node:assert/strict"
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { spawn } from "node:child_process"
import { createHash } from "node:crypto"
import { fileURLToPath, pathToFileURL } from "node:url"
import plugin, { createNodV2, userTurnsV2, setup as setupV2, buildRequest, clip, createNod, extractJson, loadConfig, mapDecision, enforceError, redact, unwrap, userTurns, NO_TOOLS } from "../../assets/opencode/nod.js"
import { AUDIT, auditDir, clipRunes, commandFields, createAuditWriter, describeArgs, formatAuditLine, hashSession } from "../../assets/opencode/nod.js"
import { generateFixture } from "./gen-audit-fixture.mjs"

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

function setup({ mode = "enforce", review = "server", routes, client, timeout_ms = 2000, audit = null, extra = {} } = {}) {
  const fetch = fakeFetch(routes || {})
  const c = client || fakeClient()
  const nod = createNod({ client: c, directory: "/work", config: loadConfig({ mode, review, timeout_ms, ...extra }, {}), fetch, audit })
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
  assert.deepEqual(loadConfig("", {}), { url: "http://127.0.0.1:11096", mode: "shadow", review: "server", timeout_ms: 15000, tools: ["bash", "shell"], audit_dir: "", audit_commands: "full" })
  const c = loadConfig('{"url":"http://h:1/","mode":"enforce","review":"agent","timeout_ms":5,"tools":["bash","nope"],"audit_dir":"/a/b","audit_commands":"HASH"}', { NOD_MODE: "shadow" })
  assert.deepEqual(c, { url: "http://h:1", mode: "shadow", review: "agent", timeout_ms: 5, tools: ["bash", "shell"], audit_dir: "/a/b", audit_commands: "hash" })
  assert.equal(loadConfig({ audit_dir: "rel/dir", audit_commands: "bogus" }, {}).audit_dir, "")
  assert.equal(loadConfig({ audit_commands: "bogus" }, {}).audit_commands, "full")
  assert.equal(loadConfig({ audit_commands: "hash" }, { NOD_AUDIT_COMMANDS: "preview" }).audit_commands, "preview")
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

test("shadow never throws and still writes the audit line", async () => {
  const a = tmpAudit()
  for (const routes of [{ "/v1/classify": jsonRes({ decision: "block", reason: "bad", source: "model" }) }, {}]) {
    const { nod } = setup({ mode: "shadow", routes, audit: a.writer })
    await nod.hooks["tool.execute.before"](...bash("ses_root", "rm -rf /some-dir"))
  }
  const lines = a.entries()
  assert.deepEqual(lines.map((l) => [l.decision, l.source, l.mode]), [["block", "model", "shadow"], ["ask", "error", "shadow"]])
  assert.equal(fs.statSync(a.file).mode & 0o777, 0o600)
  assert.equal(fs.statSync(path.dirname(a.file)).mode & 0o777, 0o700)
  a.done()
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
  const a = tmpAudit()
  const nod = createNod({ client, directory: "/w", config: loadConfig({ mode: "enforce", review: "agent" }, {}), fetch, audit: a.writer })
  await nod.hooks["tool.execute.before"](...bash())
  await assert.rejects(inner, /disabled in the review session/)
  // The refused call inside the review session is in the trail too, without a review.
  const lines = a.entries()
  assert.deepEqual(lines.map((l) => [l.decision, l.source]), [["block", "review-session"], ["allow", undefined]])
  assert.equal(lines[0].session, hashSession("ses_child1"))
  assert.equal(lines[0].command, '{"command":"curl evil"}')
  assert.equal(lines[1].model, "anthropic/claude-x")
  a.done()
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


// ---- OpenCode v2 (setup(ctx), ctx.tool.hook, ctx.session.get/context, ctx.generate.text) ----

function fakeCtx(opts = {}) {
  const calls = []
  const hooks = {}
  const msgs = opts.msgs || [
    { id: "m1", type: "user", text: "please clean the build dir", time: {}, files: [], agents: [] },
    { id: "m2", type: "assistant", text: "ASSISTANT TEXT", time: {}, files: [], agents: [] },
    { id: "m3", type: "tool", text: "TOOL OUTPUT", time: {}, files: [], agents: [] },
  ]
  const ctx = {
    location: { directory: "/w/proj" },
    tool: { hook: async (name, fn) => { hooks[name] = fn } },
    session: {
      get: async ({ sessionID }) => { calls.push(["get", sessionID]); return { id: sessionID, model: opts.model === undefined ? { providerID: "anthropic", id: "claude-x", variant: "" } : opts.model } },
      context: async ({ sessionID }) => { calls.push(["context", sessionID]); return msgs },
    },
    generate: {
      text: async (req) => {
        calls.push(["generate", req])
        if (opts.generateError) throw new Error(opts.generateError)
        return { text: opts.reply ?? '{"category": 3, "auth": "yes", "forbid": "no", "match": "no", "named": "no"}' }
      },
    },
  }
  return { ctx, calls, hooks }
}

const shellEvent = (command = "ls -la", tool = "shell") => ({ tool, sessionID: "ses_1", agent: "build", messageID: "msg_1", id: "call_1", input: { command } })

function setupNodV2({ mode = "enforce", review = "server", routes, ctxOpts, audit = null, extra = {} } = {}) {
  const c = fakeCtx(ctxOpts)
  const fetch = fakeFetch(routes || {})
  const nod = createNodV2({ ctx: c.ctx, config: loadConfig({ mode, review, ...extra }, {}), fetch, audit, now: () => new Date("2026-10-18T12:00:00Z") })
  return { ...c, nod, fetch }
}

test("v2: the default export has id, setup and server; setup registers execute.before", async () => {
  assert.equal(plugin.id, "nod")
  assert.equal(typeof plugin.setup, "function")
  assert.equal(typeof plugin.server, "function")
  const { ctx, hooks } = fakeCtx()
  await setupV2(ctx)
  assert.equal(typeof hooks["execute.before"], "function")
})

test("v2: userTurnsV2 keeps only the human's messages", () => {
  const out = userTurnsV2([{ type: "user", text: "a" }, { type: "assistant", text: "SECRET" }, { type: "tool", text: "OUT" }, { type: "user", text: " b " }, { type: "user", text: "" }])
  assert.deepEqual(out, ["a", "b"])
  assert.deepEqual(userTurnsV2(undefined), [])
})

test("v2: shell and bash are both gated; other tools are ignored", async () => {
  for (const tool of ["shell", "bash"]) {
    const { nod, fetch } = setupNodV2({ routes: { "/v1/classify": jsonRes({ decision: "allow", reason: "r", source: "model" }) } })
    await nod.onToolBefore(shellEvent("ls", tool))
    assert.equal(fetch.calls.length, 1, tool)
  }
  const { nod, fetch } = setupNodV2({ routes: {} })
  await nod.onToolBefore({ tool: "read", sessionID: "s", input: { filePath: "/x" } })
  assert.equal(fetch.calls.length, 0)
})

test("v2: enforce maps allow/ask/block; shadow never throws", async () => {
  for (const [decision, re] of [["allow", null], ["block", /nod: blocked: r\./], ["ask", /explicit approval \(r\)/]]) {
    const { nod } = setupNodV2({ routes: { "/v1/classify": jsonRes({ decision, reason: "r", source: "model" }) } })
    const p = nod.onToolBefore(shellEvent())
    if (re) await assert.rejects(p, re)
    else await p
  }
  const { nod } = setupNodV2({ mode: "shadow", routes: { "/v1/classify": jsonRes({ decision: "block", reason: "r", source: "model" }) } })
  await nod.onToolBefore(shellEvent())
})

test("v2: server review sends the command and only the human's turns", async () => {
  const { nod, fetch } = setupNodV2({ routes: { "/v1/classify": jsonRes({ decision: "allow", reason: "r", source: "model" }) } })
  await nod.onToolBefore(shellEvent("git status"))
  const body = fetch.calls[0].body
  assert.equal(body.tool, "Bash")
  assert.equal(body.input, "git status")
  assert.equal(body.cwd, "/w/proj")
  assert.deepEqual(body.conversation, [{ role: "user", text: "please clean the build dir" }])
  const sent = JSON.stringify(body)
  assert.ok(!sent.includes("ASSISTANT TEXT") && !sent.includes("TOOL OUTPUT"))
})

test("v2: agent review asks the session's own model through ctx.generate.text, then posts the verdict", async () => {
  const { nod, fetch, calls } = setupNodV2({
    review: "agent",
    routes: {
      "/v1/review/prompt": jsonRes({ system: "SYS", user: "USER", keys: ["category"] }),
      "/v1/review/verdict": jsonRes({ decision: "allow", reason: "approved", source: "agent" }),
    },
  })
  await nod.onToolBefore(shellEvent("make test"))
  const gen = calls.find((c) => c[0] === "generate")[1]
  assert.deepEqual(gen.model, { providerID: "anthropic", id: "claude-x" })
  assert.ok(gen.prompt.includes("SYS") && gen.prompt.includes("USER"))
  const verdict = fetch.calls.find((c) => c.path === "/v1/review/verdict").body
  assert.equal(verdict.answers.category, 3)
  assert.equal(verdict.request.input, "make test")
})

test("v2: every failure is ask (never allow)", async () => {
  for (const ctxOpts of [{ generateError: "boom" }, { reply: "no json here" }, { model: null }]) {
    const { nod } = setupNodV2({
      review: "agent",
      ctxOpts,
      routes: { "/v1/review/prompt": jsonRes({ system: "S", user: "U", keys: [] }), "/v1/review/verdict": jsonRes({ decision: "allow", reason: "x", source: "agent" }) },
    })
    await assert.rejects(nod.onToolBefore(shellEvent()), /explicit approval/)
  }
  const { nod } = setupNodV2({ routes: {} }) // server unreachable
  await assert.rejects(nod.onToolBefore(shellEvent()), /explicit approval/)
})

// ---- Audit trail (mirrors internal/decisionlog; the Go side verifies audit-opencode.jsonl.fixture) ----

const sha = (s) => createHash("sha256").update(s).digest("hex")

function tmpAudit(opts = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "nodaudit-"))
  const file = path.join(dir, "audit", "opencode.jsonl")
  const warnings = []
  const writer = createAuditWriter({ file, warn: (m) => warnings.push(m), ...opts })
  const entries = () => fs.readFileSync(file, "utf8").trim().split("\n").map((l) => JSON.parse(l))
  return { dir, file, writer, warnings, entries, done: () => fs.rmSync(dir, { recursive: true, force: true }) }
}

// verifyChain is decisionlog.Verify: oldest file first, prev = sha256 of the exact previous line.
function verifyChain(file, keep = 5) {
  const files = []
  for (let i = keep; i >= 1; i--) if (fs.existsSync(`${file}.${i}`)) files.push(`${file}.${i}`)
  if (fs.existsSync(file)) files.push(file)
  let prev = null
  let lines = 0
  let truncated = false
  for (const f of files) {
    const all = fs.readFileSync(f, "utf8").split("\n")
    for (let n = 0; n < all.length; n++) {
      const raw = all[n]
      if (!raw.trim()) continue
      const e = JSON.parse(raw)
      if (prev === null) truncated = e.prev !== AUDIT.genesis
      else if (e.prev !== prev) return { lines, brk: { file: f, line: n + 1 }, truncated }
      prev = sha(Buffer.from(raw, "utf8"))
      lines++
    }
  }
  return { lines, brk: null, truncated }
}

const fixed = () => new Date("2026-10-19T08:00:00.123Z")

test("audit: commandFields matches decisionlog.CommandFields (same inputs as the Go test)", () => {
  const cmd = "curl -H 'Authorization: Bearer abcdef123456789' https://x -d API_KEY=hunter2 ghp_ABCDEFGHIJKLMNOPQRST " + "a".repeat(5000)
  const full = commandFields(cmd, "")
  assert.equal(full.sha, sha(cmd))
  for (const leak of ["abcdef123456789", "hunter2", "ghp_ABCDEFGHIJKLMNOPQRST"]) assert.ok(!full.text.includes(leak), leak)
  assert.ok(full.text.startsWith("curl -H 'Authorization: <redacted>' https://x -d API_KEY=<v> <tok> aaa"), full.text.slice(0, 80))
  assert.equal(Array.from(full.text).length, AUDIT.maxCommand + 1)
  assert.ok(full.text.endsWith("…"))
  const prev = commandFields(cmd, "preview").text
  assert.equal(Array.from(prev).length, AUDIT.previewLen + 1)
  assert.deepEqual(commandFields(cmd, "hash"), { sha: full.sha, text: "" })
  assert.deepEqual(commandFields("", "full"), { sha: "", text: "" })
  assert.equal(commandFields("ls", "BOGUS").text, "ls") // unknown level = full
  assert.equal(redact("-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----"), "<private-key>")
  // The replacement strings of redact.go, one by one.
  assert.equal(redact("x Authorization: Basic dXNlcjpwYXNz y"), "x Authorization: <redacted> y")
  assert.equal(redact("Bearer abcdefgh123"), "Bearer <redacted>")
  assert.equal(redact("sk-proj-ABCDEFGHIJKLMNOP"), "<tok>")
  assert.equal(redact('DB_PASSWORD="a b" GH_TOKEN=x'), "DB_PASSWORD=<v> GH_TOKEN=<v>")
  // Go's \s is ASCII only: a no-break space ends nothing here either.
  assert.equal(redact("API_KEY=a b c"), "API_KEY=<v> c")
  // Clipping counts code points like Go's []rune, not UTF-16 units.
  assert.equal(clipRunes("😀".repeat(5), 3), "😀😀😀…")
  assert.equal(clipRunes("😀😀", 2), "😀😀")
})

test("audit: auditDir order is nod.json, NOD_AUDIT_DIR, XDG_STATE_HOME, home (absolute only)", () => {
  assert.equal(auditDir({ audit_dir: "/cfg" }, { NOD_AUDIT_DIR: "/env", XDG_STATE_HOME: "/st" }, "/h"), "/cfg")
  assert.equal(auditDir({ audit_dir: "rel" }, { NOD_AUDIT_DIR: "/env", XDG_STATE_HOME: "/st" }, "/h"), "/env")
  assert.equal(auditDir({}, { NOD_AUDIT_DIR: "env", XDG_STATE_HOME: "/st" }, "/h"), "/st/nod/audit")
  assert.equal(auditDir({}, { XDG_STATE_HOME: "st" }, "/h"), "/h/.local/state/nod/audit")
})

test("audit: a gated call writes one line with Go's fields, order and types", async () => {
  const a = tmpAudit()
  const command = "rm -rf /some-dir API_KEY=hunter2"
  const fetch = fakeFetch({ "/v1/classify": jsonRes({ decision: "block", reason: "deletes data", source: "model", category: "7", rules: ["r1", 2], ms: 3 }) })
  let t = 1000
  const nod = createNod({ client: fakeClient(), directory: "/work", config: loadConfig({ mode: "shadow" }, {}), fetch, audit: a.writer, now: fixed, clock: () => (t += 12) })
  await nod.hooks["tool.execute.before"](...bash("ses_root", command))
  const raw = fs.readFileSync(a.file, "utf8")
  assert.ok(raw.endsWith("\n") && raw.split("\n").length === 2)
  const e = JSON.parse(raw)
  assert.deepEqual(Object.keys(e), ["v", "time", "host", "session", "mode", "review", "tool", "command", "command_sha256", "decision", "source", "rules", "category", "ms", "cwd", "reason", "prev"])
  assert.deepEqual(e, {
    v: 1,
    time: "2026-10-19T08:00:00.123Z",
    host: "opencode",
    session: sha("ses_root").slice(0, 16),
    mode: "shadow",
    review: "decider",
    tool: "Bash",
    command: "rm -rf /some-dir API_KEY=<v>",
    command_sha256: sha(command),
    decision: "block",
    source: "model",
    rules: ["r1"],
    category: "7",
    ms: 12,
    cwd: "/work",
    reason: "deletes data",
    prev: AUDIT.genesis,
  })
  assert.ok(!raw.includes("hunter2"))
  a.done()
})

test("audit: shadow and enforce log the same verdict; enforce throws only after the line is written", async () => {
  for (const mode of ["shadow", "enforce"]) {
    const a = tmpAudit()
    const { nod } = setup({ mode, audit: a.writer, routes: { "/v1/classify": jsonRes({ decision: "block", reason: "r", source: "model" }) } })
    const p = nod.hooks["tool.execute.before"](...bash())
    if (mode === "enforce") await assert.rejects(p, /blocked/)
    else await p
    const [e] = a.entries()
    assert.equal(e.mode, mode)
    assert.equal(e.decision, "block")
    a.done()
  }
})

test("audit: a failed review is an ask line with source error and the error", async () => {
  const a = tmpAudit()
  const { nod } = setup({ audit: a.writer, routes: {} })
  await assert.rejects(nod.hooks["tool.execute.before"](...bash()), /explicit approval/)
  const [e] = a.entries()
  assert.equal(e.decision, "ask")
  assert.equal(e.source, "error")
  assert.match(e.error, /ECONNREFUSED/)
  assert.match(e.reason, /^nod could not review this call: /)
  a.done()
})

test("audit: agent review records the reviewing model (v1 and v2)", async () => {
  const a = tmpAudit()
  const { nod } = setup({ review: "agent", audit: a.writer, routes: { "/v1/review/prompt": jsonRes(PROMPT), "/v1/review/verdict": jsonRes({ decision: "allow", source: "agent", category: 3 }) } })
  await nod.hooks["tool.execute.before"](...bash())
  const v2 = setupNodV2({ review: "agent", audit: a.writer, routes: { "/v1/review/prompt": jsonRes(PROMPT), "/v1/review/verdict": jsonRes({ decision: "allow", source: "agent" }) } })
  await v2.nod.onToolBefore(shellEvent("make"))
  const v2fail = setupNodV2({ review: "agent", audit: a.writer, ctxOpts: { generateError: "quota" }, routes: { "/v1/review/prompt": jsonRes(PROMPT) } })
  await assert.rejects(v2fail.nod.onToolBefore(shellEvent("make")), /explicit approval/)
  const lines = a.entries()
  assert.deepEqual(lines.map((l) => [l.review, l.model, l.decision, l.category]), [
    ["agent", "anthropic/claude-x", "allow", "3"],
    ["agent", "anthropic/claude-x", "allow", undefined],
    ["agent", "anthropic/claude-x", "ask", undefined],
  ])
  assert.match(lines[2].error, /quota/)
  assert.equal(lines[1].session, hashSession("ses_1"))
  assert.equal(lines[1].cwd, "/w/proj")
  a.done()
})

test("audit: tools nod does not judge are logged as unchecked (v1 and v2)", async () => {
  const a = tmpAudit()
  const { nod, fetch } = setup({ audit: a.writer })
  await nod.hooks["tool.execute.before"]({ tool: "read", sessionID: "ses_root", callID: "c" }, { args: { filePath: "/etc/passwd" } })
  const big = { url: "https://x.example", headers: { Authorization: "Bearer abcdefghijklmnop" }, body: "z".repeat(5000) }
  await nod.hooks["tool.execute.before"]({ tool: "webfetch", sessionID: "ses_root", callID: "c" }, { args: big })
  const v2 = setupNodV2({ audit: a.writer })
  await v2.nod.onToolBefore({ tool: "edit", sessionID: "ses_1", input: { filePath: "/w/proj/a.go", oldString: "SECRET OLD", newString: "x" } })
  await v2.nod.onToolBefore({ tool: "task", sessionID: "ses_1", input: { prompt: "do it" } })
  // A gateable tool left out of "tools" is unchecked too.
  const off = setup({ audit: a.writer, extra: { tools: [] } })
  await off.nod.hooks["tool.execute.before"](...bash("ses_root", "ls"))
  assert.equal(fetch.calls.length + v2.fetch.calls.length + off.fetch.calls.length, 0)
  const lines = a.entries()
  assert.deepEqual(lines.map((l) => [l.tool, l.decision, l.source]), [
    ["read", "unchecked", "not-gated"],
    ["webfetch", "unchecked", "not-gated"],
    ["edit", "unchecked", "not-gated"],
    ["task", "unchecked", "not-gated"],
    ["bash", "unchecked", "not-gated"],
  ])
  assert.equal(lines[0].command, "/etc/passwd")
  assert.equal(lines[0].command_sha256, sha("/etc/passwd"))
  assert.equal(lines[0].cwd, "/work")
  assert.ok(!lines[1].command.includes("abcdefghijklmnop") && lines[1].command.includes("Bearer <redacted>"))
  assert.equal(Array.from(lines[1].command).length, AUDIT.maxArgs + 1)
  assert.equal(lines[1].command_sha256, sha(JSON.stringify(big)))
  assert.equal(lines[2].command, "/w/proj/a.go")
  assert.equal(lines[3].command, '{"prompt":"do it"}')
  assert.equal(lines[4].command, '{"command":"ls"}')
  assert.equal(describeArgs(undefined), "")
  a.done()
})

test("audit: command levels preview and hash (nod.json and NOD_AUDIT_COMMANDS)", async () => {
  const long = "echo " + "q".repeat(400)
  for (const [level, check] of [
    ["preview", (e) => assert.equal(Array.from(e.command).length, AUDIT.previewLen + 1)],
    ["hash", (e) => assert.equal(e.command, undefined)],
  ]) {
    const a = tmpAudit()
    const { nod } = setup({ mode: "shadow", audit: a.writer, extra: { audit_commands: level }, routes: { "/v1/classify": jsonRes({ decision: "allow" }) } })
    await nod.hooks["tool.execute.before"](...bash("ses_root", long))
    await nod.hooks["tool.execute.before"]({ tool: "read", sessionID: "ses_root" }, { args: { filePath: "/p/" + "f".repeat(300) } })
    const [g, u] = a.entries()
    assert.equal(g.command_sha256, sha(long))
    check(g)
    check(u)
    a.done()
  }
  assert.equal(loadConfig({}, { NOD_AUDIT_COMMANDS: "hash" }).audit_commands, "hash")
})

test("audit: the chain holds over many lines", async () => {
  const a = tmpAudit()
  for (let i = 0; i < 50; i++) assert.equal(await a.writer.append({ time: fixed().toISOString(), host: "opencode", tool: "Bash", decision: "allow", reason: "x".repeat(i % 7) }), true)
  const r = verifyChain(a.file)
  assert.deepEqual(r, { lines: 50, brk: null, truncated: false })
  assert.equal(a.entries()[0].prev, AUDIT.genesis)
  // Tampering is visible to the same check.
  const lines = fs.readFileSync(a.file, "utf8").trim().split("\n")
  lines[5] = lines[5].replace('"allow"', '"block"')
  fs.writeFileSync(a.file, lines.join("\n") + "\n")
  assert.equal(verifyChain(a.file).brk.line, 7)
  a.done()
})

test("audit: rotation keeps the chain across files and keeps at most `keep` old files", async () => {
  const a = tmpAudit({ maxBytes: 600, keep: 3 })
  for (let i = 0; i < 60; i++) await a.writer.append({ time: fixed().toISOString(), host: "opencode", tool: "Bash", decision: "allow", reason: "x".repeat(i % 7) })
  assert.ok(fs.existsSync(a.file + ".1") && fs.existsSync(a.file + ".3"))
  assert.ok(!fs.existsSync(a.file + ".4"))
  for (const f of [a.file, a.file + ".1"]) assert.ok(fs.statSync(f).size <= 600, f)
  const r = verifyChain(a.file)
  assert.equal(r.brk, null)
  assert.equal(r.truncated, true) // older files were rotated away
  const lastOf = (f) => fs.readFileSync(f, "utf8").trim().split("\n").pop()
  assert.equal(a.entries()[0].prev, sha(lastOf(a.file + ".1")))
  // An empty current file continues from .1 as well.
  fs.renameSync(a.file, a.file + ".1")
  await a.writer.append({ time: fixed().toISOString(), tool: "Bash", decision: "ask" })
  assert.equal(a.entries()[0].prev, sha(lastOf(a.file + ".1")))
  a.done()
})

test("audit: writing never throws or changes the outcome; the failure is reported once", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "nodaudit-"))
  const blocker = path.join(dir, "not-a-dir")
  fs.writeFileSync(blocker, "x")
  const warnings = []
  const writer = createAuditWriter({ file: path.join(blocker, "audit", "opencode.jsonl"), warn: (m) => warnings.push(m) })
  assert.equal(await writer.append({ tool: "Bash", decision: "allow" }), false)
  for (const [decision, re] of [["allow", null], ["block", /nod: blocked: r\./]]) {
    const { nod } = setup({ audit: writer, routes: { "/v1/classify": jsonRes({ decision, reason: "r", source: "model" }) } })
    const p = nod.hooks["tool.execute.before"](...bash())
    if (re) await assert.rejects(p, re)
    else await p
    await nod.hooks["tool.execute.before"]({ tool: "read", sessionID: "s" }, { args: { filePath: "/x" } })
  }
  assert.equal(warnings.length, 1)
  assert.match(warnings[0], /cannot write the audit trail/)
  // A failing write in the middle, and a writer that throws outright.
  const broken = createAuditWriter({ file: path.join(dir, "a", "opencode.jsonl"), warn: () => {}, fs: { ...fs, writeSync: () => { throw new Error("EIO") } } })
  assert.equal(await broken.append({ tool: "Bash", decision: "allow" }), false)
  assert.ok(!fs.existsSync(path.join(dir, "a", "opencode.jsonl.lock.d")), "the lock is released after a failure")
  const thrower = { append: () => { throw new Error("boom") } }
  const v2 = setupNodV2({ mode: "shadow", audit: thrower, routes: { "/v1/classify": jsonRes({ decision: "block", reason: "r" }) } })
  await v2.nod.onToolBefore(shellEvent())
  await v2.nod.onToolBefore({ tool: "read", sessionID: "s", input: {} })
  fs.rmSync(dir, { recursive: true, force: true })
})

test("audit: the lock directory serialises writers; stale locks are taken over, busy ones time out", async () => {
  const a = tmpAudit({ lockWaitMs: 150 })
  fs.mkdirSync(path.dirname(a.file), { recursive: true })
  const lock = a.file + ".lock.d"
  // Held by someone else: wait, then write once it is released.
  fs.mkdirSync(lock)
  setTimeout(() => fs.rmdirSync(lock), 60)
  assert.equal(await a.writer.append({ tool: "Bash", decision: "allow" }), true)
  // Held and never released: give up after lockWaitMs, report once.
  fs.mkdirSync(lock)
  assert.equal(await a.writer.append({ tool: "Bash", decision: "allow" }), false)
  assert.equal(a.warnings.length, 1)
  // Older than lockStaleMs: a crashed writer's lock, taken over.
  const old = new Date(Date.now() - 60000)
  fs.utimesSync(lock, old, old)
  assert.equal(await a.writer.append({ tool: "Bash", decision: "ask" }), true)
  assert.ok(!fs.existsSync(lock))
  assert.deepEqual(verifyChain(a.file), { lines: 2, brk: null, truncated: false })
  a.done()
})

test("audit: interleaved appenders (two writers in this process, four processes) keep one chain", async () => {
  const a = tmpAudit()
  const w2 = createAuditWriter({ file: a.file })
  const entry = (i) => ({ time: new Date().toISOString(), host: "opencode", tool: "Bash", decision: "allow", reason: "in-process " + i })
  const results = await Promise.all(Array.from({ length: 40 }, (_, i) => (i % 2 ? a.writer : w2).append(entry(i))))
  assert.ok(results.every(Boolean))
  const nodJs = fileURLToPath(new URL("../../assets/opencode/nod.js", import.meta.url))
  const script = `import { createAuditWriter } from ${JSON.stringify(pathToFileURL(nodJs).href)}
const w = createAuditWriter({ file: process.argv[1] })
for (let i = 0; i < 30; i++) if (!(await w.append({ time: new Date().toISOString(), host: "opencode", tool: "Bash", decision: "allow", reason: "pid " + process.pid + " " + i }))) process.exit(3)`
  const codes = await Promise.all(
    Array.from({ length: 4 }, () => new Promise((resolve) => spawn(process.execPath, ["--input-type=module", "-e", script, a.file], { stdio: "inherit" }).on("exit", resolve))),
  )
  assert.deepEqual(codes, [0, 0, 0, 0])
  assert.deepEqual(verifyChain(a.file), { lines: 160, brk: null, truncated: false })
  assert.equal(new Set(a.entries().map((e) => e.prev)).size, 160)
  a.done()
})

test("audit: formatAuditLine coerces types Go's Entry needs", () => {
  const e = JSON.parse(formatAuditLine({ time: "2026-10-19T08:00:00.000Z", tool: undefined, decision: "allow", ms: 12.6, category: 4, rules: ["a", null], session: "" }, AUDIT.genesis))
  assert.deepEqual(e, { v: 1, time: "2026-10-19T08:00:00.000Z", tool: "", decision: "allow", rules: ["a"], category: "4", ms: 13, prev: AUDIT.genesis })
})

test("audit: the committed Go interop fixture is what the plugin code writes", async () => {
  const want = fs.readFileSync(new URL("./audit-opencode.jsonl.fixture", import.meta.url), "utf8")
  const got = await generateFixture()
  assert.equal(got, want, "regenerate: node internal/hosts/opencode/testdata/gen-audit-fixture.mjs > internal/hosts/opencode/testdata/audit-opencode.jsonl.fixture")
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "nodfix-"))
  fs.writeFileSync(path.join(dir, "opencode.jsonl"), got)
  assert.deepEqual(verifyChain(path.join(dir, "opencode.jsonl")), { lines: 12, brk: null, truncated: false })
  fs.rmSync(dir, { recursive: true, force: true })
})
