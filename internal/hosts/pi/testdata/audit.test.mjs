// Unit tests for the Pi extension's audit trail (internal/hosts/assets/pi/audit.mjs) and the extension's use of it.
// Not embedded. Run (offline, read-only source, tmpfs scratch):
//   podman run --rm --network=none --read-only --tmpfs /tmp -v "$PWD":/src:ro -w /src docker.io/library/node:22 \
//     node --test internal/hosts/pi/testdata/
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, statSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { after, test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import {
	AuditLog, GENESIS, auditDir, buildEntry, commandFields, createAudit, describeInput, encodeLine, formatTime, gatedFields, hashSession, redact, uncheckedFields,
} from "../../assets/pi/audit.mjs";
import { FIXTURE_LINES, redactCases, writeFixtureLog } from "./gen-audit-fixture.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const AUDIT_MJS = join(here, "../../assets/pi/audit.mjs");
const EXTENSION_TS = join(here, "../../assets/pi/extension.ts");
const tmp = () => mkdtempSync(join(tmpdir(), "nod-audit-"));
const sha = (b) => createHash("sha256").update(b).digest("hex");

/** JS port of Go's decisionlog.Verify over path.5 .. path.1, path. */
function verify(path) {
	const files = [];
	for (let i = 5; i >= 1; i--) if (existsSync(`${path}.${i}`)) files.push(`${path}.${i}`);
	if (existsSync(path)) files.push(path);
	let prev = null;
	let lines = 0;
	let truncated = false;
	for (const f of files) {
		const raw = readFileSync(f);
		let start = 0;
		let n = 0;
		while (start < raw.length) {
			let end = raw.indexOf(0x0a, start);
			if (end < 0) end = raw.length;
			const line = raw.subarray(start, end);
			start = end + 1;
			n++;
			if (line.toString().trim() === "") continue;
			const e = JSON.parse(line.toString("utf8"));
			if (prev === null) truncated = e.prev !== GENESIS;
			else if (e.prev !== prev) return { lines, files, break: `${f}:${n}` };
			prev = sha(line);
			lines++;
		}
	}
	return { lines, files, truncated, break: null };
}
const readLines = (p) => readFileSync(p, "utf8").trimEnd().split("\n").map((l) => JSON.parse(l));

test("redaction matches decisionlog_test inputs and levels", () => {
	const cmd = `curl -H 'Authorization: Bearer abcdef123456789' https://x -d API_KEY=hunter2 ghp_ABCDEFGHIJKLMNOPQRST ${"a".repeat(5000)}`;
	const full = commandFields(cmd, "");
	assert.equal(full.sha, sha(Buffer.from(cmd)));
	assert.ok(full.text.startsWith("curl -H 'Authorization: <redacted>' https://x -d API_KEY=<v> <tok> aaa"), full.text.slice(0, 80));
	for (const s of ["abcdef123456789", "hunter2", "ghp_ABCDEFGHIJKLMNOPQRST"]) assert.ok(!full.text.includes(s));
	assert.equal(Array.from(full.text).length, 4001);
	assert.ok(full.text.endsWith("…"));
	const prev = commandFields(cmd, "preview");
	assert.equal(Array.from(prev.text).length, 161);
	assert.deepEqual(commandFields(cmd, "HASH "), { sha: full.sha, text: "" });
	assert.deepEqual(commandFields("", "full"), { sha: "", text: "" });
	assert.equal(commandFields("short", "bogus").text, "short");
	assert.equal(redact("-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----"), "<private-key>");
	// clipping counts code points, like Go runes
	assert.equal(commandFields("🙂".repeat(200), "preview").text, `${"🙂".repeat(160)}…`);
});

test("committed redact-cases.json is what redact() produces now", () => {
	const committed = JSON.parse(readFileSync(join(here, "redact-cases.json"), "utf8"));
	assert.deepEqual(committed, redactCases());
});

test("line encoding: Go field order, omitempty, Go escaping, ms time", () => {
	const line = encodeLine({ v: 1, time: "2026-10-19T08:00:00.5Z", host: "omp", tool: "", decision: "", command: "a<b>&c\u2028", ms: 0, rules: [], prev: GENESIS });
	assert.equal(line, `{"v":1,"time":"2026-10-19T08:00:00.5Z","host":"omp","tool":"","command":"a\\u003cb\\u003e\\u0026c\\u2028","decision":"","prev":"${GENESIS}"}`);
	const keys = Object.keys(JSON.parse(encodeLine({ prev: "p", reason: "r", cwd: "c", error: "e", ms: 3, category: "k", answer: "a", rules: ["x"], source: "s", decision: "d", command_sha256: "h", command: "c", tool: "t", review: "decider", mode: "enforce", session: "s", host: "omp", time: "t", v: 1 })));
	assert.deepEqual(keys, ["v", "time", "host", "session", "mode", "review", "tool", "command", "command_sha256", "decision", "source", "rules", "answer", "category", "ms", "error", "cwd", "reason", "prev"]);
	assert.equal(formatTime(new Date(Date.UTC(2026, 0, 2, 3, 4, 5, 120))), "2026-01-02T03:04:05.12Z");
	assert.equal(formatTime(new Date(Date.UTC(2026, 0, 2, 3, 4, 5, 0))), "2026-01-02T03:04:05Z");
	assert.equal(formatTime(new Date(Date.UTC(2026, 0, 2, 3, 4, 5, 7))), "2026-01-02T03:04:05.007Z");
	assert.equal(hashSession("abc"), sha(Buffer.from("abc")).slice(0, 16));
	assert.equal(hashSession(""), "");
});

test("audit dir resolution", () => {
	assert.equal(auditDir({ NOD_AUDIT_DIR: "/a/b", XDG_STATE_HOME: "/x" }, "/h"), "/a/b");
	assert.equal(auditDir({ NOD_AUDIT_DIR: "rel", XDG_STATE_HOME: "/x" }, "/h"), "/x/nod/audit");
	assert.equal(auditDir({ XDG_STATE_HOME: "rel" }, "/h"), "/h/.local/state/nod/audit");
	assert.equal(auditDir({}, "/h"), "/h/.local/state/nod/audit");
});

test("chain: genesis first, each prev is the sha256 of the previous line's bytes, modes 0600/0700", async () => {
	const dir = join(tmp(), "audit");
	const a = createAudit({ env: { NOD_AUDIT_DIR: dir } });
	assert.equal(a.path, join(dir, "omp.jsonl"));
	for (let i = 0; i < 10; i++) assert.equal(await a.record(gatedFields({ decision: "allow", source: "decider" }, { tool: "bash", text: `echo ${i} ü`, answer: "allow", ms: i })), true);
	const raw = readFileSync(a.path);
	const lines = raw.toString("utf8").trimEnd().split("\n");
	assert.equal(lines.length, 10);
	assert.equal(JSON.parse(lines[0]).prev, GENESIS);
	for (let i = 1; i < lines.length; i++) assert.equal(JSON.parse(lines[i]).prev, sha(Buffer.from(lines[i - 1], "utf8")));
	const e = JSON.parse(lines[3]);
	assert.equal(e.v, 1);
	assert.equal(e.host, "omp");
	assert.equal(e.review, "decider");
	assert.equal(e.mode, "enforce");
	assert.equal(e.command, "echo 3 ü");
	assert.match(e.time, /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d{1,3})?Z$/);
	assert.equal(statSync(a.path).mode & 0o777, 0o600);
	assert.equal(statSync(dir).mode & 0o777, 0o700);
	assert.equal(verify(a.path).break, null);
	assert.equal(existsSync(`${a.path}.lock.d`), false);
});

test("rotation keeps the chain and the file count", async () => {
	const path = join(tmp(), "omp.jsonl");
	const log = new AuditLog(path, { maxBytes: 700, keep: 3 });
	let lastBefore = null;
	for (let i = 0; i < 40; i++) {
		const before = existsSync(path) ? readFileSync(path) : null;
		await log.append(buildEntry(gatedFields({ decision: "allow", source: "decider" }, { tool: "bash", text: `cmd ${i} ${"y".repeat(i % 9)}` }), "full"));
		if (before && readFileSync(path).length < before.length) lastBefore = before; // rotated on this append
	}
	assert.ok(existsSync(`${path}.1`) && existsSync(`${path}.3`));
	assert.ok(!existsSync(`${path}.4`));
	for (const f of [path, `${path}.1`, `${path}.2`]) assert.ok(statSync(f).size <= 700, f);
	const r = verify(path);
	assert.equal(r.break, null);
	assert.ok(r.truncated, "older files were rotated away");
	// the first line of a fresh file continues from the last line of the file rotated away
	assert.ok(lastBefore);
	const first = readFileSync(path, "utf8").split("\n")[0];
	const rotated = readFileSync(`${path}.1`).toString("utf8").trimEnd().split("\n");
	assert.equal(JSON.parse(first).prev, sha(Buffer.from(rotated.at(-1), "utf8")));
});

test("an empty file continues from <file>.1", async () => {
	const path = join(tmp(), "omp.jsonl");
	const log = new AuditLog(path);
	await log.append(buildEntry({ tool: "a", decision: "allow" }, "full"));
	const line = readFileSync(path, "utf8").trimEnd();
	writeFileSync(`${path}.1`, `${line}\n`);
	writeFileSync(path, "");
	await log.append(buildEntry({ tool: "b", decision: "allow" }, "full"));
	assert.equal(readLines(path)[0].prev, sha(Buffer.from(line)));
});

test("levels via NOD_AUDIT_COMMANDS", async () => {
	const text = "deploy API_KEY=hunter2 " + "z".repeat(300);
	for (const [level, check] of [
		["preview", (e) => assert.equal(Array.from(e.command).length, 161)],
		["hash", (e) => assert.equal(e.command, undefined)],
		["", (e) => assert.equal(e.command, text.replace("hunter2", "<v>"))],
	]) {
		const dir = tmp();
		const a = createAudit({ env: { NOD_AUDIT_DIR: dir, NOD_AUDIT_COMMANDS: level } });
		await a.record(gatedFields({ decision: "ask", source: "decider" }, { tool: "bash", text }));
		const [e] = readLines(a.path);
		assert.equal(e.command_sha256, sha(Buffer.from(text)));
		assert.ok(!JSON.stringify(e).includes("hunter2"));
		check(e);
	}
});

test("unchecked lines: path for file tools, command for bash, redacted JSON clipped to 500", async () => {
	assert.deepEqual(describeInput("read", { path: "/p/a.txt" }), { text: "/p/a.txt", max: 500 });
	assert.deepEqual(describeInput("Edit", { file_path: "/p/b.ts", edits: [{ oldText: "x" }] }), { text: "/p/b.ts", max: 500 });
	assert.equal(describeInput("bash", { command: "ls -la" }).text, "ls -la");
	const dir = tmp();
	const a = createAudit({ env: { NOD_AUDIT_DIR: dir } });
	const input = { query: "q".repeat(900), token: "sk-ABCDEFGHIJKLMNOPQRSTUV" };
	await a.record(uncheckedFields("mcp_search", input, { cwd: "/w", session: "s1", source: "disabled", mode: "off" }));
	await a.record(uncheckedFields("write", { path: "/w/x", content: "secret body" }, { cwd: "/w" }));
	const [e1, e2] = readLines(a.path);
	assert.equal(e1.decision, "unchecked");
	assert.equal(e1.source, "disabled");
	assert.equal(e1.mode, "off");
	assert.equal(e1.tool, "mcp_search");
	assert.equal(e1.session, hashSession("s1"));
	assert.equal(Array.from(e1.command).length, 501);
	assert.equal(e1.command_sha256, sha(Buffer.from(JSON.stringify(input))));
	assert.equal(e2.command, "/w/x");
	assert.equal(e2.source, "not-gated");
	assert.ok(!readFileSync(a.path, "utf8").includes("secret body"));
	assert.ok(!readFileSync(a.path, "utf8").includes("sk-ABCDEFGH"));
});

test("a failed verdict is logged with source error and the fail-closed decision", () => {
	const f = gatedFields({ decision: "block", source: "plugin", error: "decider timed out" }, { tool: "bash", text: "x", answer: "block" });
	assert.equal(f.decision, "block");
	assert.equal(f.source, "error");
	assert.equal(f.error, "decider timed out");
	assert.equal(gatedFields({ decision: "weird" }, {}).decision, "block");
	assert.equal(gatedFields(null, {}).decision, "block");
});

test("audit failure never throws and warns once", async () => {
	const base = tmp();
	writeFileSync(join(base, "file"), "x");
	const warnings = [];
	const a = createAudit({ env: { NOD_AUDIT_DIR: join(base, "file", "audit") }, warn: (m) => warnings.push(m) });
	assert.equal(await a.record(gatedFields({ decision: "allow" }, { tool: "bash", text: "ls" })), false);
	assert.equal(await a.record(uncheckedFields("read", { path: "/x" })), false);
	assert.equal(warnings.length, 1);
	assert.match(warnings[0], /cannot write/);
	// a throwing warn callback and garbage fields are swallowed too
	const b = createAudit({ env: { NOD_AUDIT_DIR: join(base, "file", "b") }, warn: () => { throw new Error("x"); } });
	assert.equal(await b.record({ text: { toString() { throw new Error("bad"); } } }), false);
});

test("a stale lock is stolen; a held lock delays, then the line is still written", async () => {
	const dir = tmp();
	const path = join(dir, "omp.jsonl");
	mkdirSync(`${path}.lock.d`);
	const old = new Date(Date.now() - 60_000);
	utimesSync(`${path}.lock.d`, old, old);
	assert.equal(await new AuditLog(path).append(buildEntry({ tool: "a", decision: "allow" }, "full")), true);
	assert.ok(!existsSync(`${path}.lock.d`));
	mkdirSync(`${path}.lock.d`); // fresh lock held by "someone else"
	const t0 = Date.now();
	assert.equal(await new AuditLog(path, { lockWaitMs: 150 }).append(buildEntry({ tool: "b", decision: "allow" }, "full")), true);
	assert.ok(Date.now() - t0 >= 140);
	assert.ok(existsSync(`${path}.lock.d`), "a lock we did not take is not removed");
	assert.equal(verify(path).lines, 2);
	assert.equal(verify(path).break, null);
});

test("interleaved appenders: several processes and writers keep one chain", async () => {
	const dir = tmp();
	const path = join(dir, "omp.jsonl");
	const child = `
		import { AuditLog, buildEntry } from ${JSON.stringify(AUDIT_MJS)};
		const log = new AuditLog(${JSON.stringify(path)}, { maxBytes: 9000 });
		const id = process.argv[1];
		const all = [];
		for (let i = 0; i < 25; i++) all.push(log.append(buildEntry({ tool: "bash", text: "p" + id + " " + i, decision: "allow" }, "full")));
		const ok = await Promise.all(all);
		if (!ok.every(Boolean)) process.exit(3);`;
	const runs = [];
	for (let p = 0; p < 4; p++) {
		runs.push(new Promise((res, rej) => {
			const c = spawn(process.execPath, ["--input-type=module", "-e", child, String(p)], { stdio: "inherit" });
			c.on("exit", (code) => (code === 0 ? res() : rej(new Error(`child ${p} exit ${code}`))));
		}));
	}
	// and two writers in this process at the same time
	for (const w of [new AuditLog(path, { maxBytes: 9000 }), new AuditLog(path, { maxBytes: 9000 })]) {
		for (let i = 0; i < 10; i++) runs.push(w.append(buildEntry({ tool: "bash", text: `local ${i}`, decision: "allow" }, "full")));
	}
	await Promise.all(runs);
	const r = verify(path);
	assert.equal(r.break, null);
	assert.equal(r.lines, 120);
	assert.equal(r.truncated, false);
	assert.ok(r.files.length > 1, "rotated while interleaving");
});

test("committed fixture equals a fresh generation (deterministic, one rotation)", async () => {
	const fresh = await writeFixtureLog(tmp());
	const committed = readFileSync(join(here, "audit-omp.jsonl.fixture"));
	assert.equal(fresh.toString("utf8"), committed.toString("utf8"));
	const lines = committed.toString("utf8").trimEnd().split("\n");
	assert.equal(lines.length, FIXTURE_LINES);
	const p = join(tmp(), "omp.jsonl");
	writeFileSync(p, committed);
	assert.deepEqual([verify(p).break, verify(p).lines, verify(p).truncated], [null, FIXTURE_LINES, false]);
});

// ---- the extension itself (Node 22 strips the TypeScript types) ----

function fakePi() {
	const handlers = {};
	return {
		handlers,
		on: (name, fn) => { handlers[name] = fn; },
		registerCommand: (name, def) => { handlers[`cmd:${name}`] = def.handler; },
	};
}
function ctx({ hasUI = false, confirm = async () => true, notes = [] } = {}) {
	return {
		cwd: "/work",
		hasUI,
		signal: undefined,
		ui: { confirm, notify: (m, l) => notes.push([m, l]) },
		sessionManager: { getSessionId: () => "sess-42", getBranch: () => [{ type: "message", message: { role: "user", content: "run the tests" } }] },
	};
}
const decideBody = (allowP) => ({
	"Which category best describes this tool call?": { choice: "c", probabilities: { "running the project's tests, linters, type checks or local build": allowP, "pushing code, opening pull requests, or publishing anything or making it public": 1 - allowP } },
});

const realFetch = globalThis.fetch;
after(() => { globalThis.fetch = realFetch; });

test("extension: every call it sees is audited, verdicts unchanged", async () => {
	const dir = tmp();
	process.env.NOD_AUDIT_DIR = dir;
	delete process.env.NOD_AUDIT_COMMANDS;
	process.env.DECIDER_URL = "http://127.0.0.1:9";
	process.env.DECIDER_TIMEOUT = "2";
	let next = null;
	globalThis.fetch = async () => {
		if (next instanceof Error) throw next;
		return { ok: true, status: 200, json: async () => next };
	};
	const { default: ext } = await import(pathToFileURL(EXTENSION_TS).href);
	const pi = fakePi();
	ext(pi);
	const call = (toolName, input, c = ctx()) => pi.handlers.tool_call({ type: "tool_call", toolName, toolCallId: "t1", input }, c);

	next = decideBody(0.95);
	assert.equal(await call("bash", { command: "go test ./..." }), undefined);
	next = decideBody(0.1);
	assert.equal((await call("bash", { command: "git push" })).block, true); // ask, no UI
	assert.equal(await call("bash", { command: "git push" }, ctx({ hasUI: true, confirm: async () => true })), undefined);
	assert.equal((await call("bash", { command: "git push" }, ctx({ hasUI: true, confirm: async () => false }))).block, true);
	next = new Error("connect ECONNREFUSED");
	assert.equal((await call("bash", { command: "make" })).block, true);
	assert.equal(await call("wait", {}), undefined); // internal rule
	next = decideBody(0.1);
	await assert.rejects(call("bash", { command: "boom" }, ctx({ hasUI: true, confirm: async () => { throw new Error("ui gone"); } })), /ui gone/);
	await pi.handlers["cmd:decider"]("off", ctx());
	assert.equal(await call("read", { path: "/work/README.md" }), undefined);

	const es = readLines(join(dir, "omp.jsonl"));
	const summary = es.map((e) => [e.tool, e.decision, e.source, e.answer ?? ""]);
	assert.deepEqual(summary.slice(0, 7), [
		["bash", "allow", "decider", "allow"],
		["bash", "ask", "decider", "ask-no-ui"],
		["bash", "ask", "decider", "ask-approved"],
		["bash", "ask", "decider", "ask-declined"],
		["bash", "block", "error", "block"],
		["wait", "allow", "plugin", "allow"],
		["bash", "block", "error", "block"],
	]);
	assert.equal(es[6].error, "extension error: ui gone");
	assert.equal(es.length, 8);
	assert.deepEqual(summary.at(-1), ["read", "unchecked", "disabled", ""]);
	assert.equal(es[4].error, "decider unreachable");
	assert.equal(es[0].session, hashSession("sess-42"));
	assert.equal(es[0].cwd, "/work");
	assert.equal(es[0].mode, "enforce");
	assert.equal(es.at(-1).mode, "off");
	assert.equal(es.at(-1).command, "/work/README.md");
	assert.equal(verify(join(dir, "omp.jsonl")).break, null);
});

test("extension: an unwritable audit dir changes nothing and warns once", async () => {
	const base = tmp();
	writeFileSync(join(base, "f"), "x");
	process.env.NOD_AUDIT_DIR = join(base, "f", "audit");
	globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => decideBody(0.95) });
	const { default: ext } = await import(pathToFileURL(EXTENSION_TS).href);
	const pi = fakePi();
	ext(pi);
	const notes = [];
	for (let i = 0; i < 3; i++) {
		assert.equal(await pi.handlers.tool_call({ type: "tool_call", toolName: "bash", input: { command: "ls" } }, ctx({ hasUI: true, notes })), undefined);
	}
	assert.equal(notes.filter(([, l]) => l === "warning").length, 1);
});
