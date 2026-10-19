package hermes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MQ37/nod/internal/decisionlog"
)

// The fixtures are written by the plugin's own audit.py (internal/hosts/hermes/pytests/gen_fixture.py): a 12-line Hermes audit trail
// with one rotation boundary, and redaction results. Go must accept the chain byte for byte and redact the same way.

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPythonAuditFixtureVerifies(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hermes.jsonl")
	copyFile(t, filepath.Join("testdata", "audit-hermes.jsonl.1.fixture"), p+".1")
	copyFile(t, filepath.Join("testdata", "audit-hermes.jsonl.fixture"), p)
	res, err := decisionlog.Verify(p)
	if err != nil || res.Break != nil || res.Lines != 12 || res.Truncated || len(res.Files) != 2 {
		t.Fatalf("python-written chain: %+v break=%v err=%v", res, res.Break, err)
	}
	es, err := decisionlog.ReadLast(p, 0)
	if err != nil || len(es) != 12 {
		t.Fatalf("entries: %d err=%v", len(es), err)
	}
	seen := map[string]bool{}
	for _, e := range es {
		if e.V != 1 || e.Host != "hermes" || e.Time.IsZero() || e.Tool == "" || e.Decision == "" || len(e.Prev) != 64 {
			t.Fatalf("entry fields: %+v", e)
		}
		seen[e.Decision] = true
	}
	for _, d := range []string{"allow", "ask", "block", "unchecked"} {
		if !seen[d] {
			t.Fatalf("fixture lacks a %q line", d)
		}
	}
	// a changed byte in the python-written file breaks the chain where Go expects it
	b, _ := os.ReadFile(p + ".1")
	if err := os.WriteFile(p+".1", []byte(strings.Replace(string(b), `"decision":"block"`, `"decision":"allow"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, _ := decisionlog.Verify(p); res.Break == nil || res.Break.Line != 2 {
		t.Fatalf("tampering not detected: %+v", res.Break)
	}
}

func TestPythonRedactionMatchesGo(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "audit-redact-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Redact []struct{ In, Out string } `json:"redact"`
		Fields []struct {
			In    string `json:"in"`
			Level string `json:"level"`
			SHA   string `json:"sha"`
			Text  string `json:"text"`
		} `json:"fields"`
		Session struct{ In, Out string } `json:"session"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases.Redact) == 0 || len(cases.Fields) == 0 {
		t.Fatal("empty cases")
	}
	for _, c := range cases.Redact {
		if got := decisionlog.Redact(c.In); got != c.Out {
			t.Errorf("Redact(%q):\n go     %q\n python %q", c.In, got, c.Out)
		}
	}
	for _, c := range cases.Fields {
		sha, text := decisionlog.CommandFields(c.In, c.Level)
		if sha != c.SHA || text != c.Text {
			t.Errorf("CommandFields(%.40q, %q): go (%s, %d runes) python (%s, %d runes)", c.In, c.Level, sha, len([]rune(text)), c.SHA, len([]rune(c.Text)))
		}
	}
	if got := decisionlog.HashSession(cases.Session.In); got != cases.Session.Out {
		t.Errorf("HashSession: go %q python %q", got, cases.Session.Out)
	}
}
