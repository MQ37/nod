# Configuration

`nod config get [KEY]`, `config set KEY VALUE`, and `config path` read or update the JSON configuration. `--config PATH` overrides the config file. Unknown keys are rejected.

| Key | Default | Meaning |
|---|---|---|
| `addr` | `127.0.0.1:11096` | Local server listen address (`host:port`). |
| `remote` | empty | Remote server URL; when set, hooks and `check` ask it first instead of the local `addr`. Set with `connect URL` or install `--remote URL`. |
| `remote_fallback` | `local` | With a remote set: `local` falls back to the local daemon when the remote cannot answer; `none` goes straight to `on_unreachable`. |
| `local_power` | `plugged` | When the local daemon may load and run its model: `plugged` = not while on battery, `always`, or `never`. `nod serve --power` overrides it. |
| `model` | empty until install | Model size label: `fast`, `lite` or `full` (lite adds the chat-layout file check), or `agent` (no local model: the host plugin asks the agent's own model; see README). |
| `llama_lib_dir` | empty | Directory with your own llama.cpp libraries (for a GPU build); searched after `NOD_LLAMA_LIB_DIR`. |
| `engine` | `model` | `model`: the model judges every call. `hybrid`: a shell lexer and rule set in front of the model (routine commands allowed without a model call; see README). Also `nod serve --engine`. |
| `model_path` | empty until install | Your Decider GGUF; `install --model-path` records it. Nod never downloads it. |
| `service_name` | `nod` | User service identity; validated as lowercase letters, digits and hyphens. `NOD_SERVICE_NAME` overrides it; commands that support it accept `--service-name`. |
| `mode` | `shadow` | `shadow` logs decisions without changing host permission flow; `enforce` applies adapter behavior. |
| `on_unreachable` | `ask` | Fallback decision for a server/classifier error: `ask`, `block`, or `allow`. |
| `timeout_secs` | `10` | HTTP client timeout for check/health calls. Claude's installed command has a 15-second host timeout; Hermes plugin uses its own 2.5-second timeout. |
| `idle_unload_secs` | `900` | Unload model, context and batch after this many idle seconds. `0` keeps them resident. |
| `warm_wait_secs` | `5` | Maximum cold-load wait for a classification. When still loading, the call fails safe to ask. |
| `shadow_until` | empty | Optional stored timestamp; no automatic mode switch is implemented. |
| `log_commands` | `false` | Compatibility setting; command text is not written to the verdict log. |

Paths derive from XDG variables when absolute: config `$XDG_CONFIG_HOME/nod/config.json` (default `~/.config/nod/config.json`); state `$XDG_STATE_HOME/nod/state.json` (default `~/.local/state/nod/state.json`); model/library data `$XDG_DATA_HOME/nod/` (default `~/.local/share/nod/`). The runtime log is `decisions.jsonl` in `$XDG_RUNTIME_DIR/nod/`; without that variable it is under the OS temporary directory in `nod-UID/`. Files are mode 0600 and the runtime directory mode 0700.

The active verdict log path is not configurable. `claude.matcher`, `theta_allow`, `theta_block`, `hosts`, `shadow_days`, and `log.*` are not config keys in this implementation. Claude's matcher is selected with `hooks install --matcher TOOL`; default `Bash`.
