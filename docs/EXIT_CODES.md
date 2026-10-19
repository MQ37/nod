# Exit codes

| Code | Meaning |
|---:|---|
| 0 | Success; `check` decision is allow. |
| 1 | `check` decision is ask (also when the server was unreachable and `on_unreachable` is `ask`). Never a usage error. |
| 2 | `check` decision is block. |
| 3 | `check` failed before classifying (such as unreadable configuration); for other commands, not installed or unreachable. |
| 4 | Verification failed, including failed doctor checks. |
| 5 | A noninteractive command needs an explicit choice or confirmation (for example `install` without `--model-path` or `--llama-lib-dir`; the `needs` JSON names the flag). |
| 64 | Usage error: unknown command, bad or missing flag or argument (for example `check` with no command). |

For `check`, the mapping is allow=0, ask=1, block=2, error=3. Usage errors use 64, so exit 1 always means a real `ask` verdict. An unreachable service is not an error: `check` returns the `on_unreachable` decision (default ask, exit 1), sets JSON `source` to `unreachable` and `error` to `server unreachable`, and prints a one-line note on stderr. Scripts can tell it apart from a model verdict by `source`.

Exit 5 with `--json` prints `{"needs":[{"flag":"--yes","question":"execute the complete installation plan"}]}` for confirmation, or a choice-specific object such as `{"needs":[{"flag":"--model","choices":["full","lite","fast"]}]}`. Ask the operator and rerun with the named flag.

`hook <host>` always exits 0. Claude enforce mode emits the host answer JSON; shadow mode emits nothing. Internal hook failures fall back to the configured policy (default ask) and diagnostics go to stderr.
