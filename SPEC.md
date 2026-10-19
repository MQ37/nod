# nod CLI: spec (2026-10-17)

A Go CLI that gates an AI agent's tool calls (shell commands, edits, fetches) with the Decider model. One static binary, no Python. Humans get an interactive installer, agents get the same commands non-interactively. The Python implementation in `../nod/` is the reference: every behaviour below is pinned by golden tests generated from it.

## 1. Commands
```
nod install   [--model full|lite|fast] [--host claude,hermes,pi,opencode|none] [--service-name NAME] [--remote URL] [--mode shadow|enforce] [--addr HOST:PORT] [--on-unreachable ask|block|allow] [--yes] [--dry-run] [--json]
nod uninstall [--service-name NAME] [--yes]
nod doctor    [--service-name NAME] [--json] # checks: binary, models + sha256, llama.cpp libs, server health, hooks present, permissions, latency probe
nod status    [--service-name NAME] [--json]
nod serve     [--addr 127.0.0.1:11096] [--model PATH] [--idle-unload SECS] [--warm-wait SECS]   # the daemon (HTTP: /v1/classify /decide /healthz)
nod start|stop|restart [--service-name NAME] # the installed service (systemd --user / launchd)
nod check     [--tool Bash] [--cwd DIR] [--task TXT] [--json] -- COMMAND...      # exit 0 allow, 1 ask, 2 block, 3 error
nod hook      <claude|hermes|pi|opencode>  # host adapter: reads the host's JSON on stdin, prints the host's answer; never prints anything else on stdout
nod hooks     install|remove|status [--host H] [--yes] [--dry-run] [--json]     # edits the host's config, always with a timestamped backup
nod models    list|pull MODEL|use MODEL|verify|remove MODEL
nod config    get|set|path
nod decisions [-n 20] [--follow] [--json]  # verdict log (never the full command text unless --verbose)
nod connect   URL                          # use a remote server instead of a local daemon
nod skill     print|install [--dir DIR]    # the agent skill (SKILL.md) from skill/
nod version [--json] ; nod completion bash|zsh|fish
```
Global flags: `--json` (machine output on stdout, human text on stderr), `--yes/-y`, `--non-interactive` (default when stdin or stdout is not a TTY or `CI`/`NOD_NONINTERACTIVE` is set), `--quiet`, `--verbose`, `--config PATH`. For `check`, exits are exactly 0 allow, 1 ask, 2 block, 3 error; for other commands, 1 is usage/other error, 3 is not installed/unreachable, 4 verification failed (checksum, hook not wired), 5 needs a human decision (non-interactive run hit a prompt without a default).
In non-interactive mode a command never blocks on input: it takes its default, or exits 5 and prints exactly which flag answers the question (`--json`: `{"needs":[{"flag":"--model","choices":["full","lite","fast"]}]}`).

## 2. Install flow (interactive and non-interactive are the same plan)
1. Detect: OS/arch, RAM and GPU (Vulkan/ROCm/Metal/CUDA via `llama-cli --list-devices`-style probing or sysfs), existing install, hosts present (`~/.claude`, `~/.hermes`, `~/.pi`/`~/.omp`, opencode config).
2. Choose the model tier (recommend from RAM/GPU; `--model` overrides): **full** = Decider-35B-A3B Q8_0 36.9 GB (>=64 GB RAM and GPU), **lite** = Gemma-4-12B-it Q8_0 12.7 GB (>=24 GB RAM), **fast** = Decider-4B Q4_K_M 2.7 GB. See `docs/MODELS_AND_LIBS.md` for the benchmark.
3. Check what the user (or their agent, following the skill) provided: `--model-path` (the GGUF, with `tokenizer.json`, `decider_config.json` and for lite `decider_chat.json` beside it) and `--llama-lib-dir` (a directory with `libllama`). Missing pieces exit 5 with `needs` JSON. **Nod never downloads, builds or converts anything.**
4. Print the plan (every file it will write and every config edit as a diff). `--dry-run` stops here. Interactive: ask to proceed; `--yes` proceeds. Execute: write config (including `model_path` and `llama_lib_dir`), install the user service (systemd --user unit / launchd plist; no root), start it, wait healthy.
5. Wire hosts: `hooks install` for the chosen hosts (Claude Code: user-level `PreToolUse` hook entry `nod hook claude` in `~/.claude/settings.json`, merged not overwritten, backup first; Hermes: plugin dir + env file entries; Pi: extension file). `--host none` leaves host wiring out for a server-only install. Default mode **shadow** (log only) for 7 days unless `--mode enforce`; after 7 days shadow remains in effect until an operator changes it. In Claude shadow mode the hook emits no decision, leaving the host's normal permission flow intact.
6. Verify: `doctor`, a canned allow case and a canned block case through the real hook path. Exit 4 if any fails and print the rollback command.
Idempotent: re-running changes nothing that is already right. `uninstall` removes exactly what `install` recorded in `state.json`.

## 3. Engine (port of the Python reference, golden-tested)
`internal/engine`: `tiers` (deterministic lexer/AST tiers T0/T0b), `rules` (BLOCK/ASK rules with `ASK_IGNORE`), `pipeline` (severity x consent matrix), `conversation` (D4 context: last 6 user turns + replied-to assistant text, bare-reply detection, conditional/negation cues), `prompt` (categories, policy, questions: byte-identical to `guard.py`), `decider` (typed one-forward-pass readout; use `github.com/hybridgroup/yzma` `exp/decide` or a thin purego binding, newest llama.cpp, no cgo). Golden corpora live in `testdata/` generated by `tools/gen_golden.py` from the Python code (rules/tiers outputs for ~20k commands, pipeline cases, rendered prompts). Go must match the Python results exactly on rules/tiers/pipeline/prompt, and within 1e-3 on probabilities.
Fail-safe: any error ends in `ask`, never `allow`; service unreachable = `ask` (configurable `on_unreachable = ask|block|allow`, default ask). Model loading is lazy; model, context and batch unload after 900 idle seconds by default for both tiers (`idle_unload_secs=0` keeps them resident). A cold model load runs in the background; requests wait at most `warm_wait_secs=5`, then take the rules-only fail-safe ask path while loading continues.

## 4. Hosts
Adapters in `internal/hosts/{claude,hermes,pi,opencode}`: each implements `Detect() / Install(plan) / Remove() / Status() / Hook(stdin) stdout`. Claude Code hook protocol: PreToolUse JSON on stdin (`tool_name`, `tool_input`, `cwd`, `transcript_path`, `session_id`), answer JSON `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow|ask|deny","permissionDecisionReason":"..."}}`. The conversation context is rebuilt from `transcript_path` (user turns only, never tool output). Hermes/Pi/opencode details follow `../nod/hermes/` and `../nod/pi/`.

## 5. Quality bar
Podman-only builds and tests (`./build.sh`, rootless, read-only, no network for tests, tmpfs); stdlib + yzma + a minimal CLI framework (cobra/pflag only if vetted) -- no other dependencies; prompts hand-rolled (no TTY libraries). `--help` for every command with examples; shell completion; man-style `docs/`; every command has `--json`; table-driven tests; `go vet`, `staticcheck`-clean; no secrets or conversation text in logs (verdict lines only); verdict log in `$XDG_RUNTIME_DIR` (RAM). Threat model stated in README (same-uid agent can tamper; not a sandbox).

## 6. Agent skill (`skill/SKILL.md`)
For an agent installing nod for a human operator: what it is, the decision to ask the operator (model tier, hosts, shadow vs enforce, remote vs local), exact non-interactive commands, how to verify, how to roll back, what never to do (do not edit the guard's own files, do not disable hooks, do not pipe installers from the network, confirm with the operator before touching `~/.claude/settings.json`), troubleshooting table, and the machine-readable outputs to parse. `nod skill install` drops it into `~/.claude/skills/nod/` (and prints other hosts' paths).

## 7. Work split
A engine port + goldens; B decider/yzma + models + llama.cpp lib fetcher + serve; C CLI shell, config, install/uninstall/doctor/status/service units; D host adapters + hooks; E skill + docs + completion. Integration and end-to-end podman tests by the lead.
