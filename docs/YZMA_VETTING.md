# yzma vetting (2026-10-17)

`github.com/hybridgroup/yzma` was inspected before any module was added. It is not a dependency of this tree. The Decider readout is a small binding in `internal/decider` that calls llama.cpp the way `shim/shim.c` does.

## What was checked

- Repository: https://github.com/hybridgroup/yzma (public, Apache-2.0; portions of gollama.cpp under MIT). Default branch `main`, commit `7bba9790` (2026-10-15, release 1.30.0). Active: releases through v1.30.0, last push 2026-10-17.
- `go.mod` on `main` / v1.30.0: `go 1.26.0`. Direct requirements: `github.com/ardanlabs/jinja`, `github.com/ebitengine/purego`, `github.com/hashicorp/go-getter`, `github.com/jupiterrider/ffi`, `github.com/urfave/cli/v2`, `golang.org/x/sys`.
- `go-getter` pulls the AWS SDK, Google Cloud storage, gRPC and related modules. That graph is only reached from `pkg/download`.
- `pkg/download` fetches llama.cpp builds and models. This tree does not import it.
- `pkg/llama` has no `init` that downloads or executes anything. Libraries are loaded only when `llama.Load` is called. `exp/decide` is the same: it loads a GGUF through `pkg/llama` when the caller opens a model.
- Last Go 1.25 tag is v1.13.0 (2026-04-21, llama.cpp b8865+). It has no `exp/decide`.

## Why `exp/decide` is not the readout

`exp/decide` (v1.30.0 `doc.go`) loads a Decider GGUF as one question:

```
Context:
<state>

Question: ...
Options:
(A) ...
Answer: (
```

One decode reads option-letter logits at the last token. A score with `isolated_levels` is several yes/no passes. That is not decider-ai 1.8.0.

decider-ai 1.8.0 (`engine_gguf.py`, `prompt.py`, `temperature.py`) puts every question of one schema in a single row, requests logits at each `Answer k: (` slot, clears the llama.cpp memory before the decode, and keeps `n_seq_max = 1`. Temperatures come from `decider_config.json` (`temperature`, `temperature_by_type`, `temperature_by_options`). nod's `Guard.decide_json` / `score` use that path. yzma's reader cannot serve it.

v1.30.0 also cannot be imported here: `build.sh` uses `golang:1.25`, and a module that says `go 1.26.0` makes that toolchain try to download a newer Go. Tests run with the network off.

## What this tree links instead

`internal/decider` binds only the C functions `shim/shim.c` uses, through `github.com/jupiterrider/ffi` v0.8.0 (MIT, `go 1.18`, requires `github.com/ebitengine/purego` v0.10.0):

`llama_backend_init`, `llama_model_default_params`, `llama_model_load_from_file`, `llama_context_default_params`, `llama_init_from_model`, `llama_batch_init`, `llama_batch_free`, `llama_get_memory`, `llama_memory_clear`, `llama_decode`, `llama_get_logits_ith`, `llama_model_get_vocab`, `llama_vocab_n_tokens`, `llama_n_seq_max`, `llama_free`, `llama_model_free`, plus `ggml_backend_load_all_from_path`.

Struct layouts match llama.cpp v0.7.0 and `b11714` (`llama_model_params`, `llama_context_params` and `llama_batch` are the same in those two headers). ffi's Linux `init` dlopens `libffi.so.8`, which the `golang:1.25` image provides. It does not download. The binding does not import `yzma/pkg/download` and does not call yzma's self-downloader.

`jupiterrider/ffi` is the library yzma uses to pass the llama.cpp parameter structs by value. purego alone cannot pass those structs on the amd64 SysV ABI.
