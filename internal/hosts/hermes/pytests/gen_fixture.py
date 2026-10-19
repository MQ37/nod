"""Writes the Go/Python interop fixtures from the plugin's own audit.py (no Hermes, no network):

  audit-hermes.jsonl.1.fixture + audit-hermes.jsonl.fixture  a 12-line audit trail with one rotation boundary (6 + 6 lines)
  audit-redact-cases.json                                    redaction / command-field results for the Go side to compare

Run (podman, read-only source, output dir mounted writable):
  podman run --rm --network=none --read-only --tmpfs /tmp -v "$PWD":/src:ro -v "$PWD/internal/hosts/hermes/testdata":/out -w /src \
    docker.io/library/python:3.12 python -I internal/hosts/hermes/pytests/gen_fixture.py /out
internal/hosts/hermes/fixture_test.go verifies the files with decisionlog.Verify / Redact / CommandFields.
"""
import importlib.util
import json
import os
import pathlib
import shutil
import sys
import tempfile

PLUGIN_DIR = pathlib.Path(__file__).resolve().parents[2] / "assets" / "hermes" / "nod-gate"


def load_audit():
    spec = importlib.util.spec_from_file_location("nod_gate_audit", PLUGIN_DIR / "audit.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


audit = load_audit()

REDACT_INPUTS = [
    "curl -H 'Authorization: Bearer abcdef123456789' https://x -d API_KEY=hunter2 ghp_ABCDEFGHIJKLMNOPQRST",
    "-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----",
    "cat <<EOF\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n",
    "-----BEGIN EC PRIVATE KEY-----\nabc\n",
    "authorization:basic dXNlcjpwYXNz and AUTHORIZATION :  token  xyz'rest",
    "Authorization: \"quoted\"",
    "éBearer abcdefgh123 and xBearer abcdefgh123 and Bearer short and Bearer\tabcdefghij",
    "export OPENAI_API_KEY=\"sk-proj-abc def\" GH_TOKEN='x y' DB_PASSWORD=p@ss! mykey=lower PASSWD=1 PASS=",
    "sk-abcdefghijklmnop AKIAABCDEFGHIJKLMNOP eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0 xoxb-123456789012-abc apify_api_ABCDEFGHIJKL ünïcødé_sk-abcdefghijklmnop",
    "TOKEN=a\u000bb SECRET=c d KEY=e f",
    "Authorization:\u000bBearer xyz",
    "plain command with no secrets ✓",
]
FIELD_INPUTS = [
    ("rm -rf build", "full"),
    ("rm -rf build", "preview"),
    ("rm -rf build", "hash"),
    ("echo " + "ä" * 5000 + " API_KEY=hunter2", ""),
    ("echo " + "ä" * 5000, "preview"),
    ("x" * 4000, "full"),
    ("x" * 4001, "full"),
    ("x" * 161, "PREVIEW"),
    ("", "full"),
]


def write_fixture(out):
    with tempfile.TemporaryDirectory() as d:
        p = os.path.join(d, "hermes.jsonl")
        sess = audit.hash_session("hermes-session-1")
        rows = [
            dict(mode="enforce", review="decider", tool="terminal", text="rm -rf build", decision="block", source="model", answer="block",
                 category="deleting or overwriting data outside the project", reason="allow=0.01 block=0.9", ms=412),
            dict(mode="enforce", tool="web_search", text=json.dumps({"query": "nod ünïcødé ✓ \"quotes\" <tags> & more"}, ensure_ascii=False),
                 decision="unchecked", source="not-gated", answer="none"),
            dict(mode="enforce", review="decider", tool="read_file", text="/home/u/project/README.md", decision="unchecked", source="read-only", answer="none"),
            dict(mode="enforce", review="agent", tool="terminal", text="curl -H 'Authorization: Bearer abcdef123456789' https://example.com",
                 decision="ask", source="agent", model="openrouter/some/model", answer="approve", reason="not clearly approved", ms=2210),
            dict(mode="shadow", review="agent", tool="terminal", text="git push --force", decision="ask", source="error",
                 error="model timeout", answer="none", ms=15003),
            dict(mode="shadow", review="decider", tool="patch", text="/p/a.go: replace 'x' with 'y'", decision="allow", source="model", answer="none", ms=88),
            dict(mode="enforce", review="decider", tool="terminal", text="ssh boi 'systemctl restart x'", decision="allow", source="model",
                 answer="preapproved:explicit", ms=301),
            dict(mode="enforce", tool="terminal", text="sudo rm -rf /var/cache/x", decision="ask", source="hermes-approval", answer="once",
                 reason="recursive delete"),
            dict(mode="enforce", tool="memory", text=json.dumps({"action": "add", "content": "line1\nline2\ttab"}), decision="unchecked",
                 source="not-gated", answer="none"),
            dict(mode="enforce", review="decider", tool="terminal", text="echo -----BEGIN RSA PRIVATE KEY-----\nMII\n-----END RSA PRIVATE KEY-----",
                 decision="block", source="model", answer="block", ms=5),
            dict(mode="enforce", review="decider", tool="write_file", text="/tmp/x (write 3 chars: 'abc')", decision="unchecked", source="no-context",
                 answer="none"),
            dict(mode="enforce", review="decider", tool="terminal", text="ls -la", decision="unchecked", source="read-only", answer="none", cwd="/home/u/ü"),
        ]
        for i, r in enumerate(rows):
            text = r.pop("text")
            sha, cmd = audit.command_fields(text, "full", limit=500 if r["decision"] == "unchecked" else None)
            e = {"host": "hermes", "session": sess, "command": cmd, "command_sha256": sha, "cwd": "/home/u/project", **r}
            # 6 lines, then one forced rotation (any max smaller than the file), then 6 more lines
            audit.append(e, path=p, max_bytes=1 if i == 6 else 0)
        shutil.copyfile(p + ".1", os.path.join(out, "audit-hermes.jsonl.1.fixture"))
        shutil.copyfile(p, os.path.join(out, "audit-hermes.jsonl.fixture"))
    cases = {
        "redact": [{"in": s, "out": audit.redact(s)} for s in REDACT_INPUTS],
        "fields": [{"in": s, "level": lvl, "sha": a, "text": b} for s, lvl in FIELD_INPUTS for a, b in [audit.command_fields(s, lvl)]],
        "session": {"in": "hermes-session-1", "out": audit.hash_session("hermes-session-1")},
    }
    with open(os.path.join(out, "audit-redact-cases.json"), "w", encoding="utf-8") as f:
        json.dump(cases, f, ensure_ascii=False, indent=1)
        f.write("\n")


if __name__ == "__main__":
    write_fixture(sys.argv[1])
    print("wrote fixtures to", sys.argv[1])
