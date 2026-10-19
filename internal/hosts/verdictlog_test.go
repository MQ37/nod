package hosts

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MQ37/nod/internal/decisionlog"
)

func TestHookVerdictGoesToThePersistentAuditTrail(t *testing.T) {
	runtimeDir := t.TempDir()
	auditDir := filepath.Join(t.TempDir(), "audit")
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	env := Env{Home: t.TempDir(), RuntimeDir: runtimeDir, AuditDir: auditDir, Now: func() time.Time { return at }}
	env.LogVerdict(Verdict{Host: "claude", Tool: "Bash", Decision: "allow", Source: "tier", Ms: 3, Command: "git push -d origin x API_KEY=hunter2", Session: "sess-1"})
	path := filepath.Join(auditDir, "claude.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "decisions.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("the RAM-only log must no longer be written: %v", err)
	}
	entries, err := decisionlog.ReadLast(path, 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("read shared log: entries=%d err=%v", len(entries), err)
	}
	if entries[0].Host != "claude" || entries[0].Decision != "allow" || !entries[0].Time.Equal(at) {
		t.Fatalf("unexpected shared entry: %#v", entries[0])
	}
	e0 := entries[0]
	if e0.Command != "git push -d origin x API_KEY=<v>" || e0.CommandSHA256 != decisionlog.SHA256("git push -d origin x API_KEY=hunter2") || e0.Session != decisionlog.HashSession("sess-1") || e0.Prev != decisionlog.Genesis {
		t.Fatalf("audit fields: %#v", e0)
	}
	if got := env.LastVerdict("claude"); !got.Equal(at) {
		t.Fatalf("last verdict timestamp=%s, want %s", got, at)
	}
}
