# Host integrations

Use `nod hooks install|remove|status [--host H]`. Plans include paths and diffs; `--dry-run` never applies. Install and remove require `--yes` to apply (without it, the command prints a `needs` response). With no host specified, install targets detected hosts. Host config backups use `<path>.bak-nod-YYYYmmdd-HHMMSS` (a collision adds a numeric suffix).

## Claude Code

The adapter edits the user settings file selected by `CLAUDE_CONFIG_DIR/settings.json`, or `~/.claude/settings.json`. It merges a `hooks.PreToolUse` entry with matcher `Bash` by default (`hooks install --matcher TOOL` changes it) and timeout 15 seconds. It also supports optional `hooks install --warm`, which adds a `SessionStart` command `nod hook claude --warm`.

PreToolUse input is JSON with fields such as `hook_event_name`, `tool_name`, `tool_input`, `cwd`, `transcript_path` and `session_id`. In enforce mode the answer is:

```json
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow|ask|deny","permissionDecisionReason":"..."}}
```

Shadow mode classifies/logs but writes no stdout answer, leaving Claude's own prompt in charge. Enforce mode maps allow to `allow`, ask to `ask`, and block to `deny`. Failures use configured `on_unreachable` (default ask). The transcript parser keeps only real user messages and relevant assistant context; it skips tool results, sidechains and meta messages and clips message text.

`nod hook claude --warm` requests `GET /healthz?warm=1`; the server begins model loading in the background. The optional SessionStart hook invokes this path. Remove only nod entries with `nod hooks remove --host claude --yes`. Malformed settings JSON is refused without modification.

## Hermes

The binary embeds and copies the plugin into `~/.hermes/plugins/nod-gate/` and adds managed `NOD_URL`, `NOD_MODE`, and `NOD_TIMEOUT` lines to `~/.hermes/.env`, without overwriting user-defined values. The plugin must be enabled in Hermes (`hermes plugins enable nod-gate`) and Hermes restarted. It runs inside Hermes' Python plugin runtime. Status reports whether files match and whether the plugin is enabled; the adapter does not edit the enabled-plugin setting. Removal deletes the copied plugin and managed env block; disable the plugin through Hermes as needed.

### Agent review (`nod install --model agent`)

With `hosts.Options.Review = "agent"` the managed env block also gets `NOD_REVIEW=agent` and `NOD_TIMEOUT=15` (instead of `2.5`); `hooks status` shows the review mode next to the mode (`review=agent` or `review=decider`). In this mode the plugin does not ask the Decider. For each gated call (terminal, read_file, write_file, patch) that is not obviously read-only, it:

1. sends the call to `POST /v1/review/prompt` on the nod server and gets a system and user prompt back;
2. asks the model the Hermes session already runs, through Hermes' plugin LLM access (`ctx.llm.complete`, no provider or model override, temperature 0, at most 200 tokens), and takes the first JSON object from the reply;
3. sends the answers to `POST /v1/review/verdict`, which turns them into allow, ask or block with the same pipeline as Decider answers.

In enforce mode allow passes, ask goes to Hermes' own approval prompt, and block vetoes the call. Shadow mode only logs to `hermes-shadow.jsonl`.

- **Cost:** one extra request per gated call on the agent's own model. It is billed to the same account and counts in its usage and rate limits.
- **Injection:** the reviewer sees only the call (redacted and clipped) and the user's own messages, plus the assistant message each one replies to. It never sees tool output or file contents. The prompt text comes from the nod server, which tells the model to treat all of it as data.
- **Fail safe:** unlike the Decider path, which fails open, any failure here means ask, never allow. This covers an unreachable nod server, a Hermes without `ctx.llm` (logged once), a model error or timeout, an unparsable reply, and HTTP 400 for invalid answers. In enforce mode Hermes then shows its approval prompt with "nod: review unavailable (...)". A review never starts a second review from inside itself.
- **Timeouts:** Hermes fails closed and blocks the tool call when a `pre_tool_call` hook runs longer than `plugins.hook_callback_timeout` (default 30 s). The plugin therefore caps the model call at `NOD_TIMEOUT` (at most 20 s), with a 3 s budget for each nod server request, and asks when the model call runs out of time. A slow model shows up as approval prompts, not as blocked calls.

## Pi / Oh My Pi

The binary embeds `index.ts`, `policy.mjs` and `audit.mjs` into `~/.omp/agent/extensions/decider/` (or uses the detected `~/.pi` base). The extension is host-side TypeScript and calls the server's `/decide` endpoint. The integration has its own behavior and failure policy; verify in an isolated Pi session. `nod hook pi` itself has no decision protocol because Pi loads the extension in-process. Remove with `nod hooks remove --host pi --yes`.

**Audit trail.** The extension writes nod's audit trail itself, to `omp.jsonl` in the audit directory (the same file for Pi and Oh My Pi), in the format and hash chain of the Go writer, so `nod audit verify` and `nod decisions` read it. It writes one line for every tool call it sees, before the call runs or is blocked: judged calls with the verdict, the source (`error` when the server failed; the extension then blocks) and `answer` (what happened: `allow`, `block`, `ask-approved`, `ask-declined`, `ask-no-ui`); with the gate off (`DECIDER_PI=off`, `/decider off`) each call is logged as `unchecked` with source `disabled` (the path for file tools, the command for bash, otherwise the input JSON clipped to 500 characters). Pi has no nod settings file, so the extension reads only its own environment: `NOD_AUDIT_DIR` (absolute; default `$XDG_STATE_HOME/nod/audit`, else `~/.local/state/nod/audit`; a config file's `audit_dir` is not seen) and `NOD_AUDIT_COMMANDS` (`full` default, `preview`, `hash`). Commands are redacted with the same patterns as the Go side. Without flock, processes serialise on an `omp.jsonl.lock.d` directory. A write failure never changes a verdict; it shows one warning per session.

## opencode

The binary embeds a plugin and writes two files into the plugin directory of opencode's global config dir (`$OPENCODE_CONFIG_DIR`, else `$XDG_CONFIG_HOME/opencode`, else `~/.config/opencode`): `plugin/nod.js` and its settings `plugin/nod.json` (`url`, `mode`, `review`, `timeout_ms`, `tools`). opencode loads every `*.js`/`*.ts` in that directory at start, so restart it after install or remove. `NOD_URL`, `NOD_MODE` and `NOD_REVIEW` in opencode's environment override `nod.json`. Existing files are backed up before they are replaced; remove deletes only `nod.js` with nod's marker line and `nod.json` with `"managed_by": "nod"`. `nod hook opencode` has nothing to do.

The plugin uses opencode's `tool.execute.before` hook and gates `bash` only (tool `Bash`, input = the command). It sends the command (secrets such as `sk-…`, `ghp_…`, `AKIA…`, private keys and `Authorization` headers redacted, clipped to 4000 characters), the working directory and the human's own last six messages (600 characters each; never assistant text, tool output or attached file contents; for a subagent session, the messages of its root session).

- **shadow** (default): never interferes; appends one line per call (time, tool, decision, source, reason, no command text) to `$XDG_RUNTIME_DIR/nod/opencode-decisions.jsonl` (or the temp dir), mode 0600.
- **enforce**: allow runs the call. block throws `nod: blocked: <reason>. Do not retry this exact action; …`. An opencode plugin cannot open an approval prompt, so ask also fails the call, with a message telling the model to ask the user to confirm this exact action in plain words and retry; the user's answer is then in the conversation the next review sees. This is weaker than a real prompt: it relies on the model asking honestly.
- **Fail-safe**: an unreachable server, a timeout, invalid answers or a failed review session all count as ask, never allow (in shadow mode they are only logged).

Review modes: `server` (default) posts to `/v1/classify` and the Decider decides. `agent` (`nod install --model agent`) asks the model the opencode session is already using: the plugin gets the prompt from `/v1/review/prompt`, sends it in a hidden child session with every tool disabled, parses the JSON answer and posts it to `/v1/review/verdict`; the child session is deleted afterwards. Cost: one extra model request per gated call, billed to the same account, with a 60 s timeout. The reviewer sees only the call and the user's own messages, and its instructions treat them as data; a command crafted to talk the reviewing model into "allow" is still a risk the Decider does not have in the same way.

Nothing here has been run against a real opencode: the hook names, the plugin export shape (`export default {id, server}`, required because opencode calls every export of a module without it as a plugin) and the SDK calls were checked against the opencode source of 2026-05-22.

## Verdicts

CLI classifications and Claude hook verdicts use `decisions.jsonl` in the runtime directory, which `nod decisions` reads. Hermes writes its plugin audit to `hermes-shadow.jsonl`; Pi writes the shared audit trail (`omp.jsonl`, see above), which `nod decisions` reads. See [PRIVACY.md](PRIVACY.md).
