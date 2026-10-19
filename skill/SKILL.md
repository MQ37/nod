---
name: nod
description: Set up, configure, verify, roll back or troubleshoot nod, the allow/ask/block gate for agent tool calls. Use when asked to install nod for a person, add a local backup model, connect a machine to a remote nod server, or fix a nod install. Nod downloads nothing itself: this skill tells you what to fetch and how.
---

# nod: set it up for a human

Nod judges every agent tool call with a Decider model and answers `allow`, `ask` or `block`. **Nod does not download or build anything.** It writes its own configuration and service, and wires host hooks. Getting llama.cpp and a model onto the machine is your job, and this skill is the procedure.

The human owns the choices (host, model size, remote vs local, shadow vs enforce). Ask, show a dry run, then apply.

## 0. Know what you are setting up

- **nod** (the Go binary): config template, user service, host hooks, `check`, `doctor`.
- **llama.cpp shared libraries**: the runtime that runs the model. Nod loads `libllama` itself (no cgo, no `llama-server`).
- **A Decider GGUF model** with a few small files next to it (`tokenizer.json`, `decider_config.json`, and for `lite` also `decider_chat.json`).

If the machine only *uses* an existing nod server, skip sections 2 and 3: see "Client only".

This is **not a sandbox**: same-user code (including an agent) can change the binary, service, config or hooks. Say so to the operator.

## 1. Ask the operator

1. **Where does the model run?** Here (local), on another machine (remote server), remote first with a small local backup (laptop), or **the agent's own model** (`--model agent`, Hermes and OpenCode only; no model or llama.cpp to set up, but one extra request per reviewed call on the agent's model and account).
2. **Model size** (`nod models list` shows the recommendation for this machine):
   - `fast`: Decider-4B Q4_K_M, 2.7 GB file, ~8 GB RAM. Fine for a laptop or a backup.
   - `lite`: Decider-12B (Gemma-4-12B-it base) Q8_0, 12.7 GB, plan 24 GB RAM. Fits beside another large model.
   - `full`: Decider-35B-A3B Q8_0, 36.9 GB, 64 GB RAM and a GPU.
3. **Hosts to wire**: Claude Code, Hermes, Pi and opencode can be installed (opencode: plugin file, restart opencode after). `--host none` installs the service without hooks.
4. **Mode**: `shadow` (default: logs verdicts, the host's own prompts stay in charge) or `enforce`.
5. **Unreachable policy** (`on_unreachable`): `ask` (default), `block`, `allow`.
6. **GPU?** The downloadable llama.cpp release is CPU-only. A GPU needs a build or a GPU release asset (section 2).
7. **Permission** to download the files below, to edit the host config files (backups are made), and to run a service as the user.

## 2. Get llama.cpp (a directory containing `libllama.so` / `libllama.dylib`)

Check first: `nod doctor --json` reports `llama_cpp_libs`. If a llama.cpp build already exists (for example `/opt/llama.cpp/build/bin`), use that directory and skip this section.

### Option A: a prebuilt release (CPU, easiest)

Tested release: **b11714** from `https://github.com/ggml-org/llama.cpp/releases/tag/b11714`. Download the asset for the platform, verify its sha256 (GitHub shows the digest next to each asset; the values below are the ones we recorded), extract into a new empty directory you own.

| platform | asset | sha256 |
|---|---|---|
| linux x86_64 | `llama-b11714-bin-ubuntu-x64.tar.gz` | `639059a1106e706810486285ec6d1d92c961f3f920ef78bbdd1ee359428679d0` |
| linux arm64 | `llama-b11714-bin-ubuntu-arm64.tar.gz` | `61491da8c08636a61be9a8428d37a7c065a2afd44fa8339ce02a6a78cb68ec32` |
| macOS arm64 | `llama-b11714-bin-macos-arm64.tar.gz` | `063f58b7d8458d89eacb2f28a10a6d6e0efa2a81866690b0f6f8ebac6e3846bb` |

```sh
mkdir -p ~/.local/share/nod/llama && cd ~/.local/share/nod/llama
curl -fL -O https://github.com/ggml-org/llama.cpp/releases/download/b11714/llama-b11714-bin-ubuntu-x64.tar.gz
echo "639059a1106e706810486285ec6d1d92c961f3f920ef78bbdd1ee359428679d0  llama-b11714-bin-ubuntu-x64.tar.gz" | sha256sum -c -   # macOS: shasum -a 256 -c -
tar -xzf llama-b11714-bin-ubuntu-x64.tar.gz
find . -name 'libllama.so' -o -name 'libllama.dylib'      # the directory it prints is the lib dir
```

Never run anything from the archive; Nod only loads the libraries. Keep `libggml*.so` and the `libggml-cpu-*.so` plugins in the same directory as `libllama`; Nod loads those backend plugins itself.

### Option B: your own build (GPU, or a newer llama.cpp)

Build the llama.cpp tree you trust, then use its `build/bin` as the lib dir. Backends: `-DGGML_VULKAN=ON` (most GPUs), `-DGGML_HIP=ON` (AMD ROCm), `-DGGML_CUDA=ON` (NVIDIA); macOS builds use Metal by default.

```sh
git clone https://github.com/ggml-org/llama.cpp && cd llama.cpp
cmake -B build -DGGML_VULKAN=ON -DBUILD_SHARED_LIBS=ON && cmake --build build -j
# lib dir: $PWD/build/bin
```

Nod's binding matches the `b11714` struct layout. A much newer llama.cpp may change `llama_model_params`/`llama_context_params`; if loading fails after a rebuild, go back to b11714.

## 3. Get the model

`nod models list --json` is the source of truth: per tier it lists every file with `url`, `size`, `sha256`, whether it must be converted, and the exact conversion commands. Use one directory per model, for example `~/.local/share/nod/models/fast/`.

### `fast` (a ready-made GGUF)

```sh
nod models list --json    # read the "fast" entry: five files
mkdir -p ~/.local/share/nod/models/fast && cd ~/.local/share/nod/models/fast
# for each file in the entry:
curl -fL -C - -O <url>
nod models verify ~/.local/share/nod/models/fast --tier fast     # exit 0 only when every sha256 matches
```

### `lite` and `full` (download safetensors, convert to GGUF)

These are not published as GGUF, so you convert on the machine. Plan the disk: `lite` downloads about 24 GB and produces 12.7 GB; `full` downloads about 70 GB and produces 36.9 GB. The converter and the patch script come from the **llama.cpp tree you trust**, never from the model repository (do not run any `decider/*.py` file from it).

1. Download every file in the tier's `files` list into `<dir>/src/` and check the sizes and sha256 (`nod models verify` also checks the sidecars).
2. In a Python venv: `pip install -r <llama.cpp>/requirements/requirements-convert_hf_to_gguf.txt`.
3. Run the commands from the tier's `convert` list (`nod models list --json`). For `lite`: `python3 convert_hf_to_gguf.py <dir>/src --outtype q8_0 --outfile <dir>/decider-12b-Q8_0.gguf`. For `full` the list also contains two `gguf_set_metadata.py` patch steps that are required.
4. Copy `tokenizer.json` and `decider_config.json` from `src/` next to the GGUF.
5. **lite only**: `nod models sidecar lite <dir>` writes `decider_chat.json` (the chat prompt layout and the token ids Nod checks at load time).
6. `nod models verify <dir> --tier lite`: the converted GGUF shows `unpinned` with its computed sha256 (no published hash exists for a local conversion). That is expected; anything else other than `ok` is not.

## 4. Install

Always dry-run first and show the operator the plan. It lists the files Nod will write or edit. **Nothing is downloaded.**

```sh
nod install --model fast --model-path ~/.local/share/nod/models/fast/decider-4b-v2.1-Q4_K_M.gguf \
  --llama-lib-dir ~/.local/share/nod/llama/<dir-from-find> --host claude --mode shadow --dry-run --json
# after approval:
nod install ...same flags... --yes --json
```

- A missing `--model-path` or `--llama-lib-dir` exits **5** with `needs` JSON naming the flag; a missing sidecar (`tokenizer.json`, `decider_config.json`, `decider_chat.json` for lite) is named in the message.
- `--host claude,hermes,pi` selects hosts; omit it to detect them; `--host none` installs the service only.
- Other flags: `--service-name`, `--addr HOST:PORT`, `--on-unreachable ask|block|allow`, `--mode shadow|enforce`, `--remote URL`.
- Install starts the service, warms the model and runs `nod doctor`; a failing doctor exits 4 and the message says how to roll back.

### Remote server with a local backup (laptop)

On the big machine (bind to loopback or a private network only; the HTTP server has no authentication or TLS):

```sh
nod install --model lite --model-path ... --llama-lib-dir ... --host none --addr 192.0.2.10:11096 --yes --json
```

On the laptop, install a small local backup that talks to it first:

```sh
nod install --model fast --model-path ... --llama-lib-dir ... --remote http://192.0.2.10:11096 --host claude --mode shadow --yes --json
```

Calls go to the remote; if it cannot answer, Nod skips it for 15 seconds and asks the local daemon. The local model never loads on battery (`local_power`, default `plugged`). Tune with `nod config set local_power plugged|always|never` and `nod config set remote_fallback local|none`.

### Client only

`nod connect http://192.0.2.10:11096` then `nod hooks install --host claude --yes`. No model or libraries needed on this machine.

### Agent review (no local model, Hermes or OpenCode)

Skip sections 2 and 3: nothing to download. Confirm with the operator that they accept one extra model request per reviewed call (cost and latency on the agent's own model) and that a model reviewing its own session is a weaker check than a separate Decider. Then:

```sh
nod install --model agent --host hermes --mode shadow --dry-run --json     # or --host opencode
nod install --model agent --host hermes --mode shadow --yes --json
```

This writes the config, a tiny local service (`nod serve --model agent`, it only builds review prompts and applies the pipeline) and the host plugin with `NOD_REVIEW=agent`. Claude Code and Pi are rejected in this mode (exit 64): their hooks cannot reach the agent's model. Start a fresh agent session afterwards. Stay in `shadow` until `nod decisions` look right: failures and timeouts mean ask, and Hermes blocks a tool if its hook takes longer than 30 s.

## 5. Verify

1. `nod doctor --json`: every check `ok`. A failing doctor exits 4.
2. `nod status --json`: `installed`, `active`, `model_status`, `power`, and for a remote setup `local_model` (`paused: ...` explains why a local backup is not loaded).
3. `nod check --json -- ls`: a quick probe. Verdicts depend on the model; do not promise a specific result for arbitrary input.
4. Start a fresh host session after installing hooks, then review `nod decisions -n 20 -v`: every action Nod saw, per host, with the (redacted) command. It is a persistent audit trail in `~/.local/state/nod/audit/`; `nod audit verify` checks its hash chain, and `docs/AUDIT.md` explains the fields. Tell the operator the trail contains commands (redacted), so the directory is sensitive.

## 6. Roll back

Show the plan and get approval first. Host backups are named `<settings>.bak-nod-YYYYmmdd-HHMMSS`.

```sh
nod hooks remove --host claude --dry-run
nod hooks remove --host claude --yes
nod uninstall --yes
```

`uninstall` removes what Nod wrote (config, service, hooks). Models and libraries you downloaded are yours; delete them yourself if you want the space.

## Troubleshooting

- **`no backends are loaded` / model fails to load**: the lib dir lacks the `libggml-cpu-*.so` plugins or points at the wrong directory. Use the directory that holds `libllama` *and* the `libggml*` files.
- **doctor `models` fails**: a file next to the GGUF is missing; the message names it.
- **doctor `llama_cpp_libs` fails**: pass `--llama-lib-dir` or `nod config set llama_lib_dir DIR`; `NOD_LLAMA_LIB_DIR` also works and wins.
- **Cold call asks while the model warms up**: expected; the model loads on first use and unloads after 900 s idle. `nod hook claude --warm` / `nod hooks install --warm` warms it at session start.
- **Server unreachable**: `nod check` answers per `on_unreachable` (default `ask`) with `source: unreachable`; for a remote, check the URL and `/healthz`.
- **Claude hook not firing**: start a fresh session; check `nod hooks status --json`.
- **Hermes** can show "copied but not enabled": enable the plugin in Hermes and restart it. **Pi** loads its embedded extension in-process. **opencode** loads its plugin at start: restart it after install; in enforce mode ask and block fail the tool call with a message.
- Do not edit host settings to work around a block, and do not skip a checksum failure: report it.

## Exit codes, in short

`0` ok · `1` check says ask · `2` check says block · `3` not installed or unreachable · `4` verification failed (checksum, doctor, hook) · `5` needs a choice (`needs` JSON names the flag) · `64` usage error. Full table: `docs/EXIT_CODES.md`.

## Privacy and limits

Nod itself makes no network requests except to the server you configured; every download in this procedure is yours and visible. Requests to a server include tool input, task and filtered transcript context, over HTTP without built-in TLS or authentication: use loopback or a private network. The model is probabilistic and can be wrong; coverage is the implemented host adapters and matchers.
