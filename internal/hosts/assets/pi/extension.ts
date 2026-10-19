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
 * DECIDER_LOG          audit log, JSONL, default ~/.local/share/nod/omp-decisions.jsonl;
 *                      "off" disables. One line per gated call: input, verdict, final outcome.
 *                      Kept apart from the Claude hook's events.jsonl. Holds tool inputs: mode 0600.
 *
 * This is not a sandbox. It only decides whether this Pi process may run the call.
 */

import { appendFileSync, chmodSync, mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { dirname } from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { buildContext, conversationFromBranch, decideSchema, internalOverride, pluginAction, reasonText, shortReplyProposal, taskFromBranch, toolPayload, verdictFromDecide } from "./policy.mjs";

type Verdict = {
	decision?: string;
	source?: string;
	rules?: string[];
	error?: string;
	body?: unknown;
};

type Stats = { allow: number; ask: number; block: number; error: number };

const DEFAULT_URL = "http://127.0.0.1:11096";
const MANUAL_FALLBACK =
	"If this command is still needed, ask the user to run it manually with `!` in the OMP session. " +
	"Do not issue it as a tool call again; OMP will add the user's shell output to the conversation.";

const LOG_MAX_INPUT = 4000;
const PREVIEW_MAX = 3000;

function logPath(): string | null {
	const raw = process.env.DECIDER_LOG;
	if (raw === "off") return null;
	return raw || `${homedir()}/.local/share/nod/omp-decisions.jsonl`;
}

/** Best-effort audit record; never throws, never blocks the gate. */
function audit(rec: Record<string, unknown>): void {
	try {
		const path = logPath();
		if (!path) return;
		mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
		appendFileSync(path, `${JSON.stringify({ ts: Date.now() / 1000, ...rec })}\n`, { mode: 0o600 });
		chmodSync(path, 0o600);
	} catch {
		/* auditing must not affect the gate */
	}
}

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

	pi.on("tool_call", async (event, ctx) => {
		if (!enabled) return undefined;
		const { tool, text } = toolPayload(event.toolName, event.input);
		const started = Date.now();
		const base = {
			harness: "omp",
			tool_call_id: (event as { toolCallId?: string }).toolCallId,
			tool,
			cwd: ctx.cwd,
			input: text.length > LOG_MAX_INPUT ? text.slice(0, LOG_MAX_INPUT) : text,
			input_len: text.length,
		};
		const done = (outcome: string, verdict: Verdict | null, task?: string) => {
			audit({
				...base,
				task,
				outcome,
				decision: verdict?.decision,
				source: verdict?.source,
				rules: verdict?.rules,
				category: verdict?.category,
				p_allow: verdict?.p_allow,
				p_block: verdict?.p_block,
				error: verdict?.error,
				decider_body: verdict?.body,
				ms: Date.now() - started,
			});
		};
		const local = internalOverride(tool, text);
		if (local?.decision === "allow") {
			stats.allow += 1;
			done("allowed", local);
			return undefined;
		}
		if (local?.decision === "block") {
			stats.block += 1;
			done("blocked", local);
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
			done("allowed", verdict, task);
			return undefined;
		}
		if (action === "ask") {
			stats.ask += 1;
			if (!ctx.hasUI) {
				stats.block += 1;
				done("ask_blocked_no_ui", verdict, task);
				return { block: true, reason: `${why} (no UI, blocked). ${MANUAL_FALLBACK}` };
			}
			const preview = text.length > PREVIEW_MAX ? `${text.slice(0, PREVIEW_MAX)}\n… (${text.length - PREVIEW_MAX} more characters not shown)` : text;
			const ok = await ctx.ui.confirm(
				`decider: allow this ${tool} call?`,
				`${why}\n\ncwd: ${ctx.cwd}\n\n${preview}`,
			);
			if (!ok) {
				stats.block += 1;
				done("ask_declined", verdict, task);
				return { block: true, reason: `${why}; declined. ${MANUAL_FALLBACK}` };
			}
			done("ask_approved", verdict, task);
			return undefined;
		}
		stats.block += 1;
		done("blocked", verdict, task);
		return { block: true, reason: `${why}. ${MANUAL_FALLBACK}` };
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
