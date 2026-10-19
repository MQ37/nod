// nod OpenCode plugin (managed by nod: `nod hooks install --host opencode`; remove with `nod hooks remove --host opencode`)
//
// Gates OpenCode tool calls (by default only `bash`) with nod. Settings come from nod.json next to
// this file (written by nod), overridable with NOD_URL / NOD_MODE / NOD_REVIEW / NOD_AUDIT_COMMANDS.
//
//   review "server": POST {url}/v1/classify, the Decider on the nod server decides.
//   review "agent":  POST {url}/v1/review/prompt, ask the agent's OWN model in a hidden child
//                    session (no tools), then POST {url}/v1/review/verdict with its answers.
//
// mode "shadow" (default) never interferes. mode "enforce": allow runs the call, block and ask throw
// (OpenCode turns the error into the tool result; a plugin cannot open a prompt). Any failure counts as
// ask, never as allow.
//
// Audit trail (both modes): every tool call the hook sees gets one line in <audit dir>/opencode.jsonl,
// the same hash-chained format as nod's Go writer (internal/decisionlog), so `nod audit verify` checks
// it. Gated calls carry the verdict; other tools are logged as decision "unchecked". Logging never
// changes an outcome and never throws.
//
// The default export is an object {id, server, setup} on purpose: OpenCode calls EVERY export of a module
// without it as a plugin, and the named helper exports below exist only for the unit tests.
// Plain ESM, Node/Bun built-ins only, no build step.

import { createHash } from "node:crypto"
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

export const DEFAULTS = Object.freeze({
  url: "http://127.0.0.1:11096",
  mode: "shadow",
  review: "server",
  timeout_ms: 15000,
  tools: ["bash", "shell"],
  audit_dir: "", // "" = $NOD_AUDIT_DIR, else $XDG_STATE_HOME/nod/audit, else ~/.local/state/nod/audit
  audit_commands: "full", // full | preview | hash
})

// OpenCode tool id -> the tool name nod knows and the text it judges. Keep it small and explicit.
export const TOOL_MAP = Object.freeze({
  // OpenCode v1 calls the shell tool "bash", v2 calls it "shell".
  bash: { name: "Bash", input: (args) => (args && typeof args.command === "string" ? args.command : "") },
  shell: { name: "Bash", input: (args) => (args && typeof args.command === "string" ? args.command : "") },
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
  // "bash" (v1) and "shell" (v2) are the same tool: gating one gates the other.
  if (cfg.tools.includes("bash") && !cfg.tools.includes("shell")) cfg.tools.push("shell")
  if (cfg.tools.includes("shell") && !cfg.tools.includes("bash")) cfg.tools.push("bash")
  // audit_dir is taken only when absolute; auditDir() falls back to NOD_AUDIT_DIR and the XDG default.
  if (typeof file.audit_dir === "string" && path.isAbsolute(file.audit_dir.trim())) cfg.audit_dir = file.audit_dir.trim()
  if (typeof file.audit_commands === "string") cfg.audit_commands = file.audit_commands
  if (env.NOD_URL) cfg.url = String(env.NOD_URL).trim()
  if (env.NOD_MODE) cfg.mode = String(env.NOD_MODE)
  if (env.NOD_REVIEW) cfg.review = String(env.NOD_REVIEW)
  if (env.NOD_AUDIT_COMMANDS) cfg.audit_commands = String(env.NOD_AUDIT_COMMANDS)
  cfg.url = cfg.url.replace(/\/+$/, "")
  cfg.mode = cfg.mode.trim().toLowerCase() === "enforce" ? "enforce" : "shadow"
  cfg.review = cfg.review.trim().toLowerCase() === "agent" ? "agent" : "server"
  cfg.audit_commands = auditLevel(cfg.audit_commands)
  return cfg
}

// The patterns and replacements of internal/decisionlog/redact.go. Go's \s is [\t\n\f\r ] (ASCII only),
// so it is spelled out here to match byte for byte.
const SECRETS = [
  [/-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)/g, "<private-key>"],
  [/(Authorization[\t\n\f\r ]*:[\t\n\f\r ]*)(?:Bearer|Basic|token)?[\t\n\f\r ]*[^\t\n\f\r '"]+/gi, "$1<redacted>"],
  [/\bBearer[\t\n\f\r ]+[A-Za-z0-9._~+/=-]{8,}/g, "Bearer <redacted>"],
  [/\b(?:sk-|ghp_|gho_|ghs_|ghu_|github_pat_|glpat-|xox[a-z]-|apify_api_|AKIA|ASIA|eyJ)[A-Za-z0-9_\-.]{12,}/g, "<tok>"],
  [/\b([A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|PASS)[A-Z0-9_]*=)(?:"[^"]*"|'[^']*'|[^\t\n\f\r ]+)/g, "$1<v>"],
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

// ---------------------------------------------------------------------------------------------
// Audit trail. Mirrors internal/decisionlog (decisionlog.go, redact.go): one JSON object per line,
// "prev" = sha256 hex of the exact bytes of the previous line (64 zeros for the first line ever; the
// last line of <file>.1 when <file> is empty), rotation at 10 MiB keeping 5 files. Node has no flock:
// processes serialise on a lock directory <file>.lock.d (mkdir is atomic), appends inside one process
// on a promise chain.

export const AUDIT = Object.freeze({
  host: "opencode",
  genesis: "0".repeat(64),
  maxBytes: 10 << 20,
  keep: 5,
  maxCommand: 4000,
  previewLen: 160,
  maxArgs: 500,
  maxReason: 300,
  lockWaitMs: 2000,
  lockStaleMs: 10000,
})

export function sha256Hex(s) {
  return createHash("sha256").update(s).digest("hex")
}

// hashSession is decisionlog.HashSession: a short, stable pseudonym of a session id.
export function hashSession(id) {
  return typeof id === "string" && id ? sha256Hex(id).slice(0, 16) : ""
}

// clipRunes counts code points, like Go's []rune, and marks a cut with "…".
export function clipRunes(s, n) {
  const t = typeof s === "string" ? s : String(s ?? "")
  if (t.length <= n) return t
  const r = Array.from(t)
  return r.length <= n ? t : r.slice(0, n).join("") + "…"
}

export function auditLevel(v) {
  const l = String(v ?? "").trim().toLowerCase()
  return l === "preview" || l === "hash" ? l : "full"
}

// commandFields is decisionlog.CommandFields: {sha, text} of a command at a level (full | preview | hash).
// sha is of the original text; text is redacted and clipped (max code points, preview 160).
export function commandFields(cmd, level, max = AUDIT.maxCommand) {
  if (typeof cmd !== "string" || !cmd) return { sha: "", text: "" }
  const sha = sha256Hex(cmd)
  const l = auditLevel(level)
  if (l === "hash") return { sha, text: "" }
  return { sha, text: clipRunes(redact(cmd), l === "preview" ? Math.min(AUDIT.previewLen, max) : max) }
}

// The audit directory: audit_dir from nod.json, else $NOD_AUDIT_DIR, else $XDG_STATE_HOME/nod/audit,
// else ~/.local/state/nod/audit (absolute values only).
export function auditDir(cfg = {}, env = {}, home = "") {
  const abs = (v) => typeof v === "string" && v.trim() && path.isAbsolute(v.trim())
  if (cfg && abs(cfg.audit_dir)) return cfg.audit_dir.trim()
  if (abs(env.NOD_AUDIT_DIR)) return env.NOD_AUDIT_DIR.trim()
  if (abs(env.XDG_STATE_HOME)) return path.join(env.XDG_STATE_HOME.trim(), "nod", "audit")
  return path.join(home || os.homedir(), ".local", "state", "nod", "audit")
}

// What an ungated call did, in one string: the file path for file tools, else the JSON of the args.
export function describeArgs(args) {
  if (!args || typeof args !== "object") return typeof args === "string" ? args : ""
  for (const k of ["filePath", "file_path"]) if (typeof args[k] === "string" && args[k]) return args[k]
  try {
    return JSON.stringify(args) || ""
  } catch {
    return ""
  }
}

const str = (v) => (typeof v === "string" && v ? v : undefined)

// formatAuditLine builds the line (no newline) with the keys and types of Go's decisionlog.Entry, in
// its field order, empty fields omitted. Types matter: Go's Verify rejects a line that does not
// unmarshal into Entry (ms must be an integer, rules strings, category a string).
export function formatAuditLine(e, prev) {
  const o = { v: 1, time: str(e.time) || new Date().toISOString() }
  o.host = str(e.host)
  o.session = str(e.session)
  o.mode = str(e.mode)
  o.review = str(e.review)
  o.tool = typeof e.tool === "string" ? e.tool : ""
  o.command = str(e.command)
  o.command_sha256 = str(e.command_sha256)
  o.decision = typeof e.decision === "string" ? e.decision : ""
  o.source = str(e.source)
  o.model = str(e.model)
  const rules = Array.isArray(e.rules) ? e.rules.filter((r) => typeof r === "string") : []
  if (rules.length) o.rules = rules
  o.category = typeof e.category === "number" && Number.isFinite(e.category) ? String(e.category) : str(e.category)
  const ms = Number.isFinite(e.ms) ? Math.round(e.ms) : 0
  if (ms > 0) o.ms = ms
  o.error = str(e.error)
  o.cwd = str(e.cwd)
  o.reason = str(e.reason)
  o.prev = prev
  return JSON.stringify(o) // undefined values are dropped
}

function defaultWarn(msg) {
  try {
    process.stderr.write(msg + "\n")
  } catch {}
}

// createAuditWriter returns {file, append(entry) -> Promise<boolean>}. append never rejects; the first
// failure is reported once through warn. opts: {file, fs, maxBytes, keep, lockWaitMs, lockStaleMs,
// nowMs, sleep, warn} (everything but file is for tests).
export function createAuditWriter(opts) {
  const file = opts.file
  const fsx = opts.fs || fs
  const maxBytes = opts.maxBytes ?? AUDIT.maxBytes
  const keep = opts.keep > 0 ? opts.keep : AUDIT.keep
  const lockWaitMs = opts.lockWaitMs ?? AUDIT.lockWaitMs
  const lockStaleMs = opts.lockStaleMs ?? AUDIT.lockStaleMs
  const nowMs = opts.nowMs || Date.now
  const sleep = opts.sleep || ((ms) => new Promise((r) => setTimeout(r, ms)))
  const warn = opts.warn || defaultWarn
  const lock = file + ".lock.d"
  let tail = Promise.resolve()
  let warned = false

  function lastLine(fd, size) {
    if (!size) return null
    const start = Math.max(0, size - (1 << 20))
    const buf = Buffer.alloc(size - start)
    let off = 0
    while (off < buf.length) {
      const n = fsx.readSync(fd, buf, off, buf.length - off, start + off)
      if (!n) break
      off += n
    }
    let end = off
    while (end > 0 && buf[end - 1] === 0x0a) end--
    const nl = buf.lastIndexOf(0x0a, end - 1)
    return buf.subarray(nl + 1, end)
  }

  function lastLineOf(p) {
    let fd
    try {
      fd = fsx.openSync(p, "r")
    } catch {
      return null
    }
    try {
      return lastLine(fd, fsx.fstatSync(fd).size)
    } catch {
      return null
    } finally {
      try {
        fsx.closeSync(fd)
      } catch {}
    }
  }

  function rotate() {
    try {
      fsx.unlinkSync(`${file}.${keep}`)
    } catch {}
    for (let i = keep - 1; i >= 1; i--) {
      try {
        fsx.renameSync(`${file}.${i}`, `${file}.${i + 1}`)
      } catch {}
    }
    try {
      fsx.renameSync(file, `${file}.1`)
    } catch {}
  }

  function open() {
    const fd = fsx.openSync(file, "a+", 0o600)
    try {
      fsx.fchmodSync(fd, 0o600)
    } catch (err) {
      fsx.closeSync(fd)
      throw err
    }
    return fd
  }

  async function acquire() {
    const start = nowMs()
    for (;;) {
      try {
        fsx.mkdirSync(lock, { mode: 0o700 })
        return
      } catch (err) {
        if (!err || err.code !== "EEXIST") throw err
      }
      try {
        // A lock older than lockStaleMs belongs to a crashed writer: take it over.
        if (nowMs() - fsx.statSync(lock).mtimeMs > lockStaleMs) {
          fsx.rmdirSync(lock)
          continue
        }
      } catch (err) {
        if (err && err.code === "ENOENT") continue // released meanwhile
      }
      if (nowMs() - start > lockWaitMs) throw new Error("audit lock is busy")
      await sleep(2 + Math.floor(Math.random() * 8))
    }
  }

  function writeLocked(entry) {
    let fd = open()
    try {
      const size = fsx.fstatSync(fd).size
      const last = lastLine(fd, size)
      let prev = AUDIT.genesis
      if (last && last.length) prev = sha256Hex(last)
      else {
        const l = lastLineOf(file + ".1") // a new file continues the chain of its rotated predecessor
        if (l && l.length) prev = sha256Hex(l)
      }
      const buf = Buffer.from(formatAuditLine(entry, prev) + "\n", "utf8")
      if (maxBytes > 0 && size > 0 && size + buf.length > maxBytes) {
        fsx.closeSync(fd)
        fd = -1
        rotate()
        fd = open() // prev stays the hash of the last line of the file just rotated away
      }
      let off = 0
      while (off < buf.length) off += fsx.writeSync(fd, buf, off, buf.length - off)
    } finally {
      if (fd >= 0) fsx.closeSync(fd)
    }
  }

  async function appendOnce(entry) {
    fsx.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 })
    await acquire()
    try {
      writeLocked(entry)
    } finally {
      try {
        fsx.rmdirSync(lock)
      } catch {}
    }
  }

  function append(entry) {
    const run = tail
      .then(() => appendOnce(entry))
      .then(
        () => true,
        (err) => {
          if (!warned) {
            warned = true
            try {
              warn(`nod: cannot write the audit trail ${file}: ${short(err)}`)
            } catch {}
          }
          return false
        },
      )
    tail = run // never rejects
    return run
  }

  return { file, append }
}

// One writer per file per process, so every hook instance shares the in-process chain.
const writers = new Map()
export function sharedAuditWriter(file) {
  if (!writers.has(file)) writers.set(file, createAuditWriter({ file }))
  return writers.get(file)
}

function defaultAuditWriter(cfg) {
  try {
    return sharedAuditWriter(path.join(auditDir(cfg, process.env, os.homedir()), AUDIT.host + ".jsonl"))
  } catch {
    return null
  }
}

// createAuditor turns hook events into audit entries. writer may be null (no audit). Never throws.
export function createAuditor({ config, writer, now = () => new Date() }) {
  const cfg = config || loadConfig({}, {})
  const level = auditLevel(cfg.audit_commands)
  async function write(build) {
    if (!writer) return
    try {
      await writer.append(build())
    } catch {}
  }
  const base = (sid) => ({
    time: now().toISOString(),
    host: AUDIT.host,
    session: hashSession(sid),
    mode: cfg.mode,
    review: cfg.review === "agent" ? "agent" : "decider",
  })
  return {
    // A reviewed call: v = {decision, source, reason, model, category, rules, ms, error}.
    verdict: ({ sid, tool, command, cwd, v }) =>
      write(() => {
        const { sha, text } = commandFields(command, level)
        return {
          ...base(sid),
          tool,
          command: text,
          command_sha256: sha,
          decision: v.decision,
          source: v.source,
          model: v.model,
          category: v.category,
          rules: v.rules,
          ms: v.ms,
          error: v.error,
          cwd,
          reason: clipRunes(redact(v.reason || ""), AUDIT.maxReason),
        }
      }),
    // A call nod did not judge (decision "unchecked"), or refused without a review (child session).
    other: ({ sid, tool, args, cwd, decision = "unchecked", source = "not-gated", reason = "" }) =>
      write(() => {
        const desc = describeArgs(args)
        const { text } = commandFields(desc.length > AUDIT.maxArgs * 4 ? desc.slice(0, AUDIT.maxArgs * 4) : desc, level, AUDIT.maxArgs)
        return {
          ...base(sid),
          tool: typeof tool === "string" ? tool : "",
          command: text,
          command_sha256: desc ? sha256Hex(desc) : "",
          decision,
          source,
          cwd,
          reason,
        }
      }),
  }
}

// Extra verdict fields of a nod response for the audit line.
function verdictExtras(resp) {
  const r = resp && typeof resp === "object" ? resp : {}
  return {
    category: typeof r.category === "string" || typeof r.category === "number" ? String(r.category) : "",
    rules: Array.isArray(r.rules) ? r.rules.filter((x) => typeof x === "string") : [],
  }
}

function short(err) {
  const m = err && typeof err.message === "string" ? err.message : String(err)
  return clip(m.replace(/\s+/g, " "), 200)
}

// createNod builds the hooks. deps: {client, directory, config, fetch, audit, now, clock}; audit is an
// audit writer (createAuditWriter), null for none, default the shared one for the configured dir.
export function createNod(deps) {
  const cfg = deps.config || loadConfig({}, {})
  const client = deps.client
  const doFetch = deps.fetch || globalThis.fetch
  const now = deps.now || (() => new Date())
  const clock = deps.clock || Date.now
  const audit = createAuditor({ config: cfg, writer: deps.audit === undefined ? defaultAuditWriter(cfg) : deps.audit, now })
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

  async function agentReview(sid, root, req, signal, meta) {
    const p = await post("/v1/review/prompt", req, signal)
    if (!p || typeof p.system !== "string" || typeof p.user !== "string") throw new Error("bad review prompt from nod")
    const model = await modelFor(sid, root, signal)
    meta.model = `${model.providerID}/${model.modelID}`
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

  // review returns {decision, reason, source, model, category, rules, ms, error}; every failure is ask.
  async function review(sid, toolName, input, cwd) {
    const ctl = new AbortController()
    const meta = {}
    const t0 = clock()
    const run = async () => {
      const root = await rootOf(sid, ctl.signal)
      const req = buildRequest({ tool: toolName, input, cwd, turns: await turnsFor(root, ctl.signal) })
      const resp = cfg.review === "agent" ? await agentReview(sid, root, req, ctl.signal, meta) : await post("/v1/classify", req, ctl.signal)
      return { ...mapDecision(resp), ...verdictExtras(resp) }
    }
    try {
      const v = await withTimeout(run(), cfg.timeout_ms, () => ctl.abort())
      return { ...v, model: meta.model, ms: clock() - t0 }
    } catch (err) {
      return { decision: "ask", reason: `nod could not review this call: ${short(err)}`, source: "error", error: short(err), model: meta.model, ms: clock() - t0 }
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
      const tool = input && input.tool
      const args = (output && output.args) || {}
      const cwd = typeof args.workdir === "string" && args.workdir ? args.workdir : deps.directory || ""
      if (sid && children.has(sid)) {
        const why = "tools are disabled in the review session"
        await audit.other({ sid, tool, args, cwd, decision: "block", source: "review-session", reason: why })
        throw new Error("nod: " + why)
      }
      if (!gated.has(tool) || !TOOL_MAP[tool]) {
        await audit.other({ sid, tool, args, cwd })
        return
      }
      const spec = TOOL_MAP[tool]
      const command = spec.input(args)
      const v = await review(sid, spec.name, command, cwd)
      await audit.verdict({ sid, tool: spec.name, command, cwd, v })
      if (cfg.mode !== "enforce") return
      const err = enforceError(v.decision, v.reason)
      if (err) throw err
    },
  }
  return { hooks, children, models, turns }
}

// ---------------------------------------------------------------------------------------------
// OpenCode v2. v2 ignores server(); a plugin is {id, setup(ctx)} and registers hooks in setup.
// Verified against opencode 2.0.32: the hook is ctx.tool.hook("execute.before", handler) with the event
// {tool, sessionID, agent, messageID, id, input}; the shell tool is "shell" with input.command;
// ctx.session.get({sessionID}) -> {model:{providerID,id,variant}, ...}; ctx.session.context({sessionID})
// -> messages [{type, text, ...}] where the human's are type "user"; ctx.generate.text({model, prompt})
// is a direct model call with no session, no tools and no history.

// Human turns from ctx.session.context() output. Never assistant, tool or synthetic items.
export function userTurnsV2(messages) {
  if (!Array.isArray(messages)) return []
  const out = []
  for (const m of messages) {
    if (!m || m.type !== "user" || typeof m.text !== "string") continue
    const t = m.text.trim()
    if (t) out.push(t)
  }
  return out.slice(-MAX_TURNS)
}

export function createNodV2(deps) {
  const ctx = deps.ctx
  const cfg = deps.config || loadConfig({}, {})
  const doFetch = deps.fetch || globalThis.fetch
  const now = deps.now || (() => new Date())
  const clock = deps.clock || Date.now
  const audit = createAuditor({ config: cfg, writer: deps.audit === undefined ? defaultAuditWriter(cfg) : deps.audit, now })
  const gated = new Set(cfg.tools)

  async function post(p, body, signal) {
    const res = await doFetch(cfg.url + p, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body), signal })
    if (!res || !res.ok) {
      const status = res ? res.status : "no response"
      throw new Error(status === 400 && p === "/v1/review/verdict" ? "invalid review answers" : `nod server ${p}: HTTP ${status}`)
    }
    return await res.json()
  }

  async function sessionContext(sid) {
    const [info, msgs] = await Promise.all([ctx.session.get({ sessionID: sid }), ctx.session.context({ sessionID: sid })])
    return { model: info && info.model, turns: userTurnsV2(msgs) }
  }

  async function agentReview(req, model, signal, meta) {
    const p = await post("/v1/review/prompt", req, signal)
    if (!p || typeof p.system !== "string" || typeof p.user !== "string") throw new Error("bad review prompt from nod")
    if (!model || !model.providerID || !model.id) throw new Error("the session's model is not known")
    meta.model = `${model.providerID}/${model.id}`
    const out = await ctx.generate.text({ model: { providerID: model.providerID, id: model.id }, prompt: p.system + "\n\n" + p.user })
    const answers = extractJson(out && typeof out.text === "string" ? out.text : "")
    if (!answers) throw new Error("the review reply had no JSON object")
    return await post("/v1/review/verdict", { request: req, answers }, signal)
  }

  // {decision, reason, source, model, category, rules, ms, error}; every failure is ask.
  async function review(sid, toolName, input, cwd) {
    const ctl = new AbortController()
    const meta = {}
    const t0 = clock()
    const run = async () => {
      const { model, turns } = await sessionContext(sid)
      const req = buildRequest({ tool: toolName, input, cwd, turns })
      const resp = cfg.review === "agent" ? await agentReview(req, model, ctl.signal, meta) : await post("/v1/classify", req, ctl.signal)
      return { ...mapDecision(resp), ...verdictExtras(resp) }
    }
    try {
      const v = await withTimeout(run(), cfg.timeout_ms, () => ctl.abort())
      return { ...v, model: meta.model, ms: clock() - t0 }
    } catch (err) {
      return { decision: "ask", reason: `nod could not review this call: ${short(err)}`, source: "error", error: short(err), model: meta.model, ms: clock() - t0 }
    }
  }

  async function onToolBefore(event) {
    const tool = event && event.tool
    const sid = event && event.sessionID
    const args = (event && event.input) || {}
    const cwd = typeof args.workdir === "string" && args.workdir ? args.workdir : (ctx.location && ctx.location.directory) || ""
    if (!gated.has(tool) || !TOOL_MAP[tool]) {
      await audit.other({ sid, tool, args, cwd })
      return
    }
    const spec = TOOL_MAP[tool]
    const command = spec.input(args)
    const v = await review(sid, spec.name, command, cwd)
    await audit.verdict({ sid, tool: spec.name, command, cwd, v })
    if (cfg.mode !== "enforce") return
    const err = enforceError(v.decision, v.reason)
    if (err) throw err
  }

  return { onToolBefore }
}

export async function setup(ctx) {
  const config = loadConfig(readConfigFile(), process.env)
  const nod = createNodV2({ ctx, config })
  await ctx.tool.hook("execute.before", nod.onToolBefore)
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

// One definition for both generations (OpenCode documents this): v1 calls server(), v2 ignores it and
// calls setup(ctx). v2 refuses a definition without an id and a setup or effect function
// (error: "Plugin must export a default definition with an id and an effect or setup function").
export default { id: "nod", server, setup }
