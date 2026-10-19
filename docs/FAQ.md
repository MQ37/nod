# FAQ

**Is this a sandbox?** No. Same-uid code can alter nod or host settings. Use a container or VM for untrusted agents.

**Does it send commands to a cloud model?** Not by default. The local service listens on `127.0.0.1:11096`. If configured with a remote URL, request fields are sent to that server over HTTP; there is no built-in authentication or TLS.

**What does shadow mode do?** It classifies and logs. Claude's hook emits no answer, so Claude Code continues its own permission flow. Enforce mode applies allow/ask/deny decisions. There is no automatic switch after seven days.

**Which models are available?** `fast` is Decider-4B Q4_K_M, 2.7 GB; `lite` is Gemma-4-12B-it Q8_0, 12.7 GB; `full` is Decider-35B-A3B Q8_0, 36.9 GB. See README and `docs/MODELS_AND_LIBS.md` for benchmark context and artifacts.

**How are cold starts handled?** The model loads on demand and unloads after 900 idle seconds by default. A cold classification waits at most 5 seconds. If loading continues, the call fails safe to ask. `nod hook claude --warm` requests asynchronous warming; `hooks install --warm` adds an optional SessionStart hook.

**What happens when the server is unreachable?** The default `on_unreachable=ask` asks. `block` and `allow` are configurable alternatives.

**How do I inspect decisions?** `nod decisions -n 20` or `nod decisions --follow`. The shared runtime file is `decisions.jsonl`; command and conversation text are not stored.

**Which hosts are supported?** Claude Code, Hermes, and Pi adapters are available with host-specific behavior. opencode gets an embedded plugin (bash calls only; ask is a failed call with an approval hint, not a prompt). See `docs/HOSTS.md`.

**How do I disable or remove it?** Set `nod config set mode shadow` to leave classification logging active without enforcement. `nod hooks remove --yes` removes host wiring; `nod stop` stops the service; `nod uninstall --yes` removes recorded installation files.

**Which platforms are built?** Linux amd64, Linux arm64, and macOS arm64. Windows and macOS amd64 are not build targets in `build.sh`.
