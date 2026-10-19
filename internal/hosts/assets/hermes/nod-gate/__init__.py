"""nod-gate: Hermes plugin that asks a Decider model whether the user's own messages approved a tool call.

Shadow mode (default) logs verdicts and blocks nothing. Enforce mode returns {"action": "block"} only for the Decider's "block" verdict; "ask" and
"allow" pass through to Hermes' own approval flow, so this plugin can only make Hermes stricter. Any error, timeout or missing context fails open.

Env: NOD_URL (default http://127.0.0.1:11096), NOD_MODE=shadow|enforce|off, NOD_TIMEOUT (seconds, default 2.5),
     NOD_LOG (default $XDG_RUNTIME_DIR/nod/hermes-shadow.jsonl, RAM on Linux; "off" disables),
     NOD_ASK=approve (experimental, enforce mode, newer Hermes: turn "ask" verdicts into Hermes' own human-approval prompt),
     NOD_PREAPPROVE=off|approved|auto (default off; acts in every mode): answer Hermes' own dangerous-command approval for ONE terminal call when the Decider says the
       user's own messages approved exactly this command (approved) or the command is routine (auto, additionally). One-shot: the approval is withdrawn right after the call.
       It never touches Hermes' hardline floor, never approves when the user forbade it, and any error leaves Hermes' prompt in place. NOD_PREAPPROVE_MIN_AUTH (default 0.8).
     NOD_REVIEW=agent (nod install --model agent): instead of the Decider, the model this Hermes session already runs (ctx.llm, same provider, account and
       usage) answers the review questions that the nod server writes (/v1/review/prompt), and the server turns the answers into a verdict (/v1/review/verdict).
       Enforce mode: allow -> pass, ask -> Hermes' human approval, block -> veto. Unlike the Decider path this one fails SAFE: any error is "ask", never allow.
       NOD_TIMEOUT bounds the model call (default 15 s, at most 20 s, so the hook always answers before Hermes' 30 s hook timeout blocks the call).
"""
from __future__ import annotations

import contextvars
import json
import logging
import os
import threading
import time
import urllib.error

from . import core

logger = logging.getLogger(__name__)

_lock = threading.Lock()
_conversations: dict = {}   # session_id -> [{"role","text"}] of the human's turns (from pre_llm_call)
_parent: dict = {}          # subagent session_id -> parent session_id (a subagent's "user" message is written by the agent, never trusted)
_meta: dict = {}            # session_id -> {"platform", "sender"}
_granted: dict = {}         # tool_call_id -> (hermes session key, pattern key) pre-approved for one call, withdrawn in post_tool_call
_ctx = None                 # the PluginContext from register(); NOD_REVIEW=agent calls the session's own model through ctx.llm
_reviewing = contextvars.ContextVar("nod_gate_reviewing", default=False)   # set while an agent review runs (also seen by its helper thread)
_warned_no_llm = False
HTTP_TIMEOUT = 3.0          # each call to the local nod server


def _mode() -> str:
    m = os.environ.get("NOD_MODE", "shadow").strip().lower()
    return m if m in ("shadow", "enforce", "off") else "shadow"


def _log_path():
    raw = os.environ.get("NOD_LOG")
    if raw == "off":
        return None
    if raw:
        return raw
    base = os.environ.get("XDG_RUNTIME_DIR") or os.path.join(os.path.expanduser("~"), ".cache")
    return os.path.join(base, "nod", "hermes-shadow.jsonl")


def _log(rec: dict) -> None:
    """append-only JSONL, mode 600; never raises (logging must not change behaviour)"""
    try:
        path = _log_path()
        if not path:
            return
        os.makedirs(os.path.dirname(path), mode=0o700, exist_ok=True)
        line = (json.dumps({"ts": round(time.time(), 3), **rec}, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8", "replace")
        fd = os.open(path, os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o600)
        try:
            os.write(fd, line)
        finally:
            os.close(fd)
    except Exception:
        pass


def _root(session_id: str) -> str:
    seen = 0
    while session_id in _parent and seen < 8:
        session_id, seen = _parent[session_id], seen + 1
    return session_id


def _approval_module():
    """Hermes' own approval module (internal API, newer builds); None when unavailable so the plugin degrades to a pure observer."""
    try:
        from tools import approval as A
        for name in ("detect_dangerous_command", "approve_session", "is_approved", "get_current_session_key", "_lock", "_session_approved"):
            if not hasattr(A, name):
                return None
        return A
    except Exception:
        return None


def _revoke(call_id: str) -> None:
    grant = _granted.pop(call_id, None)
    if not grant:
        return
    A = _approval_module()
    if A is not None:
        try:
            with A._lock:
                A._session_approved.get(grant[0], set()).discard(grant[1])
        except Exception:
            pass


def _preapprove_enabled() -> bool:
    return os.environ.get("NOD_PREAPPROVE", "off").strip().lower() in ("approved", "auto")


def _hermes_flag(tool_name, args):
    """(is_dangerous, pattern_key, description) as Hermes' own matcher sees this terminal command, or None when unknown."""
    if tool_name != "terminal" or not isinstance(args, dict) or not args.get("command"):
        return None
    A = _approval_module()
    if A is None:
        return None
    try:
        return A.detect_dangerous_command(str(args["command"]))
    except Exception:
        return None


def _maybe_preapprove(tool_name, args, res, call_id, flagged):
    """One-shot answer to Hermes' own approval gate. Returns (log dict or None, reason string for the log)."""
    mode = os.environ.get("NOD_PREAPPROVE", "off").strip().lower()
    if mode not in ("approved", "auto") or tool_name != "terminal" or not isinstance(args, dict):
        return None, "off"
    A = _approval_module()
    if A is None:
        return None, "skip:hermes_approval_api_unavailable"
    if not flagged or not flagged[0]:
        return None, "skip:hermes_would_not_ask"
    _, pattern_key, description = flagged
    if res["p_forbid"] >= 0.5:
        return None, f"skip:forbidden({res['p_forbid']})"
    min_auth = float(os.environ.get("NOD_PREAPPROVE_MIN_AUTH", "0.8"))
    explicit = res["p_auth"] >= min_auth and (not res["bare_reply"] or res["p_match"] >= min_auth)
    routine = res["verdict"] == 0
    if not (explicit or (mode == "auto" and routine)):
        if res["p_auth"] < min_auth:
            return None, f"skip:not_clearly_approved(auth={res['p_auth']}<{min_auth})"
        return None, f"skip:bare_yes_does_not_match_proposal(match={res['p_match']}<{min_auth})"
    key = A.get_current_session_key()
    if A.is_approved(key, pattern_key):
        return None, "skip:already_approved_in_hermes"
    A.approve_session(key, pattern_key)
    _granted[call_id or f"{key}:{pattern_key}"] = (key, pattern_key)
    return {"preapproved": "explicit" if explicit else "routine", "hermes_flag": description, "hermes_key": str(key)[:12], "pattern": pattern_key}, "granted"


def on_pre_llm_call(session_id: str = "", user_message: str = "", conversation_history=None, platform: str = "", sender_id: str = "",
                    parent_session_id: str = "", **_):
    """remember the human's turns of this session (original user_message, not the injected one)"""
    if _reviewing.get():      # a hook fired by our own review call: its "user" text is nod's prompt, never the human's
        return None
    try:
        if parent_session_id and session_id:          # newer Hermes tells us directly: this is a subagent, its "user" message is the delegated goal
            with _lock:
                _parent[session_id] = parent_session_id
                _conversations.pop(session_id, None)
            return None
        if not session_id or session_id in _parent:
            return None
        conv = core.conversation_from_history(conversation_history, user_message)
        with _lock:
            _conversations[session_id] = conv
            _meta[session_id] = {"platform": platform or "", "sender": bool(sender_id)}
    except Exception as exc:  # fail open
        logger.debug("nod-gate pre_llm_call: %s", exc)
    return None


def on_subagent_start(parent_session_id=None, child_session_id=None, **_):
    if parent_session_id and child_session_id:
        with _lock:
            _parent[child_session_id] = parent_session_id
            _conversations.pop(child_session_id, None)
    return None


def on_pre_tool_call(tool_name: str = "", args=None, session_id: str = "", task_id: str = "", tool_call_id: str = "", **_):
    mode = _mode()
    call_id = str(tool_call_id or "")
    for stale in [c for c in list(_granted) if c != call_id]:   # never leave an earlier one-shot approval behind
        _revoke(stale)
    if mode == "off" or tool_name not in core.GATED:
        return None
    t0 = time.time()
    sid = session_id or task_id or ""
    root = _root(sid)
    base = {"sid": sid[:12], "tool": tool_name, "mode": mode, "sub": root != sid}
    if _reviewing.get():      # never start a second review from inside one; never allow either
        _log({**base, "review": "agent", "result": "ask", "err": "reentrant"})
        return _ask_unavailable("nested review") if mode == "enforce" else None
    try:
        mapped = core.tool_for_decider(tool_name, args)
        if mapped is None:
            return None
        tool, text = mapped
        shown = core.redact(text)[:300]
        with _lock:
            conv = list(_conversations.get(root) or [])
            meta = dict(_meta.get(root) or {})
        if _review_mode() == "agent":
            return _agent_gate(base, mode, tool, text, shown, conv, t0)
        if not conv:
            _log({**base, "cmd": shown, "result": "no_context"})
            return None
        flagged = _hermes_flag(tool_name, args) if _preapprove_enabled() else None
        if core.fast_safe(tool, text) and not (flagged and flagged[0]):   # a command Hermes itself would ask about always goes to the model when pre-approve is on
            _log({**base, "cmd": shown, "result": "t0_safe"})
            return None
        url = os.environ.get("NOD_URL", "http://127.0.0.1:11096")
        timeout = float(os.environ.get("NOD_TIMEOUT", "2.5"))
        context = core.render("", tool, text, os.getcwd(), conv)
        res = core.evaluate(core.decide(url, context, timeout), conv)
        verdict = core.NAME[res["verdict"]]
        _log({**base, "cmd": shown, "result": verdict, "ms": round((time.time() - t0) * 1000), "users": sum(1 for m in conv if m["role"] == "user"),
              "platform": meta.get("platform"), "p_allow": res["allow"], "p_block": res["block"],
              **{k: res[k] for k in ("p_auth", "p_forbid", "p_match", "p_named", "bare_reply")}})
        pre, why_not = None, "off"
        try:
            pre, why_not = _maybe_preapprove(tool_name, args, res, call_id, flagged)
        except Exception as exc:  # a failed pre-approval just leaves Hermes' own prompt in place
            pre, why_not = None, f"skip:error:{type(exc).__name__}"
        if _preapprove_enabled() and tool_name == "terminal":
            _log({**base, "result": "preapproved" if pre else "not_preapproved", "why": why_not, "nod": verdict,
                  "hermes_flag": (flagged[2] if flagged and flagged[0] else None), **(pre or {})})
        if mode == "enforce" and verdict == "ask" and os.environ.get("NOD_ASK", "").strip().lower() == "approve":
            # experimental, newer Hermes only (ignored by older builds): escalate to Hermes' own human-approval prompt. The plugin never approves anything itself.
            return {"action": "approve", "message": "nod-gate: no message of the user clearly approves exactly this; asking you to confirm."}
        if mode == "enforce" and verdict == "block":
            why = "the user told you not to do this" if res["p_forbid"] >= 0.5 else "it looks destructive or out of scope and none of the user's messages approve exactly this"
            return {"action": "block", "message": f"nod-gate: blocked ({why}). If the user really wants this, tell them exactly what would run and on which target, "
                                                  "ask them to confirm in chat, then try again."}
    except Exception as exc:  # Decider path: fail open (unreachable server, timeout, bad answer); agent review: fail safe (ask)
        _log({**base, "result": "error", "err": type(exc).__name__, "ms": round((time.time() - t0) * 1000)})
        if mode == "enforce" and _review_mode() == "agent":
            return _ask_unavailable(f"error: {type(exc).__name__}")
    return None


def _review_mode() -> str:
    return os.environ.get("NOD_REVIEW", "").strip().lower()


def _llm_timeout() -> float:
    try:
        t = float(os.environ.get("NOD_TIMEOUT") or 15)
    except ValueError:
        t = 15.0
    return max(1.0, min(t, 20.0))


def _ask_unavailable(why: str) -> dict:
    return {"action": "approve", "message": f"nod: review unavailable ({why}); asking you."}


class _ReviewError(Exception):
    """a failed step of the agent review; str() is the short reason shown to the human"""


def _call_llm(llm, messages, timeout):
    """ctx.llm.complete on a helper thread (copied context: Hermes' profile scope and our re-entrancy flag) with a hard deadline,
    so a stuck provider call still answers before Hermes' own hook timeout fails the call closed."""
    out = {}

    def run():
        try:
            out["res"] = llm.complete(messages, temperature=0, max_tokens=200, timeout=timeout)
        except BaseException as exc:  # noqa: BLE001 - reported to the caller, never raised in the thread
            out["err"] = exc

    th = threading.Thread(target=contextvars.copy_context().run, args=(run,), name="nod-gate-review", daemon=True)
    th.start()
    th.join(timeout + 1.0)
    if th.is_alive():
        raise _ReviewError("model timeout")
    if "err" in out:
        e = out["err"]
        raise _ReviewError("model timeout" if isinstance(e, TimeoutError) else f"model error: {type(e).__name__}")
    text = getattr(out.get("res"), "text", None)
    if not isinstance(text, str) or not text.strip():
        raise _ReviewError("empty model reply")
    return text


def _review(url, request):
    """the three-step agent review; returns the server's verdict dict or raises _ReviewError"""
    global _warned_no_llm
    try:
        prompt = core.post_json(url, "/v1/review/prompt", request, HTTP_TIMEOUT)
    except Exception as exc:
        raise _ReviewError(f"nod server: {type(exc).__name__}") from None
    system, user, keys = prompt.get("system"), prompt.get("user"), prompt.get("keys")
    if not isinstance(system, str) or not isinstance(user, str) or not system or not user:
        raise _ReviewError("bad prompt from nod server")
    try:
        llm = getattr(_ctx, "llm", None) if _ctx is not None else None
    except Exception:  # the property itself can fail on a partial Hermes install
        llm = None
    if llm is None or not callable(getattr(llm, "complete", None)):
        if not _warned_no_llm:
            _warned_no_llm = True
            logger.warning("nod-gate: NOD_REVIEW=agent needs a Hermes with plugin LLM access (ctx.llm); every gated call will ask")
        raise _ReviewError("this Hermes has no plugin LLM access")
    text = _call_llm(llm, [{"role": "system", "content": system}, {"role": "user", "content": user}], _llm_timeout())
    answers = core.extract_json(text)
    if answers is None:
        raise _ReviewError("unparsable model reply")
    if isinstance(keys, list) and keys:
        answers = {k: answers[k] for k in keys if isinstance(k, str) and k in answers}
    try:
        res = core.post_json(url, "/v1/review/verdict", {"request": request, "answers": answers}, HTTP_TIMEOUT)
    except urllib.error.HTTPError as exc:
        raise _ReviewError("invalid model answers" if exc.code == 400 else f"nod server: HTTP {exc.code}") from None
    except Exception as exc:
        raise _ReviewError(f"nod server: {type(exc).__name__}") from None
    if res.get("decision") not in ("allow", "ask", "block"):
        raise _ReviewError("bad verdict from nod server")
    return res


def _agent_gate(base, mode, tool, text, shown, conv, t0):
    """NOD_REVIEW=agent: the session's own model reviews the call. Only the human's turns (and the assistant line each replies to) and the redacted,
    clipped call go out; never tool output. Fails safe: any error is "ask"."""
    base = {**base, "review": "agent"}
    if core.fast_safe(tool, text):
        _log({**base, "cmd": shown, "result": "t0_safe"})
        return None
    request = {"tool": tool, "input": core.clip_input(core.redact(text)), "task": "", "cwd": core.clip(os.getcwd(), core.LIMITS["cwd"])}
    if conv:
        request["conversation"] = [{"role": m["role"], "text": core.redact(str(m.get("text") or ""))} for m in conv
                                   if isinstance(m, dict) and m.get("role") in ("user", "assistant")]
    url = os.environ.get("NOD_URL", "http://127.0.0.1:11096")
    token = _reviewing.set(True)
    try:
        res = _review(url, request)
    except Exception as exc:
        why = str(exc) if isinstance(exc, _ReviewError) else f"error: {type(exc).__name__}"
        _log({**base, "cmd": shown, "result": "ask", "err": why[:120], "ms": round((time.time() - t0) * 1000)})
        return _ask_unavailable(why[:120]) if mode == "enforce" else None
    finally:
        _reviewing.reset(token)
    verdict, reason = res["decision"], str(res.get("reason") or "")[:300]
    _log({**base, "cmd": shown, "result": verdict, "reason": reason, "ms": round((time.time() - t0) * 1000),
          "users": sum(1 for m in conv if m.get("role") == "user"), "category": str(res.get("category") or "")[:120]})
    if mode != "enforce" or verdict == "allow":
        return None
    if verdict == "block":
        return {"action": "block", "message": f"nod: blocked ({reason or 'the review found no approval for this'}). If the user really wants this, "
                                              "tell them exactly what would run and on which target, ask them to confirm in chat, then try again."}
    return {"action": "approve", "message": f"nod: {reason or 'no message of the user clearly approves exactly this'}; asking you to confirm."}


def on_post_tool_call(tool_call_id: str = "", **_):
    """withdraw the one-shot approval as soon as the call is over"""
    if tool_call_id:
        _revoke(str(tool_call_id))
    return None


def on_pre_approval_request(command: str = "", description: str = "", session_key: str = "", **_):
    _log({"kind": "hermes_approval_request", "sid": str(session_key)[:12], "desc": description, "cmd": core.redact(command or "")[:300]})
    return None


def on_post_approval_response(command: str = "", description: str = "", session_key: str = "", choice: str = "", **_):
    _log({"kind": "hermes_approval_response", "sid": str(session_key)[:12], "desc": description, "choice": choice, "cmd": core.redact(command or "")[:300]})
    return None


def register(ctx) -> None:
    global _ctx
    _ctx = ctx
    ctx.register_hook("pre_llm_call", on_pre_llm_call)
    ctx.register_hook("pre_tool_call", on_pre_tool_call)
    ctx.register_hook("post_tool_call", on_post_tool_call)
    ctx.register_hook("subagent_start", on_subagent_start)
    ctx.register_hook("pre_approval_request", on_pre_approval_request)
    ctx.register_hook("post_approval_response", on_post_approval_response)
