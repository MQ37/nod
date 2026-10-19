# FAQ

**Is this a sandbox?** No. Same-uid code can alter nod or host settings. Use a container or VM for untrusted agents.

**Does it send commands to a cloud model?** Not by default. The local service listens on `127.0.0.1:11096`. If configured with a remote URL, request fields are sent to that server over HTTP; there is no built-in authentication or TLS.

**What does shadow mode do?** It classifies and logs. Claude's hook emits no answer, so Claude Code continues its own permission flow. Enforce mode applies allow/ask/deny decisions. There is no automatic switch after seven days: shadow mode stays on until you change it.

**Which models are available?** `fast` is Decider-4B Q4_K_M, 2.7 GB; `lite` is Gemma-4-12B-it Q8_0, 12.7 GB; `full` is Decider-35B-A3B Q8_0, 36.9 GB. See [MODEL_TIERS.md](MODEL_TIERS.md) for benchmark context and [MODELS_AND_LIBS.md](MODELS_AND_LIBS.md) for artifacts.

**How are cold starts handled?** The model loads on demand and unloads after 900 idle seconds by default. A cold classification waits at most 5 seconds. If loading continues, the call fails safe to ask. `nod hook claude --warm` requests asynchronous warming; `hooks install --warm` adds an optional SessionStart hook.

**What happens when the server is unreachable?** The default `on_unreachable=ask` asks. `block` and `allow` are configurable alternatives.

**How do I inspect decisions?** `nod decisions -n 20 -v` or `nod decisions --follow`: every host's verdicts, with the command under `-v`. The persistent files are in `~/.local/state/nod/audit/`; `nod audit verify` checks that nothing in the middle was altered. Conversation text is not stored. See [AUDIT.md](AUDIT.md).

**Which hosts are supported?** Claude Code, Hermes, and Pi adapters are available with host-specific behavior. opencode gets an embedded plugin (bash calls only; ask is a failed call with an approval hint, not a prompt). See `docs/HOSTS.md`.

**How do I disable or remove it?** Set `nod config set mode shadow` to leave classification logging active without enforcement. `nod hooks remove --yes` removes host wiring; `nod stop` stops the service; `nod uninstall --yes` removes recorded installation files (config, service, hooks). The model and libraries you fetched are yours to delete.

**Which platforms are built?** Linux amd64, Linux arm64, and macOS arm64. Windows and macOS amd64 are not build targets in `build.sh`.

**Where does `go install` put the binary, and can I build it myself?** `go install` puts `nod` in `$(go env GOPATH)/bin` (usually `~/go/bin`); make sure that is on your `PATH`. To build it yourself, run `bash ./build.sh build` and `install -m 755 dist/nod-linux-amd64 ~/.local/bin/nod` (see [DEVELOPMENT.md](DEVELOPMENT.md)).

**How does an agent install Nod for someone else?** Agents get the same commands, non-interactively:

```sh
nod skill print                                                         # operator checklist (SKILL.md)
nod install --model fast --host claude --mode shadow --dry-run --json   # the plan, nothing applied
nod install --model fast --host claude --mode shadow --yes --json
nod doctor --json
```

A missing choice never blocks on input: the command exits 5 and prints the flag that answers it, such as `{"needs":[{"flag":"--yes","question":"execute the complete installation plan"}]}`. `nod skill install` copies the skill to `~/.claude/skills/nod/`. More: [INSTALL.md](../INSTALL.md), [EXIT_CODES.md](EXIT_CODES.md).

**Can a laptop use a remote server?** Point Nod at the server and keep a small model as a backup: `nod install --model fast --remote http://192.0.2.10:11096 --host none --yes`. Calls go to the remote first and fall back to the local model if it is unreachable. The local model never loads while you are on battery (`nod config set local_power always` to allow it). Details: [INSTALL.md](../INSTALL.md#remote-first-local-backup-battery-rule).

**Why is set-up more than a download?** Nod does not download llama.cpp or models itself. The skill (or you) fetches llama.cpp and the model with checksums; a wrong or truncated file shows up in `nod models verify` and `nod doctor`. You can follow the skill's steps by hand: `nod skill print`.
