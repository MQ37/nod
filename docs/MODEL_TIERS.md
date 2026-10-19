# Model tiers: what the numbers mean

What to download for each tier, checksums and conversion commands: [MODELS_AND_LIBS.md](MODELS_AND_LIBS.md). This page explains the measurements behind the tier table in the README.

| Tier | Model | Size and memory | Stop rate | SSH/SCP calls stopped | Auto-mode denials caught | Benign calls over-restricted | Model call under load |
|---|---|---|---:|---:|---:|---:|---:|
| `fast` | Decider-4B, Q4_K_M | 2.7 GB | 19.5% | 66.0% | 23 of 41 | 28 | 1.65 s |
| `lite` | Decider-12B (Gemma-4-12B-it base), Q8_0 | 12.7 GB, ≥ 24 GB RAM | 16.5% | 60.0% | 23 of 41 | 21 | 4.7 s |
| `full` | Decider-35B-A3B, Q8_0 | 36.9 GB, ≥ 64 GB RAM and a GPU | 17.0% | 61.3% | 22 of 41 | 8 | 2.3 s |

Measured with the `hybrid` engine on the same sample of 200 real tool calls that reached the model, with the same prompt and pipeline for every tier. One sample, not a guarantee.

## Model-only (the default) on the `lite` model

On 286 synthetic calls with no conversation: it blocked all 195 commands the old block rules target and 48 of the 51 ask-rule commands (the other three were a harmless `$'\x41'`), but it stopped 13 of 40 routine calls that the tiers allow for free, such as `gh auth status` and `rm -rf node_modules`. Expect more prompts than `hybrid` and one model call per tool call. Total decision time differs from model-call time; see [MODELS_AND_LIBS.md](MODELS_AND_LIBS.md).

## Published files and checksums

`fast` is a ready-made GGUF with published checksums. `lite` and `full` are published as safetensors; the skill has the agent convert them with llama.cpp's converter, so there is no published hash for the result (`nod models verify` shows its computed sha256 as `unpinned`). `nod models list --json` has every URL, size, checksum and conversion command.
