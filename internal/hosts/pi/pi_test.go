package pi

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MQ37/nod/internal/decisionlog"
	"github.com/MQ37/nod/internal/hosts"
)

func TestInstallStatusRemove(t *testing.T) {
	home := t.TempDir()
	a := New(hosts.Env{Home: home, Getenv: func(string) string { return "" }})
	p, err := a.PlanInstall(hosts.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Install(p); err != nil {
		t.Fatal(err)
	}
	s := a.Status()
	if !s.Present || !s.Wired {
		t.Fatalf("unexpected installed status: %#v", s)
	}
	for _, name := range []string{"index.ts", "policy.mjs", "audit.mjs"} {
		if b, e := os.ReadFile(filepath.Join(a.extDir(), name)); e != nil || len(b) == 0 {
			t.Fatalf("asset %s missing: %v", name, e)
		}
	}
	if !s.LastVerdict.IsZero() {
		t.Fatalf("no audit line yet, got %v", s.LastVerdict)
	}
	when := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	if err = decisionlog.New(filepath.Join(home, ".local", "state", "nod", "audit", "omp.jsonl"), false).Append(decisionlog.Entry{Time: when, Host: "omp", Tool: "bash", Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	if got := a.Status().LastVerdict; !got.Equal(when) {
		t.Fatalf("last verdict from the omp audit file: %v", got)
	}
	rp, err := a.PlanRemove()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Install(rp); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(a.extDir()); !os.IsNotExist(err) {
		t.Fatalf("extension directory remains: %v", err)
	}
	var out bytes.Buffer
	a.Hook(nil, &out, nil)
	if out.Len() != 0 {
		t.Fatalf("hook wrote stdout: %q", out.String())
	}
}
