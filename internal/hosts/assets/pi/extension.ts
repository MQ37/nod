/**
 * Pi extension: score each tool call with Decider's native /decide API.
 *
 * The plugin builds the context and schema itself. The server returns per-question
 * probabilities. This file turns those into allow / ask / block.
 *
 * allow  -> the tool runs
 * ask    -> confirm in the UI; decline, cancel, or no UI blocks
 * block  -> the tool does not run
 * error, timeout, or a malformed verdict -> block
 *
 * Installed for Oh My Pi by linking this file to
 * ~/.omp/agent/extensions/decider/index.ts. A new `omp` session loads it.
 * Inside a running session, /reload picks up the link.
 *
 * DECIDER_URL          default http://127.0.0.1:11096
 * DECIDER_TIMEOUT      seconds, default 20 (covers a cold model load)
 * DECIDER_PI=off       starts the session with the gate disabled
 * /decider on|off      changes the gate for the current session
 *
 * Audit trail (audit.mjs): one line per tool call this extension sees, judged or not (with the gate
 * off: decision "unchecked"), in nod's shared format at <audit dir>/omp.jsonl (sha256 hash chain,
 * `nod audit verify`, `nod decisions`). Holds redacted tool input; files 0600 in a 0700 directory.
 * NOD_AUDIT_DIR        absolute directory; default $XDG_STATE_HOME/nod/audit, else ~/.local/state/nod/audit
 * NOD_AUDIT_COMMANDS   full (default: redacted, 4000 characters) | preview (160) | hash (sha256 only)
 * Writing the trail never changes a verdict; the first failure shows one warning.
 *
 * This is not a sandbox. It only decides whether this Pi process may run the call.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { buildContext, conversationFromBranch, decideSchema, internalOverride, pluginAction, reasonText, shortReplyProposal, taskFromBranch, toolPayload, verdictFromDecide } from "./policy.mjs";
import { createAudit, gatedFields, uncheckedFields } from "./audit.mjs";

type Verdict = {
	decision?: string;
	source?: string;
	rules?: string[];
	category?: string;
	error?: string;
	body?: unknown;
};

type Stats = { allow: number; ask: number; block: number; error: number };

const DEFAULT_URL = "http://127.0.0.1:11096";
const MANUAL_FALLBACK =
	"If this command is still needed, ask the user to run it manually with `!` in the OMP session. " +
	"Do not issue it as a tool call again; OMP will add the user's shell output to the conversation.";

const PREVIEW_MAX = 3000;

function endpoint(): string {
	const raw = (process.env.DECIDER_URL || DEFAULT_URL).replace(/\/$/, "");
	let url: URL;
	try {
		url = new URL(raw);
	} catch {
		throw new Error("DECIDER_URL is not a URL");
	}
	if (url.protocol !== "http:" && url.protocol !== "https:") {
		throw new Error("DECIDER_URL must be http or https");
	}
	return url.toString().replace(/\/$/, "");
}

async function classify(args: {
	tool: string;
	text: string;
	task: string;
	conversation: { role: string; text: string }[];
	cwd: string;
	signal?: AbortSignal;
}): Promise<Verdict> {
	const timeoutMs = Math.max(1, Number(process.env.DECIDER_TIMEOUT || "20")) * 1000;
	const controller = new AbortController();
	const timer = setTimeout(() => controller.abort(), timeoutMs);
	const onParentAbort = () => controller.abort();
	args.signal?.addEventListener("abort", onParentAbort, { once: true });
	try {
		const response = await fetch(`${endpoint()}/decide`, {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({
				context: buildContext(args),
				schema: decideSchema(args.conversation),
			}),
			signal: controller.signal,
		});
		if (!response.ok) {
			return { decision: "block", source: "plugin", error: `decider HTTP ${response.status}` };
		}
		const body = await response.json();
		return { ...verdictFromDecide(body, { bareReply: shortReplyProposal(args.conversation) !== null }), body };
	} catch (error) {
		const name = error instanceof Error ? error.name : "Error";
		return {
			decision: "block",
			source: "plugin",
			error: name === "AbortError" ? "decider timed out" : "decider unreachable",
		};
	} finally {
		clearTimeout(timer);
		args.signal?.removeEventListener("abort", onParentAbort);
	}
}

export default function deciderExtension(pi: ExtensionAPI) {
	const stats: Stats = { allow: 0, ask: 0, block: 0, error: 0 };
	let enabled = process.env.DECIDER_PI !== "off";
	let lastCtx: { hasUI?: boolean; ui?: { notify(message: string, level: string): void } } | undefined;
	const audit = createAudit({
		warn: (msg: string) => {
			try {
				if (lastCtx?.hasUI && lastCtx.ui) lastCtx.ui.notify(msg, "warning");
				else process.stderr.write(`${msg}\n`);
			} catch {
				/* a warning must not affect the gate */
			}
		},
	});

	pi.on("tool_call", async (event, ctx) => {
		lastCtx = ctx;
		const started = Date.now();
		let session = "";
		try {
			session = String((ctx.sessionManager as { getSessionId?: () => string }).getSessionId?.() ?? "");
		} catch {
			/* no session id */
		}
		// Best effort: an audit failure never throws and never changes the outcome.
		const record = async (fields: Record<string, unknown>) => {
			try {
				await audit.record(fields);
			} catch {
				/* never */
			}
		};
		if (!enabled) {
			await record(uncheckedFields(event.toolName, event.input, { cwd: ctx.cwd, session, source: "disabled", mode: "off" }));
			return undefined;
		}
		const { tool, text } = toolPayload(event.toolName, event.input);
		const done = (verdict: Verdict, answer: string) => {
			let reason = "";
			try {
				reason = reasonText(verdict);
			} catch {
				/* keep empty */
			}
			return record(gatedFields(verdict, { tool, text, cwd: ctx.cwd, session, answer, ms: Date.now() - started, reason }));
		};
		try {
			const local = internalOverride(tool, text);
			if (local?.decision === "allow") {
				stats.allow += 1;
				await done(local, "allow");
				return undefined;
			}
			if (local?.decision === "block") {
				stats.block += 1;
				await done(local, "block");
				return { block: true, reason: `${reasonText(local)}. ${MANUAL_FALLBACK}` };
			}
			const branch = ctx.sessionManager.getBranch();
			const task = taskFromBranch(branch);
			const verdict = await classify({
				tool,
				text,
				task,
				conversation: conversationFromBranch(branch),
				cwd: ctx.cwd,
				signal: ctx.signal,
			});
			if (verdict.source === "plugin") stats.error += 1;
			const action = pluginAction(verdict);
			const why = reasonText(verdict);

			if (action === "allow") {
				stats.allow += 1;
				await done(verdict, "allow");
				return undefined;
			}
			if (action === "ask") {
				stats.ask += 1;
				if (!ctx.hasUI) {
					stats.block += 1;
					await done(verdict, "ask-no-ui");
					return { block: true, reason: `${why} (no UI, blocked). ${MANUAL_FALLBACK}` };
				}
				const preview = text.length > PREVIEW_MAX ? `${text.slice(0, PREVIEW_MAX)}\n… (${text.length - PREVIEW_MAX} more characters not shown)` : text;
				const ok = await ctx.ui.confirm(
					`decider: allow this ${tool} call?`,
					`${why}\n\ncwd: ${ctx.cwd}\n\n${preview}`,
				);
				if (!ok) {
					stats.block += 1;
					await done(verdict, "ask-declined");
					return { block: true, reason: `${why}; declined. ${MANUAL_FALLBACK}` };
				}
				await done(verdict, "ask-approved");
				return undefined;
			}
			stats.block += 1;
			await done(verdict, "block");
			return { block: true, reason: `${why}. ${MANUAL_FALLBACK}` };
		} catch (error) {
			// The host blocks a call whose extension throws; record that, then let it throw as before.
			const msg = error instanceof Error ? error.message : String(error);
			await done({ decision: "block", source: "plugin", error: `extension error: ${msg}` }, "block");
			throw error;
		}
	});

	pi.registerCommand("decider", {
		description: "Show status or enable/disable Decider for this session: /decider [on|off]",
		handler: async (args, ctx) => {
			const action = args.trim().toLowerCase();
			if (action === "on") enabled = true;
			else if (action === "off") enabled = false;
			else if (action && action !== "status") {
				ctx.ui.notify("Usage: /decider [on|off|status]", "warning");
				return;
			}
			let where = "unset";
			try {
				where = new URL(endpoint()).host;
			} catch {
				where = "invalid DECIDER_URL";
			}
			ctx.ui.notify(
				`decider ${enabled ? "on" : "off"} @ ${where}  allow ${stats.allow}  ask ${stats.ask}  block ${stats.block}  errors ${stats.error}`,
				"info",
			);
		},
	});
}
