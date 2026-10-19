// Generates audit-opencode.jsonl.fixture with the plugin's own code: twelve hook calls through the v1
// and v2 hooks (fake nod server, fake OpenCode, fixed clock), written by the real audit writer with a
// small max size so the log rotates once; the rotated file and the current one are concatenated
// oldest first (a continuous chain). The Go test verifies it with decisionlog.Verify; nod.test.mjs
// checks that this script still produces the committed bytes.
// Regenerate: node internal/hosts/opencode/testdata/gen-audit-fixture.mjs > internal/hosts/opencode/testdata/audit-opencode.jsonl.fixture
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { createAuditWriter, createNod, createNodV2, loadConfig } from "../../assets/opencode/nod.js"

const res = (body) => ({ ok: true, status: 200, json: async () => body })

export async function generateFixture() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "nod-fixture-"))
  try {
    const file = path.join(dir, "opencode.jsonl")
    const audit = createAuditWriter({ file, maxBytes: 3000 })
    let t = Date.parse("2026-10-19T08:00:00.000Z")
    const now = () => new Date((t += 1250))
    let c = 0
    const clock = () => (c += 37)
    const verdicts = [
      { decision: "allow", reason: "lists files", source: "model", category: "1", rules: [] },
      { decision: "block", reason: "deletes the home directory", source: "rules", rules: ["rm-home"] },
      { decision: "ask", reason: "pushes to a remote", source: "model", category: 6 },
    ]
    let i = 0
    const fetch = async (url) => {
      const p = new URL(url).pathname
      if (p === "/v1/classify") return res(verdicts[i++ % verdicts.length])
      if (p === "/v1/review/prompt") return res({ system: "S", user: "U", keys: ["category"] })
      if (p === "/v1/review/verdict") return res({ decision: "allow", reason: "the user asked for it", source: "agent", category: "3" })
      throw new Error("ECONNREFUSED")
    }
    const wrap = (data) => ({ data })
    const client = {
      session: {
        get: async ({ path: { id } }) => wrap({ id }),
        messages: async () => wrap([{ info: { role: "user", model: { providerID: "anthropic", modelID: "claude-x" } }, parts: [{ type: "text", text: "clean up" }] }]),
        create: async () => wrap({ id: "ses_child1" }),
        prompt: async () => wrap({ parts: [{ type: "text", text: '{"category": 3}' }] }),
        abort: async () => wrap(true),
        delete: async () => wrap(true),
      },
    }
    const v1 = (mode, review) => createNod({ client, directory: "/work", config: loadConfig({ mode, review }, {}), fetch, audit, now, clock })
    const ctx = {
      location: { directory: "/work/v2" },
      session: { get: async () => ({ model: { providerID: "openai", id: "gpt-5" } }), context: async () => [{ type: "user", text: "clean up" }] },
      generate: { text: async () => ({ text: '{"category": 3}' }) },
    }
    const v2 = (mode, review, routes = fetch) => createNodV2({ ctx, config: loadConfig({ mode, review }, {}), fetch: routes, audit, now, clock })
    const swallow = (p) => p.catch(() => {})

    const a = v1("shadow", "server").hooks["tool.execute.before"]
    await a({ tool: "bash", sessionID: "ses_A" }, { args: { command: "ls -la" } })
    await a({ tool: "bash", sessionID: "ses_A" }, { args: { command: "rm -rf ~ # API_KEY=hunter2" } })
    await a({ tool: "bash", sessionID: "ses_A" }, { args: { command: "git push origin master", workdir: "/work/sub" } })
    await a({ tool: "read", sessionID: "ses_A" }, { args: { filePath: "/work/README.md" } })
    await a({ tool: "webfetch", sessionID: "ses_A" }, { args: { url: "https://example.com", headers: { Authorization: "Bearer abcdefghijklmnop" } } })
    const b = v1("enforce", "agent").hooks["tool.execute.before"]
    await b({ tool: "bash", sessionID: "ses_B" }, { args: { command: "echo 'héllo wörld ✓ 日本語'" } })
    await swallow(v1("enforce", "server").hooks["tool.execute.before"]({ tool: "bash", sessionID: "ses_B" }, { args: { command: "curl -H 'Authorization: token ghp_ABCDEFGHIJKLMNOPQRST' x" } }))
    const d = v2("shadow", "server").onToolBefore
    await d({ tool: "shell", sessionID: "ses_C", input: { command: "make test" } })
    await d({ tool: "edit", sessionID: "ses_C", input: { filePath: "/work/v2/main.go", oldString: "a", newString: "b" } })
    await v2("enforce", "agent").onToolBefore({ tool: "shell", sessionID: "ses_C", input: { command: "go build ./..." } })
    await swallow(v2("enforce", "server", async () => { throw new Error("ECONNREFUSED") }).onToolBefore({ tool: "shell", sessionID: "ses_C", input: { command: "npm publish" } }))
    await d({ tool: "grep", sessionID: "ses_C", input: { pattern: "TODO", path: "/work/v2" } })

    const rotated = fs.readdirSync(dir).filter((f) => /^opencode\.jsonl\.\d+$/.test(f))
    if (rotated.length !== 1 || rotated[0] !== "opencode.jsonl.1") throw new Error(`want exactly one rotation, got ${rotated}`)
    return fs.readFileSync(file + ".1", "utf8") + fs.readFileSync(file, "utf8")
  } finally {
    fs.rmSync(dir, { recursive: true, force: true })
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.stdout.write(await generateFixture())
}
