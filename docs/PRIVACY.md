# Privacy: what is sent and stored

## Requests

Classification requests send the tool name and input, current working directory, task text, and filtered conversation context to the configured server. The local default is `127.0.0.1:11096`. With `--remote` or `connect URL`, these fields cross the network; the service has no authentication or TLS, so use loopback or a private network. The server does not intentionally persist request contents. Keep secrets out of command inputs, especially with a remote server.

Claude transcript context contains only eligible user text and assistant context, never tool results. The parser skips sidechains and metadata, ignores user text beginning with `<`, and clips messages. Tool input and context are bounded before model classification.

Nod itself downloads nothing and contacts no server except the one you configured. The model and llama.cpp files are fetched by you or your agent (see the `nod` skill); those downloads are ordinary HTTPS requests to Hugging Face and GitHub that expose your IP and the requested file, not tool inputs.

## Verdict log

`decisions.jsonl` is shared by CLI classifications and Claude hook verdicts. It stores timestamps, host/tool identifiers, decision, source, rule labels and elapsed time as available; it does not store command text or conversation text. File mode is 0600; the runtime directory is mode 0700. The runtime location is `$XDG_RUNTIME_DIR/nod/decisions.jsonl`, or the OS temporary directory under `nod-UID/` when `XDG_RUNTIME_DIR` is unset. Rotation keeps one `.1` file. The fallback may be disk-backed.

Hermes' embedded plugin writes a separate `hermes-shadow.jsonl` audit log under the runtime nod directory. Pi's embedded extension writes `~/.local/share/nod/omp-decisions.jsonl` by default (override with `DECIDER_LOG`, or set it to `off`); each Pi record includes up to 4,000 characters of tool input. These host-side logs are not read by `nod decisions`; protect and remove them according to the host's retention needs.

`nod decisions --verbose` does not recover or display command text: command text is never written to this shared log. Hook diagnostics go to stderr and should not include conversation content.

## Limits

A same-uid process can read the request in memory, replace nod files, edit hooks, or change configuration. This is not a sandbox. Remote HTTP exposes request contents to parties able to observe that network path; a tailnet provides an encrypted transport layer.
