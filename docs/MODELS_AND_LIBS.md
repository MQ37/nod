# Models and llama.cpp: what to fetch

Nod downloads nothing. This page is the reference behind the `nod` skill (`nod skill print`): what each model and llama.cpp release is, where it lives, and how to check it. `nod models list --json` prints the same data for agents (URLs, sizes, sha256, conversion commands), and `nod models verify DIR --tier T` checks a directory against it.

Hashes were read from the Hugging Face file list (`lfs.oid`) or from `sha256sum` of the small sidecars, and from the GitHub release asset `digest` for llama.cpp `b11714`. A converted GGUF has no published hash (it depends on the converter build), so `models verify` reports it as `unpinned` with the computed sha256.

## Tiers

| id | source | what to download | GGUF sha256 |
|---|---|---|---|
| `fast` | `hf-gguf` `Mapika/decider-4b-GGUF` | `decider-4b-v2.1-Q4_K_M.gguf` (2708804640 bytes) plus tokenizer and `decider_config.json` (5 files) | `c7083fcfc93f650cd66caeade4a3840ef9eeeeb1e80920907c554f70619d7c56` |
| `lite` | `hf-convert` `Mapika/decider-12b` | one safetensors file (23919549408 bytes) and sidecars; convert to Q8_0 (~12.7 GB), no metadata patch | converted locally, none published |
| `full` | `hf-convert` `Mapika/decider-35b-a3b` | 15 safetensors shards, tokenizer and configs; convert to Q8_0 (~36.9 GB) and apply two metadata patches | converted locally, none published |
| `full-q4` | `hf-convert` (same shards) | same sources; Q4_K_M (~21 GB). This quantization loses most of the measured gain of Q8_0 | converted locally, none published |

Measured on 200 model-judged real calls with the same prompt and pipeline (hybrid engine): fast/lite/full stop rates were 19.5%/16.5%/17.0%; SSH/SCP stop rates 66.0%/60.0%/61.3%; auto-mode denials caught 23/23/22 of 41; benign over-restrictions 28/21/8; model-call latency under load 1.65/4.7/2.3 s; memory 2.7/12.7/36.9 GB. Lite exists because a 128 GB machine cannot hold the 35B alongside another large model. The 35B Q4_K_M stopped 20.0% versus 17.0% for Q8_0, so it loses most of the gain and is not the recommended full-tier artifact. A decision's total latency is different from the model-call time.

## Layout

`fast` uses the `plain` prompt layout; `lite` uses the `chat` layout and needs `decider_chat.json` next to the GGUF (`nod models sidecar lite DIR` writes it with the validated head and tail token ids; Nod checks the ids at load time and fails closed on a mismatch). The 35B config has no `layout` key, which decider-ai reads as `plain`. Next to each GGUF Nod reads `tokenizer.json` and `decider_config.json`; the other files in the repository (`tokenizer_config.json`, `chat_template.jinja`) are published with the model but are not required.

Use one directory per model, for example:

```
~/.local/share/nod/models/fast/    decider-4b-v2.1-Q4_K_M.gguf  tokenizer.json  decider_config.json ...
~/.local/share/nod/models/lite/    decider-12b-Q8_0.gguf  tokenizer.json  decider_config.json  decider_chat.json
```

## Converting (lite, full)

Run the converter from the llama.cpp tree you trust, never a script from the model repository:

```
python3 convert_hf_to_gguf.py <src> --outtype q8_0 --outfile <dst>
```

`lite` needs nothing more. `full` also needs, after conversion, `python3 gguf_set_metadata.py --force <dst> qwen35moe.block_count 40` and the same with `qwen35moe.nextn_predict_layers 0` (the checkpoint has no multi-token-prediction layer). `full-q4` uses `--outtype q4_k_m`. Dependencies: `pip install -r requirements/requirements-convert_hf_to_gguf.txt` from the llama.cpp tree, in a venv.

## llama.cpp shared libraries

Nod loads `libllama.so` (Linux) or `libllama.dylib` (macOS) at run time, with the `libggml*` libraries and backend plugins in the same directory. Tested release: ggml-org/llama.cpp `b11714` (2026-10-17). CPU archives:

| platform | asset | bytes | sha256 |
|---|---|---|---|
| linux x86_64 | `llama-b11714-bin-ubuntu-x64.tar.gz` | 17968637 | `639059a1106e706810486285ec6d1d92c961f3f920ef78bbdd1ee359428679d0` |
| linux arm64 | `llama-b11714-bin-ubuntu-arm64.tar.gz` | 14020865 | `61491da8c08636a61be9a8428d37a7c065a2afd44fa8339ce02a6a78cb68ec32` |
| macOS arm64 | `llama-b11714-bin-macos-arm64.tar.gz` | 12292418 | `063f58b7d8458d89eacb2f28a10a6d6e0efa2a81866690b0f6f8ebac6e3846bb` |

URL: `https://github.com/ggml-org/llama.cpp/releases/download/b11714/<asset>`.

Nod looks for the libraries in this order: `--llama-lib-dir` at install time, `NOD_LLAMA_LIB_DIR`, the `llama_lib_dir` setting, `/opt/llama.cpp/build/bin`, then `$XDG_DATA_HOME/nod/lib`. A directory counts when it contains `libllama`. Release builds ship their compute backends as plugins (`libggml-cpu-*.so`, ...); Nod registers them when it loads the library, so keep them beside `libllama`.

GPU: the Vulkan, ROCm and CUDA archives are on the same release page, or build llama.cpp yourself (`-DGGML_VULKAN=ON`, `-DGGML_HIP=ON`, `-DGGML_CUDA=ON`; Metal is on by default on macOS) and use its `build/bin`. Verify any archive against the digest GitHub shows for it.

The binding is written for the `b11714` / v0.7.0 `llama.h` struct layout (`n_seq_max` is forced to 1 and checked with `llama_n_seq_max`). A newer llama.cpp that changes `llama_model_params` or `llama_context_params` will misread those structs; update the field lists in `internal/decider/native_ffi.go` together with the tested release.
