/**
 * nod audit trail for the Pi / Oh My Pi extension: one JSONL file per host, here
 * <audit dir>/omp.jsonl, in the exact format of nod's Go internal/decisionlog, so
 * `nod audit verify` and `nod decisions` read it.
 *
 * Every line carries `prev`, the sha256 of the exact bytes of the previous line (64 zeros for
 * the first line ever; the last line of <file>.1 for the first line after a rotation). At
 * 10 MiB the file rotates to .1 (five kept). Directory 0700, files 0600.
 *
 * Node has no flock: appenders in different processes serialise on a lock directory
 * <file>.lock.d (mkdir is atomic), waiting up to ~2 s and stealing a lock older than 10 s;
 * after the wait the line is written anyway (a lost line is worse than a rare chain break,
 * which `nod audit verify` would show). Inside one process appends run one after another.
 *
 * Settings, from the environment of the agent process:
 *   NOD_AUDIT_DIR       absolute directory; else $XDG_STATE_HOME/nod/audit; else ~/.local/state/nod/audit
 *   NOD_AUDIT_COMMANDS  full (default: redacted, 4000 characters) | preview (160) | hash (only the sha256)
 *
 * Logging never throws and never changes a gate outcome; the first failure is reported once.
 */

import { createHash } from "node:crypto";
import { closeSync, fchmodSync, fstatSync, mkdirSync, openSync, readSync, renameSync, rmdirSync, statSync, unlinkSync, writeSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, isAbsolute, join } from "node:path";

export const HOST = "omp";
export const FORMAT_VERSION = 1;
export const GENESIS = "0".repeat(64);
export const DEFAULT_MAX_BYTES = 10 << 20;
export const DEFAULT_KEEP = 5;
export const MAX_COMMAND = 4000;
export const PREVIEW_LEN = 160;
export const UNCHECKED_MAX = 500;
export const REASON_MAX = 300;
const LOCK_WAIT_MS = 2000;
const LOCK_STALE_MS = 10000;

/** Audit directory: NOD_AUDIT_DIR if absolute, else $XDG_STATE_HOME/nod/audit if absolute, else ~/.local/state/nod/audit. */
export function auditDir(env = process.env, home = homedir()) {
	const d = env.NOD_AUDIT_DIR;
	if (d && isAbsolute(d)) return d;
	const x = env.XDG_STATE_HOME;
	if (x && isAbsolute(x)) return join(x, "nod", "audit");
	return join(home, ".local", "state", "nod", "audit");
}

export function auditFile(env = process.env, home = homedir()) {
	return join(auditDir(env, home), `${HOST}.jsonl`);
}

// The patterns of internal/decisionlog/redact.go. Go's \s is [\t\n\f\r ] and \S its complement,
// spelled out here because JavaScript's \s also matches \v and Unicode spaces. The "u" flag makes
// the case-insensitive match fold like Go's (?i) (e.g. U+017F matches s) and work on code points.
const WS = "[\\t\\n\\f\\r ]";
const SECRET_PATTERNS = [
	[new RegExp("-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[^]*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)", "gu"), "<private-key>"],
	[new RegExp(`(Authorization${WS}*:${WS}*)(?:Bearer|Basic|token)?${WS}*[^\\t\\n\\f\\r '"]+`, "giu"), "$1<redacted>"],
	[new RegExp(`\\bBearer${WS}+[A-Za-z0-9._~+/=-]{8,}`, "gu"), "Bearer <redacted>"],
	[/\b(?:sk-|ghp_|gho_|ghs_|ghu_|github_pat_|glpat-|xox[a-z]-|apify_api_|AKIA|ASIA|eyJ)[A-Za-z0-9_\-.]{12,}/gu, "<tok>"],
	[/\b([A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|PASS)[A-Z0-9_]*=)(?:"[^"]*"|'[^']*'|[^\t\n\f\r ]+)/gu, "$1<v>"],
];

/** Same as Go's decisionlog.Redact: removes obvious credentials. A safety net, not a guarantee. */
export function redact(s) {
	let out = String(s ?? "");
	for (const [re, rep] of SECRET_PATTERNS) out = out.replace(re, rep);
	return out;
}

/** Clip to n code points (Go runes), appending "…" when clipped. */
export function clipRunes(s, n) {
	const r = Array.from(s);
	return r.length <= n ? s : `${r.slice(0, n).join("")}…`;
}

export function sha256Hex(data) {
	return createHash("sha256").update(typeof data === "string" ? Buffer.from(data, "utf8") : data).digest("hex");
}

/** First 16 hex of sha256 of a session id ("" for none), like Go's HashSession. */
export function hashSession(id) {
	return id ? sha256Hex(String(id)).slice(0, 16) : "";
}

/** Normalised command level: full (default and unknown values), preview, hash. */
export function commandLevel(raw) {
	const l = String(raw ?? "").trim().toLowerCase();
	return l === "hash" || l === "preview" ? l : "full";
}

/**
 * {sha, text} for a command at a level, like Go's CommandFields. sha is of the original text.
 * max optionally lowers the clip length further (unchecked calls use 500).
 */
export function commandFields(cmd, level, max = MAX_COMMAND) {
	const c = String(cmd ?? "");
	if (c === "") return { sha: "", text: "" };
	const sha = sha256Hex(c);
	const l = commandLevel(level);
	if (l === "hash") return { sha, text: "" };
	const n = Math.min(l === "preview" ? PREVIEW_LEN : MAX_COMMAND, max);
	return { sha, text: clipRunes(redact(c), n) };
}

/** RFC3339 UTC with millisecond precision, trailing zeros dropped like Go's time.Time JSON. */
export function formatTime(date) {
	return date.toISOString().replace(/(\.\d*?)0+Z$/, "$1Z").replace(/\.Z$/, "Z");
}

// Field order and omitempty of Go's decisionlog.Entry. time, tool and decision are always present.
const FIELDS = ["v", "time", "host", "session", "mode", "review", "tool", "command", "command_sha256", "decision", "source", "model", "rules", "answer", "category", "ms", "error", "cwd", "reason", "prev"];
const ALWAYS = new Set(["time", "tool", "decision"]);

/** One line (no newline), byte-identical to Go's json.Marshal of the same Entry. */
export function encodeLine(entry) {
	const o = {};
	for (const k of FIELDS) {
		let v = entry[k];
		if (k === "rules") v = Array.isArray(v) ? v.filter((x) => typeof x === "string") : [];
		if (k === "ms" || k === "v") v = Number.isFinite(Number(v)) ? Math.trunc(Number(v)) : 0;
		const empty = v === undefined || v === null || v === "" || v === 0 || (Array.isArray(v) && v.length === 0);
		if (empty && !ALWAYS.has(k)) continue;
		o[k] = typeof v === "number" || Array.isArray(v) ? v : String(v ?? "");
	}
	// Go's encoder also escapes <, >, & and U+2028/U+2029.
	return JSON.stringify(o).replace(/[<>&\u2028\u2029]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, "0")}`);
}

/** The audit text for a call nod does not judge: the path for file tools, the command for bash, else the JSON of the input. */
export function describeInput(toolName, input) {
	const name = String(toolName || "").toLowerCase();
	const args = input && typeof input === "object" ? input : {};
	if (name === "bash" && typeof args.command === "string") return { text: args.command, max: MAX_COMMAND };
	const path = args.path ?? args.file_path;
	if (["read", "write", "edit", "ls", "grep", "find"].includes(name) && typeof path === "string") return { text: path, max: UNCHECKED_MAX };
	let json = "";
	try {
		json = JSON.stringify(args) ?? "";
	} catch {
		json = "[unserialisable input]";
	}
	return { text: json, max: UNCHECKED_MAX };
}

/**
 * Build the entry fields (without prev) for one call.
 * f: {time, session (raw id), mode, review, tool, text (original command), textMax, decision, source, rules, answer, category, ms, error, cwd, reason}
 */
export function buildEntry(f, level) {
	const { sha, text } = commandFields(f.text, level, f.textMax ?? MAX_COMMAND);
	return {
		v: FORMAT_VERSION,
		time: formatTime(f.time instanceof Date ? f.time : new Date()),
		host: HOST,
		session: hashSession(f.session),
		mode: f.mode,
		review: f.review,
		tool: String(f.tool ?? ""),
		command: text,
		command_sha256: sha,
		decision: String(f.decision ?? ""),
		source: f.source,
		rules: f.rules,
		answer: f.answer,
		category: f.category,
		ms: f.ms,
		error: f.error,
		cwd: f.cwd,
		reason: f.reason ? clipRunes(String(f.reason), REASON_MAX) : "",
	};
}

/**
 * Entry fields for a call the extension judged. verdict is the policy verdict ({decision, source,
 * rules, category, error}); a verdict with an error is logged with source "error". The extension fails
 * closed, so an error verdict's decision stays "block". answer is what happened to the call: allow,
 * block, ask-approved, ask-declined or ask-no-ui.
 */
export function gatedFields(verdict, { tool, text, cwd, session, answer, ms, reason, mode = "enforce" } = {}) {
	const v = verdict && typeof verdict === "object" ? verdict : {};
	const error = v.error ? String(v.error) : "";
	return {
		session,
		mode,
		review: "decider",
		tool,
		text,
		decision: v.decision === "allow" || v.decision === "ask" ? v.decision : "block",
		source: error ? "error" : v.source,
		rules: v.rules,
		answer,
		category: v.category,
		ms,
		error,
		cwd,
		reason,
	};
}

/** Entry fields for a call the extension saw but did not judge (decision "unchecked"). */
export function uncheckedFields(toolName, input, { cwd, session, source = "not-gated", mode } = {}) {
	const { text, max } = describeInput(toolName, input);
	return { session, mode, tool: String(toolName || "tool").toLowerCase(), text, textMax: max, decision: "unchecked", source, cwd };
}

/** Last non-empty line of an open file as bytes (empty Buffer when none); reads only the last MiB, like Go. */
function lastLine(fd) {
	const size = fstatSync(fd).size;
	if (size === 0) return Buffer.alloc(0);
	const start = Math.max(0, size - (1 << 20));
	const buf = Buffer.alloc(size - start);
	let off = 0;
	while (off < buf.length) {
		const n = readSync(fd, buf, off, buf.length - off, start + off);
		if (n <= 0) break;
		off += n;
	}
	let end = off;
	while (end > 0 && buf[end - 1] === 0x0a) end--;
	const i = buf.lastIndexOf(0x0a, end - 1);
	return buf.subarray(i + 1, end);
}

function lastLineOf(path) {
	let fd;
	try {
		fd = openSync(path, "r");
		return lastLine(fd);
	} catch {
		return Buffer.alloc(0);
	} finally {
		if (fd !== undefined) closeSync(fd);
	}
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export class AuditLog {
	/**
	 * path: the log file. opts: {maxBytes, keep, warn(msg), lockWaitMs, lockStaleMs}
	 */
	constructor(path, opts = {}) {
		this.path = path;
		this.maxBytes = opts.maxBytes ?? DEFAULT_MAX_BYTES;
		this.keep = opts.keep > 0 ? opts.keep : DEFAULT_KEEP;
		this.warnFn = opts.warn;
		this.lockWaitMs = opts.lockWaitMs ?? LOCK_WAIT_MS;
		this.lockStaleMs = opts.lockStaleMs ?? LOCK_STALE_MS;
		this.warned = false;
		this.tail = Promise.resolve();
	}

	warn(msg) {
		if (this.warned) return;
		this.warned = true;
		try {
			this.warnFn?.(msg);
		} catch {
			/* never throw */
		}
	}

	/** Append one entry (fields of buildEntry). Resolves true when written; never rejects. */
	append(entry) {
		const run = this.tail.then(() => this.appendLocked(entry));
		this.tail = run.catch(() => {});
		return run.catch((err) => {
			this.warn(`nod audit: cannot write ${this.path}: ${err?.message ?? err}`);
			return false;
		});
	}

	async appendLocked(entry) {
		const lock = `${this.path}.lock.d`;
		mkdirSync(dirname(this.path), { recursive: true, mode: 0o700 });
		const locked = await this.acquire(lock);
		try {
			this.appendSync(entry);
			return true;
		} finally {
			if (locked) {
				try {
					rmdirSync(lock);
				} catch {
					/* stolen or gone */
				}
			}
		}
	}

	async acquire(lock) {
		const deadline = Date.now() + this.lockWaitMs;
		for (;;) {
			try {
				mkdirSync(lock, { mode: 0o700 });
				return true;
			} catch (err) {
				if (err?.code !== "EEXIST") throw err;
			}
			try {
				// A crashed holder leaves its lock behind; steal it once it is clearly stale.
				if (Date.now() - statSync(lock).mtimeMs > this.lockStaleMs) {
					rmdirSync(lock);
					continue;
				}
			} catch {
				continue; // released meanwhile
			}
			if (Date.now() >= deadline) return false;
			await sleep(5 + Math.floor(Math.random() * 20));
		}
	}

	appendSync(entry) {
		let fd = openSync(this.path, "a+", 0o600);
		try {
			fchmodSync(fd, 0o600);
			const last = lastLine(fd);
			let prev = GENESIS;
			if (last.length === 0) {
				const l = lastLineOf(`${this.path}.1`);
				if (l.length > 0) prev = sha256Hex(l);
			} else {
				prev = sha256Hex(last);
			}
			const buf = Buffer.from(`${encodeLine({ ...entry, prev })}\n`, "utf8");
			const size = fstatSync(fd).size;
			if (this.maxBytes > 0 && size > 0 && size + buf.length > this.maxBytes) {
				closeSync(fd);
				fd = undefined;
				this.rotate();
				fd = openSync(this.path, "a+", 0o600);
				fchmodSync(fd, 0o600);
				// prev stays the hash of the last line of the file just rotated away.
			}
			let off = 0;
			while (off < buf.length) off += writeSync(fd, buf, off, buf.length - off);
		} finally {
			if (fd !== undefined) closeSync(fd);
		}
	}

	rotate() {
		const p = this.path;
		try {
			unlinkSync(`${p}.${this.keep}`);
		} catch {
			/* none */
		}
		for (let i = this.keep - 1; i >= 1; i--) {
			try {
				renameSync(`${p}.${i}`, `${p}.${i + 1}`);
			} catch {
				/* none */
			}
		}
		try {
			renameSync(p, `${p}.1`);
		} catch {
			/* none */
		}
	}
}

/**
 * The extension's audit: settings from env, one AuditLog. record(fields) never throws and resolves
 * when the line is written (or failed).
 */
export function createAudit({ env = process.env, home, warn, maxBytes } = {}) {
	let path = "";
	let level = "full";
	let log = null;
	try {
		path = auditFile(env, home ?? homedir());
		level = commandLevel(env.NOD_AUDIT_COMMANDS);
		log = new AuditLog(path, { warn, maxBytes });
	} catch (err) {
		try {
			warn?.(`nod audit: disabled: ${err?.message ?? err}`);
		} catch {
			/* never throw */
		}
	}
	return {
		path,
		level,
		record(fields) {
			try {
				if (!log) return Promise.resolve(false);
				return log.append(buildEntry(fields, level));
			} catch (err) {
				log?.warn(`nod audit: ${err?.message ?? err}`);
				return Promise.resolve(false);
			}
		},
	};
}
