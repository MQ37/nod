# Privacy: what is sent and stored

## Requests

Classification requests send the tool name and input, current working directory, task text, and filtered conversation context to the configured server. The local default is `127.0.0.1:11096`. With `--remote` or `connect URL`, these fields cross the network; the service has no authentication or TLS, so use loopback or a private network. The server does not intentionally persist request contents. Keep secrets out of command inputs, especially with a remote server.

Claude transcript context contains only eligible user text and assistant context, never tool results. The parser skips sidechains and metadata, ignores user text beginning with `<`, and clips messages. Tool input and context are bounded before model classification.

Nod itself downloads nothing and contacts no server except the one you configured. The model and llama.cpp files are fetched by you or your agent (see the `nod` skill); those downloads are ordinary HTTPS requests to Hugging Face and GitHub that expose your IP and the requested file, not tool inputs.

## Audit trail

Nod keeps a persistent audit trail of every action it sees: one JSONL file per host in `$XDG_STATE_HOME/nod/audit/` (default `~/.local/state/nod/audit/`), directory mode 0700, files 0600. See [AUDIT.md](AUDIT.md) for the fields, the hash chain and `nod audit verify`.

**It stores commands.** Each line holds the tool, the command (redacted and clipped to 4,000 characters), the sha256 of the original command, the decision and its reason, a hashed session id, the working directory and timing. Redaction removes private keys, `Authorization` and `Bearer` values, well-known token prefixes and values of `*KEY`/`*TOKEN`/`*SECRET`/`*PASSWORD` variables, but it is a safety net and not a guarantee: treat the directory as sensitive. `nod config set audit_commands preview|hash` stores less (160 characters, or only the hash). **Conversation text is never stored.** Calls a host plugin sees but does not judge are recorded as `unchecked`.

`nod decisions` reads all hosts' files (and the legacy RAM-only `decisions.jsonl` from older versions); `nod decisions -v` shows the stored command. Delete the directory to delete the trail; Nod recreates it on the next call.

## Limits

A same-uid process can read the request in memory, replace nod files, edit hooks, or change configuration. This is not a sandbox. Remote HTTP exposes request contents to parties able to observe that network path; a tailnet provides an encrypted transport layer.
