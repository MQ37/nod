"""nod-gate audit trail: one JSONL line per tool call in <audit dir>/hermes.jsonl, the same format, hash chain, rotation and redaction as
nod's Go internal/decisionlog (so `nod audit verify` checks this file). Stdlib only; write() never raises.

Audit dir: $NOD_AUDIT_DIR if absolute, else $XDG_STATE_HOME/nod/audit if absolute, else ~/.local/state/nod/audit (0700; files 0600).
Command text: $NOD_AUDIT_COMMANDS = full (default: redacted, 4000 characters) | preview (160) | hash (only command_sha256).
"""
import datetime
import fcntl
import hashlib
import json
import logging
import os
import re
import threading

logger = logging.getLogger(__name__)

HOST = "hermes"
FORMAT_VERSION = 1
MAX_BYTES = 10 * 1024 * 1024
KEEP = 5
GENESIS = "0" * 64
MAX_COMMAND, PREVIEW_LEN = 4000, 160
FIELDS = ("v", "time", "host", "session", "mode", "review", "tool", "command", "command_sha256", "decision", "source", "model", "rules",
          "answer", "category", "ms", "error", "cwd", "reason", "prev")   # Go's decisionlog.Entry order
ALWAYS = ("time", "tool", "decision")                                     # no omitempty in Go

# Ported from internal/decisionlog/redact.go. Go's RE2 \s is [\t\n\f\r ] and \b is ASCII, so both are spelled out / forced ASCII here;
# Go's (?i) folds k with U+212A and s with U+017F, nothing else in these words.
_WS, _NWS = r"[\t\n\f\r ]", r"[^\t\n\f\r ]"


def _ci(word):
    extra = {"k": "K", "s": "ſ"}
    return "".join(f"[{c.upper()}{c.lower()}{extra.get(c.lower(), '')}]" if c.isalpha() else re.escape(c) for c in word)


SECRET_PATTERNS = [
    (re.compile(r"-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\Z)", re.S), "<private-key>"),
    (re.compile(f"({_ci('Authorization')}{_WS}*:{_WS}*)(?:{_ci('Bearer')}|{_ci('Basic')}|{_ci('token')})?{_WS}*[^\\t\\n\\f\\r '\"]+"), r"\g<1><redacted>"),
    (re.compile(r"\bBearer" + _WS + r"+[A-Za-z0-9._~+/=-]{8,}", re.A), "Bearer <redacted>"),
    (re.compile(r"\b(?:sk-|ghp_|gho_|ghs_|ghu_|github_pat_|glpat-|xox[a-z]-|apify_api_|AKIA|ASIA|eyJ)[A-Za-z0-9_\-.]{12,}", re.A), "<tok>"),
    (re.compile(r"\b([A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|PASS)[A-Z0-9_]*=)(?:\"[^\"]*\"|'[^']*'|" + _NWS + r"+)", re.A), r"\g<1><v>"),
]


def redact(s):
    """Same output as Go's decisionlog.Redact."""
    for p, rep in SECRET_PATTERNS:
        s = p.sub(rep, s)
    return s


def sha256_hex(s):
    return hashlib.sha256(s.encode("utf-8", "replace")).hexdigest()


def hash_session(sid):
    return sha256_hex(sid)[:16] if sid else ""


def clip(s, n):
    return s if len(s) <= n else s[:n] + "…"


def level():
    return (os.environ.get("NOD_AUDIT_COMMANDS") or "").strip().lower()


def command_fields(cmd, lvl=None, limit=None):
    """(sha256 of the original, stored text) like Go's decisionlog.CommandFields; limit clips further (descriptions of ungated calls)."""
    cmd = str(cmd or "")
    if not cmd:
        return "", ""
    lvl = level() if lvl is None else lvl.strip().lower()
    sha = sha256_hex(cmd)
    if lvl == "hash":
        return sha, ""
    n = PREVIEW_LEN if lvl == "preview" else MAX_COMMAND
    return sha, clip(redact(cmd), min(n, limit) if limit else n)


def audit_dir():
    d = os.environ.get("NOD_AUDIT_DIR") or ""
    if d and os.path.isabs(d):
        return d
    x = os.environ.get("XDG_STATE_HOME") or ""
    if x and os.path.isabs(x):
        return os.path.join(x, "nod", "audit")
    return os.path.join(os.path.expanduser("~"), ".local", "state", "nod", "audit")


def audit_file():
    return os.path.join(audit_dir(), HOST + ".jsonl")


def now():
    t = datetime.datetime.now(datetime.timezone.utc)
    return t.strftime("%Y-%m-%dT%H:%M:%S.") + f"{t.microsecond // 1000:03d}Z"


def _last_line(fd):
    size = os.fstat(fd).st_size
    if size == 0:
        return b""
    start = max(0, size - (1 << 20))
    buf = os.pread(fd, size - start, start).rstrip(b"\n")
    i = buf.rfind(b"\n")
    return buf[i + 1:] if i >= 0 else buf


def _rotate(path, keep):
    try:
        os.remove(f"{path}.{keep}")
    except OSError:
        pass
    for i in range(keep - 1, 0, -1):
        try:
            os.rename(f"{path}.{i}", f"{path}.{i + 1}")
        except OSError:
            pass
    try:
        os.rename(path, path + ".1")
    except OSError:
        pass


def _open(path):
    fd = os.open(path, os.O_RDWR | os.O_CREAT | os.O_APPEND, 0o600)
    try:
        os.fchmod(fd, 0o600)
    except OSError:
        os.close(fd)
        raise
    return fd


def append(entry, path=None, max_bytes=MAX_BYTES, keep=KEEP):
    """Write one chained line; returns the bytes written (without the newline). Raises on I/O errors (see write())."""
    path = path or audit_file()
    e = {k: v for k, v in entry.items() if k in FIELDS}
    e["v"] = FORMAT_VERSION
    e.setdefault("time", now())
    os.makedirs(os.path.dirname(path), mode=0o700, exist_ok=True)
    lock = os.open(path + ".lock", os.O_CREAT | os.O_RDWR, 0o600)
    try:
        fcntl.flock(lock, fcntl.LOCK_EX)
        fd = _open(path)
        try:
            last = _last_line(fd)
            prev = GENESIS
            if last:
                prev = hashlib.sha256(last).hexdigest()
            else:   # a new file: continue the chain of a rotated predecessor, if any
                try:
                    g = os.open(path + ".1", os.O_RDONLY)
                except OSError:
                    g = None
                if g is not None:
                    try:
                        prior = _last_line(g)
                    finally:
                        os.close(g)
                    if prior:
                        prev = hashlib.sha256(prior).hexdigest()
            e["prev"] = prev
            out = {}
            for k in FIELDS:
                v = e.get(k)
                if k in ALWAYS:
                    out[k] = "" if v is None else v
                elif v not in (None, "", 0, [], False):
                    out[k] = v
            line = json.dumps(out, ensure_ascii=False, separators=(",", ":")).encode("utf-8", "replace")
            if max_bytes and max_bytes > 0:
                size = os.fstat(fd).st_size
                if size > 0 and size + len(line) + 1 > max_bytes:
                    os.close(fd)
                    fd = None
                    _rotate(path, keep if keep and keep > 0 else KEEP)
                    fd = _open(path)   # prev stays the hash of the last line of the file just rotated away
            data = line + b"\n"
            while data:
                data = data[os.write(fd, data):]
            return line
        finally:
            if fd is not None:
                os.close(fd)
    finally:
        try:
            fcntl.flock(lock, fcntl.LOCK_UN)
        finally:
            os.close(lock)


_warned = False
_warn_lock = threading.Lock()


def write(**fields):
    """Best-effort append for the hooks: never raises, never blocks on anything but the local file lock; warns once when it cannot write."""
    global _warned
    try:
        append(fields)
        return True
    except Exception as exc:  # noqa: BLE001 - the audit trail must never change a hook answer
        with _warn_lock:
            if not _warned:
                _warned = True
                try:
                    logger.warning("nod-gate: cannot write the audit trail %s: %s", audit_file(), exc)
                except Exception:
                    pass
        return False
