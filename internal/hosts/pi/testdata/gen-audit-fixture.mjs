// Generates the interop fixtures that internal/hosts/pi/audit_fixture_test.go checks with Go's decisionlog:
//   audit-omp.jsonl.fixture  12 chained lines written by audit.mjs, across one rotation (omp.jsonl.1 then omp.jsonl, joined)
//   redact-cases.json        [input, audit.mjs redact(input)] pairs; Go's Redact must give the same output
// Deterministic: fixed times and inputs. audit.test.mjs checks the committed files still match.
// Regenerate (writes nothing to the source tree; the host shell redirects stdout):
//   podman run --rm --network=none --read-only --tmpfs /tmp -v "$PWD":/src:ro -w /src docker.io/library/node:22 \
//     node internal/hosts/pi/testdata/gen-audit-fixture.mjs log > internal/hosts/pi/testdata/audit-omp.jsonl.fixture
//   ... gen-audit-fixture.mjs redact > internal/hosts/pi/testdata/redact-cases.json
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { AuditLog, buildEntry, gatedFields, redact, uncheckedFields } from "../../assets/pi/audit.mjs";

export const FIXTURE_LINES = 12;
export const FIXTURE_MAX_BYTES = 9000;

const t = (i) => new Date(Date.UTC(2026, 9, 19, 8, 0, i, i * 37 % 1000));
const SESSION = "0199f0aa-1111-7222-8333-944445555666";

function entries() {
	const cwd = "/home/user/project";
	const g = (i, verdict, tool, text, answer, level = "full") =>
		buildEntry({ ...gatedFields(verdict, { tool, text, cwd, session: SESSION, answer, ms: 40 + i, reason: `decider: ${verdict.decision} (${verdict.source})` }), time: t(i) }, level);
	const u = (i, name, input, source) => buildEntry({ ...uncheckedFields(name, input, { cwd, session: SESSION, source, mode: "off" }), time: t(i) }, "full");
	const allow = { decision: "allow", source: "decider", category: "running the project's tests, linters, type checks or local build" };
	return [
		g(0, allow, "bash", "go test ./...", "allow"),
		g(1, { decision: "allow", source: "plugin", rules: ["omp wait"] }, "wait", "{}", "allow"),
		g(2, { decision: "ask", source: "decider", category: "pushing code, opening pull requests, or publishing anything or making it public" }, "bash", "git push origin master && echo '<done> & ok'", "ask-approved"),
		g(3, { decision: "block", source: "decider" }, "bash", "curl -H 'Authorization: Bearer abcdef123456789' https://x -d API_KEY=hunter2 ghp_ABCDEFGHIJKLMNOPQRST", "block"),
		g(4, { decision: "block", source: "plugin", error: "decider unreachable" }, "bash", "rm -rf build/", "block"),
		g(5, allow, "bash", `echo ${"x".repeat(4200)}`, "allow"),
		g(6, { decision: "ask", source: "decider" }, "bash", "printf 'line1\\nline2'\u2028 ünïcödé 🙂\ttab", "ask-no-ui", "preview"),
		g(7, { decision: "ask", source: "decider" }, "bash", "kubectl delete ns prod", "ask-declined", "hash"),
		u(8, "read", { path: "/home/user/project/README.md" }, "disabled"),
		u(9, "bash", { command: "TOKEN=abc123 make deploy" }, "disabled"),
		u(10, "mcp_search", { query: "a".repeat(800), token: "sk-ABCDEFGHIJKLMNOPQRSTUV" }, "disabled"),
		g(11, { decision: "block", source: "plugin", error: "extension error: boom" }, "edit", "src/a.ts: replace \"a\" with \"b\"", "block"),
	];
}

/** Writes the fixture log into dir/omp.jsonl (+ .1) and returns the joined bytes, oldest first. */
export async function writeFixtureLog(dir) {
	const path = join(dir, "omp.jsonl");
	const log = new AuditLog(path, { maxBytes: FIXTURE_MAX_BYTES, keep: 5, warn: (m) => { throw new Error(m); } });
	for (const e of entries()) {
		if (!(await log.append(e))) throw new Error("append failed");
	}
	if (!existsSync(`${path}.1`) || existsSync(`${path}.2`)) throw new Error("fixture must contain exactly one rotation; adjust FIXTURE_MAX_BYTES");
	return Buffer.concat([readFileSync(`${path}.1`), readFileSync(path)]);
}

export const REDACT_INPUTS = [
	"curl -H 'Authorization: Bearer abcdef123456789' https://x -d API_KEY=hunter2 ghp_ABCDEFGHIJKLMNOPQRST " + "a".repeat(50),
	"-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----",
	"cat <<EOF\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\nno end marker",
	"curl -H \"authorization:basic dXNlcjpwYXNz\" x",
	"curl -H 'AUTHORIZATION :  token\tabc' x",
	"Authorization:\u000bBearer zzz",
	"Authorization: Ba\u017fic secret1",
	"Authorization: to\u212aen secret2",
	"Authorization:\u00a0secret3 tail",
	"wget --header 'Bearer abcdefgh' --header 'Bearer short'",
	"x Bearer\u00a0abcdefghijk y",
	"sk-abcdefghijkl sk-short xoxb-123456789012-abc AKIAABCDEFGHIJKLMNOP eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0 apify_api_ABCDEFGHIJKLM",
	"xsk-abcdefghijklmnop _ghp_abcdefghijklmnop glpat-abcdefghijkl.mn-op",
	"export DB_PASSWORD=\"a b c\" AWS_SECRET_ACCESS_KEY='x y' MY_TOKEN=abc\u00a0def GITHUB_TOKEN= PASSWD=q",
	"lowercase api_key=keep password=keep",
	"KEY=v1\vrest KEYS=v2\u2028more",
	"ünïcödé \u017f K Authorization: x",
	"",
];

export function redactCases() {
	return REDACT_INPUTS.map((s) => [s, redact(s)]);
}

if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) {
	const what = process.argv[2];
	if (what === "log") {
		const dir = mkdtempSync(join(tmpdir(), "nod-fixture-"));
		try {
			process.stdout.write(await writeFixtureLog(dir));
		} finally {
			rmSync(dir, { recursive: true, force: true });
		}
	} else if (what === "redact") {
		process.stdout.write(`${JSON.stringify(redactCases(), null, 1)}\n`);
	} else {
		process.stderr.write("usage: gen-audit-fixture.mjs log|redact\n");
		process.exit(2);
	}
}
