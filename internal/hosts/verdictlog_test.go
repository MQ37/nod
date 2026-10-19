package hosts

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MQ37/nod/internal/decisionlog"
)

func TestHookVerdictUsesSharedDecisionsLog(t *testing.T) {
	runtimeDir := t.TempDir()
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	env := Env{Home: t.TempDir(), RuntimeDir: runtimeDir, Now: func() time.Time { return at }}
	env.LogVerdict(Verdict{Host: "claude", Tool: "Bash", Decision: "allow", Source: "tier", Ms: 3})
	path := filepath.Join(runtimeDir, "decisions.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "verdicts.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("legacy verdict log exists: %v", err)
	}
	entries, err := decisionlog.ReadLast(path, 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("read shared log: entries=%d err=%v", len(entries), err)
	}
	if entries[0].Host != "claude" || entries[0].Decision != "allow" || !entries[0].Time.Equal(at) {
		t.Fatalf("unexpected shared entry: %#v", entries[0])
	}
	if got := env.LastVerdict("claude"); !got.Equal(at) {
		t.Fatalf("last verdict timestamp=%s, want %s", got, at)
	}
}
