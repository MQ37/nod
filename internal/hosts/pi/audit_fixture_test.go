package pi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MQ37/nod/internal/decisionlog"
)

// The fixtures are written by the extension's own audit.mjs (testdata/gen-audit-fixture.mjs); these
// tests check that Go's decisionlog reads, verifies and redacts them exactly like the JS side.

const fixtureLines = 12

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "audit-omp.jsonl.fixture"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestJSAuditFixtureVerifies(t *testing.T) {
	p := filepath.Join(t.TempDir(), "omp.jsonl")
	if err := os.WriteFile(p, fixture(t), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := decisionlog.Verify(p)
	if err != nil || res.Break != nil || res.Lines != fixtureLines || res.Truncated {
		t.Fatalf("verify: %+v break=%v err=%v", res, res.Break, err)
	}
	es, err := decisionlog.ReadLast(p, 0)
	if err != nil || len(es) != fixtureLines {
		t.Fatalf("read: n=%d err=%v", len(es), err)
	}
	for i, e := range es {
		if e.V != 1 || e.Host != "omp" || e.Decision == "" || e.Time.IsZero() || len(e.Prev) != 64 {
			t.Fatalf("line %d: %+v", i+1, e)
		}
	}
}

// Every split of the fixture into omp.jsonl.1 + omp.jsonl (a rotation at any point) still verifies.
func TestJSAuditFixtureAcrossRotatedFiles(t *testing.T) {
	lines := strings.SplitAfter(strings.TrimRight(string(fixture(t)), "\n"), "\n")
	for k := 1; k < len(lines); k++ {
		dir := t.TempDir()
		p := filepath.Join(dir, "omp.jsonl")
		older := strings.Join(lines[:k], "")
		if !strings.HasSuffix(older, "\n") {
			older += "\n"
		}
		_ = os.WriteFile(p+".1", []byte(older), 0o600)
		_ = os.WriteFile(p, []byte(strings.Join(lines[k:], "")+"\n"), 0o600)
		res, err := decisionlog.Verify(p)
		if err != nil || res.Break != nil || res.Lines != fixtureLines || len(res.Files) != 2 {
			t.Fatalf("split %d: %+v break=%v err=%v", k, res, res.Break, err)
		}
	}
	// and an edited line is caught
	p := filepath.Join(t.TempDir(), "omp.jsonl")
	_ = os.WriteFile(p, bytes.Replace(fixture(t), []byte(`"decision":"block"`), []byte(`"decision":"allow"`), 1), 0o600)
	if res, _ := decisionlog.Verify(p); res.Break == nil {
		t.Fatal("tampered fixture verified")
	}
}

// audit.mjs writes each line byte-for-byte as Go's json.Marshal would write the same Entry.
func TestJSAuditLinesAreGoEncoding(t *testing.T) {
	for i, line := range bytes.Split(bytes.TrimRight(fixture(t), "\n"), []byte("\n")) {
		var e decisionlog.Entry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		again, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again, line) {
			t.Fatalf("line %d differs from Go's encoding:\n js: %s\n go: %s", i+1, line, again)
		}
	}
}

// audit.mjs redact() gives the same output as decisionlog.Redact.
func TestJSRedactMatchesGo(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "redact-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases [][2]string
	if err := json.Unmarshal(b, &cases); err != nil || len(cases) < 10 {
		t.Fatalf("cases: n=%d err=%v", len(cases), err)
	}
	for _, c := range cases {
		if got := decisionlog.Redact(c[0]); got != c[1] {
			t.Errorf("Redact(%q)\n go: %q\n js: %q", c[0], got, c[1])
		}
	}
}
