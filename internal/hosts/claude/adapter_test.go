package claude

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/hosts"
)

func testAdapter(t *testing.T) *Adapter {
	t.Helper()
	home := t.TempDir()
	env := hosts.Env{Home: home, Getenv: func(string) string { return "" }, Now: func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }, Stderr: &bytes.Buffer{}}
	return New(env)
}
func TestInstallBackupIdempotentAndRoundTrip(t *testing.T) {
	a := testAdapter(t)
	p := a.SettingsPath()
	original := []byte("{\n  \"other\": [1, 2]\n}\n")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := a.PlanInstall(hosts.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Steps[0].Diff, `"matcher": "Bash"`) || !strings.Contains(string(plan.Steps[0].Content), `"timeout": 15`) {
		t.Fatalf("wrong hook: %s", plan.Steps[0].Diff)
	}
	if err = a.Install(plan); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(p + ".bak-nod-20260102-030405"); err != nil {
		t.Fatal(err)
	}
	again, err := a.PlanInstall(hosts.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if again.Changes() {
		t.Fatal("install not idempotent")
	}
	rp, err := a.PlanRemove()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Install(rp); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("round trip changed bytes:\n%s", got)
	}
}
func TestMalformedSettingsRefused(t *testing.T) {
	a := testAdapter(t)
	p := a.SettingsPath()
	os.MkdirAll(filepath.Dir(p), 0700)
	os.WriteFile(p, []byte("{bad"), 0600)
	_, err := a.PlanInstall(hosts.Options{})
	if hosts.CodeOf(err) != 4 {
		t.Fatalf("expected exit 4, got %v", err)
	}
}

func TestReasonOmitsMissingProbabilities(t *testing.T) {
	allow := 0.75
	for _, tc := range []struct {
		name string
		resp api.ClassifyResponse
		want string
	}{
		{name: "tier", resp: api.ClassifyResponse{Decision: api.Allow, Source: "tier"}, want: "nod: allow (tier)"},
		{name: "one probability", resp: api.ClassifyResponse{Decision: api.Ask, Source: "model", PAllow: &allow}, want: "nod: ask (model); p_allow=0.75"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reason(tc.resp); got != tc.want {
				t.Fatalf("reason=%q want %q", got, tc.want)
			}
		})
	}
}

type warmClassifier struct{ called bool }

func (w *warmClassifier) Classify(api.ClassifyRequest) (api.ClassifyResponse, error) {
	return api.ClassifyResponse{}, nil
}
func (w *warmClassifier) HealthWarm(v bool) (string, error) { w.called = v; return "", nil }

func TestSessionStartWarmIsOptInAndRemoved(t *testing.T) {
	a := testAdapter(t)
	p, err := a.PlanInstall(hosts.Options{Warm: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p.Steps[0].Content), `"SessionStart"`) || !strings.Contains(string(p.Steps[0].Content), `hook claude --warm`) {
		t.Fatalf("warm hook missing: %s", p.Steps[0].Content)
	}
	if err = a.Install(p); err != nil {
		t.Fatal(err)
	}
	if _, obs, err := state(mustRead(t, a.SettingsPath())); err != nil || !obs {
		t.Fatalf("warm hook not detected: obs=%t err=%v", obs, err)
	}
	w := &warmClassifier{}
	a.Hook(strings.NewReader(`{"hook_event_name":"SessionStart"}`), &bytes.Buffer{}, w)
	if !w.called {
		t.Fatal("session start did not request warm")
	}
	rp, err := a.PlanRemove()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Install(rp); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustRead(t, a.SettingsPath())), `--warm`) {
		t.Fatal("warm hook remained after removal")
	}
}
func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
