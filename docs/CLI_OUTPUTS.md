# Machine-readable CLI outputs

All commands accept the global `--json` flag unless their output is a host wire protocol. JSON is written to stdout; human diagnostics/progress go to stderr.

| Command | JSON shape |
|---|---|
| `install --dry-run --json` | One plan object: `os`, `arch`, `ram_bytes`, `gpu`, `tier`, `mode`, `hosts`, `steps`, `rollback`. Each step may contain `description`, `files`, `downloads` (`url`, `size`, `sha256`), and `diffs`. A successful applied install also prints a result object with `installed`, `model`, `hosts`, and `state`. |
| `check --json -- COMMAND...` | `internal/api.ClassifyResponse`: `decision`, `source`, `rules`, `model_ran`, `ms`; optional `tier`, `reason`, `p_allow`, `p_ask`, `p_block`, `p_auth`, `p_forbid`, `p_match`, `p_named`, `category`, `error`. |
| `status --json` | `installed`, `active`, `detail`, `service_name`, `mode`, `addr`, `model_status`, `idle_seconds`. `model_status` is `loaded`, `loading`, `unloaded`, or `unknown`. |
| `doctor --json` | Object with `ok` and `checks`; each check has `name`, `ok`, and optional `detail`. |
| `hooks status --json` | Array of host status objects: `host`, `supported`, `present`, `wired`, optional `observers`, `mode`, `last_verdict`, `config`, `detail`. |
| `hooks install|remove --dry-run --json` | Array of host plans: `host`, `steps` (`path`, `op`, optional `desc`, `diff`, `backup`), optional `notes`. |
| `hook claude` in enforce mode | Claude Code `hookSpecificOutput` with `hookEventName`, `permissionDecision`, and `permissionDecisionReason`. Shadow mode writes no JSON. |
| Exit 5 | `{"needs":[{"flag":"--yes","question":"..."}]}` for confirmation, or a `flag` plus `choices` for a missing selection. |

`models list --json` returns one entry per tier with `id`, `repo`, `source` (`hf-gguf` or `hf-convert`), `gguf`, `gguf_gb`, `min_ram_gb`, `needs_gpu`, `recommended`, `note`, `files` (each with `path`, `size`, `sha256`, `url`) and, for converted tiers, `convert` (the commands). `models verify DIR --tier T --json` returns one row per expected file with `path`, `status` (`ok`, `missing`, `size`, `checksum`, `unpinned`) and `detail`; it exits 4 on any status other than `ok` or `unpinned`. `models sidecar lite DIR` writes `decider_chat.json` and reports `wrote`. Refer to command help for accepted flags.
