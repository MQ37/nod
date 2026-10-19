// nod OpenCode plugin (managed by nod: `nod hooks install --host opencode`; remove with `nod hooks remove --host opencode`)
//
// Gates OpenCode tool calls (by default only `bash`) with nod. Settings come from nod.json next to
// this file (written by nod), overridable with NOD_URL / NOD_MODE / NOD_REVIEW in the environment.
//
//   review "server": POST {url}/v1/classify, the Decider on the nod server decides.
//   review "agent":  POST {url}/v1/review/prompt, ask the agent's OWN model in a hidden child
//                    session (no tools), then POST {url}/v1/review/verdict with its answers.
//
// mode "shadow" (default) never interferes: it only appends a line (no command text) to
// <XDG_RUNTIME_DIR or tmpdir>/nod/opencode-decisions.jsonl. mode "enforce": allow runs the call,
// block and ask throw (OpenCode turns the error into the tool result; a plugin cannot open a prompt).
// Any failure counts as ask, never as allow.
//
// The default export is an object {id, server, setup} on purpose: OpenCode calls EVERY export of a module
// without it as a plugin, and the named helper exports below exist only for the unit tests.
// Plain ESM, Node/Bun built-ins only, no build step.

import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

export const DEFAULTS = Object.freeze({
  url: "http://127.0.0.1:11096",
  mode: "shadow",
  review: "server",
  timeout_ms: 15000,
  tools: ["bash"],
})

// OpenCode tool id -> the tool name nod knows and the text it judges. Keep it small and explicit.
export const TOOL_MAP = Object.freeze({
  bash: { name: "Bash", input: (args) => (args && typeof args.command === "string" ? args.command : "") },
})

// Every tool off in the review session ("*" covers the rest; permissions are keyed by these names).
export const NO_TOOLS = Object.freeze(
  Object.fromEntries(
    ["*", "bash", "edit", "write", "read", "grep", "glob", "list", "webfetch", "websearch", "task", "todowrite", "todoread", "patch", "apply_patch", "skill", "question", "lsp"].map((t) => [t, false]),
  ),
)

const MAX_INPUT = 4000
const MAX_TURN = 600
const MAX_TURNS = 6

export function loadConfig(raw, env = {}) {
  let file = {}
  if (typeof raw === "string" && raw.trim()) {
    try {
      file = JSON.parse(raw)
    } catch {
      file = {}
    }
  } else if (raw && typeof raw === "object") file = raw
  if (!file || typeof file !== "object" || Array.isArray(file)) file = {}
  const cfg = { ...DEFAULTS, tools: [...DEFAULTS.tools] }
  if (typeof file.url === "string" && file.url.trim()) cfg.url = file.url.trim()
  if (typeof file.mode === "string") cfg.mode = file.mode
  if (typeof file.review === "string") cfg.review = file.review
  if (Number.isFinite(file.timeout_ms) && file.timeout_ms > 0) cfg.timeout_ms = file.timeout_ms
  if (Array.isArray(file.tools)) cfg.tools = file.tools.filter((t) => typeof t === "string" && TOOL_MAP[t])
  if (env.NOD_URL) cfg.url = String(env.NOD_URL).trim()
  if (env.NOD_MODE) cfg.mode = String(env.NOD_MODE)
  if (env.NOD_REVIEW) cfg.review = String(env.NOD_REVIEW)
  cfg.url = cfg.url.replace(/\/+$/, "")
  cfg.mode = cfg.mode.trim().toLowerCase() === "enforce" ? "enforce" : "shadow"
  cfg.review = cfg.review.trim().toLowerCase() === "agent" ? "agent" : "server"
  return cfg
}

const SECRETS = [
  [/-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)/g, "<private-key>"],
  [/(Authorization\s*:\s*)(?:Bearer|Basic|token|Token)?\s*[^\s'"]+/gi, "$1<redacted>"],
  [/\bBearer\s+[A-Za-z0-9._~+/=-]{8,}/g, "Bearer <redacted>"],
  [/\b(?:sk-|ghp_|gho_|ghs_|ghu_|github_pat_|glpat-|xox[a-z]-|apify_api_|AKIA|ASIA|eyJ)[A-Za-z0-9_\-.]{12,}/g, "<tok>"],
  [/\b([A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|PASS)[A-Z0-9_]*=)(?:"[^"]*"|'[^']*'|\S+)/g, "$1<v>"],
]

export function redact(s) {
  let out = typeof s === "string" ? s : String(s ?? "")
  for (const [re, rep] of SECRETS) out = out.replace(re, rep)
  return out
}

export function clip(s, n) {
  const t = typeof s === "string" ? s : String(s ?? "")
  return t.length > n ? t.slice(0, n) + " [clipped]" : t
}

// The text of a user message, from its parts: real text only (no synthetic file contents, no ignored parts).
export function userText(parts) {
  if (!Array.isArray(parts)) return ""
  return parts
    .filter((p) => p && p.type === "text" && typeof p.text === "string" && !p.synthetic && !p.ignored)
    .map((p) => p.text)
    .join("\n")
    .trim()
}

// User turns from client.session.messages() output ({info, parts}[]). Never assistant or tool text.
export function userTurns(messages) {
  if (!Array.isArray(messages)) return []
  const out = []
  for (const m of messages) {
    if (!m || !m.info || m.info.role !== "user") continue
    const text = userText(m.parts)
    if (text) out.push(text)
  }
  return out.slice(-MAX_TURNS)
}

// ClassifyRequest for nod. turns are the human's own message texts, oldest first.
export function buildRequest({ tool, input, cwd, turns }) {
  const req = { tool, input: clip(redact(input), MAX_INPUT), cwd: typeof cwd === "string" ? cwd : "" }
  const conv = (Array.isArray(turns) ? turns : [])
    .filter((t) => typeof t === "string" && t.trim())
    .slice(-MAX_TURNS)
    .map((t) => ({ role: "user", text: clip(redact(t.trim()), MAX_TURN) }))
  if (conv.length) {
    req.task = conv[conv.length - 1].text
    req.conversation = conv
  }
  return req
}

// First balanced {...} that parses as a JSON object (tolerates code fences and prose around it).
export function extractJson(text) {
  if (typeof text !== "string") return null
  for (let start = text.indexOf("{"); start >= 0; start = text.indexOf("{", start + 1)) {
    let depth = 0
    let inStr = false
    let esc = false
    for (let i = start; i < text.length; i++) {
      const c = text[i]
      if (inStr) {
        if (esc) esc = false
        else if (c === "\\") esc = true
        else if (c === '"') inStr = false
        continue
      }
      if (c === '"') inStr = true
      else if (c === "{") depth++
      else if (c === "}" && --depth === 0) {
        try {
          const v = JSON.parse(text.slice(start, i + 1))
          if (v && typeof v === "object" && !Array.isArray(v)) return v
        } catch {}
        break
      }
    }
  }
  return null
}

// A nod ClassifyResponse -> {decision, reason, source}; anything unexpected is ask.
export function mapDecision(resp) {
  const r = resp && typeof resp === "object" ? resp : {}
  const d = typeof r.decision === "string" ? r.decision.toLowerCase() : ""
  const decision = d === "allow" || d === "block" || d === "ask" ? d : "ask"
  let reason = typeof r.reason === "string" && r.reason ? r.reason : typeof r.error === "string" ? r.error : ""
  if (!reason && decision !== d) reason = "nod returned no valid decision"
  return { decision, reason, source: typeof r.source === "string" ? r.source : "" }
}

// The error to throw in enforce mode, or null to let the call run.
export function enforceError(decision, reason) {
  const why = reason || "no reason given"
  if (decision === "allow") return null
  if (decision === "block") {
    return new Error(`nod: blocked: ${why}. Do not retry this exact action; choose a different approach or ask the user.`)
  }
  return new Error(`nod: this needs the user's explicit approval (${why}). Ask the user to confirm this exact action in plain words, then retry.`)
}

// SDK results come either raw or as {data, error, ...} (hey-api "fields" style): return the data, throw the error.
export function unwrap(res) {
  if (res && typeof res === "object" && ("data" in res || "error" in res)) {
    if (res.error) {
      const e = res.error
      throw e instanceof Error ? e : new Error(typeof e === "string" ? e : e?.data?.message || e?.message || e?.name || "opencode request failed")
    }
    return res.data
  }
  return res
}

// The reply text of a session.prompt result ({info, parts}).
export function replyText(res) {
  if (!res || typeof res !== "object") throw new Error("empty review reply")
  if (res.info && res.info.error) {
    const e = res.info.error
    throw new Error("review model error: " + (e?.data?.message || e?.name || "unknown"))
  }
  const parts = Array.isArray(res.parts) ? res.parts : []
  return parts
    .filter((p) => p && p.type === "text" && typeof p.text === "string")
    .map((p) => p.text)
    .join("\n")
}

export function withTimeout(promise, ms, onTimeout) {
  let timer
  const t = new Promise((_, reject) => {
    timer = setTimeout(() => {
      try {
        onTimeout?.()
      } catch {}
      reject(new Error(`timed out after ${ms} ms`))
    }, ms)
  })
  return Promise.race([promise, t]).finally(() => clearTimeout(timer))
}

// abortable settles like promise, or rejects as soon as signal aborts (even if promise ignores it).
export function abortable(promise, signal) {
  if (!signal) return promise
  return new Promise((resolve, reject) => {
    const onAbort = () => reject(new Error("aborted"))
    if (signal.aborted) return onAbort()
    signal.addEventListener("abort", onAbort, { once: true })
    Promise.resolve(promise).then(
      (v) => (signal.removeEventListener("abort", onAbort), resolve(v)),
      (e) => (signal.removeEventListener("abort", onAbort), reject(e)),
    )
  })
}

export function defaultLogPath(env = process.env) {
  const base = env.XDG_RUNTIME_DIR && path.isAbsolute(env.XDG_RUNTIME_DIR) ? env.XDG_RUNTIME_DIR : os.tmpdir()
  return path.join(base, "nod", "opencode-decisions.jsonl")
}

function appendLog(file, rec) {
  if (!file) return
  try {
    fs.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 })
    fs.appendFileSync(file, JSON.stringify(rec) + "\n", { mode: 0o600 })
  } catch {}
}

function short(err) {
  const m = err && typeof err.message === "string" ? err.message : String(err)
  return clip(m.replace(/\s+/g, " "), 200)
}

// createNod builds the hooks. deps: {client, directory, config, fetch, logPath, now}.
export function createNod(deps) {
  const cfg = deps.config || loadConfig({}, {})
  const client = deps.client
  const doFetch = deps.fetch || globalThis.fetch
  const logPath = deps.logPath === undefined ? defaultLogPath() : deps.logPath
  const now = deps.now || (() => new Date())
  const gated = new Set(cfg.tools)
  const children = new Set() // review sessions we created: never reviewed, never cached
  const models = new Map() // sessionID -> {providerID, modelID}
  const turns = new Map() // sessionID -> user texts, oldest first
  const roots = new Map() // sessionID -> root sessionID (or null when unknown)

  async function post(p, body, signal) {
    const res = await doFetch(cfg.url + p, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
      signal,
    })
    if (!res || !res.ok) {
      const status = res ? res.status : "no response"
      throw new Error(status === 400 && p === "/v1/review/verdict" ? "invalid review answers" : `nod server ${p}: HTTP ${status}`)
    }
    return await res.json()
  }

  function remember(sid, text) {
    if (!text) return
    const list = turns.get(sid) || []
    list.push(text)
    turns.set(sid, list.slice(-MAX_TURNS))
  }

  async function fetchMessages(sid, signal) {
    return unwrap(await client.session.messages({ path: { id: sid }, throwOnError: true, signal }))
  }

  // Subagent sessions get their "user" messages from the parent model, not the human:
  // their conversation is the root session's.
  async function rootOf(sid, signal) {
    if (roots.has(sid)) return roots.get(sid)
    let cur = sid
    try {
      for (let i = 0; i < 8; i++) {
        const info = unwrap(await client.session.get({ path: { id: cur }, throwOnError: true, signal }))
        if (!info || !info.parentID) break
        cur = info.parentID
      }
    } catch {
      return null // unknown: send no conversation rather than possibly agent-written text
    }
    roots.set(sid, cur)
    return cur
  }

  async function turnsFor(root, signal) {
    if (!root) return []
    if (!turns.has(root)) {
      try {
        turns.set(root, userTurns(await fetchMessages(root, signal)))
      } catch {
        return []
      }
    }
    return turns.get(root) || []
  }

  async function modelFor(sid, root, signal) {
    const m = models.get(sid) || (root && models.get(root))
    if (m) return m
    const list = await fetchMessages(sid, signal)
    for (let i = (Array.isArray(list) ? list.length : 0) - 1; i >= 0; i--) {
      const info = list[i]?.info
      if (info?.role === "user" && info.model?.providerID && info.model?.modelID) {
        const found = { providerID: info.model.providerID, modelID: info.model.modelID }
        models.set(sid, found)
        return found
      }
    }
    throw new Error("the session's model is not known")
  }

  async function agentReview(sid, root, req, signal) {
    const p = await post("/v1/review/prompt", req, signal)
    if (!p || typeof p.system !== "string" || typeof p.user !== "string") throw new Error("bad review prompt from nod")
    const model = await modelFor(sid, root, signal)
    const created = Promise.resolve(client.session.create({ body: { parentID: sid, title: "nod review" }, throwOnError: true, signal }))
    let child
    try {
      child = unwrap(await abortable(created, signal))
    } catch (err) {
      // A session created after we gave up is still ours to delete.
      created.then((r) => {
        const id = unwrap(r)?.id
        if (id) {
          children.add(id)
          return client.session.delete({ path: { id } })
        }
      }).catch(() => {})
      throw err
    }
    const childID = child && child.id
    if (!childID) throw new Error("could not create the review session")
    children.add(childID)
    let text
    try {
      const res = await abortable(
        client.session.prompt({
          path: { id: childID },
          body: { model: { providerID: model.providerID, modelID: model.modelID }, system: p.system, tools: { ...NO_TOOLS }, parts: [{ type: "text", text: p.user }] },
          throwOnError: true,
          signal,
        }),
        signal,
      )
      text = replyText(unwrap(res))
    } finally {
      // Best effort and never under the review's (possibly aborted) signal.
      if (signal && signal.aborted) {
        try {
          await withTimeout(Promise.resolve(client.session.abort({ path: { id: childID } })), 3000)
        } catch {}
      }
      try {
        await withTimeout(Promise.resolve(client.session.delete({ path: { id: childID } })), 3000)
      } catch {}
    }
    const answers = extractJson(text)
    if (!answers) throw new Error("the review reply had no JSON object")
    return await post("/v1/review/verdict", { request: req, answers }, signal)
  }

  // review returns {decision, reason, source}; every failure is ask.
  async function review(sid, toolName, input, cwd) {
    const ctl = new AbortController()
    const run = async () => {
      const root = await rootOf(sid, ctl.signal)
      const req = buildRequest({ tool: toolName, input, cwd, turns: await turnsFor(root, ctl.signal) })
      const resp = cfg.review === "agent" ? await agentReview(sid, root, req, ctl.signal) : await post("/v1/classify", req, ctl.signal)
      return mapDecision(resp)
    }
    try {
      return await withTimeout(run(), cfg.timeout_ms, () => ctl.abort())
    } catch (err) {
      return { decision: "ask", reason: `nod could not review this call: ${short(err)}`, source: "error" }
    }
  }

  const hooks = {
    "chat.message": async (input, output) => {
      const sid = input && input.sessionID
      if (!sid || children.has(sid)) return
      const m = (output && output.message && output.message.model) || (input && input.model)
      if (m && m.providerID && m.modelID) models.set(sid, { providerID: m.providerID, modelID: m.modelID })
      if (turns.has(sid)) remember(sid, userText(output && output.parts))
    },
    "tool.execute.before": async (input, output) => {
      const sid = input && input.sessionID
      if (sid && children.has(sid)) throw new Error("nod: tools are disabled in the review session")
      const tool = input && input.tool
      if (!gated.has(tool) || !TOOL_MAP[tool]) return
      const spec = TOOL_MAP[tool]
      const args = (output && output.args) || {}
      const cwd = typeof args.workdir === "string" && args.workdir ? args.workdir : deps.directory || ""
      const v = await review(sid, spec.name, spec.input(args), cwd)
      appendLog(logPath, { time: now().toISOString(), host: "opencode", mode: cfg.mode, review: cfg.review, tool: spec.name, decision: v.decision, source: v.source, reason: clip(v.reason, 300) })
      if (cfg.mode !== "enforce") return
      const err = enforceError(v.decision, v.reason)
      if (err) throw err
    },
  }
  return { hooks, children, models, turns }
}

function readConfigFile() {
  try {
    return fs.readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), "nod.json"), "utf8")
  } catch {
    return ""
  }
}

export async function server(input) {
  const config = loadConfig(readConfigFile(), process.env)
  return createNod({ client: input.client, directory: input.directory, config }).hooks
}

// OpenCode v2 rejects a plugin without "an id and an effect or setup function" (error: "Plugin must
// export a default definition with an id and an effect or setup function"). The hooks come from
// server(); setup() has nothing to prepare.
export default { id: "nod", server, setup() {} }
