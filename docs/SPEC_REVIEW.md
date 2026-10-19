# Specification decisions reflected in the CLI

This file records resolutions that affect commands and documentation. The implementation in `internal/cli`, `internal/config`, `internal/install`, `internal/hosts`, and `internal/server` is authoritative.

- `check` exits 0/1/2 for allow/ask/block and 3 for a classification error. Usage errors exit 64.
- Claude shadow hooks write no answer to stdout; enforce mode writes Claude's PreToolUse JSON response. Hook commands always exit 0 and failures use the configured policy (default ask).
- Verdicts are written to `decisions.jsonl` by CLI classifications and Claude hooks. Hermes and Pi use host-side audit files with their own formats; Pi records truncated tool input.
- `install --host none` selects server-only installation. Install supports `--service-name`, `--mode`, `--addr`, `--on-unreachable`, `--remote`, `--dry-run` and `--yes`.
- Hook install/remove plans are shown before apply; `--dry-run` stops without writing, and `--yes` applies. Noninteractive commands without required confirmation exit 5.
- Backups use `<path>.bak-nod-<timestamp>` (for Claude settings: `settings.json.bak-nod-YYYYmmdd-HHMMSS`).
- Both tiers default to lazy unload at 900 idle seconds; `idle_unload_secs=0` keeps the model resident. A cold classification waits up to `warm_wait_secs=5`; `/healthz` reports `model_status` and `idle_seconds`, and `/healthz?warm=1` requests background loading.
- Full install tier is Decider-35B-A3B Q8_0 (36.9 GB); fast is 4B Q4_K_M (2.7 GB). Performance claims are limited to benchmark data documented in README and `docs/MODELS_AND_LIBS.md`.
- The server has no built-in token or TLS. Bind it to loopback or a private network.
