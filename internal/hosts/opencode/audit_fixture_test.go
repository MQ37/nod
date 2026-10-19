package opencode

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MQ37/nod/internal/decisionlog"
)

// fixtureLines is the line count of testdata/audit-opencode.jsonl.fixture, which the plugin's own code
// wrote (testdata/gen-audit-fixture.mjs, through both hook generations and across one rotation); the
// node tests check that the generator still produces it byte for byte.
const fixtureLines = 12

func readFixture(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "audit-opencode.jsonl.fixture"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != fixtureLines {
		t.Fatalf("fixture has %d lines, want %d", len(lines), fixtureLines)
	}
	return lines
}

func writeLines(t *testing.T, p string, lines []string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The plugin's JavaScript chain must verify with Go's decisionlog.Verify, as one file and split across
// a rotation at any point, and Go must be able to continue it.
func TestPluginAuditFixtureVerifiesInGo(t *testing.T) {
	lines := readFixture(t)
	for k := 0; k < len(lines); k++ {
		dir := t.TempDir()
		p := filepath.Join(dir, "opencode.jsonl")
		if k > 0 {
			writeLines(t, p+".1", lines[:k])
		}
		writeLines(t, p, lines[k:])
		res, err := decisionlog.Verify(p)
		if err != nil || res.Break != nil || res.Lines != fixtureLines || res.Truncated {
			t.Fatalf("split at %d: %+v break=%v err=%v", k, res, res.Break, err)
		}
	}

	p := filepath.Join(t.TempDir(), "opencode.jsonl")
	writeLines(t, p, lines)
	es, err := decisionlog.ReadLast(p, 0)
	if err != nil || len(es) != fixtureLines {
		t.Fatalf("ReadLast: %d entries, err %v", len(es), err)
	}
	seen := map[string]bool{}
	for _, e := range es {
		if e.V != 1 || e.Host != Name || e.Time.IsZero() || e.Prev == "" {
			t.Fatalf("entry fields: %+v", e)
		}
		seen[e.Decision] = true
		if strings.Contains(e.Command, "hunter2") {
			t.Fatalf("secret in the stored command: %q", e.Command)
		}
	}
	for _, d := range []string{"allow", "ask", "block", "unchecked"} {
		if !seen[d] {
			t.Fatalf("fixture has no %q line", d)
		}
	}

	// Go appends after the JavaScript lines: the chain still holds.
	if err := decisionlog.New(p, false).Append(decisionlog.Entry{Host: Name, Tool: "Bash", Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	if res, err := decisionlog.Verify(p); err != nil || res.Break != nil || res.Lines != fixtureLines+1 {
		t.Fatalf("after a Go append: %+v err=%v", res, err)
	}

	// And an edited JavaScript line is caught.
	bad := append([]string(nil), lines...)
	bad[4] = string(bytes.Replace([]byte(bad[4]), []byte(`"opencode"`), []byte(`"opencod3"`), 1))
	writeLines(t, p, bad)
	if res, _ := decisionlog.Verify(p); res.Break == nil || res.Break.Line != 6 {
		t.Fatalf("tampered fixture: %+v", res.Break)
	}
}
