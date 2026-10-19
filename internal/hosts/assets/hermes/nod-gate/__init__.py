"""nod-gate: Hermes plugin that asks a Decider model whether the user's own messages approved a tool call.

Shadow mode (default) records verdicts in the audit trail and blocks nothing. Enforce mode returns {"action": "block"} only for the Decider's "block" verdict; "ask" and
"allow" pass through to Hermes' own approval flow, so this plugin can only make Hermes stricter. Any error, timeout or missing context fails open.

Env: NOD_URL (default http://127.0.0.1:11096), NOD_MODE=shadow|enforce|off, NOD_TIMEOUT (seconds, default 2.5),
     NOD_AUDIT_DIR / NOD_AUDIT_COMMANDS=full|preview|hash: the persistent audit trail <dir>/hermes.jsonl (see audit.py), one hash-chained line for
       every tool call this hook sees, gated or not (decision "unchecked" for calls nod does not judge). It never changes an answer.
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

from . import audit, core

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


def _cwd() -> str:
    try:
        return os.getcwd()
    except Exception:
        return ""


def _ms(t0) -> int:
    return round((time.time() - t0) * 1000)


def _describe(args) -> str:
    """short description of an ungated call's input: a file path, a command, else the args as JSON"""
    a = args if isinstance(args, dict) else {}
    for k in ("path", "file_path", "command"):
        if isinstance(a.get(k), str) and a[k]:
            return a[k]
    try:
        return json.dumps(args, ensure_ascii=False, sort_keys=True, default=str)
    except Exception:
        return str(args)


def _audit(rec: dict, text, ret=None, limit=None, **fields):
    """one audit line (see audit.py), written after the verdict and before the hook returns; returns ret unchanged and never raises"""
    try:
        sha, cmd = audit.command_fields(text, limit=limit)
        audit.write(**{"answer": ret.get("action", "") if isinstance(ret, dict) else "none", **rec, "command": cmd, "command_sha256": sha,
                       "cwd": _cwd(), **fields})
    except Exception:
        pass
    return ret


def on_pre_tool_call(tool_name: str = "", args=None, session_id: str = "", task_id: str = "", tool_call_id: str = "", **_):
    mode = _mode()
    call_id = str(tool_call_id or "")
    for stale in [c for c in list(_granted) if c != call_id]:   # never leave an earlier one-shot approval behind
        _revoke(stale)
    if mode == "off":
        return None
    t0 = time.time()
    sid = session_id or task_id or ""
    rec = {"host": audit.HOST, "session": audit.hash_session(str(sid)), "mode": mode, "tool": str(tool_name or "")}
    if tool_name not in core.GATED:   # the trail shows what nod did not judge, too
        return _audit(rec, _describe(args), limit=500, decision="unchecked", source="not-gated")
    review = "agent" if _review_mode() == "agent" else "decider"
    root = _root(sid)
    text = _describe(args)
    if _reviewing.get():      # never start a second review from inside one; never allow either
        return _audit(rec, text, _ask_unavailable("nested review") if mode == "enforce" else None,
                      review=review, decision="ask", source="error", error="nested review")
    try:
        mapped = core.tool_for_decider(tool_name, args)
        if mapped is None:
            return _audit(rec, text, limit=500, decision="unchecked", source="not-gated")
        tool, text = mapped
        with _lock:
            conv = list(_conversations.get(root) or [])
        if review == "agent":
            return _agent_gate(rec, mode, tool, text, conv, t0)
        if not conv:
            return _audit(rec, text, decision="unchecked", source="no-context")
        flagged = _hermes_flag(tool_name, args) if _preapprove_enabled() else None
        if core.fast_safe(tool, text) and not (flagged and flagged[0]):   # a command Hermes itself would ask about always goes to the model when pre-approve is on
            return _audit(rec, text, decision="unchecked", source="read-only")
        url = os.environ.get("NOD_URL", "http://127.0.0.1:11096")
        timeout = float(os.environ.get("NOD_TIMEOUT", "2.5"))
        context = core.render("", tool, text, os.getcwd(), conv)
        res = core.evaluate(core.decide(url, context, timeout), conv)
        verdict = core.NAME[res["verdict"]]
        pre = None
        try:
            pre, _why = _maybe_preapprove(tool_name, args, res, call_id, flagged)
        except Exception:  # a failed pre-approval just leaves Hermes' own prompt in place
            pre = None
        ret = None
        if mode == "enforce" and verdict == "ask" and os.environ.get("NOD_ASK", "").strip().lower() == "approve":
            # experimental, newer Hermes only (ignored by older builds): escalate to Hermes' own human-approval prompt. The plugin never approves anything itself.
            ret = {"action": "approve", "message": "nod-gate: no message of the user clearly approves exactly this; asking you to confirm."}
        elif mode == "enforce" and verdict == "block":
            why = "the user told you not to do this" if res["p_forbid"] >= 0.5 else "it looks destructive or out of scope and none of the user's messages approve exactly this"
            ret = {"action": "block", "message": f"nod-gate: blocked ({why}). If the user really wants this, tell them exactly what would run and on which target, "
                                                 "ask them to confirm in chat, then try again."}
        extra = {"answer": "preapproved:" + pre["preapproved"]} if pre else {}
        reason = " ".join(f"{k}={res[k]}" for k in ("allow", "block", "p_auth", "p_forbid", "p_match", "p_named"))
        return _audit(rec, text, ret, review="decider", decision=verdict, source="model", category=str(res.get("category") or "")[:120],
                      reason=reason[:300], ms=_ms(t0), **extra)
    except Exception as exc:  # Decider path: fail open (unreachable server, timeout, bad answer); agent review: fail safe (ask)
        ret = _ask_unavailable(f"error: {type(exc).__name__}") if mode == "enforce" and review == "agent" else None
        return _audit(rec, text, ret, review=review, decision="ask", source="error", error=type(exc).__name__, ms=_ms(t0))


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
    return text, _model_id(out.get("res"))


def _model_id(res) -> str:
    """provider/model of a ctx.llm result when it exposes them (for the audit trail), else an empty string"""
    try:
        return "/".join(x for x in (getattr(res, "provider", None), getattr(res, "model", None)) if isinstance(x, str) and x)[:120]
    except Exception:
        return ""


def _review(url, request, info=None):
    """the three-step agent review; returns the server's verdict dict or raises _ReviewError. info["model"] gets the reviewing model's id."""
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
    text, model = _call_llm(llm, [{"role": "system", "content": system}, {"role": "user", "content": user}], _llm_timeout())
    if info is not None:
        info["model"] = model
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


def _agent_gate(rec, mode, tool, text, conv, t0):
    """NOD_REVIEW=agent: the session's own model reviews the call. Only the human's turns (and the assistant line each replies to) and the redacted,
    clipped call go out; never tool output. Fails safe: any error is "ask"."""
    if core.fast_safe(tool, text):
        return _audit(rec, text, decision="unchecked", source="read-only")
    request = {"tool": tool, "input": core.clip_input(core.redact(text)), "task": "", "cwd": core.clip(os.getcwd(), core.LIMITS["cwd"])}
    if conv:
        request["conversation"] = [{"role": m["role"], "text": core.redact(str(m.get("text") or ""))} for m in conv
                                   if isinstance(m, dict) and m.get("role") in ("user", "assistant")]
    url = os.environ.get("NOD_URL", "http://127.0.0.1:11096")
    info = {}
    token = _reviewing.set(True)
    try:
        res = _review(url, request, info)
    except Exception as exc:
        why = str(exc) if isinstance(exc, _ReviewError) else f"error: {type(exc).__name__}"
        return _audit(rec, text, _ask_unavailable(why[:120]) if mode == "enforce" else None,
                      review="agent", decision="ask", source="error", error=why[:120], model=info.get("model", ""), ms=_ms(t0))
    finally:
        _reviewing.reset(token)
    verdict, reason = res["decision"], str(res.get("reason") or "")[:300]
    ret = None
    if mode == "enforce" and verdict == "block":
        ret = {"action": "block", "message": f"nod: blocked ({reason or 'the review found no approval for this'}). If the user really wants this, "
                                             "tell them exactly what would run and on which target, ask them to confirm in chat, then try again."}
    elif mode == "enforce" and verdict == "ask":
        ret = {"action": "approve", "message": f"nod: {reason or 'no message of the user clearly approves exactly this'}; asking you to confirm."}
    return _audit(rec, text, ret, review="agent", decision=verdict, source=str(res.get("source") or "agent")[:40], model=info.get("model", ""),
                  category=str(res.get("category") or "")[:120], reason=reason, ms=_ms(t0))


def on_post_tool_call(tool_call_id: str = "", **_):
    """withdraw the one-shot approval as soon as the call is over"""
    if tool_call_id:
        _revoke(str(tool_call_id))
    return None


def on_post_approval_response(command: str = "", description: str = "", session_key: str = "", choice: str = "", **_):
    """Hermes' own approval prompt was answered by the human: one audit line (decision ask, answer = the human's choice)"""
    mode = _mode()
    if mode == "off":
        return None
    _audit({"host": audit.HOST, "session": audit.hash_session(str(session_key or "")), "mode": mode, "tool": "terminal"}, str(command or ""),
           decision="ask", source="hermes-approval", answer=str(choice or "")[:40], reason=str(description or "")[:300])
    return None


def register(ctx) -> None:
    global _ctx
    _ctx = ctx
    ctx.register_hook("pre_llm_call", on_pre_llm_call)
    ctx.register_hook("pre_tool_call", on_pre_tool_call)
    ctx.register_hook("post_tool_call", on_post_tool_call)
    ctx.register_hook("subagent_start", on_subagent_start)
    ctx.register_hook("post_approval_response", on_post_approval_response)
