package decisionlog

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func entry(i int) Entry {
	return Entry{Host: "test", Tool: "Bash", Decision: "allow", Source: "model", Reason: strings.Repeat("x", i%7)}
}

func TestChainVerifiesAndDetectsTampering(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit", "test.jsonl")
	w := New(p, false)
	for i := 0; i < 20; i++ {
		if err := w.Append(entry(i)); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Verify(p)
	if err != nil || res.Break != nil || res.Lines != 20 || res.Truncated {
		t.Fatalf("clean log: %+v err=%v", res, err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(p)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", fi.Mode().Perm())
	}
	orig, _ := os.ReadFile(p)
	lines := strings.Split(strings.TrimRight(string(orig), "\n"), "\n")

	edit := func(name string, mutate func([]string) []string, wantLine int) {
		t.Helper()
		cp := filepath.Join(t.TempDir(), "x.jsonl")
		if err := os.WriteFile(cp, []byte(strings.Join(mutate(append([]string(nil), lines...)), "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r, err := Verify(cp)
		if err != nil || r.Break == nil || r.Break.Line != wantLine {
			t.Fatalf("%s: want a break at line %d, got %+v err=%v", name, wantLine, r.Break, err)
		}
	}
	edit("a line was changed", func(l []string) []string { l[5] = strings.Replace(l[5], `"allow"`, `"block"`, 1); return l }, 7)
	edit("a line was removed", func(l []string) []string { return append(l[:9], l[10:]...) }, 10)
	edit("lines were swapped", func(l []string) []string { l[3], l[4] = l[4], l[3]; return l }, 4)
	edit("a line is junk", func(l []string) []string { l[2] = "not json"; return l }, 3)
}

func TestDeletingTheTailIsNotDetectableButTheMiddleIs(t *testing.T) {
	// Documented limit: a chain proves order and integrity of what is there, not that nothing newer
	// was cut off the end. Truncating the end leaves a valid chain.
	p := filepath.Join(t.TempDir(), "t.jsonl")
	w := New(p, false)
	for i := 0; i < 5; i++ {
		_ = w.Append(entry(i))
	}
	b, _ := os.ReadFile(p)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	_ = os.WriteFile(p, []byte(strings.Join(lines[:3], "\n")+"\n"), 0o600)
	if r, _ := Verify(p); r.Break != nil || r.Lines != 3 {
		t.Fatalf("%+v", r)
	}
}

func TestRotationKeepsTheChain(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.jsonl")
	w := New(p, false)
	w.MaxBytes = 600
	w.Keep = 3
	for i := 0; i < 60; i++ {
		if err := w.Append(entry(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(p + ".1"); err != nil {
		t.Fatal("expected a rotated file")
	}
	if _, err := os.Stat(p + ".4"); err == nil {
		t.Fatal("more rotated files than Keep")
	}
	// DefaultKeep is used by Verify/ReadLast; files beyond w.Keep were deleted, so the oldest kept line
	// need not start at genesis, but the chain across the files that remain must hold.
	r, err := Verify(p)
	if err != nil || r.Break != nil || r.Lines == 0 {
		t.Fatalf("%+v err=%v", r, err)
	}
	if !r.Truncated {
		t.Fatal("older files were rotated away: Truncated must say so")
	}
}

func TestConcurrentAppendsStayChained(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.jsonl")
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := New(p, false) // separate writers: only the flock serialises them, as separate processes would
			for i := 0; i < 25; i++ {
				if err := w.Append(entry(i)); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	r, err := Verify(p)
	if err != nil || r.Break != nil || r.Lines != 200 {
		t.Fatalf("%+v err=%v", r, err)
	}
}

func TestCommandFieldsRedactAndLevels(t *testing.T) {
	cmd := "curl -H 'Authorization: Bearer abcdef123456789' https://x -d API_KEY=hunter2 ghp_ABCDEFGHIJKLMNOPQRST " + strings.Repeat("a", 5000)
	sha, full := CommandFields(cmd, "")
	if sha != SHA256(cmd) || strings.Contains(full, "abcdef123456789") || strings.Contains(full, "hunter2") || strings.Contains(full, "ghp_ABCDEFGHIJKLMNOPQRST") {
		t.Fatalf("not redacted: %q", full)
	}
	if n := len([]rune(full)); n > MaxCommand+1 {
		t.Fatalf("full text not clipped: %d", n)
	}
	_, prev := CommandFields(cmd, CommandsPreview)
	if n := len([]rune(prev)); n > PreviewLen+1 || n == 0 {
		t.Fatalf("preview length %d", n)
	}
	if s, text := CommandFields(cmd, CommandsHash); s != sha || text != "" {
		t.Fatalf("hash level stores text: %q", text)
	}
	if s, text := CommandFields("", CommandsFull); s != "" || text != "" {
		t.Fatal("empty command")
	}
	if got := Redact("-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----"); got != "<private-key>" {
		t.Fatalf("private key: %q", got)
	}
}

func TestReadLastSpansRotatedFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.jsonl")
	w := New(p, false)
	w.MaxBytes = 500
	for i := 0; i < 30; i++ {
		_ = w.Append(entry(i))
	}
	es, err := ReadLast(p, 12)
	if err != nil || len(es) != 12 {
		t.Fatalf("n=%d err=%v", len(es), err)
	}
}
