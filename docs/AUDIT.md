# Audit trail

Every action Nod sees is recorded, kept across reboots, and chained so that tampering shows. This page is the reference: where the files are, what a line holds, what the chain proves and what it does not.

```sh
nod decisions -n 20 -v      # recent verdicts from every host, with the command (-v)
nod audit verify            # check the hash chain of every host's file
nod audit path              # where the files are
```

## Where

One file per host, in a persistent directory (not a tmpfs):

```
~/.local/state/nod/audit/
  check.jsonl    claude.jsonl    hermes.jsonl    opencode.jsonl    omp.jsonl
  <host>.jsonl.1 … .5          # rotated files (10 MiB each, five kept)
```

The directory is `$XDG_STATE_HOME/nod/audit` by default. Override it with `nod config set audit_dir /abs/path` or the `NOD_AUDIT_DIR` environment variable (the environment wins). The directory is mode 0700 and the files are 0600. Hermes and OpenCode receive the setting when their plugin is installed (`nod hooks install --host ... --yes` again after you change it); Pi and omp read `NOD_AUDIT_DIR` from their own environment.

## What is recorded

One JSON object per line:

| Field | Meaning |
|---|---|
| `v`, `time` | format version (1) and the UTC time |
| `host` | `check`, `claude`, `hermes`, `opencode` or `omp` |
| `session` | first 16 hex characters of the sha256 of the host's session id (a stable pseudonym) |
| `mode`, `review` | `shadow` or `enforce`; `decider` or `agent` |
| `tool`, `command` | the tool name and the command (or input description), redacted and clipped |
| `command_sha256` | sha256 of the **original** command, so a known command can be matched to its line |
| `decision`, `source`, `reason`, `category` | what Nod answered and why. `source` is `model`, `agent`, `tier`, `unreachable`, `error` or `not-gated` |
| `model` | in agent review, the model that did the review |
| `ms`, `error`, `cwd` | review time, a short error when the review failed (the answer is then `ask`), working directory |
| `prev` | sha256 of the previous line (the chain) |

Calls a host plugin sees but does not judge are recorded too, with `decision: "unchecked"` and `source: "not-gated"`, so the trail shows everything the agent did and which part Nod did not review. Coverage is whatever the host lets the plugin see: for example Claude Code's hook is installed for `Bash` by default, so other Claude tools do not appear unless you widen the matcher.

## The command text

By default the audit stores the command itself, **redacted**: private keys, `Authorization` headers, `Bearer` tokens, well-known token prefixes (`sk-`, `ghp_`, `AKIA`, ...) and values of variables named `*KEY`, `*TOKEN`, `*SECRET`, `*PASSWORD` become placeholders. The text is clipped to 4,000 characters. Redaction is a safety net, not a guarantee, so keep the directory private.

`nod config set audit_commands preview` clips the text to 160 characters; `hash` stores only `command_sha256`. For the plugins, change the setting and run `nod hooks install --host ... --yes` again.

## The chain

Each line's `prev` is the sha256 of the exact bytes of the line before it. The first line of a host's log has 64 zeros. When a file rotates, the new file's first `prev` is the hash of the last line of the rotated one. `nod audit verify` walks the files oldest to newest and reports the first line where the chain breaks, and exits 4 if any host's chain is broken.

What it proves: lines in the middle were not changed, removed, inserted or reordered. What it does not prove:
- **The newest line**, or lines cut off the end, can be changed or removed without a trace, because nothing follows them yet. If that matters, record the newest line's hash somewhere you trust.
- **A rewrite of the whole file** by the same user. This is tamper-evidence, not a security boundary; the same user can also edit Nod's binary and hooks. For a stronger guarantee make the directory append-only (`chattr +a`) or ship the files to another machine.
- **Retention:** when older rotated files are deleted, `verify` reports that earlier history was rotated away; that is normal, not tampering.

## Writing

Appends from several processes are serialised with a lock and written in one piece. Logging never changes an answer: if the directory is not writable, the verdict still goes out and one warning is printed.
