# Using a GPU

The prebuilt llama.cpp release the skill suggests is the CPU build (the tested release is `b11714`). To run on a GPU, build llama.cpp yourself (Vulkan, ROCm/HIP, CUDA or Metal) and point Nod at the directory that holds `libllama.so` (`libllama.dylib` on macOS):

```sh
nod config set llama_lib_dir /path/to/llama.cpp/build/bin
nod restart
```

Nod looks in this order: `NOD_LLAMA_LIB_DIR`, the `llama_lib_dir` setting, `/opt/llama.cpp/build/bin`, then its own data directory. `nod doctor` reports which one it found. Nod uses whatever backend that build was compiled with, and we have not verified Metal or GPU offload on macOS yet.

Build flags, the prebuilt GPU archives and the library search order at install time: [MODELS_AND_LIBS.md](MODELS_AND_LIBS.md#llamacpp-shared-libraries).
